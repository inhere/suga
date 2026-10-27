package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// Handoff package (design D4, "交接包模板"): the model-written body (see
// compactTemplate), then the recent user requests verbatim, the steering
// messages a person typed into the turn that is being cut (design D10) and the
// current plan verbatim. The rendered text is what the segment record's Summary
// holds and what the segment context message shows.
const (
	// recentRequestCount is how many recent user requests a package carries (Q5).
	recentRequestCount = 3
	// requestTextLimit caps one request's text (bytes, head+tail kept): a
	// pasted log must not blow up every request of the new segment.
	requestTextLimit = 4000
	// shortRequestRunes: shorter requests ("继续", "go on", "ok") say nothing
	// about the task and are skipped.
	shortRequestRunes = 6

	recentRequestsHeading = "## 最近的用户请求（原文）"
	turnSteersHeading     = "## 本回合插话（原文）"
	packagePlanHeading    = "## 当前计划（原文）"
)

// continueWords are "go on" style requests of 6+ characters that are skipped
// like the short ones (compared lower-cased, trailing punctuation trimmed).
var continueWords = map[string]bool{
	"continue": true, "go ahead": true, "keep going": true, "proceed": true, "carry on": true,
	"继续吧": true, "继续继续": true, "请继续": true, "接着做": true, "继续执行": true,
}

// metaInjected marks (Message.Metadata, never sent by providers) a turn's
// user message that was not typed by a person: RunRequest.Injected - a Stop
// hook block reason, a socket delivery or `suga run --injected`. It is the
// persisted form of that existing marker; recent requests prefer the others.
const metaInjected = "suga.injected"

// segmentPackage is a built handoff package.
type segmentPackage struct {
	requests []string // recent user requests, oldest first
	refs     []string // refs the model picked (format-checked)
	rendered string   // body + recent requests + plan
}

// buildPackage builds the handoff package of a segment break: items is the
// closing view and items[:cut] the part leaving the raw context; previous is
// the current segment's package; turnID ("" between turns) lets the current
// turn's request count before the turn completes - and lets the package name
// the steering messages of that turn, which are not requests of it (D10).
// rebuilt is the request the main loop is about to send for the step of the
// break (nil between turns): the reuse path continues it when this process has
// sent no request yet.
func (r *Runner) buildPackage(ctx context.Context, sessionID, turnID, previous string, items []ctxItem, cut, threshold int, scenario string, rebuilt *agentcore.GenerateRequest) (segmentPackage, error) {
	requests, err := r.recentRequests(ctx, sessionID, turnID, recentRequestCount)
	if err != nil {
		return segmentPackage{}, err
	}
	steers, err := r.turnSteers(ctx, sessionID, turnID)
	if err != nil {
		return segmentPackage{}, err
	}
	plan := r.latestPlan(ctx, sessionID)
	cands := refCandidates(sessionID, items, cut)
	in := packageInput{previous: previous, dropped: items[:cut], plan: plan, requests: requests, candidates: cands}
	// The handoff call may continue the segment's last main-loop request
	// (segment_handoff_reuse.go): tail is what the new segment keeps verbatim.
	in.reuse, in.reuseFallback = r.prepareHandoffReuse(sessionID, scenario, len(items)-cut, rebuilt)
	body, err := r.summarize(ctx, scenario, in, threshold)
	if err != nil {
		return segmentPackage{}, err
	}
	return segmentPackage{
		requests: requests,
		refs:     parseRefs(body, sessionID, cands),
		rendered: renderPackage(body, requests, steers, plan),
	}, nil
}

// latestPlan returns the session's latest plan entry JSON ("" = none).
func (r *Runner) latestPlan(ctx context.Context, sessionID string) string {
	if e, err := r.cfg.Repository.LatestEntry(ctx, sessionID, store.EntryPlan); err == nil && e != nil && len(e.JSON) > 0 {
		return string(e.JSON)
	}
	return ""
}

// recentRequests returns up to n recent user requests (original text, oldest
// first) of the session's adopted turns plus turnID (the turn in progress,
// whose messages are stored but not yet adopted). Filler requests ("继续",
// "go on", anything under 6 characters) are skipped; injected ones (Stop hook
// replies, deliveries) are skipped too while typed ones remain, and only fill
// up the list when fewer than n typed requests exist. Entries injected into a
// running turn (task notices, later steers) are never requests: they are part
// of the turn's history, not something a person asked for.
func (r *Runner) recentRequests(ctx context.Context, sessionID, turnID string, n int) ([]string, error) {
	turns, err := r.cfg.Repository.ListTurns(ctx, sessionID, store.TurnCompleted)
	if err != nil {
		return nil, fmt.Errorf("list completed turns: %w", err)
	}
	done := make(map[string]bool, len(turns)+1)
	for _, t := range turns {
		done[t.ID] = true
	}
	if turnID != "" {
		done[turnID] = true
	}
	entries, err := r.cfg.Repository.ListEntries(ctx, sessionID, store.EntryFilter{Types: []string{store.EntryUser}})
	if err != nil {
		return nil, fmt.Errorf("list user requests: %w", err)
	}
	type request struct {
		seq  int64
		text string
	}
	var typed, injected []request
	for i := len(entries) - 1; i >= 0 && len(typed) < n; i-- { // newest first
		e := entries[i]
		if !done[e.TurnID] {
			continue
		}
		var m agentcore.Message
		if err := json.Unmarshal(e.JSON, &m); err != nil || m.Role != "user" || isContextUpdate(m) || isSegmentContext(m) || isInjectedItem(m) {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if isFillerRequest(text) {
			continue
		}
		req := request{seq: e.Seq, text: truncateMiddle(text, requestTextLimit)}
		if v, _ := m.Metadata[metaInjected].(bool); v {
			injected = append(injected, req)
			continue
		}
		typed = append(typed, req)
	}
	for _, req := range injected {
		if len(typed) >= n {
			break
		}
		typed = append(typed, req)
	}
	sort.Slice(typed, func(i, j int) bool { return typed[i].seq < typed[j].seq })
	out := make([]string, len(typed))
	for i, req := range typed {
		out[i] = req.text
	}
	return out, nil
}

// isFillerRequest reports a request that carries no task content: under
// shortRequestRunes characters, or a known "go on" phrase.
func isFillerRequest(text string) bool {
	t := strings.ToLower(strings.TrimRightFunc(strings.TrimSpace(text), func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	}))
	return len([]rune(t)) < shortRequestRunes || continueWords[t]
}

// refCandidate is a ref the summarizer may pick for the package.
type refCandidate struct {
	ref, note string
}

// Candidate list bounds: externalised outputs of the closing view, then the
// most recent user/final-answer messages leaving the raw context.
const (
	maxOutputCandidates  = 12
	maxMessageCandidates = 8
	candidateNoteRunes   = 80
)

// refCandidates lists the refs offered to the summarizer: the externalised
// tool outputs (out:<sid>:<hex>) anywhere in the closing view, and message
// refs (<sid>/<seq>) of the latest user requests and final answers among the
// dropped items (the kept tail stays verbatim and needs no ref).
func refCandidates(sessionID string, items []ctxItem, cut int) []refCandidate {
	var outs, msgs []refCandidate
	for i := len(items) - 1; i >= 0; i-- {
		m := items[i].msg
		if m.ToolResult != nil && len(outs) < maxOutputCandidates {
			if info, ok := tools.OutputInfoOf(m.ToolResult.Metadata); ok && info.Ref != "" {
				outs = append(outs, refCandidate{ref: info.Ref,
					note: fmt.Sprintf("[tool %s] 输出 %d 字节 / %d 行", m.ToolResult.Name, info.Bytes, info.Lines)})
			}
		}
		if i >= cut || items[i].seq <= 0 || len(msgs) >= maxMessageCandidates || isContextUpdate(m) {
			continue
		}
		if m.Role == "user" || (m.Role == "assistant" && m.ToolCall == nil && strings.TrimSpace(m.Content) != "") {
			preview := truncateRunes(strings.Join(strings.Fields(m.Content), " "), candidateNoteRunes)
			// A steering message is not the person's request of the turn: it is
			// labeled as what it is (design D10).
			label := "[" + m.Role + "] "
			if isSteerItem(m) {
				label = steerLabel(m) + " "
			}
			msgs = append(msgs, refCandidate{ref: messageRef(sessionID, items[i].seq), note: label + preview})
		}
	}
	return append(outs, msgs...)
}

// refToken finds ref-shaped tokens; parseRefs validates each one.
var refToken = regexp.MustCompile(`out:[A-Za-z0-9._-]+:[0-9a-f]{16}|[A-Za-z0-9._-]+/[0-9]+`)

// parseRefs extracts the refs the model listed in the package's refs section
// (refsHeading). Output refs are kept when they pass the output ref format;
// message refs when they pass the message ref format and are either offered
// candidates or refs into this session (a bare "dir/123" path is neither).
func parseRefs(body, sessionID string, cands []refCandidate) []string {
	offered := make(map[string]bool, len(cands))
	for _, c := range cands {
		offered[c.ref] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, tok := range refToken.FindAllString(markdownSection(body, refsHeading), -1) {
		if seen[tok] {
			continue
		}
		if strings.HasPrefix(tok, outputRefPrefix) {
			if _, _, err := parseOutputRef(tok); err != nil {
				continue
			}
		} else if sid, _, err := parseMessageRef(tok); err != nil || (!offered[tok] && sid != sessionID) {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

// markdownSection returns the lines under the first "## " heading starting
// with heading, up to the next "## " heading ("" when absent).
func markdownSection(body, heading string) string {
	var sb strings.Builder
	in := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if in {
				break
			}
			in = strings.HasPrefix(trimmed, heading)
			continue
		}
		if in {
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}

// renderRequests renders requests as a numbered list (continuation lines
// indented so a multi-line request stays one item).
func renderRequests(requests []string) string {
	var sb strings.Builder
	for i, req := range requests {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, strings.ReplaceAll(req, "\n", "\n   "))
	}
	return sb.String()
}

// renderPackage renders the full handoff package: the model-written body, the
// recent requests verbatim, the steering messages of the turn being cut
// (design D10) and the plan verbatim (when there is one).
func renderPackage(body string, requests, steers []string, plan string) string {
	parts := []string{strings.TrimSpace(body)}
	if len(requests) > 0 {
		parts = append(parts, recentRequestsHeading+"\n"+strings.TrimRight(renderRequests(requests), "\n"))
	}
	if len(steers) > 0 {
		parts = append(parts, turnSteersHeading+"\n"+strings.TrimRight(renderRequests(steers), "\n"))
	}
	if p := renderPlan(plan); p != "" {
		parts = append(parts, packagePlanHeading+"\n"+p)
	}
	return strings.Join(parts, "\n\n")
}

// renderPlan renders an update_plan entry as status-marked steps (the raw
// JSON when it does not decode).
func renderPlan(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var plan native.Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil || len(plan.Steps) == 0 {
		return raw
	}
	var lines []string
	if s := strings.TrimSpace(plan.Explanation); s != "" {
		lines = append(lines, s)
	}
	for _, st := range plan.Steps {
		lines = append(lines, "- "+planSymbol(st.Status)+" "+st.Step)
	}
	return strings.Join(lines, "\n")
}

// injectedStorage tags the instruction message of an injected turn with
// metaInjected as it is persisted (the runtime builds that message itself).
// Only the first user message of the run is tagged; the in-memory copy is
// untouched, so the request sent this turn is unchanged.
type injectedStorage struct {
	agentcore.Storage
	tagged bool
}

func (s *injectedStorage) AppendMessages(ctx context.Context, runID string, msgs ...agentcore.Message) error {
	if !s.tagged {
		for i, m := range msgs {
			if m.Role != "user" {
				continue
			}
			cp := append([]agentcore.Message(nil), msgs...)
			md := make(map[string]any, len(m.Metadata)+1)
			for k, v := range m.Metadata {
				md[k] = v
			}
			md[metaInjected] = true
			cp[i].Metadata = md
			msgs, s.tagged = cp, true
			break
		}
	}
	return s.Storage.AppendMessages(ctx, runID, msgs...)
}
