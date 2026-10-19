package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/redact"
)

// A turn that ends failed or cancelled leaves two things behind: its side
// effects (the files it wrote, the commands it ran, the commits and issues it
// created) reach the workspace, and its entries stay in the store. Only
// completed turns became history, so the next turn knew nothing of them: it
// repeated the work, blamed the change on the person or rolled it back (issue
// suga-y0sk; design docs/design/2026-10-15-interrupted-turn-replay-design.md).
//
// Two pieces close that gap:
//
//   - replayablePrefix is the part of such a turn that the next context keeps -
//     its messages up to the last complete tool exchange. Replaying an
//     assistant tool_call without its result would be rejected by some
//     providers, so the kept part stops at the first call that has no result.
//   - the interruption note (writeInterruptionNote, metaTurnInterrupted) is one
//     user entry saying what the turn ran and why it stopped. It always
//     replays, even when the cut drops the entries written before it, so the
//     next turn knows an interrupted turn happened whatever else was kept.

// metaTurnInterrupted marks the user entry recording an interrupted turn: its
// status, the tool calls it had run and the reason it stopped. Such an entry is
// an injected item (injectedItemKeys): it is not a request a person wrote.
const metaTurnInterrupted = "suga.turn_interrupted"

// Bounds of an interruption note: how many tool calls it lists, how long one
// listed call and the reason may be, and the cap of the whole message.
const (
	interruptionNoteCalls       = 8
	interruptionNoteCallChars   = 160
	interruptionNoteReasonChars = 300
	interruptionNoteChars       = 1500
)

// isInterruptionNote reports whether m is an interrupted turn's note.
func isInterruptionNote(m agentcore.Message) bool {
	v, _ := m.Metadata[metaTurnInterrupted].(bool)
	return v
}

// interruptionStatusLabel names the turn status in the note.
func interruptionStatusLabel(status string) string {
	if status == store.TurnCancelled {
		return "已取消"
	}
	return "运行失败"
}

// interruptionNoteText renders the note of an interrupted turn (design D3): what
// it ran, how much of it did not make it into the context, and why it stopped.
func interruptionNoteText(status, reason string, calls []string, dropped int) string {
	lines := []string{"- 未执行任何工具调用"}
	if len(calls) > 0 {
		shown := calls
		if len(shown) > interruptionNoteCalls {
			shown = shown[:interruptionNoteCalls]
		}
		lines[0] = fmt.Sprintf("- 已执行的工具调用（共 %d 次）：%s", len(calls), strings.Join(shown, "；"))
	}
	if dropped > 0 {
		lines = append(lines, fmt.Sprintf("- 另有 %d 条记录没有进入上下文（中断发生在未完成的工具调用处）", dropped))
	}
	if reason != "" {
		lines = append(lines, "- 原因："+truncateRunes(redact.Secrets(reason), interruptionNoteReasonChars))
	}
	head := fmt.Sprintf("[上一回合%s——这不是新请求。中断的回合没有正常收尾，但它已经执行过的步骤仍然有效："+
		"工作区、文件和提交可能已被它改动，继续之前请先核对这些的实际状态。", interruptionStatusLabel(status))
	return truncateRunes(head+"\n"+strings.Join(lines, "\n")+"]", interruptionNoteChars)
}

// interruptionNoteMessage wraps the note text as the stored user entry (an
// injected item: not a request a person wrote).
func interruptionNoteMessage(text string) agentcore.Message {
	return agentcore.Message{Role: "user", Content: text, Metadata: map[string]any{metaTurnInterrupted: true}}
}

// toolCallSummary renders one tool call for the note as "<name> key=value", the
// display the transcript uses, redacted and bounded.
func toolCallSummary(c *agentcore.ToolCall) string {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = "tool"
	}
	summary := name
	if brief := summarizeToolArgs(c.Arguments); brief != "" {
		summary += " " + brief
	}
	return truncateRunes(redact.Secrets(summary), interruptionNoteCallChars)
}

// replayablePrefix returns the entries of one interrupted turn that the next
// turn's context keeps (design D2), the tool calls that ran in its kept part
// (already rendered for the note) and how many entries the cut left behind.
//
// The kept part is the turn's messages up to the last complete tool exchange: a
// user message (a steering entry or a task notice excepted - both go back to the
// queue they came from and replaying them would duplicate it, design D5 and
// issue suga-o26e), an assistant message whose tool call has its result in this
// turn, and the results themselves. An assistant message whose call has no
// result cuts everything after it: the dropped tail refers to content the model
// can no longer see, so keeping parts of it would make the context contradict
// itself; the note reports the cut. Context updates, compaction records and the
// note itself are not part of the prefix and never cut it.
func replayablePrefix(entries []store.Entry) (kept []store.Entry, calls []string, dropped int, err error) {
	results := make(map[string]bool)
	for _, e := range entries {
		if e.Type != store.EntryTool {
			continue
		}
		var m agentcore.Message
		if err := json.Unmarshal(e.JSON, &m); err != nil {
			return nil, nil, 0, fmt.Errorf("decode tool entry %d: %w", e.Seq, err)
		}
		if m.ToolResult != nil && m.ToolResult.CallID != "" {
			results[m.ToolResult.CallID] = true
		}
	}

	cut := len(entries)
	for i, e := range entries {
		if e.Type != store.EntryAssistant {
			continue
		}
		var m agentcore.Message
		if err := json.Unmarshal(e.JSON, &m); err != nil {
			return nil, nil, 0, fmt.Errorf("decode assistant entry %d: %w", e.Seq, err)
		}
		if c := m.ToolCall; c != nil && (c.CallID == "" || !results[c.CallID]) {
			cut = i
			break
		}
	}

	// The note is replayed whatever the cut did (design D4): it is the one entry
	// describing what the whole turn did and why it stopped.
	var note *store.Entry
	kept = make([]store.Entry, 0, cut+1)
	for i, e := range entries[:cut] {
		switch e.Type {
		case store.EntryUser:
			var m agentcore.Message
			if err := json.Unmarshal(e.JSON, &m); err != nil {
				return nil, nil, 0, fmt.Errorf("decode user entry %d: %w", e.Seq, err)
			}
			if isInterruptionNote(m) {
				note = &entries[i]
				continue
			}
			if isSteerItem(m) {
				continue // the caller gets the steer back; replaying it would duplicate it
			}
			if isTaskNoticeItem(m) {
				// The task notices the turn took go back to the task manager
				// (finish, returnTaskNotices): replaying one here would show the
				// same task end twice (issue suga-o26e).
				continue
			}
		case store.EntryAssistant:
			var m agentcore.Message
			if err := json.Unmarshal(e.JSON, &m); err != nil {
				return nil, nil, 0, fmt.Errorf("decode assistant entry %d: %w", e.Seq, err)
			}
			if m.ToolCall != nil {
				calls = append(calls, toolCallSummary(m.ToolCall))
			}
		case store.EntryTool:
			// Kept: the assistant message that asked for it was kept too (an
			// unanswered call cut the prefix above).
		default:
			continue
		}
		kept = append(kept, e)
	}
	for i := cut; i < len(entries); i++ {
		isNote, err := isInterruptionNoteEntry(entries[i])
		if err != nil {
			return nil, nil, 0, err
		}
		if isNote {
			note = &entries[i]
			continue
		}
		dropped++
	}
	if note != nil {
		kept = append(kept, *note)
	}
	return kept, calls, dropped, nil
}

// isInterruptionNoteEntry reports whether e is an interrupted turn's note. It
// decodes only the metadata head, like decodeTurnEntry.
func isInterruptionNoteEntry(e store.Entry) (bool, error) {
	var head turnEntryHead
	if err := json.Unmarshal(e.JSON, &head); err != nil {
		return false, fmt.Errorf("decode entry %d: %w", e.Seq, err)
	}
	return isInterruptionNote(agentcore.Message{Metadata: head.Metadata}), nil
}

// writeInterruptionNote appends the note of an interrupted turn (design D3) so
// the next turn learns what the turn ran and why it stopped. It reads the turn's
// entries to list the tool calls and to count the tail the replay cut drops; the
// note becomes the turn's last entry, and replayablePrefix keeps it whatever the
// cut does (design D4). A failure is only logged by the caller - the turn's own
// outcome is what it reports.
func (r *Runner) writeInterruptionNote(ctx context.Context, sessionID, turnID, status, reason string) error {
	if turnID == "" {
		return nil
	}
	entries, err := r.cfg.Repository.ListEntries(ctx, sessionID, store.EntryFilter{
		TurnID: turnID, Types: []string{store.EntryUser, store.EntryAssistant, store.EntryTool},
	})
	if err != nil {
		return fmt.Errorf("list the turn's entries: %w", err)
	}
	_, calls, dropped, err := replayablePrefix(entries)
	if err != nil {
		return err
	}
	raw, err := store.EncodeMessage(interruptionNoteMessage(interruptionNoteText(status, reason, calls, dropped)))
	if err != nil {
		return fmt.Errorf("encode the interruption note: %w", err)
	}
	if _, err := r.cfg.Repository.AppendEntries(ctx, sessionID, store.NewEntry{
		TurnID: turnID, Type: store.EntryUser, JSON: raw,
	}); err != nil {
		return fmt.Errorf("append the interruption note: %w", err)
	}
	return nil
}
