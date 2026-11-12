package axconfig

import (
	"testing"

	goccyyaml "github.com/goccy/go-yaml"
	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// loadHookEntriesConfig replaces the process configuration with the hooks block
// of content, the way a config.yaml is loaded (the YAML loader yields []any).
func loadHookEntriesConfig(t *testing.T, content string) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	var data map[string]any
	assert.Require(t, assert.NoErr(t, goccyyaml.Unmarshal([]byte(content), &data)))
	assert.Require(t, assert.NoErr(t, gkconfig.LoadData(data)))
}

// setHookEntriesConfig writes hooks.commands through gkconfig.Set, which keeps
// the Go container type instead of the YAML loader's []any.
func setHookEntriesConfig(t *testing.T, value any) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, gkconfig.Set("hooks.commands", value))
}

// assertHookEntriesError runs ReadHookEntries and asserts the error text names
// the offending position and field.
func assertHookEntriesError(t *testing.T, wantContains string) {
	t.Helper()
	entries, err := ReadHookEntries()
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), wantContains)
	assert.Eq(t, 0, len(entries))
}

func TestReadHookEntries(t *testing.T) {
	t.Run("unset returns no entries", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)

		entries, err := ReadHookEntries()
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(entries))
	})

	t.Run("empty list returns no entries", func(t *testing.T) {
		loadHookEntriesConfig(t, "hooks:\n  commands: []\n")

		entries, err := ReadHookEntries()
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(entries))
	})

	t.Run("reads two entries in configuration order", func(t *testing.T) {
		loadHookEntriesConfig(t, `hooks:
  commands:
    - command: ["gofer", "hook", "generic", "--agent", "suga"]
      timeout_ms: 1500
    - command: ["idevstd", "script", "continuation_hook", "--agent", "suga"]
`)

		entries, err := ReadHookEntries()
		assert.NoErr(t, err)
		assert.Eq(t, 2, len(entries))
		assert.Eq(t, []string{"gofer", "hook", "generic", "--agent", "suga"}, entries[0].Command)
		assert.Eq(t, 1500, entries[0].TimeoutMS)
		assert.Eq(t, []string{"idevstd", "script", "continuation_hook", "--agent", "suga"}, entries[1].Command)
		// 0 means "unset": internal/hooks applies the hooks.timeout_ms default.
		assert.Eq(t, 0, entries[1].TimeoutMS)
	})

	t.Run("accepts the typed slice written by gkconfig.Set", func(t *testing.T) {
		setHookEntriesConfig(t, []map[string]any{
			{"command": []string{"first"}, "timeout_ms": 2000},
			{"command": []any{"second", "--flag"}},
		})

		entries, err := ReadHookEntries()
		assert.NoErr(t, err)
		assert.Eq(t, 2, len(entries))
		assert.Eq(t, []string{"first"}, entries[0].Command)
		assert.Eq(t, 2000, entries[0].TimeoutMS)
		assert.Eq(t, []string{"second", "--flag"}, entries[1].Command)
		assert.Eq(t, 0, entries[1].TimeoutMS)
	})

	t.Run("accepts both command list types", func(t *testing.T) {
		setHookEntriesConfig(t, []any{
			map[string]any{"command": []string{"typed"}},
			map[string]any{"command": []any{"generic", "arg"}},
		})

		entries, err := ReadHookEntries()
		assert.NoErr(t, err)
		assert.Eq(t, []string{"typed"}, entries[0].Command)
		assert.Eq(t, []string{"generic", "arg"}, entries[1].Command)
	})

	t.Run("accepts the numeric timeout_ms types", func(t *testing.T) {
		setHookEntriesConfig(t, []any{
			map[string]any{"command": []string{"a"}, "timeout_ms": 10},
			map[string]any{"command": []string{"b"}, "timeout_ms": int64(20)},
			map[string]any{"command": []string{"c"}, "timeout_ms": uint64(30)},
			map[string]any{"command": []string{"d"}, "timeout_ms": float64(40)},
		})

		entries, err := ReadHookEntries()
		assert.NoErr(t, err)
		assert.Require(t, assert.Eq(t, 4, len(entries)))
		assert.Eq(t, 10, entries[0].TimeoutMS)
		assert.Eq(t, 20, entries[1].TimeoutMS)
		assert.Eq(t, 30, entries[2].TimeoutMS)
		assert.Eq(t, 40, entries[3].TimeoutMS)
	})

	t.Run("rejects a non-list value", func(t *testing.T) {
		t.Run("string", func(t *testing.T) {
			loadHookEntriesConfig(t, "hooks:\n  commands: gofer hook\n")
			assertHookEntriesError(t, "hooks.commands must be a list of entries")
		})

		t.Run("mapping", func(t *testing.T) {
			loadHookEntriesConfig(t, "hooks:\n  commands:\n    command: [gofer, hook]\n")
			assertHookEntriesError(t, "hooks.commands must be a list of entries")
		})
	})

	t.Run("rejects an entry that is not a mapping", func(t *testing.T) {
		setHookEntriesConfig(t, []any{"gofer hook"})

		assertHookEntriesError(t, "hooks.commands[0]: must be a mapping")
	})

	t.Run("rejects an unknown entry field", func(t *testing.T) {
		loadHookEntriesConfig(t, `hooks:
  commands:
    - command: [gofer, hook]
      stop_args: [--wait]
`)

		assertHookEntriesError(t, `hooks.commands[0]: unknown field "stop_args"`)
	})

	t.Run("reports the first unknown field alphabetically", func(t *testing.T) {
		setHookEntriesConfig(t, []any{map[string]any{
			"command": []string{"gofer"}, "zebra": 1, "alpha": 2,
		}})

		assertHookEntriesError(t, `hooks.commands[0]: unknown field "alpha"`)
	})

	t.Run("rejects a bad command list", func(t *testing.T) {
		t.Run("missing", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"timeout_ms": 1000}})
			assertHookEntriesError(t, "hooks.commands[0].command")
		})

		t.Run("empty", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": []string{}}})
			assertHookEntriesError(t, "hooks.commands[0].command")
		})

		t.Run("blank program name", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": []string{"  ", "--flag"}}})
			assertHookEntriesError(t, "hooks.commands[0].command")
		})

		t.Run("non-string element", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": []any{"gofer", 1}}})
			assertHookEntriesError(t, "hooks.commands[0].command")
		})

		t.Run("string instead of list", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": "gofer hook"}})
			assertHookEntriesError(t, "hooks.commands[0].command")
		})
	})

	t.Run("rejects a bad timeout_ms", func(t *testing.T) {
		t.Run("zero", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": []string{"gofer"}, "timeout_ms": 0}})
			assertHookEntriesError(t, "hooks.commands[0].timeout_ms")
		})

		t.Run("negative", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": []string{"gofer"}, "timeout_ms": -5}})
			assertHookEntriesError(t, "hooks.commands[0].timeout_ms")
		})

		t.Run("fractional", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": []string{"gofer"}, "timeout_ms": 1.5}})
			assertHookEntriesError(t, "hooks.commands[0].timeout_ms")
		})

		t.Run("string", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": []string{"gofer"}, "timeout_ms": "1000"}})
			assertHookEntriesError(t, "hooks.commands[0].timeout_ms")
		})

		t.Run("uint64 overflow", func(t *testing.T) {
			setHookEntriesConfig(t, []any{map[string]any{"command": []string{"gofer"}, "timeout_ms": ^uint64(0)}})
			assertHookEntriesError(t, "hooks.commands[0].timeout_ms")
		})
	})

	t.Run("rejects a bad second entry by position", func(t *testing.T) {
		loadHookEntriesConfig(t, `hooks:
  commands:
    - command: [gofer, hook]
    - timeout_ms: 1000
`)

		assertHookEntriesError(t, "hooks.commands[1].command")
	})
}
