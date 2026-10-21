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
