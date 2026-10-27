package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/llm"
)

// Request prefix outcomes recorded on model events (attr "prefix"): how the
// request relates to the previous one of the session, which decides how much
// of it a provider prompt cache can serve.
const (
	prefixFirst   = "first"   // no earlier request in this process
	prefixAppend  = "append"  // the previous request is a prefix of this one
	prefixFold    = "fold"    // an earlier message got shorter (history folding)
	prefixSegment = "segment" // a new segment replaced the history
	prefixSystem  = "system"  // the system prompt changed
	prefixTools   = "tools"   // the tool set changed
	prefixRewrite = "rewrite" // any other change to an earlier message
)

// How the first request of a process relates to the fingerprint the previous
// process left in the session meta (attr "prefix_resume"): the bucket report
// splits a resumed conversation into resume_same (only appended, the provider
// cache still matches) and resume_changed (the prefix was rewritten).
const (
	resumeSame    = "same"    // the persisted request is a prefix of this one
	resumeChanged = "changed" // an earlier request part differs
	resumeKeep    = 64        // messages kept in the persisted fingerprint
)

// metaPrefixPrint is the session meta key of the persisted request fingerprint
// (printRecord JSON as a string): written at the end of a turn and read when a
// new process resumes the session, so its first request is reported as
// resume_same / resume_changed instead of a bare first (prefix.go).
const metaPrefixPrint = "cache_prefix_print"

// prefixReport splits a request into the part an unchanged prefix could serve
// from the provider cache (reused), the part that had been sent before but
// changed position or form (rebuilt: re-sent after the break) and what is new
// since the previous request. Sizes are JSON characters, a proxy for tokens.
type prefixReport struct {
	Reason                 string
	Reused, Rebuilt, Fresh int
	// Resume is resumeSame or resumeChanged when this request was compared with
	// the fingerprint of a previous process ("" for the ordinary in-process
	// comparison); Reason then names what changed for resumeChanged.
	Resume string
}

// requestPrint is what is kept of a request to compare the next one with.
type requestPrint struct {
	system, tools         [32]byte
	systemSize, toolsSize int
	msgs                  []messagePrint
	// offset is how many messages were dropped from the head of msgs: a
	// persisted print keeps only its last resumeKeep messages. The cumulative
	// sum of the first kept message still covers every dropped one, so a match
	// at offset proves the dropped head unchanged as well.
	offset int
}

// printRecord is the persisted form of a requestPrint (session meta
// metaPrefixPrint, written at the end of a turn).
//
// Trade-off: only the last resumeKeep messages are kept, because a long
// conversation holds hundreds of messages and the session meta is read with
// every session load. The cumulative hash chains the whole request, so the
// first kept message's sum stands in for the dropped head: a resumed process
// can still tell "only appended" from "an earlier message changed", but when
// the window start itself differs it cannot say which earlier message changed
// (it reports the rewrite reason without a position).
type printRecord struct {
	System     string       `json:"system"`
	SystemSize int          `json:"system_bytes"`
	Tools      string       `json:"tools"`
	ToolsSize  int          `json:"tools_bytes"`
	MsgCount   int          `json:"messages"`
	Msgs       []printEntry `json:"last_messages"`
}

// printEntry is one message of a persisted print: its cumulative hash, the
// identifier the fold/segment detection uses and its JSON size.
type printEntry struct {
	Sum  string `json:"sum"`
	ID   string `json:"id"`
	Size int    `json:"bytes"`
	Seg  bool   `json:"segment,omitempty"`
}

// recordOf keeps the persisted form of a request print: the tail window of the
// messages plus the total count (see printRecord for the trade-off).
func recordOf(p *requestPrint) *printRecord {
	if p == nil {
		return nil
	}
	start := max(len(p.msgs)-resumeKeep, 0)
	rec := &printRecord{System: hex.EncodeToString(p.system[:]), SystemSize: p.systemSize,
		Tools: hex.EncodeToString(p.tools[:]), ToolsSize: p.toolsSize, MsgCount: len(p.msgs),
		Msgs: make([]printEntry, 0, len(p.msgs)-start)}
	for _, m := range p.msgs[start:] {
		rec.Msgs = append(rec.Msgs, printEntry{Sum: hex.EncodeToString(m.sum[:]), ID: m.id, Size: m.size, Seg: m.segment})
	}
	return rec
}

// printOf rebuilds a requestPrint from a persisted record (nil when the record
// is unusable: no hashes at all). A hash that does not decode leaves an empty
// sum, which never matches a live message and so reads as "changed".
func printOf(rec *printRecord) *requestPrint {
	if rec == nil || (rec.System == "" && rec.Tools == "" && len(rec.Msgs) == 0) {
		return nil
	}
	p := &requestPrint{systemSize: rec.SystemSize, toolsSize: rec.ToolsSize,
		offset: max(rec.MsgCount-len(rec.Msgs), 0)}
	copy(p.system[:], decodeSum(rec.System))
	copy(p.tools[:], decodeSum(rec.Tools))
	p.msgs = make([]messagePrint, 0, len(rec.Msgs))
	for _, m := range rec.Msgs {
		var sum [32]byte
		copy(sum[:], decodeSum(m.Sum))
		p.msgs = append(p.msgs, messagePrint{sum: sum, id: m.ID, size: m.Size, segment: m.Seg})
	}
	return p
}

// decodeSum decodes a hex sha256 into bytes (nil when it does not decode).
func decodeSum(s string) []byte {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return raw
}

// messagePrint: sum is cumulative (this message and all before it), id names
// the message (role and tool call), size is its JSON length.
type messagePrint struct {
	sum     [32]byte
	id      string
	size    int
	segment bool
}

// prefixTracker compares each model request with the previous one of its
// session. A BeforeModel hook feeds it; the model event recorder reads the
// report of the request in flight.
type prefixTracker struct {
	mu      sync.Mutex
	last    map[string]*requestPrint
	reports map[string]prefixReport
	// resumed holds the fingerprint a previous process left for a session: it
	// is the baseline of the session's first request only.
	resumed map[string]*requestPrint
	// requests keeps each session's last main-loop request as it was sent: the
	// handoff-package call of a segment break continues it when it may
	// (segment_handoff_reuse.go).
	requests map[string]*agentcore.GenerateRequest
}

func newPrefixTracker() *prefixTracker {
	return &prefixTracker{last: map[string]*requestPrint{}, reports: map[string]prefixReport{},
		resumed: map[string]*requestPrint{}, requests: map[string]*agentcore.GenerateRequest{}}
}

// seed gives the tracker the request fingerprint a previous process persisted,
// so the session's first request of this process is compared with it instead of
// being reported as a bare prefixFirst (resume_same / resume_changed). A request
// already seen in this process wins: the in-process history is more precise.
func (t *prefixTracker) seed(sessionID string, rec *printRecord) {
	if t == nil || sessionID == "" {
		return
	}
	p := printOf(rec)
	if p == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last[sessionID] != nil {
		return
	}
	t.resumed[sessionID] = p
}

// print returns the fingerprint of the session's last request, for persisting
// it at the end of a turn (nil when the session sent nothing in this process).
func (t *prefixTracker) print(sessionID string) *printRecord {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return recordOf(t.last[sessionID])
}

// observe fingerprints req, compares it with the session's previous request
// and keeps the report for the model event.
func (t *prefixTracker) observe(sessionID string, req *agentcore.GenerateRequest) {
	if t == nil || sessionID == "" || req == nil {
		return
	}
	cur := printRequest(req)
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, resumed := t.last[sessionID], false
	if prev == nil {
		// First request of this process: fall back to what the previous process
		// persisted, and consume it (only that one request resumes).
		if b := t.resumed[sessionID]; b != nil {
			prev, resumed = b, true
			delete(t.resumed, sessionID)
		}
	}
	rep := comparePrints(prev, cur)
	if resumed {
		rep.Resume = resumeChanged
		if rep.Reason == prefixAppend {
			rep.Resume = resumeSame
		}
	}
	t.reports[sessionID] = rep
	t.last[sessionID] = cur
}

// report returns the report of the session's request in flight.
func (t *prefixTracker) report(sessionID string) (prefixReport, bool) {
	if t == nil {
		return prefixReport{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.reports[sessionID]
	return r, ok
}

func printRequest(req *agentcore.GenerateRequest) *requestPrint {
	p := &requestPrint{msgs: make([]messagePrint, 0, len(req.Messages))}
	sys := []byte(req.System)
	p.system, p.systemSize = sha256.Sum256(sys), len(sys)
	tools, _ := json.Marshal(req.Tools)
	p.tools, p.toolsSize = sha256.Sum256(tools), len(tools)
	var sum [32]byte
	for _, m := range req.Messages {
		raw, _ := json.Marshal(m)
		h := sha256.New()
		h.Write(sum[:])
		h.Write(raw)
		copy(sum[:], h.Sum(nil))
		id := m.Role
		switch {
		case m.ToolResult != nil:
			id += ":" + m.ToolResult.CallID
		case m.ToolCall != nil:
			id += ":" + m.ToolCall.CallID
		}
		segment, _ := m.Metadata[llm.SegmentContextMetaKey].(bool)
		p.msgs = append(p.msgs, messagePrint{sum: sum, id: id, size: len(raw), segment: segment})
	}
	return p
}

// comparePrints finds where cur leaves prev and why.
func comparePrints(prev, cur *requestPrint) prefixReport {
	sizeOf := func(msgs []messagePrint) int {
		n := 0
		for _, m := range msgs {
			n += m.size
		}
		return n
	}
	head := cur.systemSize + cur.toolsSize
	if prev == nil {
		return prefixReport{Reason: prefixFirst, Fresh: head + sizeOf(cur.msgs)}
	}
	// prev's kept window starts at base in cur (0 for a request seen in this
	// process, prev.offset for a persisted print that dropped its head).
	base := prev.offset
	limit := base + len(prev.msgs)
	k := base // messages in the common prefix
	for k < limit && k < len(cur.msgs) && prev.msgs[k-base].sum == cur.msgs[k].sum {
		k++
	}
	if k == base && base > 0 {
		// The kept window starts with a difference, so the dropped head cannot
		// be proven unchanged: report a rewrite of the whole request.
		common := min(limit, len(cur.msgs))
		return prefixReport{Reason: prefixRewrite, Fresh: sizeOf(cur.msgs[common:]), Rebuilt: head + sizeOf(cur.msgs[:common])}
	}
	// Messages of cur at positions prev also had are "rebuilt" after a break.
	common := min(limit, len(cur.msgs))
	r := prefixReport{Fresh: sizeOf(cur.msgs[common:])}
	switch {
	case prev.system != cur.system || prev.tools != cur.tools:
		r.Reason = prefixTools
		if prev.system != cur.system {
			r.Reason = prefixSystem
		}
		r.Reused, r.Rebuilt = 0, head+sizeOf(cur.msgs[:common])
		return r
	case k == limit: // prev is a prefix of cur
		r.Reason, r.Reused = prefixAppend, head+sizeOf(cur.msgs[:k])
		return r
	}
	r.Reused, r.Rebuilt = head+sizeOf(cur.msgs[:k]), sizeOf(cur.msgs[k:common])
	switch {
	case k >= len(cur.msgs):
		r.Reason = prefixRewrite // cur is a shortened prev
	case cur.msgs[k].segment:
		r.Reason = prefixSegment
	case cur.msgs[k].id == prev.msgs[k-base].id && cur.msgs[k].size < prev.msgs[k-base].size:
		r.Reason = prefixFold
	default:
		r.Reason = prefixRewrite
	}
	return r
}

// prefixPrintFromMeta reads the persisted request fingerprint of a session (nil
// when there is none: a session recorded by an older binary, or a turn that
// never sent a request).
func prefixPrintFromMeta(meta map[string]any) *printRecord {
	raw := metaString(meta, metaPrefixPrint)
	if raw == "" {
		return nil
	}
	var rec printRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil // an unreadable record counts as none
	}
	return &rec
}

// observeRequest keeps req as the session's last main-loop request. The request
// is the runtime's own copy of what it is about to send (never mutated later),
// so the reuse request of a segment break can reproduce it byte for byte.
func (t *prefixTracker) observeRequest(sessionID string, req *agentcore.GenerateRequest) {
	if t == nil || sessionID == "" || req == nil || len(req.Messages) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requests[sessionID] = req
}

// lastRequest returns the session's last main-loop request (nil when the session
// sent none in this process, or when a segment break cleared it).
func (t *prefixTracker) lastRequest(sessionID string) *agentcore.GenerateRequest {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.requests[sessionID]
}

// clearRequest forgets the session's last main-loop request: after a segment
// break it no longer belongs to the current segment, so a break that finds no
// newer request falls back to the old handoff path (no_request).
func (t *prefixTracker) clearRequest(sessionID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.requests, sessionID)
}

// savePrefixPrint persists the session's last request fingerprint (see
// metaPrefixPrint). Best-effort: a failure only costs the resume comparison of
// the next process, so it never affects the turn.
func (r *Runner) savePrefixPrint(ctx context.Context, sessionID string) {
	if r == nil {
		return
	}
	rec := r.rec.prefix.print(sessionID)
	if rec == nil {
		return
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		slog.Warn("encode request prefix print", "session_id", sessionID, "err", err)
		return
	}
	if err := r.cfg.Repository.PatchSessionMeta(ctx, sessionID, map[string]any{metaPrefixPrint: string(raw)}); err != nil {
		slog.Warn("store request prefix print", "session_id", sessionID, "err", err)
	}
}

// prefixHook feeds the tracker with every request the runtime sends.
type prefixHook struct {
	agentcore.NopHook
	tracker *prefixTracker
}

func (h prefixHook) BeforeModel(ctx context.Context, event *agentcore.ModelEvent, _ *agentcore.Control) error {
	if event != nil {
		h.tracker.observe(observeScopeFrom(ctx).sessionID, event.Request)
	}
	return nil
}
