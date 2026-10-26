package acpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

// replayByteLimit caps one session/load replay (design 2026-10-20): past it
// only the most recent messages are replayed and a leading thought note says
// so, so a very long session never sends tens of MB of notifications.
const replayByteLimit = 2 * 1024 * 1024

// replayToolTextLimit is the text budget of one replayed tool result.
const replayToolTextLimit = 2048

// replayTitleLimit caps one replayed tool call's title.
const replayTitleLimit = 120

// replaySummaryClip caps the compaction summary note at the replay's head.
const replaySummaryClip = 200

// replayStep is one replayed message with the bytes it is accounted for.
type replayStep struct {
	update acp.SessionUpdate
	bytes  int
}

// replayHistory sends the session's current segment to the client as
// session/update notifications (design 2026-10-20, session/load 回放): the
// compaction summary as one agent_thought_chunk, then the kept messages in
// their stored order — user messages as user_message_chunk, assistant text as
// agent_message_chunk, tool calls and results as a started/completed tool
// call with the result's first 2KB of text. The view is the one loadHistory
// assembles (personal.SegmentHistory reuses its boundaries and adoption
// rules), so the client sees what the next turn's model sees. Past 2MB only
// the most recent messages ride along and a leading thought note says so.
func (a *Agent) replayHistory(ctx context.Context, id acp.SessionId, hist personal.SegmentHistory) error {
	conn := a.connection()
	if conn == nil {
		return fmt.Errorf("ACP connection is not ready")
	}
	for _, update := range buildReplay(trOrFallback(a.tr()), hist, replayByteLimit) {
		if err := conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: id, Update: update}); err != nil {
			return err
		}
	}
	return nil
}

// buildReplay returns the updates of one replay in send order: the head notes
// (compaction summary, then the truncation note when limit is exceeded), then
// the kept messages oldest first. tr renders the client-facing notes.
func buildReplay(tr i18n.Translator, hist personal.SegmentHistory, limit int) []acp.SessionUpdate {
	tr = trOrFallback(tr)
	// The head note (compaction summary) is part of the counted budget: it
	// must never make the tail of the segment disappear instead.
	notes := make([]acp.SessionUpdate, 0, 2)
	var bytes int
	if hist.Summary != "" {
		note := tr.T("host.acp.replay_summary", map[string]any{
			"summary": clipRunes(singleLine(hist.Summary), replaySummaryClip),
		})
		notes = append(notes, acp.UpdateAgentThoughtText(note))
		bytes += len(note)
	}
	items := make([]replayStep, 0, len(hist.Items))
	for _, it := range hist.Items {
		update, size := replayItem(it.Msg)
		items = append(items, replayStep{update: update, bytes: size})
		bytes += size
	}
	if bytes > limit {
		// Drop the oldest messages until the rest fits; the note tells the
		// client how much was left out (it reads the replay from the top) and
		// how large the whole history was.
		total, dropped := bytes, 0
		for len(items) > 0 && bytes > limit {
			bytes -= items[0].bytes
			items = items[1:]
			dropped++
		}
		notes = append(notes, acp.UpdateAgentThoughtText(tr.T("host.acp.replay_truncated", map[string]any{
			"total": replaySize(total), "limit": replaySize(limit), "recent": len(items), "dropped": dropped,
		})))
	}
	out := notes
	for _, item := range items {
		out = append(out, item.update)
	}
	return out
}

// replaySize renders a byte count for the truncation note: MB from 1 MB up,
// KB below (a small limit would otherwise read "0.0 MB").
func replaySize(n int) string {
	if n >= 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// replayItem maps one persisted message onto its session/update:
// a tool result is a completed (or failed) tool_call_update with its text;
// a tool call is the matching start; roles map directly otherwise.
func replayItem(m agentcore.Message) (acp.SessionUpdate, int) {
	switch {
	case m.ToolResult != nil:
		text := clipRunes(m.ToolResult.Content, replayToolTextLimit)
		status := acp.ToolCallStatusCompleted
		if m.ToolResult.IsError {
			status = acp.ToolCallStatusFailed
		}
		return acp.UpdateToolCall(acp.ToolCallId(m.ToolResult.CallID),
			acp.WithUpdateTitle(toolSummary(m.ToolResult.Name, nil)),
			acp.WithUpdateStatus(status),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.ContentBlock{Text: &acp.ContentBlockText{Text: text}})}),
		), len(text)
	case m.ToolCall != nil:
		args, _ := json.Marshal(m.ToolCall.Arguments)
		return acp.StartToolCall(acp.ToolCallId(m.ToolCall.CallID), toolSummary(m.ToolCall.Name, args),
			acp.WithStartStatus(acp.ToolCallStatusCompleted)), len(m.ToolCall.Name) + len(args)
	case m.Role == "user":
		return acp.UpdateUserMessageText(m.Content), len(m.Content)
	default:
		return acp.UpdateAgentMessageText(m.Content), len(m.Content)
	}
}

// toolSummary renders a replayed tool call's title: the tool name with a
// compact view of its arguments.
func toolSummary(name string, args []byte) string {
	view := string(args)
	if view == "null" || strings.Contains(view, `"`) {
		// Keep the arguments one line and short enough to scan.
		if args != nil {
			view = singleLine(view)
		}
	}
	if view == "null" {
		return name
	}
	return clipRunes(name+"("+view+")", replayTitleLimit)
}

// singleLine flattens a text to one line (a note or title must not carry the
// line breaks of the content it quotes).
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clipRunes keeps at most n runes, marking a cut.
func clipRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
