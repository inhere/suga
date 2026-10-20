package axconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
)

// ChatHistoryEnabled reports whether locally typed chat lines are persisted.
func ChatHistoryEnabled() bool {
	return chatSwitch("chat.history")
}

// ChatHistorySize returns the maximum number of persisted chat lines.
func ChatHistorySize() (int, error) {
	if gkconfig.Get("chat.history_size") == nil {
		return 1000, nil
	}
	raw := strings.TrimSpace(gkconfig.String("chat.history_size"))
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("chat.history_size must be a whole number >= 1, got %q", raw)
	}
	return n, nil
}

// ChatMarkdownEnabled reports whether chat renders the model's markdown in the
// terminal (chat.markdown, on by default). The key accepts a bool or a string;
// "off"/"false" turn the rendering off and show the exact model text instead.
func ChatMarkdownEnabled() bool {
	return chatSwitch("chat.markdown")
}

// ChatSpinnerEnabled reports whether chat shows its "思考中…" waiting line while
// the model is asked for its next message (chat.spinner, on by default). It
// takes the same values as chat.markdown; a non-terminal output or NO_COLOR
// turns the line off anyway.
func ChatSpinnerEnabled() bool {
	return chatSwitch("chat.spinner")
}

// ChatHighlightInputEnabled reports whether chat paints a background behind the
// line the person submitted at the prompt, and behind messages delivered from
// elsewhere (chat.highlight_input, on by default). It takes the same values as
// chat.markdown; a non-terminal output or NO_COLOR turns the background off.
func ChatHighlightInputEnabled() bool {
	return chatSwitch("chat.highlight_input")
}

// ChatSteeringEnabled reports whether chat keeps the input line available while
// a turn runs (chat.steering, on by default): the person can insert a message
// into the running turn (Enter), end this turn only (Esc, a single Ctrl-C) or
// leave chat (a second Ctrl-C within a few seconds). It takes the same values as
// chat.markdown; a non-terminal input keeps the old behaviour anyway (no line
// editing, no reading during a turn).
func ChatSteeringEnabled() bool {
	return chatSwitch("chat.steering")
}

// ChatTerminalTitleEnabled reports whether chat sets the terminal's window/tab
// title to the session, "suga · <短名称> · <会话标题>" (chat.terminal_title, on by
// default). It takes the same values as chat.markdown; a non-terminal output or
// NO_COLOR leaves the title alone anyway.
func ChatTerminalTitleEnabled() bool {
	return chatSwitch("chat.terminal_title")
}

// ChatDiffEnabled reports whether chat shows what a file tool call changed
// (chat.diff, on by default): the unified diff under the tool result, the
// pending diff before an approval question and the :diff command. It takes the
// same values as chat.markdown. run, ACP and stream-json are not affected - ACP
// builds its own diff for its client either way.
func ChatDiffEnabled() bool {
	return chatSwitch("chat.diff")
}

// DefaultChatDiffLines is how many lines one diff preview in chat shows
// (chat.diff_lines) when the key is not set.
const DefaultChatDiffLines = 20

// ChatDiffLines reads chat.diff_lines: how many lines one diff preview shows
// (the :diff command always shows the whole change). A value below 1 or
// something that is not a whole number is an error rather than a silently
// changed or ignored limit.
func ChatDiffLines() (int, error) {
	if gkconfig.Get("chat.diff_lines") == nil {
		return DefaultChatDiffLines, nil
	}
	raw := strings.TrimSpace(gkconfig.String("chat.diff_lines"))
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("chat.diff_lines must be a whole number >= 1, got %q", raw)
	}
	return n, nil
}

// DefaultChatCodeMaxLines is how many lines of one code block chat shows
// before it folds the rest (chat.code_max_lines) when the key is not set.
const DefaultChatCodeMaxLines = 40

// ChatCodeHighlightEnabled reports whether chat colors a fenced code block by
// the language its fence names (chat.code_highlight, on by default). It takes
// the same values as chat.markdown; an unknown language, a non-terminal output
// or NO_COLOR keeps the plain text anyway.
func ChatCodeHighlightEnabled() bool {
	return chatSwitch("chat.code_highlight")
}

// ChatCodeMaxLines reads chat.code_max_lines: how many lines of one code block
// chat shows before it folds the rest and names :code. 0 shows the block whole.
func ChatCodeMaxLines() (int, error) {
	if gkconfig.Get("chat.code_max_lines") == nil {
		return DefaultChatCodeMaxLines, nil
	}
	raw := strings.TrimSpace(gkconfig.String("chat.code_max_lines"))
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("chat.code_max_lines must be a whole number >= 0, got %q", raw)
	}
	return n, nil
}

// ChatAskOutsideDirs reports whether an interactive chat asks the person to
// authorize a directory a file tool wants to reach outside the workspace and the
// session's added directories (chat.ask_outside_dirs, on by default). It takes
// the same values as chat.markdown. The question is asked on the terminal, so
// run, ACP and a chat without a terminal keep refusing such a call as before.
func ChatAskOutsideDirs() bool {
	return chatSwitch("chat.ask_outside_dirs")
}

// ChatUndoConfirmEnabled reports whether :undo asks for confirmation before it
// changes files. It is on by default and accepts the same bool/string switches
// as the other chat safety settings.
func ChatUndoConfirmEnabled() bool {
	return chatSwitch("chat.undo_confirm")
}

// chatSwitch reads a chat.* switch: a missing key is on, a bool is used as is,
// and a string is off for "off"/"false" (any case, surrounding spaces ignored).
func chatSwitch(key string) bool {
	switch value := gkconfig.Get(key).(type) {
	case bool:
		return value
	case string:
		return !strings.EqualFold(strings.TrimSpace(value), "off") && !strings.EqualFold(strings.TrimSpace(value), "false")
	default:
		return true
	}
}

// ChatAgentProgress reads chat.agent_progress. Summary is the default so a busy
// group of sub-agents does not fill the terminal with one line per tool call.
func ChatAgentProgress() (string, error) {
	value := strings.ToLower(strings.TrimSpace(gkconfig.String("chat.agent_progress")))
	if value == "" {
		return ChatAgentProgressSummary, nil
	}
	switch value {
	case ChatAgentProgressSummary, ChatAgentProgressFull, ChatAgentProgressOff:
		return value, nil
	default:
		return "", fmt.Errorf("chat.agent_progress must be summary, full or off, got %q", value)
	}
}

// ChatNotifyMode selects terminal notifications: auto, bell, osc9 or off.
func ChatNotifyMode() (string, error) {
	value := strings.ToLower(strings.TrimSpace(gkconfig.String("chat.notify")))
	if value == "" {
		value = "auto"
	}
	switch value {
	case "auto", "bell", "osc9", "off":
		return value, nil
	}
	return "", fmt.Errorf("chat.notify must be auto, bell, osc9 or off, got %q", value)
}

// ChatNotifyAfter reads the minimum turn duration before a completion notice.
func ChatNotifyAfter() (time.Duration, error) {
	if gkconfig.Get("chat.notify_after") == nil {
		return time.Minute, nil
	}
	raw := strings.TrimSpace(gkconfig.String("chat.notify_after"))
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("chat.notify_after must be a non-negative duration, got %q", raw)
	}
	return d, nil
}
