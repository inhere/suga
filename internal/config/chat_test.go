package axconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestChatMarkdownEnabled: chat.markdown is on unless it is turned off; the key
// takes a bool or a string.
func TestChatMarkdownEnabled(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default on", func(t *testing.T) {
		load(t, "model: openai/m\n")
		assert.True(t, ChatMarkdownEnabled())
	})
	t.Run("bool off", func(t *testing.T) {
		load(t, "chat:\n  markdown: false\n")
		assert.False(t, ChatMarkdownEnabled())
	})
	t.Run("bool on", func(t *testing.T) {
		load(t, "chat:\n  markdown: true\n")
		assert.True(t, ChatMarkdownEnabled())
	})
	t.Run("string off", func(t *testing.T) {
		load(t, "chat:\n  markdown: off\n")
		assert.False(t, ChatMarkdownEnabled())
	})
	t.Run("other chat keys do not decide it", func(t *testing.T) {
		load(t, "chat:\n  other: 1\n")
		assert.True(t, ChatMarkdownEnabled())
	})
}

// TestChatSpinnerEnabled: chat.spinner takes the same values as chat.markdown
// and is on by default; each key decides on its own.
func TestChatSpinnerEnabled(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default on", func(t *testing.T) {
		load(t, "model: openai/m\n")
		assert.True(t, ChatSpinnerEnabled())
	})
	t.Run("bool off", func(t *testing.T) {
		load(t, "chat:\n  spinner: false\n")
		assert.False(t, ChatSpinnerEnabled())
	})
	t.Run("string off", func(t *testing.T) {
		load(t, "chat:\n  spinner: \"off\"\n")
		assert.False(t, ChatSpinnerEnabled())
	})
	t.Run("bool on", func(t *testing.T) {
		load(t, "chat:\n  spinner: true\n")
		assert.True(t, ChatSpinnerEnabled())
	})
	t.Run("markdown does not decide it", func(t *testing.T) {
		load(t, "chat:\n  markdown: false\n")
		assert.True(t, ChatSpinnerEnabled())
	})
}

// TestChatAgentProgress validates the enum and its summary default.
func TestChatAgentProgress(t *testing.T) {
	load := chatConfigLoader(t)
	for _, tc := range []struct {
		name, value, want string
		wantErr           bool
	}{
		{name: "default summary", want: "summary"},
		{name: "full", value: "full", want: "full"},
		{name: "off", value: "off", want: "off"},
		{name: "invalid", value: "nope", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value == "" {
				load(t, "model: openai/m\n")
			} else {
				load(t, "chat:\n  agent_progress: "+tc.value+"\n")
			}
			got, err := ChatAgentProgress()
			if tc.wantErr {
				assert.Err(t, err)
				return
			}
			assert.NoErr(t, err)
			assert.Eq(t, tc.want, got)
		})
	}
}

// TestChatHighlightInputEnabled: chat.highlight_input takes the same values as
// chat.markdown, is on by default and is decided on its own.
func TestChatHighlightInputEnabled(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default on", func(t *testing.T) {
		load(t, "model: openai/m\n")
		assert.True(t, ChatHighlightInputEnabled())
	})
	t.Run("bool off", func(t *testing.T) {
		load(t, "chat:\n  highlight_input: false\n")
		assert.False(t, ChatHighlightInputEnabled())
	})
	t.Run("string off", func(t *testing.T) {
		load(t, "chat:\n  highlight_input: \"off\"\n")
		assert.False(t, ChatHighlightInputEnabled())
	})
	t.Run("bool on", func(t *testing.T) {
		load(t, "chat:\n  highlight_input: true\n")
		assert.True(t, ChatHighlightInputEnabled())
	})
	t.Run("spinner does not decide it", func(t *testing.T) {
		load(t, "chat:\n  spinner: false\n")
		assert.True(t, ChatHighlightInputEnabled())
	})
}

// TestChatTerminalTitleEnabled: chat.terminal_title takes the same values as
// chat.markdown, is on by default and is decided on its own.
func TestChatTerminalTitleEnabled(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default on", func(t *testing.T) {
		load(t, "model: openai/m\n")
		assert.True(t, ChatTerminalTitleEnabled())
	})
	t.Run("bool off", func(t *testing.T) {
		load(t, "chat:\n  terminal_title: false\n")
		assert.False(t, ChatTerminalTitleEnabled())
	})
	t.Run("string off", func(t *testing.T) {
		load(t, "chat:\n  terminal_title: \"off\"\n")
		assert.False(t, ChatTerminalTitleEnabled())
	})
	t.Run("bool on", func(t *testing.T) {
		load(t, "chat:\n  terminal_title: true\n")
		assert.True(t, ChatTerminalTitleEnabled())
	})
	t.Run("spinner does not decide it", func(t *testing.T) {
		load(t, "chat:\n  spinner: false\n")
		assert.True(t, ChatTerminalTitleEnabled())
	})
}

// TestChatStatusMetricsEnabled: chat.status_metrics takes the same values as
// chat.markdown, is on by default and is decided on its own.
func TestChatStatusMetricsEnabled(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default on", func(t *testing.T) {
		load(t, "model: openai/m\n")
		assert.True(t, ChatStatusMetricsEnabled())
	})
	t.Run("bool off", func(t *testing.T) {
		load(t, "chat:\n  status_metrics: false\n")
		assert.False(t, ChatStatusMetricsEnabled())
	})
	t.Run("string off", func(t *testing.T) {
		load(t, "chat:\n  status_metrics: \"off\"\n")
		assert.False(t, ChatStatusMetricsEnabled())
	})
	t.Run("string on", func(t *testing.T) {
		load(t, "chat:\n  status_metrics: \"on\"\n")
		assert.True(t, ChatStatusMetricsEnabled())
	})
	t.Run("spinner does not decide it", func(t *testing.T) {
		load(t, "chat:\n  spinner: false\n")
		assert.True(t, ChatStatusMetricsEnabled())
	})
}

// TestChatAskOutsideDirs: chat.ask_outside_dirs takes the same values as
// chat.markdown, is on by default and is decided on its own.
func TestChatAskOutsideDirs(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default on", func(t *testing.T) {
		load(t, "model: openai/m\n")
		assert.True(t, ChatAskOutsideDirs())
	})
	t.Run("bool off", func(t *testing.T) {
		load(t, "chat:\n  ask_outside_dirs: false\n")
		assert.False(t, ChatAskOutsideDirs())
	})
	t.Run("string off", func(t *testing.T) {
		load(t, "chat:\n  ask_outside_dirs: \"off\"\n")
		assert.False(t, ChatAskOutsideDirs())
	})
	t.Run("bool on", func(t *testing.T) {
		load(t, "chat:\n  ask_outside_dirs: true\n")
		assert.True(t, ChatAskOutsideDirs())
	})
	t.Run("terminal_title does not decide it", func(t *testing.T) {
		load(t, "chat:\n  terminal_title: false\n")
		assert.True(t, ChatAskOutsideDirs())
	})
}

// TestChatDiffEnabled: chat.diff takes the same values as chat.markdown, is on
// by default and is decided on its own.
func TestChatDiffEnabled(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default on", func(t *testing.T) {
		load(t, "model: openai/m\n")
		assert.True(t, ChatDiffEnabled())
	})
	t.Run("bool off", func(t *testing.T) {
		load(t, "chat:\n  diff: false\n")
		assert.False(t, ChatDiffEnabled())
	})
	t.Run("string off", func(t *testing.T) {
		load(t, "chat:\n  diff: \"off\"\n")
		assert.False(t, ChatDiffEnabled())
	})
	t.Run("bool on", func(t *testing.T) {
		load(t, "chat:\n  diff: true\n")
		assert.True(t, ChatDiffEnabled())
	})
	t.Run("markdown does not decide it", func(t *testing.T) {
		load(t, "chat:\n  markdown: false\n")
		assert.True(t, ChatDiffEnabled())
	})
}

// TestChatDiffLines: chat.diff_lines is 20 lines unless set, and a value that is
// not a whole number of at least one line is an error instead of being guessed.
func TestChatDiffLines(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default", func(t *testing.T) {
		load(t, "model: openai/m\n")
		lines, err := ChatDiffLines()
		assert.NoErr(t, err)
		assert.Eq(t, 20, lines)
	})
	t.Run("set", func(t *testing.T) {
		load(t, "chat:\n  diff_lines: 5\n")
		lines, err := ChatDiffLines()
		assert.NoErr(t, err)
		assert.Eq(t, 5, lines)
	})
	t.Run("string", func(t *testing.T) {
		load(t, "chat:\n  diff_lines: \"8\"\n")
		lines, err := ChatDiffLines()
		assert.NoErr(t, err)
		assert.Eq(t, 8, lines)
	})
	for name, body := range map[string]string{
		"zero":         "chat:\n  diff_lines: 0\n",
		"negative":     "chat:\n  diff_lines: -3\n",
		"not a number": "chat:\n  diff_lines: many\n",
	} {
		t.Run(name, func(t *testing.T) {
			load(t, body)
			_, err := ChatDiffLines()
			assert.Err(t, err)
		})
	}
}

// TestChatCodeSettings: code highlighting is on and folds after 40 lines unless
// set; a negative fold limit or a non-number is an error, 0 is the documented
// "fold nothing".
func TestChatCodeSettings(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("defaults", func(t *testing.T) {
		load(t, "model: openai/m\n")
		assert.True(t, ChatCodeHighlightEnabled())
		lines, err := ChatCodeMaxLines()
		assert.NoErr(t, err)
		assert.Eq(t, 40, lines)
	})
	t.Run("set", func(t *testing.T) {
		load(t, "chat:\n  code_highlight: off\n  code_max_lines: 12\n")
		assert.False(t, ChatCodeHighlightEnabled())
		lines, err := ChatCodeMaxLines()
		assert.NoErr(t, err)
		assert.Eq(t, 12, lines)
	})
	t.Run("zero folds nothing", func(t *testing.T) {
		load(t, "chat:\n  code_max_lines: 0\n")
		lines, err := ChatCodeMaxLines()
		assert.NoErr(t, err)
		assert.Eq(t, 0, lines)
	})
	for name, body := range map[string]string{
		"negative":     "chat:\n  code_max_lines: -1\n",
		"not a number": "chat:\n  code_max_lines: many\n",
	} {
		t.Run(name, func(t *testing.T) {
			load(t, body)
			_, err := ChatCodeMaxLines()
			assert.Err(t, err)
		})
	}
}

// TestChatWin32InputMode: chat.win32_input_mode is auto unless set, takes the three
// documented values in any case and rejects anything else.
func TestChatWin32InputMode(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default auto", func(t *testing.T) {
		load(t, "model: openai/m\n")
		mode, err := ChatWin32InputMode()
		assert.NoErr(t, err)
		assert.Eq(t, "auto", mode)
	})
	t.Run("on", func(t *testing.T) {
		load(t, "chat:\n  win32_input_mode: ON\n")
		mode, err := ChatWin32InputMode()
		assert.NoErr(t, err)
		assert.Eq(t, "on", mode)
	})
	t.Run("off", func(t *testing.T) {
		load(t, "chat:\n  win32_input_mode: \" off \"\n")
		mode, err := ChatWin32InputMode()
		assert.NoErr(t, err)
		assert.Eq(t, "off", mode)
	})
	t.Run("invalid", func(t *testing.T) {
		load(t, "chat:\n  win32_input_mode: yes-please\n")
		_, err := ChatWin32InputMode()
		assert.Err(t, err)
	})
	t.Run("read through the settings registry", func(t *testing.T) {
		load(t, "chat:\n  win32_input_mode: off\n")
		settings, err := ReadChatSettings()
		assert.NoErr(t, err)
		assert.Eq(t, "off", settings.Win32InputMode)
		value, err := ReadConfigValue("chat.win32_input_mode")
		assert.NoErr(t, err)
		assert.Eq(t, "off", value.Value)
	})
}

// TestChatTheme: chat.theme is auto unless set and resolves to the palette
// (dark/light); SUGA_THEME overrides the config, and both an unknown value on
// either is an error. The COLORFGBG cases live in TestChatThemeColorFgbg.
func TestChatTheme(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default auto resolves through COLORFGBG", func(t *testing.T) {
		load(t, "model: openai/m\n")
		t.Setenv("SUGA_THEME", "")
		t.Setenv("COLORFGBG", "")
		value, err := ChatTheme()
		assert.NoErr(t, err)
		assert.Eq(t, "dark", value) // an unset COLORFGBG stays dark
	})
	t.Run("dark and light stay dark and light", func(t *testing.T) {
		load(t, "chat:\n  theme: dark\n")
		value, err := ChatTheme()
		assert.NoErr(t, err)
		assert.Eq(t, "dark", value)
		load(t, "chat:\n  theme: LIGHT\n")
		value, err = ChatTheme()
		assert.NoErr(t, err)
		assert.Eq(t, "light", value)
	})
	t.Run("an unknown config value is an error", func(t *testing.T) {
		load(t, "chat:\n  theme: sunset\n")
		_, err := ChatTheme()
		assert.Err(t, err)
	})
	t.Run("SUGA_THEME overrides the config", func(t *testing.T) {
		load(t, "chat:\n  theme: dark\n")
		t.Setenv("SUGA_THEME", "light")
		value, err := ChatTheme()
		assert.NoErr(t, err)
		assert.Eq(t, "light", value)
	})
	t.Run("an unknown SUGA_THEME is an error", func(t *testing.T) {
		load(t, "model: openai/m\n")
		t.Setenv("SUGA_THEME", "sunset")
		_, err := ChatTheme()
		assert.Err(t, err)
	})
	t.Run("read through the settings registry", func(t *testing.T) {
		load(t, "chat:\n  theme: light\n")
		settings, err := ReadChatSettings()
		assert.NoErr(t, err)
		assert.Eq(t, "light", settings.Theme)
		value, err := ReadConfigValue("chat.theme")
		assert.NoErr(t, err)
		assert.Eq(t, "light", value.Value)
	})
}

// TestChatThemeColorFgbg: auto takes the background color from the last segment
// of $COLORFGBG; a light background (7, 15) resolves to light, anything else and
// an unset variable to dark.
func TestChatThemeColorFgbg(t *testing.T) {
	load := chatConfigLoader(t)
	cases := map[string]string{
		"0;15":      "light",
		"15;0":      "dark",
		"0;7":       "light",
		"default":   "dark",  // the reported default fg; bg unset
		"40;47":     "dark",  // a non-ANSI-code report takes the safe default
		"38;5;0;15": "light", // some emulators add a third segment
	}
	for fgbg, want := range cases {
		t.Run(fgbg, func(t *testing.T) {
			load(t, "model: openai/m\n")
			t.Setenv("SUGA_THEME", "")
			t.Setenv("COLORFGBG", fgbg)
			got, err := ChatTheme()
			assert.NoErr(t, err)
			assert.Eq(t, want, got)
		})
	}
	t.Run("unset stays dark", func(t *testing.T) {
		load(t, "model: openai/m\n")
		t.Setenv("SUGA_THEME", "")
		t.Setenv("COLORFGBG", "")
		got, err := ChatTheme()
		assert.NoErr(t, err)
		assert.Eq(t, "dark", got)
	})
	t.Run("the config file wins over COLORFGBG", func(t *testing.T) {
		load(t, "chat:\n  theme: dark\n")
		t.Setenv("SUGA_THEME", "")
		t.Setenv("COLORFGBG", "0;15")
		got, err := ChatTheme()
		assert.NoErr(t, err)
		assert.Eq(t, "dark", got)
	})
}

// chatConfigLoader returns a helper writing body to a fresh SUGA_HOME's
// config.yaml and loading it.
func chatConfigLoader(t *testing.T) func(t *testing.T, body string) {
	t.Helper()
	return func(t *testing.T, body string) {
		t.Helper()
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		root := t.TempDir()
		t.Setenv("SUGA_HOME", root)
		t.Setenv("SUGA_CONFIG", "")
		t.Chdir(t.TempDir())
		assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0600))
		assert.NoErr(t, Load())
	}
}

// TestChatColors: chat.colors is a mapping of element to color. A scalar where the
// block belongs is an error rather than a silently ignored setting; the values
// themselves are resolved by chat (internal/chat/colors.go), so this reader only
// hands them over. An empty value counts as unset and keeps the built-in default.
func TestChatColors(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("no block", func(t *testing.T) {
		load(t, "chat:\n  markdown: true\n")
		colors, err := ChatColors()
		assert.NoErr(t, err)
		assert.Empty(t, colors)
	})
	t.Run("a mapping of element to color", func(t *testing.T) {
		load(t, "chat:\n  colors:\n    style: line\n    mode: \"256\"\n    read: blue\n    shell: \"256:170\"\n")
		colors, err := ChatColors()
		assert.NoErr(t, err)
		assert.Eq(t, "line", colors["style"])
		assert.Eq(t, "256", colors["mode"])
		assert.Eq(t, "blue", colors["read"])
		assert.Eq(t, "256:170", colors["shell"])
	})
	t.Run("an empty value is unset", func(t *testing.T) {
		load(t, "chat:\n  colors:\n    read: \"\"\n")
		colors, err := ChatColors()
		assert.NoErr(t, err)
		_, shown := colors["read"]
		assert.False(t, shown)
	})
	t.Run("a scalar where the block belongs", func(t *testing.T) {
		load(t, "chat:\n  colors: blue\n")
		_, err := ChatColors()
		assert.Err(t, err)
	})
}

// TestChatHistoryScope: chat.history_scope 默认 project，接收 project/all，
// 其他值报错而不是静默改默认行为。
func TestChatHistoryScope(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("default project", func(t *testing.T) {
		load(t, "model: openai/m\n")
		got, err := ChatHistoryScope()
		assert.NoErr(t, err)
		assert.Eq(t, "project", got)
	})
	t.Run("project", func(t *testing.T) {
		load(t, "chat:\n  history_scope: project\n")
		got, err := ChatHistoryScope()
		assert.NoErr(t, err)
		assert.Eq(t, "project", got)
	})
	t.Run("all", func(t *testing.T) {
		load(t, "chat:\n  history_scope: all\n")
		got, err := ChatHistoryScope()
		assert.NoErr(t, err)
		assert.Eq(t, "all", got)
	})
	t.Run("invalid", func(t *testing.T) {
		load(t, "chat:\n  history_scope: everyone\n")
		_, err := ChatHistoryScope()
		assert.Err(t, err)
	})
}

func TestChatNotifySettings(t *testing.T) {
	load := chatConfigLoader(t)
	t.Run("defaults", func(t *testing.T) {
		load(t, "model: openai/m\n")
		mode, err := ChatNotifyMode()
		assert.NoErr(t, err)
		assert.Eq(t, "auto", mode)
		after, err := ChatNotifyAfter()
		assert.NoErr(t, err)
		assert.Eq(t, time.Minute, after)
	})
	t.Run("configured", func(t *testing.T) {
		load(t, "chat:\n  notify: osc9\n  notify_after: 1s\n")
		mode, err := ChatNotifyMode()
		assert.NoErr(t, err)
		assert.Eq(t, "osc9", mode)
		after, err := ChatNotifyAfter()
		assert.NoErr(t, err)
		assert.Eq(t, time.Second, after)
	})
	t.Run("invalid", func(t *testing.T) {
		load(t, "chat:\n  notify: pager\n")
		_, err := ChatNotifyMode()
		assert.Err(t, err)
	})
}
