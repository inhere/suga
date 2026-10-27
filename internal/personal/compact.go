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
//
// Each section but 采纳与否决 carries its output budget (handoffSectionBudgets)
// in the heading's note: the model is told the budgets so a summary stays small
// where it may, while the protected section is told to stay verbatim and
// uncapped (the long-session evaluation checks those lines).
const compactTemplate = `请按下列固定结构输出交接包正文（没有内容的小节写“无”）；每节括号里的“≤N”是该节的字节预算，超出时只保留继续任务最必要的行：
## 当前任务（最近的用户请求原文会附在交接包里，不必复述；≤1 KB）
## 坐标（文件 / 符号 / 分支 / 版本；≤2 KB，同一文件同一符号只写最新的一次）
## 采纳与否决（硬约束、规则与用户原话一律原文，不设上限，不得改写或省略）
## 验证到哪（≤1.5 KB）
## 进展与阻塞（≤2 KB）
## 下一步（≤1 KB）
` + refsHeading + `（ref + 一句话说明，可用 recall 读取；≤1 KB）`

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
	scenario := scenarioForReason(reason) // also reported on the compaction event
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
	pkg, err := b.r.buildPackage(ctx, b.sessionID, b.turnID, b.summary, items, cut, b.threshold(), scenario, b.reuseBase, b.reuseBaseWhy)
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
			"first_kept_seq": firstKept, "dropped_messages": cut, "model": b.r.scenarioModelName(scenario),
			"segment": segment, "requests": len(pkg.requests), "refs": len(pkg.refs),
			// Which part of the new segment context cost bytes and which changed
			// since the previous segment (cache attribution after a break).
			"sections": segmentSectionAttrs(seg, pkg.rendered)},
	})
	// The meta cache follows the record (best-effort). The system prompt is
	// the stable prefix and does not change: this very step already renders
	// the new snapshot through compose.
	b.r.cacheSegmentMeta(wctx, b.sessionID, seg) // also resets the fold baseline
	b.foldBase, b.foldPending, b.foldActive = 0, nil, false
	// The closing segment's last request is spent: a later break that finds no
	// newer main-loop request falls back to the old handoff path (no_request).
	b.r.rec.prefix.clearRequest(b.sessionID)
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
// Scenario keys pick the summary model of one background call by what served
// it (models.compact / models.idle / models.handoff): a segment break uses the
// compact model unless it was the idle consolidation, and a --from handoff
// package uses the handoff model.
const (
	scenarioCompact = "compact"
	scenarioIdle    = "idle"
	scenarioHandoff = "handoff"
)

// scenarioForReason maps a segment break reason to its summary scenario: an
// idle consolidation is its own scenario, every other break (threshold,
// manual :compact) is the compact one.
func scenarioForReason(reason string) string {
	if reason == segmentReasonIdle {
		return scenarioIdle
	}
	return scenarioCompact
}

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

// scenarioProvider picks the provider of one summary call: the scenario's own
// provider (models.idle / models.handoff) when configured, else the compact
// provider (models.compact / compact_model), else the turn's own provider.
func (r *Runner) scenarioProvider(scenario string) agentcore.Provider {
	switch scenario {
	case scenarioIdle:
		if r.cfg.IdleProvider != nil {
			return r.cfg.IdleProvider
		}
	case scenarioHandoff:
		if r.cfg.HandoffProvider != nil {
			return r.cfg.HandoffProvider
		}
	}
	return r.compactProvider()
}

// scenarioModelName is the model id reported for the scenario's summary
// calls; the fallback chain mirrors scenarioProvider.
func (r *Runner) scenarioModelName(scenario string) string {
	switch scenario {
	case scenarioIdle:
		if r.cfg.IdleProvider != nil && r.cfg.IdleModel != "" {
			return r.cfg.IdleModel
		}
	case scenarioHandoff:
		if r.cfg.HandoffProvider != nil && r.cfg.HandoffModel != "" {
			return r.cfg.HandoffModel
		}
	}
	return r.compactModelName()
}

// packageInput is what a handoff package is summarized from.
type packageInput struct {
	previous   string         // the current segment's package ("" = none)
	dropped    []ctxItem      // the part of the view leaving the raw context
	plan       string         // latest plan entry JSON ("" = none)
	requests   []string       // recent user requests, original text, oldest first
	candidates []refCandidate // refs the model may pick from
	// reuse is the segment's last main-loop request the handoff call may
	// continue (segment_handoff_reuse.go); nil takes the old path.
	reuse *handoffReuse
	// reuseFallback names why the reuse was not attempted at all ("" = this path
	// is no segment break: the cross-session handoff and the idle consolidation).
	reuseFallback string
}

// handoffPromptHead renders the parts both handoff requests share: the template,
// the previous package, the plan, the recent requests and the candidate refs.
// The two paths differ only in what follows them (the transcript, or the
// instruction of the reuse path).
func handoffPromptHead(in packageInput, transcript string) string {
	var sb strings.Builder
	sb.WriteString(compactTemplate)
	if in.previous != "" {
		sb.WriteString("\n\n# 已有摘要（需并入新摘要）\n" + foldPreviousPackage(in.previous, transcript))
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
	return sb.String()
}

// handoffPrompt is the old path's user message: the shared parts and the
// size-capped transcript of the part of the view that leaves the raw context.
// The reuse path sends the view itself and appends an instruction instead
// (segment_handoff_reuse.go), so this text stays byte for byte what it was.
func handoffPrompt(in packageInput, transcript string) string {
	return handoffPromptHead(in, transcript) + "\n\n# 待压缩的对话\n" + transcript
}

// summarize asks the model for the handoff package body (compactTemplate) of
// in.dropped, folding in the previous package and the latest plan, pointed at
// the current task by the recent requests and offered the candidate refs. The
// transcript is size-capped so the request itself fits the window. Context
// updates are skipped: the next segment re-reads rules and the rest into its
// own snapshot.
//
// The fold is bounded on three sides (research 2026-10-22 §5 #2): the previous
// package enters deduped and cut to its budgets (foldPreviousPackage), the call
// sets no output cap (handoffOutputTokens: a cap starved reasoning models), and the body
// that comes back is normalized again (normalizeHandoffBody) - which cuts the
// capped sections and never the protected one. A summary cut short by the cap
// is asked for once more with the budgets restated.
func (r *Runner) summarize(ctx context.Context, scenario string, in packageInput, threshold int) (string, error) {
	dropped := withoutContextUpdates(in.dropped)
	transcript := renderTranscript(dropped, max(threshold*3, 8000))

	// No cap of our own (handoffOutputTokens is 0): going over the budgets is
	// handled downstream by normalizeHandoffBody (capped sections cut, protected
	// section kept), and an answer the provider still truncated is asked for once
	// more.
	maxTokens := handoffOutputTokens()
	fallback := in.reuseFallback
	if in.reuse != nil {
		// The reuse path: the segment's own request plus one instruction, so the
		// provider cache serves the conversation (design 2026-10-27, H1/H2).
		instruction := handoffPromptHead(in, transcript) + "\n\n" + r.reuseScopeText(in.reuse.tail)
		body, truncated, reason := r.reuseCall(ctx, in.reuse, instruction, maxTokens)
		if reason == "" {
			if truncated {
				slog.Warn("handoff summary hit the output cap; asking once more with the budgets restated",
					"max_tokens", maxTokens, "summary_bytes", len(body))
				if again, _, why := r.reuseCall(ctx, in.reuse, instruction+completeProtectedNote, maxTokens); why == "" && strings.TrimSpace(again) != "" {
					body = again
				}
			}
			return normalizeHandoffBody(canonicalHandoffHeadings(strings.TrimSpace(body))), nil
		}
		fallback = reason
	}
	if fallback != "" {
		// The trace says why this handoff call did not continue the prefix.
		ctx = withHandoffReuse(ctx, handoffReuseFallbackPrefix+fallback)
	}
	body, truncated, err := r.summaryCall(ctx, scenario, handoffPrompt(in, transcript), maxTokens)
	if err != nil {
		return "", err
	}
	if truncated {
		slog.Warn("handoff summary hit the output cap; asking once more with the budgets restated",
			"max_tokens", maxTokens, "summary_bytes", len(body))
		if again, _, aerr := r.summaryCall(ctx, scenario, handoffPrompt(in, transcript)+completeProtectedNote, maxTokens); aerr == nil && strings.TrimSpace(again) != "" {
			body = again
		}
	}
	if strings.TrimSpace(body) == "" {
		return "", errors.New("summarize context: empty summary")
	}
	return normalizeHandoffBody(canonicalHandoffHeadings(strings.TrimSpace(body))), nil
}

// summaryCall makes one summary request (prompt is the whole user message).
// truncated reports that the provider cut the answer at the output limit, so
// the package may be missing its last sections.
func (r *Runner) summaryCall(ctx context.Context, scenario, prompt string, maxTokens int) (body string, truncated bool, err error) {
	res, err := generateQuietly(withPurpose(ctx, purposeCompact), r.scenarioProvider(scenario), &agentcore.GenerateRequest{
		System:    compactSystemPrompt,
		Messages:  []agentcore.Message{{Role: "user", Content: prompt}},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return "", false, fmt.Errorf("summarize context: %w", err)
	}
	if res == nil || strings.TrimSpace(res.Content) == "" {
		return "", false, errors.New("summarize context: empty summary")
	}
	return res.Content, res.Truncated, nil
}

// summaryRequest makes one already built summary request (the reuse path of a
// segment break) and reports the answer's text, how many tool calls it asked
// for and whether the provider cut it at the output limit. An empty answer is
// not an error here: the reuse path discards it and asks the old way.
func summaryRequest(ctx context.Context, p agentcore.Provider, req *agentcore.GenerateRequest) (body string, toolCalls int, truncated bool, err error) {
	res, err := generateQuietly(withPurpose(ctx, purposeCompact), p, req)
	if err != nil {
		return "", 0, false, fmt.Errorf("summarize context: %w", err)
	}
	if res == nil {
		return "", 0, false, nil
	}
	return res.Content, len(res.ToolCalls), res.Truncated, nil
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
	// The same prompt builder and tool registry the main loop uses, so a break
	// between turns can rebuild the head of the session's last request (H3).
	b := r.newContextBuilder(model.ID, "", hist, stablePromptBuilder{base: r.cfg.PromptBuilder}, nil, "")
	// Folding is part of the view: the baseline of the closing turn's builder
	// keeps the rebuilt messages on the bytes that turn actually sent.
	b.foldBase, b.foldActive = metaCount(model.Meta[metaFoldBoundary]), metaBool(model.Meta[metaFoldActive])
	// Between turns there is no RunState, so the system prompt cannot be counted
	// here (the tool schemas still are); estimate keeps the same unit as the
	// mid-turn threshold check, which is what the figures are compared with.
	view := b.prepareImages(b.compose(nil))
	before := b.estimate(ctx, nil, view)
	// The handoff call of this break may continue the last main-loop request of
	// the session instead of re-rendering a transcript (H3): a fresh process (suga
	// session compact) has no request in memory, so it is rebuilt from the
	// persisted view and checked against the fingerprint the last turn left.
	if r.cfg.HandoffReusePrefix && scenarioForReason(reason) == scenarioCompact {
		b.reuseBase, b.reuseBaseWhy = b.requestBaseBetweenTurns(ctx, view, prefixPrintFromMeta(model.Meta))
	}
	out, err := b.startSegment(ctx, nil, reason, before)
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{TokensBefore: out.tokensBefore, TokensAfter: out.tokensAfter, FirstKeptSeq: out.firstKeptSeq, Segment: out.segment}, nil
}
