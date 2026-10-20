package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// ErrContextOverflow ends a turn whose model context still exceeds the window
// after compaction and tool-result truncation. It is returned instead of
// retrying forever.
var ErrContextOverflow = errors.New("context window exceeded even after compaction and tool-result truncation")

// ctxItem is one context message together with the entries seq it was
// persisted under. Every message of a turn is persisted right after it is
// produced, so in-turn messages also have a seq (see turnItems). An injected
// entry (isInjectedItem) is a stored entry of the turn that is not an
// in-memory message; it carries its own seq the same way.
type ctxItem struct {
	msg agentcore.Message
	seq int64
}

// injectedItemKeys are the Message.Metadata keys marking a user entry the
// runtime injected into a running turn instead of a person writing it:
// background task notices (metaTaskNotice), steering messages (metaSteer),
// undo facts (metaUndo) and the note an interrupted turn leaves behind
// (metaTurnInterrupted). Such an
// entry is stored with the turn's EntryUser type and replayed in place, so the
// turn view (items), the notices compose renders and the user requests
// (recentRequests) all have to tell it apart from the turn's own messages and
// requests.
var injectedItemKeys = []string{metaTaskNotice, metaSteer, metaTurnInterrupted, metaUndo}

// isInjectedItem reports whether m is an entry injected into a running turn
// (one of injectedItemKeys set true in its metadata).
func isInjectedItem(m agentcore.Message) bool {
	for _, key := range injectedItemKeys {
		if v, _ := m.Metadata[key].(bool); v {
			return true
		}
	}
	return false
}

// historyView is the persisted part of a session's context: the current
// segment's handoff package, its context snapshot, the adopted context updates
// and the message entries kept after its start.
type historyView struct {
	// summary is the handoff package of the current segment: the latest
	// adopted compaction summary, or the rendered --from handoff (segment 0;
	// fromHandoff set). It is also the previous package a later segment
	// break folds in, so --from content survives every later break.
	summary     string
	fromHandoff bool
	// segment is the context snapshot of the latest adopted segment record;
	// nil when that record has none (older sessions/binaries): resolveSegment
	// then fills in the legacy (meta) view.
	segment *segmentContext
	// updates are the adopted context updates written after that record, in
	// order; eff (set by resolveSegment) is segment overlaid with them.
	updates []contextUpdateRecord
	eff     *segmentContext
	// items are the kept message entries and replayed context updates.
	items []ctxItem
}

// compactionRecord is the JSON body of a compaction entry.
//
// first_kept_seq is the entries seq of the first message that stays in the
// context. Both turn-start and mid-turn compactions use it: the view of a later
// turn is "summary + message entries of completed turns with seq >= first_kept_seq",
// which equals the in-memory view because a turn's messages are persisted as
// they are produced. A cut after the very last message uses lastSeq+1.
//
// The same record is the segment start record (design D2): Reason, Context,
// RecentRequests and Refs are added fields; Summary always holds the full
// rendered handoff package ("" for a start record), so an older binary that
// decodes only the original fields still sees the whole context.
type compactionRecord struct {
	Summary      string `json:"summary"`
	FirstKeptSeq int64  `json:"first_kept_seq"`
	TokensBefore int    `json:"tokens_before"`
	TokensAfter  int    `json:"tokens_after"`

	Reason         string          `json:"reason,omitempty"`
	Context        *segmentContext `json:"context,omitempty"`
	RecentRequests []string        `json:"recent_requests,omitempty"`
	Refs           []string        `json:"refs,omitempty"`
}

// loadHistory rebuilds the persisted context of a session. Entries of completed
// turns count in full. A failed or cancelled turn contributes the prefix it
// managed to finish plus the note its finish left behind (design D1-D4 of
// docs/design/2026-10-15-interrupted-turn-replay-design.md): its side effects
// are in the workspace, so hiding its record made the next turn repeat or undo
// work it had already done (suga-y0sk). A compaction is adopted only when it was
// written by a completed turn (or outside any turn, i.e. a manual :compact), so
// a failed or cancelled turn's compaction never leaks into later turns. The same
// holds for context_update entries (design D3): adopted with their turn,
// replayed as user messages at their seq - a failed turn's update is recomputed
// by the next turn instead (see segment.go). An update written before the latest
// adopted segment record is superseded by that record's snapshot (it re-read
// every source) and is neither replayed nor applied.
func (r *Runner) loadHistory(ctx context.Context, sessionID string) (historyView, error) {
	turns, err := r.cfg.Repository.ListTurns(ctx, sessionID)
	if err != nil {
		return historyView{}, fmt.Errorf("list turns: %w", err)
	}
	done := make(map[string]bool, len(turns))
	// interrupted are the failed/cancelled turns whose replayable prefix has to be
	// computed below; a turn with no entries of its own simply contributes none.
	interrupted := make(map[string][]store.Entry)
	for _, t := range turns {
		switch t.Status {
		case store.TurnCompleted:
			done[t.ID] = true
		case store.TurnFailed, store.TurnCancelled:
			interrupted[t.ID] = nil
		}
	}
	entries, err := r.cfg.Repository.ListEntries(ctx, sessionID, store.EntryFilter{
		Types: []string{store.EntryUser, store.EntryAssistant, store.EntryTool, store.EntryCompaction, store.EntryHandoff, store.EntryContextUpdate},
		// The bodies of the entries before the current segment are never sent: they
		// are read back for the items only, once the view is known (below).
		NoHydrate: true,
	})
	if err != nil {
		return historyView{}, fmt.Errorf("load session history: %w", err)
	}
	replay := make(map[int64]bool)
	if len(interrupted) > 0 {
		for _, e := range entries {
			if _, ok := interrupted[e.TurnID]; ok {
				interrupted[e.TurnID] = append(interrupted[e.TurnID], e)
			}
		}
		for turnID, es := range interrupted {
			kept, _, _, rerr := replayablePrefix(es)
			if rerr != nil {
				return historyView{}, fmt.Errorf("replay interrupted turn %s: %w", turnID, rerr)
			}
			for _, e := range kept {
				replay[e.Seq] = true
			}
		}
	}
	var view historyView
	var firstKept, segmentSeq int64
	updates := map[int64]contextUpdateRecord{} // adopted updates by seq
	for _, e := range entries {                // ascending: the last adopted record wins
		switch {
		case e.Type == store.EntryContextUpdate && done[e.TurnID]:
			var rec contextUpdateRecord
			if err := json.Unmarshal(e.JSON, &rec); err != nil {
				return historyView{}, fmt.Errorf("decode context update entry %d: %w", e.Seq, err)
			}
			updates[e.Seq] = rec
			view.updates = append(view.updates, rec)
		case e.Type == store.EntryHandoff:
			// A --from handoff belongs to no turn: it is the handoff package of
			// segment 0 (not a history item), until a later segment record
			// replaces it with a package that folded it in.
			var rec handoffRecord
			if err := json.Unmarshal(e.JSON, &rec); err != nil {
				return historyView{}, fmt.Errorf("decode handoff entry %d: %w", e.Seq, err)
			}
			view.summary, view.fromHandoff = handoffMessage(rec).Content, true
		case e.Type == store.EntryCompaction && (e.TurnID == "" || done[e.TurnID]):
			var rec compactionRecord
			if err := json.Unmarshal(e.JSON, &rec); err != nil {
				return historyView{}, fmt.Errorf("decode compaction entry %d: %w", e.Seq, err)
			}
			// An older binary's record has no context: fall back to meta.
			view.segment, view.updates, segmentSeq = rec.Context, nil, e.Seq
			if rec.Reason == segmentReasonStart {
				continue // snapshot only: no package, keeps every message
			}
			view.summary, view.fromHandoff, firstKept = rec.Summary, false, rec.FirstKeptSeq
		}
	}
	// pending holds the items whose body has to come back from the session file and
	// pendingAt their index in view.items. Only those are read: an entry cut away
	// by the compaction point stays a stub, so a long session reads no more than its
	// current segment (design 2026-10-20, revision 2b).
	var pending []store.Entry
	var pendingAt []int
	for _, e := range entries {
		if e.Type == store.EntryCompaction || e.Type == store.EntryHandoff || e.Seq < firstKept {
			continue
		}
		if e.Type == store.EntryContextUpdate {
			// Adopted updates only (see the comment above): a failed turn's update is
			// recomputed by the next turn, so replaying its entry would duplicate it.
			if done[e.TurnID] && e.Seq > segmentSeq {
				view.items = append(view.items, ctxItem{msg: updates[e.Seq].message(), seq: e.Seq})
			}
			continue
		}
		if !done[e.TurnID] && !replay[e.Seq] {
			continue
		}
		view.items = append(view.items, ctxItem{seq: e.Seq})
		pending = append(pending, e)
		pendingAt = append(pendingAt, len(view.items)-1)
	}
	if err := r.cfg.Repository.HydrateEntries(ctx, sessionID, pending); err != nil {
		return historyView{}, fmt.Errorf("read the kept bodies: %w", err)
	}
	for i := range pending {
		// A stub that comes back from the store is a bug that would send the model a
		// message without its Reasoning, Metadata and tool call arguments: refuse to
		// build the view instead (design 2026-10-20, revision 2).
		if pending[i].Stub() {
			return historyView{}, fmt.Errorf("history entry %d is still a stub: refusing to send a body-less message", pending[i].Seq)
		}
		var m agentcore.Message
		if err := json.Unmarshal(pending[i].JSON, &m); err != nil {
			return historyView{}, fmt.Errorf("decode history entry %d: %w", pending[i].Seq, err)
		}
		view.items[pendingAt[i]].msg = m
	}
	return view, nil
}

// summaryMessage renders a compaction summary as a user message (the segment
// context message wraps its content, see segmentContextMessage).
func summaryMessage(summary string) agentcore.Message {
	return agentcore.Message{Role: "user", Content: "以下是此前对话的压缩摘要（早期消息已被省略）：\n\n" + summary}
}

// contextBuilder builds the model context of one turn: the segment context
// message (snapshot + handoff package) first, then kept history (with replayed
// context updates, this turn's included), the hook context, and this turn's
// messages. It runs before every model call (agentcore calls ContextBuilder in
// each step ahead of listTools/model), so the threshold check here also
// compacts a single very long turn. It never mutates state.Messages, and the
// system prompt stays the stable prefix for the whole segment: a segment
// break only switches what this builder renders.
type contextBuilder struct {
	r         *Runner
	sessionID string
	turnID    string
	prompt    agentcore.PromptBuilder
	usage     *usageCollector
	// hookContext is the turn's hook context (SystemContext: UserPromptSubmit/
	// SessionStart additional context, a Stop-wait reply). It is sent as a user
	// message for this turn only (never role=system), right before the turn.
	hookContext string

	summary     string // current segment's handoff package ("" = none)
	fromHandoff bool   // summary is a --from handoff (segment 0)
	// seg is the current segment's snapshot (rendered as the segment context
	// message); eff is it overlaid with the context updates in effect - the
	// base a segment break carries unreadable sources over from.
	seg, eff  *segmentContext
	prev      []ctxItem // kept history before this turn's own messages
	turnStart int       // index of the first kept message in state.Messages
	// turnItemsCache is this turn's merged view - state.Messages with the turn's
	// injected entries at their stored position (turnItems) - and turnMsgs the
	// number of in-memory messages it was built from. turnFrom is the index of
	// the first item of it the view still keeps: a segment cut of this turn may
	// drop the messages before turnStart and an injected entry that sits between
	// them, so a message index alone cannot express where the view starts. It is
	// filled lazily: the runtime only appends to state.Messages, so another
	// length invalidates the cache, and takeTaskNotices/takeSteers drop it when
	// they store an injected entry.
	turnItemsCache []ctxItem
	turnMsgs       int
	turnFrom       int

	window, reserve int
	truncLimit      int // tool-result truncation limit in chars; 0 = off

	fixedTokens    int // system prompt + tool schemas, computed once
	haveFixed      bool
	lastViewTokens int // tokens of the view sent in the latest model call
	haveView       bool

	// foldBase is the fold boundary already recorded in this segment (session
	// meta fold_boundary); foldPending is a larger boundary seen by the latest
	// compose, recorded by recordFold once the view is final.
	foldBase    int
	foldPending *foldStats

	// contextRecorded: this turn's context event (recordContext) is written.
	contextRecorded bool
	// injected are the entries injected into this turn while it ran - task
	// notices and steers - rendered at their place among this turn's messages:
	// they are stored entries and never part of state.Messages. A segment cut of
	// this turn drops the ones it cut away (see keptInjected).
	injected []turnInjected
	// steers is the session inbox of this turn (nil outside a turn, e.g. for a
	// between-turn :compact) and emit is the turn's event sink: takeSteers hands
	// the steers it delivers to the context through both.
	steers *steerInbox
	emit   EmitFunc
	// notices is the sink of the task notices this turn takes (finish puts them
	// back into the task manager when the turn does not complete, issue
	// suga-o26e); nil outside a turn.
	notices *turnNotices
}

func (r *Runner) newContextBuilder(sessionID, turnID string, hist historyView, prompt agentcore.PromptBuilder, usage *usageCollector, hookContext string) *contextBuilder {
	window, reserve := r.compactLimits()
	return &contextBuilder{
		r: r, sessionID: sessionID, turnID: turnID, prompt: prompt, usage: usage, hookContext: hookContext,
		summary: hist.summary, fromHandoff: hist.fromHandoff, prev: hist.items, seg: hist.segment, eff: hist.eff,
		window: window, reserve: reserve,
	}
}

// threshold is the estimated size above which compaction triggers.
func (b *contextBuilder) threshold() int {
	return thresholdOf(b.window, b.reserve)
}

// BuildMessages implements agentcore.ContextBuilder.
func (b *contextBuilder) BuildMessages(ctx context.Context, state *agentcore.RunState) ([]agentcore.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.takeTaskNotices(ctx, state)
	b.takeSteers(ctx, state)
	view := b.compose(state)
	// Image bytes are read back here (the store keeps references only): the
	// history and per-request rules run on the composed view, so the estimate
	// below counts what the request will actually carry.
	view = b.prepareImages(view)
	est := b.estimate(ctx, state, view)
	if est > b.threshold() {
		_, err := b.startSegment(ctx, state, segmentReasonThreshold, est)
		switch {
		case err == nil:
			view = b.compose(state)
			view = b.prepareImages(view)
			est = b.estimate(ctx, state, view)
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, ErrNothingToCompact):
			// Nothing cuttable (e.g. one huge tool result): truncation below.
		default:
			// A failed summary must not kill the turn while truncation may still fit.
			slog.Warn("compact context", "session_id", b.sessionID, "err", err)
		}
	}
	if est > b.threshold() {
		var err error
		if view, err = b.shrink(ctx, state, view, &est); err != nil {
			return nil, err
		}
	}
	b.lastViewTokens, b.haveView = tokensOfMessages(view), true
	b.recordFold(ctx)
	b.recordContext(ctx, state, est)
	return view, nil
}

// compose assembles the current view: segment context message, kept history
// (replayed context updates included), hook context, then the kept part of
// this turn's messages, with tool results truncated when needed. The segment
// context message and the hook context are rendered here only: items() (and
// so segmentCut/validCut) never sees them.
func (b *contextBuilder) compose(state *agentcore.RunState) []agentcore.Message {
	var turn []agentcore.Message
	if state != nil && b.turnStart < len(state.Messages) {
		turn = state.Messages[b.turnStart:]
	}
	out := make([]agentcore.Message, 0, len(b.prev)+len(turn)+2)
	if msg, ok := segmentContextMessage(b.seg, b.summary, b.fromHandoff); ok {
		out = append(out, msg)
	}
	for _, it := range b.prev {
		out = append(out, b.limited(it.msg))
	}
	if b.hookContext != "" {
		out = append(out, agentcore.Message{Role: "user", Content: b.hookContext})
	}
	// The injected entries gone from the list were cut away by a segment break
	// of this turn (see keptInjected); every remaining one is in the view, at its
	// place.
	next := 0 // the next injected entry to place
	for i, m := range turn {
		for ; next < len(b.injected) && b.injected[next].at <= b.turnStart+i; next++ {
			out = append(out, b.injected[next].msg)
		}
		out = append(out, b.limited(m))
	}
	for ; next < len(b.injected); next++ {
		out = append(out, b.injected[next].msg)
	}
	every, keep := b.r.cfg.FoldEvery, b.r.cfg.FoldKeep
	b.foldPending = nil
	boundary, steps := foldBoundary(out, every, keep)
	if boundary == 0 {
		return out
	}
	folded := foldHistory(out, every, keep)
	if boundary > b.foldBase {
		b.foldPending = &foldStats{boundary: boundary, steps: steps,
			charsBefore: charsOfMessages(out), charsAfter: charsOfMessages(folded)}
	}
	return folded
}

// limited returns a tool message with its content truncated to truncLimit; the
// stored entry (and state.Messages) keeps the full text.
func (b *contextBuilder) limited(m agentcore.Message) agentcore.Message {
	if b.truncLimit <= 0 || m.Role != "tool" {
		return m
	}
	if len(m.Content) > b.truncLimit {
		m.Content = truncateMiddle(m.Content, b.truncLimit)
	}
	if m.ToolResult != nil && len(m.ToolResult.Content) > b.truncLimit {
		res := *m.ToolResult
		res.Content = truncateMiddle(res.Content, b.truncLimit)
		m.ToolResult = &res
	}
	return m
}

// estimate approximates the tokens of the next model request: the previous call's
// reported input_tokens plus the tokens added since that call's view, or - before
// any call - system prompt + tool schemas + view. Both parts count tokens, not
// bytes (tokensOfMessages): four ASCII bytes are a token, a CJK character is one.
func (b *contextBuilder) estimate(ctx context.Context, state *agentcore.RunState, view []agentcore.Message) int {
	tokens := tokensOfMessages(view)
	if b.usage != nil && b.haveView {
		if last, ok := b.usage.lastInputTokens(); ok {
			return max(last+(tokens-b.lastViewTokens), 0)
		}
	}
	return b.fixed(ctx, state) + tokens
}

// fixed estimates the tokens the request carries besides the messages: the system
// prompt and the tool schemas, computed once per segment.
func (b *contextBuilder) fixed(ctx context.Context, state *agentcore.RunState) int {
	if b.haveFixed {
		return b.fixedTokens
	}
	b.haveFixed = true
	if b.prompt != nil && state != nil {
		if s, err := b.prompt.BuildSystemPrompt(ctx, state); err == nil {
			b.fixedTokens += tokensOfText(s)
		}
	}
	if b.r.cfg.Tools != nil {
		if schemas, err := b.r.cfg.Tools.ListTools(ctx); err == nil {
			if raw, err := json.Marshal(schemas); err == nil {
				b.fixedTokens += tokensOfText(string(raw))
			}
		}
	}
	return b.fixedTokens
}

// truncation ladder for tool results: 16000 chars, halved down to 2000.
var truncateLadder = []int{16000, 8000, 4000, 2000}

// shrink truncates tool results (head+tail kept, UTF-8 safe) level by level
// until the view fits. It records a compaction error event; if even the
// smallest level does not fit it fails the turn with ErrContextOverflow.
func (b *contextBuilder) shrink(ctx context.Context, state *agentcore.RunState, view []agentcore.Message, est *int) ([]agentcore.Message, error) {
	before := *est
	for _, limit := range truncateLadder {
		if b.truncLimit != 0 && limit >= b.truncLimit {
			continue // already applied in a previous step
		}
		b.truncLimit = limit
		view = b.compose(state)
		*est = b.estimate(ctx, state, view)
		if *est <= b.threshold() {
			break
		}
	}
	fits := *est <= b.threshold()
	e := store.Event{
		Kind: store.EventCompaction, Name: "truncate", Status: eventError,
		Error: fmt.Sprintf("context estimate %d tokens exceeds threshold %d after compaction; tool results truncated to %d chars", before, b.threshold(), b.truncLimit),
		Attrs: map[string]any{"tokens_before": before, "tokens_after": *est, "truncate_limit": b.truncLimit, "fits": fits},
	}
	b.r.rec.record(ctx, e)
	if !fits {
		return nil, fmt.Errorf("%w (estimate %d, threshold %d)", ErrContextOverflow, *est, b.threshold())
	}
	return view, nil
}

// truncateMiddle keeps the head and tail of s within about limit bytes,
// cutting only on UTF-8 rune boundaries.
func truncateMiddle(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	half := limit / 2
	head := half
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	tail := len(s) - half
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return fmt.Sprintf("%s\n...[truncated %d chars]...\n%s", s[:head], tail-head, s[tail:])
}

// charsOfMessages sums the model-visible size of messages.
func charsOfMessages(ms []agentcore.Message) int {
	n := 0
	for _, m := range ms {
		n += charsOfMessage(m)
	}
	return n
}

func charsOfMessage(m agentcore.Message) int {
	// Reasoning is sent back to thinking-mode APIs, so it occupies the window too.
	n := len(m.Content) + len(m.Reasoning)
	if m.ToolResult != nil && len(m.ToolResult.Content) > n {
		n = len(m.ToolResult.Content) // tool messages carry the result twice; count once
	}
	if m.ToolCall != nil && m.Role != "tool" {
		n += len(m.ToolCall.Name)
		if raw, err := json.Marshal(m.ToolCall.Arguments); err == nil {
			n += len(raw)
		}
	}
	return n
}
