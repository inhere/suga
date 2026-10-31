package chat

import (
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/chatui"
)

// The tool call summaries (what a tool line shows after "▸ <name>") live in
// pkg/chatui (toolline.go), shared with the other hosts of the terminal UI.

// toolCallArgs is chatui.ToolCallArgs with chat's translator type.
func toolCallArgs(t i18n.Translator, name, text string, full map[string]any, workspace string) string {
	return chatui.ToolCallArgs(t, name, text, full, workspace)
}
