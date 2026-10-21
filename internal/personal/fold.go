package personal

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/llm"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// foldArgChars is the size above which a string tool argument of a folded
// step is replaced by a short note (a whole file written, a long edit).
const foldArgChars = 1000

// foldHistory is history folding (context.fold_every / context.fold_keep,
// docs/design/2026-10-12-history-folding-design.md): in the request view,
// assistant steps before a boundary lose their reasoning and their long
// string arguments. The boundary only moves every `every` steps, keeping at
// least `keep` recent steps whole, so the cached request prefix changes once
// per batch rather than at every step. msgs is not modified; the stored
// history stays complete.
func foldHistory(msgs []agentcore.Message, every, keep int) []agentcore.Message {
	boundary, _ := foldBoundary(msgs, every, keep)
	if boundary == 0 {
		return msgs
	}
	out := make([]agentcore.Message, len(msgs))
	copy(out, msgs)
	step := 0
	for i, m := range out {
		if m.Role != "assistant" {
			continue
		}
		if step >= boundary {
			break
		}
		step++
		if m.Reasoning != "" {
			// Keep the provider in thinking mode (llm.ReasoningFoldedMetaKey).
			meta := make(map[string]any, len(m.Metadata)+1)
			for k, v := range m.Metadata {
				meta[k] = v
			}
			meta[llm.ReasoningFoldedMetaKey] = true
			m.Metadata = meta
			m.Reasoning = ""
		}
		if m.ToolCall != nil {
			tc := *m.ToolCall
			tc.Arguments, _ = foldValue(tc.Arguments).(map[string]any)
			m.ToolCall = &tc
		}
		out[i] = m
	}
	return out
}

// foldBoundary returns how many leading assistant steps of msgs are folded
// and how many steps there are: ((steps - keep) / every) * every, 0 when
// folding is off (every <= 0).
func foldBoundary(msgs []agentcore.Message, every, keep int) (boundary, steps int) {
	for _, m := range msgs {
		if m.Role == "assistant" {
			steps++
		}
	}
	if every <= 0 {
		return 0, steps
	}
	return (max(steps-keep, 0) / every) * every, steps
}

// metaFoldBoundary is the session meta key holding the fold boundary last
// recorded as a fold event in the current segment (reset by segmentMeta).
const metaFoldBoundary = "fold_boundary"

// metaFoldActive is the session meta key marking a segment in which folding
// was turned on by the size trigger (context.fold_at). It is reset together
// with metaFoldBoundary when a segment starts.
const metaFoldActive = "fold_active"

// Folding cadence used once the size trigger turned folding on and
// context.fold_every is unset (design D3, Q3): fold every 10 steps, keep the
// latest 6 whole.
const (
	sizeFoldEvery = 10
	sizeFoldKeep  = 6
)

// metaBool reads a boolean stored in session meta; anything else is false.
func metaBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// foldParams returns the batch size and the kept steps the fold boundary of
// this request uses (0 = no folding here):
//   - fold_at 0: the configured fold_every/fold_keep, folding from the start
//     (the behaviour before the size trigger existed);
//   - fold_at > 0 and the segment not activated yet: no folding;
//   - activated: the configured cadence, or sizeFoldEvery/sizeFoldKeep when
//     fold_every is unset.
func (b *contextBuilder) foldParams() (every, keep int) {
	every, keep = b.r.cfg.FoldEvery, b.r.cfg.FoldKeep
	if b.foldAt <= 0 {
		return every, keep
	}
	if !b.foldActive {
		return 0, keep
	}
	if every <= 0 {
		return sizeFoldEvery, sizeFoldKeep
	}
	return every, keep
}

// foldBySize turns folding on for the rest of the segment: the estimate of
// the unfolded view passed fold_at × the compaction threshold. It records a
// fold event with the size that triggered it and stores the flag in the
// session meta, so later turns of the segment keep folding even after the
// folded view shrank below the threshold again (design D2). Best-effort, like
// the other events.
func (b *contextBuilder) foldBySize(ctx context.Context, est int) {
	b.foldActive = true
	b.r.rec.record(ctx, store.Event{
		Kind: store.EventFold, Name: "fold", Status: eventOK, StartedAt: time.Now(),
		Attrs: map[string]any{"trigger": "size", "tokens": est, "threshold": b.threshold(), "at": b.foldAt},
	})
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := b.r.cfg.Repository.PatchSessionMeta(wctx, b.sessionID, map[string]any{metaFoldActive: true}); err != nil {
		slog.Warn("store fold activation in session meta", "session_id", b.sessionID, "err", err)
	}
}

// foldThreshold is the estimate above which folding starts in this segment:
// fold_at × the compaction threshold.
func (b *contextBuilder) foldThreshold() int {
	return int(b.foldAt * float64(b.threshold()))
}

// metaCount reads a non-negative count stored in session meta (JSON numbers
// come back as float64); anything else is 0.
func metaCount(v any) int {
	switch n := v.(type) {
	case float64:
		return max(int(n), 0)
	case int:
		return max(n, 0)
	case int64:
		return max(int(n), 0)
	}
	return 0
}

// foldStats describes a fold boundary move waiting to be recorded.
type foldStats struct {
	boundary, steps         int
	charsBefore, charsAfter int
	// toolResults is how many old tool results were replaced by a note, with
	// the characters of their text before and after (toolFoldStats).
	toolResults, toolCharsBefore, toolCharsAfter int
}

// toolFoldStats counts what foldToolResults replaced in one request view.
type toolFoldStats struct {
	results, charsBefore, charsAfter int
}

// toolLookup is the slice of the tool registry the fold layer needs: the tool
// of a name, to read its Meta. tools.Registry has it; a registry that does
// not (or a runner built without tools) folds no tool results.
type toolLookup interface {
	Get(name string) (agentcore.Tool, error)
}

// refoldableTool reports whether an old result of the named tool may be
// replaced by a note: only local, stateless reads whose result the model can
// get again by calling the tool (Meta ReadOnly && !External && Prefetchable:
// file_read, file_grep, file_find, file_list, file_symbols, file_refs). The
// decision comes from the tool metadata, not from a name list, so a new
// re-runnable read tool is folded without config.
func (r *Runner) refoldableTool(name string) bool {
	lookup, ok := r.cfg.Tools.(toolLookup)
	if !ok || name == "" {
		return false
	}
	tool, err := lookup.Get(name)
	if err != nil {
		return false
	}
	meta := tools.MetaOf(tool)
	return meta.ReadOnly && !meta.External && meta.Prefetchable
}

// foldedToolResultNote is the one-line stand-in for an old result of a
// re-runnable read-only tool: it keeps the tool name and the original size,
// and tells the model to call the tool again instead of trusting the files
// as they were.
func foldedToolResultNote(name string, chars int) string {
	return fmt.Sprintf("[已折叠的 %s 结果：原 %d 字符；参数见上方调用；文件可能已变化，需要时重新调用读取]", name, chars)
}

// foldToolResults replaces the text of old tool results of re-runnable
// read-only tools with a one-line note: only results before the first
// `boundary` assistant steps (the same boundary as the reasoning folding, so
// the cached prefix still changes once per batch), only texts longer than
// foldArgChars, never error results (they explain what went wrong later) and
// never results of tools the metadata does not mark re-runnable (External,
// shell_exec, ...). msgs is not modified; the stored history stays complete.
func foldToolResults(msgs []agentcore.Message, boundary int, refoldable func(name string) bool) ([]agentcore.Message, toolFoldStats) {
	var st toolFoldStats
	if boundary <= 0 || refoldable == nil {
		return msgs, st
	}
	// cut is the index of the assistant message that starts the first kept step.
	cut, step := len(msgs), 0
	for i, m := range msgs {
		if m.Role == "assistant" {
			if step >= boundary {
				cut = i
				break
			}
			step++
		}
	}
	out := msgs
	copied := false
	for i := 0; i < cut; i++ {
		m := msgs[i]
		if m.Role != "tool" || m.ToolResult == nil || m.ToolResult.IsError {
			continue
		}
		content := m.ToolResult.Content
		if content == "" {
			content = m.Content
		}
		if len(content) <= foldArgChars || !refoldable(m.ToolResult.Name) {
			continue
		}
		if !copied {
			// Copy on the first change only: an untouched view is returned as is.
			cp := make([]agentcore.Message, len(msgs))
			copy(cp, msgs)
			out, copied = cp, true
		}
		note := foldedToolResultNote(m.ToolResult.Name, len(content))
		res := *m.ToolResult
		res.Content = note
		m.ToolResult = &res
		m.Content = note
		out[i] = m
		st.results++
		st.charsBefore += len(content)
		st.charsAfter += len(note)
	}
	return out, st
}

// recordFold writes the pending boundary move (set by compose) as a fold
// event and stores the new baseline in the session meta, so a later turn's
// builder does not record it again. Best-effort, like other events.
func (b *contextBuilder) recordFold(ctx context.Context) {
	f := b.foldPending
	b.foldPending = nil
	if f == nil || f.boundary <= b.foldBase {
		return
	}
	b.foldBase = f.boundary
	b.r.rec.record(ctx, store.Event{
		Kind: store.EventFold, Name: "fold", Status: eventOK, StartedAt: time.Now(),
		Attrs: map[string]any{"boundary": f.boundary, "steps": f.steps,
			"chars_before": f.charsBefore, "chars_after": f.charsAfter,
			"tool_results_folded": f.toolResults, "tool_chars_before": f.toolCharsBefore,
			"tool_chars_after": f.toolCharsAfter},
	})
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := b.r.cfg.Repository.PatchSessionMeta(wctx, b.sessionID, map[string]any{metaFoldBoundary: f.boundary}); err != nil {
		slog.Warn("store fold boundary in session meta", "session_id", b.sessionID, "err", err)
	}
}

// foldValue returns v with every string longer than foldArgChars replaced by
// a note, descending into maps and lists (file_edit edits). It copies what it
// changes.
func foldValue(v any) any {
	switch x := v.(type) {
	case string:
		if len(x) > foldArgChars {
			// No "re-read" hint: it invited reads the step did not need.
			return fmt.Sprintf("[folded: %d chars sent in an earlier step]", len(x))
		}
		return x
	case map[string]any:
		if x == nil {
			return x
		}
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = foldValue(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = foldValue(item)
		}
		return out
	}
	return v
}
