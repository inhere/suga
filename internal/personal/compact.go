package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// Compaction defaults (tokens). Config can override them (mainly for tests).
const (
	defaultContextWindow = 128000
	defaultReserveTokens = 16000
)

// ErrNothingToCompact means no legal cut point exists that would drop
// anything: the segment break is skipped (:compact reports it, the idle
// consolidation simply does nothing).
var ErrNothingToCompact = errors.New("nothing to compact")

// compactSystemPrompt is the fixed handoff-package instruction; tests match on
// it to tell summary calls from ordinary turns. The last sentence carries the
// three hard constraints of a summary (design "交接包模板"): they belong to the
// instruction, not to the section template the model writes out.
const compactSystemPrompt = "你是会话交接助手。把给定的对话历史整理成一份交接包，供同一个智能体在新的一段对话里继续当前任务。" +
	"只输出交接包正文，不要寒暄，不要调用工具。必须保留所有对继续当前任务有用的事实、路径、命令、标识符和数值；" +
	"引用只能从给出的候选 ref 中挑选并原样写出。硬约束：给出的路径、命令、标识符与数值一律原样保留，不得改写或省略；" +
	"计划里没有真正执行过的部分不得写成已执行；局部搜索结果（某一个目录、某一次查询）不得夸大为“不存在”“没有实现”这类全局结论。"

// refsHeading heads the handoff section the refs are parsed from.
const refsHeading = "## 与当前任务相关的引用"

// compactTemplate is the fixed handoff package structure (design "交接包模板"):
// the sections are fixed so a summary can be located by section and searched,
// and the last one is refsHeading, which parseRefs reads the refs back from.
// The plan is appended by renderPackage, so the template has no plan section.
const compactTemplate = `请按下列固定结构输出交接包正文（没有内容的小节写“无”）：
## 当前任务（最近的用户请求原文会附在交接包里，不必复述）
## 坐标（文件 / 符号 / 分支 / 版本）
## 采纳与否决
## 验证到哪
## 进展与阻塞
## 下一步
` + refsHeading + `（ref + 一句话说明，可用 recall 读取）`

// compactLimits resolves the window and reserve budgets (tokens).
//
// The reserve scales with the window so small windows stay usable:
//
//	reserve   = min(16000, window*20%)
//	threshold = window - reserve        (window/2 when the reserve eats it all)
//
// A segment break keeps only the last complete tool call/result group
// verbatim (design Q2, see segmentCut), so the new segment starts far below
// the threshold whatever the window. An explicit CompactReserveTokens wins.
func (r *Runner) compactLimits() (window, reserve int) {
	window, reserve = r.cfg.ContextWindow, r.cfg.CompactReserveTokens
	if window <= 0 {
		window = defaultContextWindow
	}
	if reserve <= 0 {
		reserve = min(defaultReserveTokens, window/5)
	}
	return window, reserve
}

// thresholdOf is the estimated size above which compaction triggers.
func thresholdOf(window, reserve int) int {
	if t := window - reserve; t > 0 {
		return t
	}
	return window / 2
}

// validCut reports whether a view may start at items[i] (0 < i < len): either
// right before a user message, or right after a complete tool call/result pair
// (items[i-1] is a tool result and items[i] is not another result). It never
// separates an assistant{ToolCall} from its tool{ToolResult}; with parallel
// calls recorded as A1,T1,A2,T2 a cut between T1 and A2 is fine.
func validCut(items []ctxItem, i int) bool {
	cur, prev := items[i].msg, items[i-1].msg
	if cur.Role == "user" {
		return true
	}
	return prev.Role == "tool" && cur.Role != "tool"
}

// validEnd reports whether everything may be dropped (cut == len): the last
// message must close a pair or be a final assistant answer, never a user
// request that has not been answered.
func validEnd(items []ctxItem) bool {
	if len(items) == 0 {
		return false
	}
	last := items[len(items)-1].msg
	return last.Role == "tool" || (last.Role == "assistant" && last.ToolCall == nil)
}

// segmentCut returns the index of the first item the new segment keeps
// verbatim, or 0 when nothing can be dropped (design Q2: only the last
// complete tool call/result group stays; the rest lives on in the handoff
// package and its refs). Cut legality is always validCut's:
//
//   - when the view ends with a tool result (work in progress, mid-turn), the
//     group is the last assistant{ToolCall} with its result; the cut is the
//     nearest legal one at or before its start, so it is kept whole (with
//     A1,T1,A2,T2 that is A2,T2; a group right after its user request keeps
//     that request too);
//   - otherwise the least legal tail is kept: nothing at all when allowEnd (a
//     break between turns) and the view ends with a final answer, else a
//     pending user request alone (or the last exchange).
func segmentCut(items []ctxItem, allowEnd bool) int {
	n := len(items)
	if n > 0 && items[n-1].msg.Role == "tool" {
		start := n - 1
		if start > 0 && items[start-1].msg.Role == "assistant" && items[start-1].msg.ToolCall != nil {
			start--
		}
		for i := start; i >= 1; i-- {
			if validCut(items, i) {
				return i
			}
		}
		return 0 // the group reaches back to the first item: nothing to drop
	}
	if allowEnd && validEnd(items) {
		return n
	}
	for i := n - 1; i >= 1; i-- {
		if validCut(items, i) {
			return i
		}
	}
	return 0
}

// items lists the current view's items: the kept history, then this turn's
// kept messages with their entries seq. The entries injected into this turn
// (task notices, later steers; see isInjectedItem) are items of the view too,
// at the position they were stored in: a segment cut splits them with
// everything else, so an injected entry before the cut is summarized instead
// of being dropped unnoticed (issue suga-0lxy).
func (b *contextBuilder) items(ctx context.Context, state *agentcore.RunState) ([]ctxItem, error) {
	out := append([]ctxItem(nil), b.prev...)
	if state == nil {
		return out, nil
	}
	turn, err := b.turnItems(ctx, state)
	if err != nil {
		return nil, err
	}
	return append(out, turn...), nil
}

// turnItems returns this turn's items that are still in the view, from the
// first item the view keeps on (turnFrom). The turn's entries are listed
// lazily and cached: the runtime only appends to state.Messages, so another
// message count reloads them, and takeTaskNotices/takeSteers drop the cache
// when they store an injected entry. The stored form of a turn is append-only,
// so the merged layout (and with it turnFrom) stays valid across a reload.
func (b *contextBuilder) turnItems(ctx context.Context, state *agentcore.RunState) ([]ctxItem, error) {
	if b.turnItemsCache == nil || b.turnMsgs != len(state.Messages) {
		entries, err := b.r.cfg.Repository.ListEntries(ctx, b.sessionID, store.EntryFilter{
			TurnID: b.turnID, Types: []string{store.EntryUser, store.EntryAssistant, store.EntryTool},
			// Only the metadata head of an injected entry is decoded and the turn's
			// messages come from memory: an assistant stub keeps its Content and a tool
			// stub its call id, so no body is read back here.
			NoHydrate: true,
		})
		if err != nil {
			return nil, fmt.Errorf("list turn entries: %w", err)
		}
		merged, err := mergeTurnItems(b.turnID, state.Messages, entries)
		if err != nil {
			return nil, err
		}
		b.turnItemsCache, b.turnMsgs = merged, len(state.Messages)
	}
	if b.turnFrom >= len(b.turnItemsCache) {
		return nil, nil
	}
	return b.turnItemsCache[b.turnFrom:], nil
}

// turnEntryHead decodes only what is needed to classify a stored turn entry.
// The field name matches the stored metadata key whatever its case (encoding/
// json matches field names case-insensitively), and the possibly huge content
// and tool result of an ordinary entry are skipped rather than decoded: their
// in-memory copies are used instead.
type turnEntryHead struct {
	Metadata map[string]any
}

// mergeTurnItems merges a turn's stored entries with its in-memory messages:
// the entries hold the turn's messages plus the entries injected into the
// running turn (isInjectedItem), all under the message entry types. Every
// non-injected entry lines up with the next in-memory message (the runtime
// stores a message right after producing it); an injected entry takes its
// stored position in the view.
func mergeTurnItems(turnID string, msgs []agentcore.Message, entries []store.Entry) ([]ctxItem, error) {
	out := make([]ctxItem, 0, len(entries))
	stored := 0 // message entries seen, injected ones excluded
	for _, e := range entries {
		msg, injected, err := decodeTurnEntry(e)
		if err != nil {
			return nil, err
		}
		if injected {
			out = append(out, ctxItem{msg: msg, seq: e.Seq})
			continue
		}
		if stored < len(msgs) {
			out = append(out, ctxItem{msg: msgs[stored], seq: e.Seq})
		}
		stored++
	}
	if stored != len(msgs) {
		return nil, fmt.Errorf("turn %s has %d stored messages but %d in memory", turnID, stored, len(msgs))
	}
	return out, nil
}

// decodeTurnEntry classifies one stored message entry: injected entries are
// returned in full, ordinary ones are only recognized as such (the caller uses
// the in-memory message).
func decodeTurnEntry(e store.Entry) (agentcore.Message, bool, error) {
	var head turnEntryHead
	if err := json.Unmarshal(e.JSON, &head); err != nil {
		return agentcore.Message{}, false, fmt.Errorf("decode turn entry %d: %w", e.Seq, err)
	}
	if !isInjectedItem(agentcore.Message{Metadata: head.Metadata}) {
		return agentcore.Message{}, false, nil
	}
	var m agentcore.Message
	if err := json.Unmarshal(e.JSON, &m); err != nil {
		return m, false, fmt.Errorf("decode turn entry %d: %w", e.Seq, err)
	}
	return m, true, nil
}

// countTurnMessages counts the items of a turn slice that are in-memory
// messages (an injected entry has no index in state.Messages).
func countTurnMessages(items []ctxItem) int {
	n := 0
	for _, it := range items {
		if !isInjectedItem(it.msg) {
			n++
		}
	}
	return n
}

// keptInjected returns the injected entries a cut of this turn leaves in the
// view: every entry whose stored entry is at or after firstKeptSeq, i.e. every
// entry that is not part of the cut prefix (which goes into the summary). An
// entry whose entry could not be stored (seq 0) was never part of the cut.
func keptInjected(injected []turnInjected, firstKeptSeq int64) []turnInjected {
	out := make([]turnInjected, 0, len(injected))
	for _, n := range injected {
		if n.seq <= 0 || n.seq >= firstKeptSeq {
			out = append(out, n)
		}
	}
	return out
}

type compactOutcome struct {
	tokensBefore, tokensAfter int
	firstKeptSeq              int64
	segment                   int // ordinal of the new segment record
}

// eventCancelled is the status of a compaction event whose segment break was
// cancelled before its commit point (not an error).
const eventCancelled = "cancelled"

// compactTrigger maps a segment reason to the compaction event's legacy
// trigger attribute (auto/manual/idle).
func compactTrigger(reason string) string {
	if reason == segmentReasonThreshold {
		return "auto"
	}
	return reason
}

// startSegment is the single segment break entry (design "段开始"): the
// threshold check of BuildMessages (mid-turn), the manual :compact and the
// idle consolidation all come here. It builds the handoff package from the
// part of the view that leaves the raw context, freezes a fresh context
// snapshot, writes the segment start record and switches this builder to the
// new segment. before is the caller's token estimate of the current view.
func (b *contextBuilder) startSegment(ctx context.Context, state *agentcore.RunState, reason string, before int) (*compactOutcome, error) {
	items, err := b.items(ctx, state)
	if err != nil {
		return nil, err
	}
	// A threshold break runs mid-turn and must keep the pending request;
	// the between-turn breaks (manual, idle) may drop everything.
	cut := segmentCut(items, reason != segmentReasonThreshold)
	if cut == 0 {
		return nil, ErrNothingToCompact
	}
	started := time.Now()
	trigger := compactTrigger(reason)
	// fail records why no segment started. A cancellation (preempted idle
	// consolidation, interrupted turn) is not an error: status cancelled.
	fail := func(err error) (*compactOutcome, error) {
		e := store.Event{
			Kind: store.EventCompaction, Name: "compact", Status: eventError, Error: err.Error(),
			StartedAt: started, DurationMs: time.Since(started).Milliseconds(),
			Attrs: map[string]any{"trigger": trigger, "reason": reason, "tokens_before": before},
		}
		if errors.Is(err, context.Canceled) {
			e.Status, e.Error = eventCancelled, ""
		}
		b.r.rec.record(ctx, e)
		return nil, err
	}
	pkg, err := b.r.buildPackage(ctx, b.sessionID, b.turnID, b.summary, items, cut, b.threshold())
	if err != nil {
		return fail(err)
	}
	firstKept := items[len(items)-1].seq + 1
	if cut < len(items) {
		firstKept = items[cut].seq
	}

	// The new segment's context snapshot: every source re-read now (rules,
	// memory index, environment, skills, deferred tools; design "段开始"). What
	// cannot be read is carried over from the effective context of the closing
	// segment. It is frozen in the record; meta only caches it.
	seg, serr := b.r.freshSnapshot(ctx, b.sessionID, b.eff)
	if serr != nil {
		slog.Warn("re-read rules for the new segment; keeping the previous rules", "session_id", b.sessionID, "err", serr)
	}

	// The segment record is the single commit point (design D5). Cancelled
	// before it: nothing is written (no record, no meta), only a cancelled
	// event. From the write on the break completes regardless of ctx.
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	// Switch the view: cut inside the kept history drops its head; a cut inside
	// this turn drops all history and every item before it (the injected entries
	// included: they went into the summary through buildPackage), and turnStart
	// follows the first kept message. The new package is the new segment's
	// handoff (it folded the previous one in), and the new snapshot supersedes
	// every earlier context update (loadHistory skips updates before the latest
	// record the same way).
	oldSummary, oldHandoff, oldPrev, oldFrom, oldStart, oldSeg, oldEff := b.summary, b.fromHandoff, b.prev, b.turnFrom, b.turnStart, b.seg, b.eff
	oldInjected := b.injected
	tokensBefore := tokensOfMessages(b.compose(state))
	b.summary, b.fromHandoff = pkg.rendered, false
	if cut <= len(oldPrev) {
		b.prev = withoutContextUpdates(oldPrev[cut:])
	} else {
		// An injected entry among the dropped items has no message index, so it
		// advances turnFrom but not turnStart.
		dropped := items[len(oldPrev):cut]
		b.prev, b.turnFrom = nil, oldFrom+len(dropped)
		b.turnStart = oldStart + countTurnMessages(dropped)
		b.injected = keptInjected(oldInjected, firstKept)
	}
	eff := *seg
	b.seg, b.eff = seg, &eff
	after := max(before-(tokensBefore-tokensOfMessages(b.compose(state))), 0)

	// Summary holds the full rendered package, so an older binary decoding
	// only the original fields still sees everything this one renders.
	rec := compactionRecord{Summary: pkg.rendered, FirstKeptSeq: firstKept, TokensBefore: before, TokensAfter: after,
		Reason: reason, Context: seg, RecentRequests: pkg.requests, Refs: pkg.refs}
	raw, _ := json.Marshal(rec)
	wctx := context.WithoutCancel(ctx)
	es, err := b.r.cfg.Repository.AppendEntries(wctx, b.sessionID, store.NewEntry{TurnID: b.turnID, Type: store.EntryCompaction, JSON: raw})
	if err != nil {
		b.summary, b.fromHandoff, b.prev, b.turnFrom, b.turnStart, b.seg, b.eff = oldSummary, oldHandoff, oldPrev, oldFrom, oldStart, oldSeg, oldEff
		b.injected = oldInjected
		return fail(fmt.Errorf("write compaction entry: %w", err))
	}
	segment := b.r.segmentOrdinal(wctx, b.sessionID, es)
	b.r.rec.record(wctx, store.Event{
		Kind: store.EventCompaction, Name: "compact", Status: eventOK,
		StartedAt: started, DurationMs: time.Since(started).Milliseconds(),
		Attrs: map[string]any{"trigger": trigger, "reason": reason, "tokens_before": before, "tokens_after": after,
			"first_kept_seq": firstKept, "dropped_messages": cut, "model": b.r.compactModelName(),
			"segment": segment, "requests": len(pkg.requests), "refs": len(pkg.refs)},
	})
	// The meta cache follows the record (best-effort). The system prompt is
	// the stable prefix and does not change: this very step already renders
	// the new snapshot through compose.
	b.r.cacheSegmentMeta(wctx, b.sessionID, seg) // also resets the fold baseline
	b.foldBase, b.foldPending = 0, nil
	return &compactOutcome{tokensBefore: before, tokensAfter: after, firstKeptSeq: firstKept, segment: segment}, nil
}

// segmentOrdinal is the segment number of the record just written: its
// position among the session's segment (compaction) records, 1-based. 0 when
// it cannot be counted (trace then omits it).
func (r *Runner) segmentOrdinal(ctx context.Context, sessionID string, written []store.Entry) int {
	if len(written) == 0 {
		return 0
	}
	es, err := r.cfg.Repository.ListEntries(ctx, sessionID, store.EntryFilter{Types: []string{store.EntryCompaction}})
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range es {
		if e.Seq <= written[0].Seq {
			n++
		}
	}
	return n
}

// compactProvider is the model used for summaries: llm.compact_model when
// configured, otherwise the turn's own (already observed) provider.
// generateQuietly asks for a whole answer that nobody watches arrive (a
// summary): through Stream when the provider streams, with the deltas
// dropped, else through Generate. Some gateways (an OpenAI Responses proxy in
// front of a subscription model) accept only stream=true requests, so a plain
// Generate failed every compaction and --from handoff on them.
func generateQuietly(ctx context.Context, p agentcore.Provider, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	if sp, ok := p.(agentcore.StreamingProvider); ok {
		return sp.Stream(ctx, req, func(context.Context, agentcore.StreamEvent) error { return nil })
	}
	return p.Generate(ctx, req)
}

func (r *Runner) compactProvider() agentcore.Provider {
	if r.cfg.CompactProvider != nil {
		return r.cfg.CompactProvider
	}
	return r.cfg.Provider
}

func (r *Runner) compactModelName() string {
	if r.cfg.CompactProvider != nil && r.cfg.CompactModel != "" {
		return r.cfg.CompactModel
	}
	return r.cfg.Model
}

// packageInput is what a handoff package is summarized from.
type packageInput struct {
	previous   string         // the current segment's package ("" = none)
	dropped    []ctxItem      // the part of the view leaving the raw context
	plan       string         // latest plan entry JSON ("" = none)
	requests   []string       // recent user requests, original text, oldest first
	candidates []refCandidate // refs the model may pick from
}

// summarize asks the model for the handoff package body (compactTemplate) of
// in.dropped, folding in the previous package and the latest plan, pointed at
// the current task by the recent requests and offered the candidate refs. The
// transcript is size-capped so the request itself fits the window. Context
// updates are skipped: the next segment re-reads rules and the rest into its
// own snapshot.
func (r *Runner) summarize(ctx context.Context, in packageInput, threshold int) (string, error) {
	dropped := withoutContextUpdates(in.dropped)
	var sb strings.Builder
	sb.WriteString(compactTemplate)
	if in.previous != "" {
		sb.WriteString("\n\n# 已有摘要（需并入新摘要）\n" + in.previous)
	}
	plan := in.plan
	if plan == "" {
		plan = "无"
	}
	sb.WriteString("\n\n# 当前计划（原文会附在交接包里；“当前计划”一节写清进度即可）\n" + plan)
	if len(in.requests) > 0 {
		sb.WriteString("\n\n# 最近的用户请求（原文会附在交接包里；“当前任务”一节据此概括）\n")
		sb.WriteString(renderRequests(in.requests))
	}
	if len(in.candidates) > 0 {
		sb.WriteString("\n\n# 候选引用（“与当前任务相关的引用”一节只能从这里挑选与当前任务相关的，ref 原样写出并附一句说明）\n")
		for _, c := range in.candidates {
			sb.WriteString("- " + c.ref + " — " + c.note + "\n")
		}
	}
	sb.WriteString("\n\n# 待压缩的对话\n")
	sb.WriteString(renderTranscript(dropped, max(threshold*3, 8000)))

	// No MaxTokens on purpose: a summary is short by nature, so the protocol's
	// own default is enough here; the output limit setting target the main
	// loop's answers (Config.MaxOutputTokens).
	res, err := generateQuietly(withPurpose(ctx, purposeCompact), r.compactProvider(), &agentcore.GenerateRequest{
		System:   compactSystemPrompt,
		Messages: []agentcore.Message{{Role: "user", Content: sb.String()}},
	})
	if err != nil {
		return "", fmt.Errorf("summarize context: %w", err)
	}
	if res == nil || strings.TrimSpace(res.Content) == "" {
		return "", errors.New("summarize context: empty summary")
	}
	return strings.TrimSpace(res.Content), nil
}

// withoutContextUpdates returns items without replayed context updates
// (items itself when there is none).
func withoutContextUpdates(items []ctxItem) []ctxItem {
	var out []ctxItem
	for i, it := range items {
		if isContextUpdate(it.msg) {
			if out == nil {
				out = append(make([]ctxItem, 0, len(items)), items[:i]...)
			}
			continue
		}
		if out != nil {
			out = append(out, it)
		}
	}
	if out == nil {
		return items
	}
	return out
}

// renderTranscript renders items as text, halving the per-item cap (4000 ->
// 500 chars) until the total fits maxChars. A message a person typed into the
// running turn (a steer) is labeled as such instead of "[user]", so a summary
// does not read it as a request of the turn.
func renderTranscript(items []ctxItem, maxChars int) string {
	var out string
	for limit := 4000; ; limit /= 2 {
		var sb strings.Builder
		for _, it := range items {
			m := it.msg
			switch {
			case isSteerItem(m):
				fmt.Fprintf(&sb, "%s %s\n", steerLabel(m), truncateMiddle(m.Content, limit))
			case m.Role == "tool":
				content := m.Content
				if m.ToolResult != nil && len(m.ToolResult.Content) > len(content) {
					content = m.ToolResult.Content
				}
				name := ""
				if m.ToolResult != nil {
					name = m.ToolResult.Name
				}
				fmt.Fprintf(&sb, "[tool_result %s] %s\n", name, truncateMiddle(content, limit))
			case m.ToolCall != nil:
				args, _ := json.Marshal(m.ToolCall.Arguments)
				fmt.Fprintf(&sb, "[assistant] %s\n[tool_call %s] %s\n", truncateMiddle(m.Content, limit), m.ToolCall.Name, truncateMiddle(string(args), limit))
			default:
				fmt.Fprintf(&sb, "[%s] %s\n", m.Role, truncateMiddle(m.Content, limit))
			}
		}
		out = sb.String()
		if len(out) <= maxChars || limit <= 500 {
			break
		}
	}
	return truncateMiddle(out, maxChars)
}

// CompactResult reports a segment break between turns (token figures are
// estimates of the message context only, excluding system prompt and tool
// schemas).
type CompactResult struct {
	TokensBefore, TokensAfter int
	FirstKeptSeq              int64
	// Segment is the ordinal of the new segment record (1-based; 0 unknown).
	Segment int
}

// Compact starts a new segment of the session right now (chat :compact,
// reason manual). The record belongs to no turn, so it is adopted immediately.
// It holds the session gate: a running Run finishes first, an idle
// consolidation is preempted (cancelled before its commit point).
func (r *Runner) Compact(ctx context.Context, sessionID string) (CompactResult, error) {
	release, err := r.gate(sessionID).Acquire(ctx)
	if err != nil {
		return CompactResult{}, err
	}
	defer release()
	return r.breakSegment(ctx, sessionID, segmentReasonManual)
}

// ErrPreempted reports an idle consolidation that gave way to a Run or
// Compact before its commit point (it also matches context.Canceled).
var ErrPreempted = errors.New("idle consolidation preempted")

// ErrBusy reports an idle consolidation given up at once because a Run or
// Compact of the session held the gate (nothing was written).
var ErrBusy = errors.New("session busy")

// ConsolidateIdle starts a new segment between turns with reason idle, as
// the preemptible holder of the session gate (design D5/D6, chat idle
// consolidation). It never waits for the gate: when the gate is taken it
// returns ErrBusy right away. A Run or Compact arriving meanwhile cancels it
// and waits for it to let go. Whether the context is large enough to bother
// is the caller's decision.
//
// Results: success (the record was written; a cancellation from the write on
// does not undo it); ErrBusy; ErrNothingToCompact; an error matching
// context.Canceled (and ErrPreempted when a Run/Compact took over) when it
// stopped before its commit point - then nothing was written except a
// cancelled event.
func (r *Runner) ConsolidateIdle(ctx context.Context, sessionID string) (CompactResult, error) {
	if err := ctx.Err(); err != nil {
		return CompactResult{}, err
	}
	release, preempted, ok := r.gate(sessionID).TryAcquirePreemptible()
	if !ok {
		return CompactResult{}, ErrBusy
	}
	defer release()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go func() {
		select {
		case <-preempted:
			cancel(ErrPreempted)
		case <-ctx.Done():
		}
	}()
	out, err := r.breakSegment(ctx, sessionID, segmentReasonIdle)
	if err != nil && errors.Is(context.Cause(ctx), ErrPreempted) && errors.Is(err, context.Canceled) {
		err = fmt.Errorf("%w: %w", ErrPreempted, err)
	}
	return out, err
}

// breakSegment starts a new segment between turns (manual or idle): it loads
// the persisted view and runs the shared segment entry on it.
func (r *Runner) breakSegment(ctx context.Context, sessionID, reason string) (CompactResult, error) {
	model, err := r.LoadSession(ctx, sessionID)
	if err != nil {
		return CompactResult{}, err
	}
	if model.Status == store.SessionArchived || model.Status == store.SessionRunning {
		return CompactResult{}, fmt.Errorf("session %q is not ready for compaction: %s", model.ID, model.Status)
	}
	r.locksMu.Lock()
	_, held := r.heldLocks[model.ID]
	r.locksMu.Unlock()
	if r.cfg.LockRoot != "" && !held {
		lock, err := process.AcquireSessionLockContext(ctx, r.cfg.LockRoot, model.ID, 30*time.Second)
		if err != nil {
			return CompactResult{}, err
		}
		defer lock.Close()
	}
	ctx = withObserveScope(ctx, model.ID, "")
	hist, err := r.loadHistory(ctx, model.ID)
	if err != nil {
		return CompactResult{}, err
	}
	r.resolveSegment(&hist, model.Meta)
	b := r.newContextBuilder(model.ID, "", hist, nil, nil, "")
	// Between turns there is no RunState, so the system prompt cannot be counted
	// here (the tool schemas still are); estimate keeps the same unit as the
	// mid-turn threshold check, which is what the figures are compared with.
	before := b.estimate(ctx, nil, b.compose(nil))
	out, err := b.startSegment(ctx, nil, reason, before)
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{TokensBefore: out.tokensBefore, TokensAfter: out.tokensAfter, FirstKeptSeq: out.firstKeptSeq, Segment: out.segment}, nil
}
