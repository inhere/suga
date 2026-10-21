package personal

// Tool call event hook and tool argument / metadata summary helpers.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/inhere/suga/internal/diffview"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/redact"
)

// toolArgSummaryLimit caps the argument summary carried by "tool_call" events.
const toolArgSummaryLimit = 200

// toolEventHook publishes "tool_call" (before execution) and "tool_result"
// (after) events. Emit failures are only logged: returning them from a hook
// stage would abort the tool or the turn, and display must never do that.
// With Config.DiffPreview the tool_call event carries the change the call makes
// on disk, read here - before the tool runs, so the file still holds the text
// before it.
type toolEventHook struct {
	agentcore.NopHook
	sink      EmitFunc
	sessionID string
	turnID    string
	runID     string
	// workspace is where a file tool call works; diffPreview turns the change
	// Config.DiffPreview asks for on.
	workspace   string
	diffPreview bool
}

func (h toolEventHook) publish(ctx context.Context, event Event) {
	event.SessionID, event.TurnID, event.RunID = h.sessionID, h.turnID, h.runID
	if err := emit(ctx, h.sink, event); err != nil {
		turnLogger(ctx).Warn("emit tool event", "kind", event.Kind, "tool", event.ToolName, "err", err)
	}
}

func (h toolEventHook) BeforeTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event == nil || event.Call == nil {
		return nil
	}
	call := Event{Kind: "tool_call", ToolName: event.Call.Name, Text: toolArgSummary(event.Call.Arguments), ToolArgs: event.Call.Arguments}
	if h.diffPreview {
		if change, ok := diffview.Call(h.workspace, event.Call.Name, event.Call.Arguments); ok {
			call.Change = change
		}
	}
	h.publish(ctx, call)
	return nil
}

func (h toolEventHook) AfterTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event == nil || event.Result == nil {
		return nil
	}
	resultEvent := Event{Kind: "tool_result", ToolName: event.Result.Name, Text: event.Result.Content, IsError: event.Result.IsError}
	if event.Result.Name == "agent" {
		resultEvent.AgentSummary = agentSummary(event.Result.Metadata)
	}
	h.publish(ctx, resultEvent)
	return nil
}

func agentSummary(metadata map[string]any) *AgentSummary {
	if metadata == nil {
		return nil
	}
	description, _ := metadata["description"].(string)
	calls := metadataInt(metadata["tool_calls"])
	duration := time.Duration(metadataInt64(metadata["duration_ms"])) * time.Millisecond
	background, _ := metadata["background"].(bool)
	if description == "" && calls == 0 && duration == 0 && !background {
		return nil
	}
	return &AgentSummary{Description: description, ToolCalls: calls, Duration: duration, Background: background}
}

func metadataInt(value any) int {
	return int(metadataInt64(value))
}

func metadataInt64(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// toolArgSummary renders call arguments as compact JSON, redacted and truncated.
func toolArgSummary(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	data, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return truncateRunes(redact.Secrets(string(data)), toolArgSummaryLimit)
}
