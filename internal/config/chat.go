package axconfig

import (
	"fmt"
	"os"
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

// ChatHistoryScope* are the values chat.history_scope accepts: project | all.
const (
	ChatHistoryScopeProject = "project"
	ChatHistoryScopeAll     = "all"
)

// ChatHistoryScope reads chat.history_scope: project (the default) makes the
// Up/Down history only recall lines typed in the current project; all keeps the
// old behaviour, where the current project comes first and the other projects'
// lines follow once its lines are exhausted.
func ChatHistoryScope() (string, error) {
	value := strings.ToLower(strings.TrimSpace(gkconfig.String("chat.history_scope")))
	if value == "" {
		return ChatHistoryScopeProject, nil
	}
	switch value {
	case ChatHistoryScopeProject, ChatHistoryScopeAll:
		return value, nil
	}
	return "", fmt.Errorf("chat.history_scope must be project or all, got %q", value)
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

// ChatColors reads chat.colors: the styles of chat's own lines (a tool call, its
// result, a plan, a hint), as a mapping of element to color - style and mode, plus
// one color per tool family and for ok/error/dim. The values are resolved by chat
// itself (internal/chat/colors.go), which knows the element names and the color
// syntax and refuses an unknown one: this reader only makes sure the block is a
// mapping, so a scalar written where the block belongs is an error rather than a
// silently ignored setting.
func ChatColors() (map[string]string, error) {
	raw := gkconfig.Get("chat.colors")
	if raw == nil {
		return nil, nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("chat.colors must be a mapping of element to color, like {style: name, read: blue}")
	}
	colors := make(map[string]string, len(fields))
	for key, value := range fields {
		key = strings.TrimSpace(key)
		colors[key] = strings.TrimSpace(stringValue(value))
		if colors[key] == "" {
			delete(colors, key)
		}
	}
	return colors, nil
}

// ChatTheme resolves chat.theme to the actual terminal palette chat paints its
// markdown and code with: "dark" or "light". A theme in the config file is the
// default; the environment variable SUGA_THEME (dark|light) overrides it, which
// lets one shell run chat on the other palette without touching config.yaml.
// auto (the default) takes the background from the terminal's COLORFGBG report
// ("foreground;background"; the last segment is the background): 7 or 15 mean a
// light background, anything else (or no report) a dark one. No terminal query
// is made; an unset COLORFGBG stays dark, like before the option existed.
func ChatTheme() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("SUGA_THEME")); raw != "" {
		value := strings.ToLower(raw)
		switch value {
		case ChatThemeDark, ChatThemeLight:
			return value, nil
		default:
			return "", fmt.Errorf("SUGA_THEME must be dark or light, got %q", raw)
		}
	}
	value := strings.ToLower(strings.TrimSpace(gkconfig.String("chat.theme")))
	if value == "" {
		value = ChatThemeAuto
	}
	switch value {
	case ChatThemeDark, ChatThemeLight:
		return value, nil
	case ChatThemeAuto:
		return colorFgbgBackground(), nil
	}
	return "", fmt.Errorf("chat.theme must be auto, dark or light, got %q", value)
}

// ChatTheme* are the values chat.theme (and SUGA_THEME) accept: auto | dark | light.
const (
	ChatThemeAuto  = "auto"
	ChatThemeDark  = "dark"
	ChatThemeLight = "light"
)

// colorFgbgBackground reads the background color of the terminal from the
// COLORFGBG environment variable ("foreground;background"; some emulators add a
// third segment, so the last one is taken): light for a background of 7 or 15,
// dark for anything else - including a report chat cannot make sense of.
func colorFgbgBackground() string {
	raw := os.Getenv("COLORFGBG")
	segments := strings.Split(raw, ";")
	background := strings.TrimSpace(segments[len(segments)-1])
	if background == "7" || background == "15" {
		return ChatThemeLight
	}
	return ChatThemeDark
}

// ChatWin32InputMode selects win32-input-mode (DECSET 9001) for the chat input:
// auto (the default) asks for it in Windows Terminal only, on always asks for it
// and off leaves the keys as plain bytes. With the mode on the terminal reports
// every key press as a record, which is what lets Ctrl+Enter put a newline into
// the input line instead of submitting it (WT binds Alt+Enter to fullscreen and
// sends Ctrl+Enter as Ctrl+J's byte otherwise).
func ChatWin32InputMode() (string, error) {
	value := strings.ToLower(strings.TrimSpace(gkconfig.String("chat.win32_input_mode")))
	if value == "" {
		value = "auto"
	}
	switch value {
	case "auto", "on", "off":
		return value, nil
	}
	return "", fmt.Errorf("chat.win32_input_mode must be auto, on or off, got %q", value)
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
