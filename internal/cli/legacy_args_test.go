package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestNormalizeArgs covers the option-in-front rewrite: since v0.36 a first
// argument that is a chat option means `suga chat …` (design
// 2026-10-19-cli-command-groups-design.md D6). The old top-level commands are
// not rewritten any more (v0.38 removed them, see TestLegacyCommandHint).
func TestNormalizeArgs(t *testing.T) {
	t.Run("chat options work without chat", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []string
			want []string
		}{
			{"resume", []string{"--resume", "x"}, []string{"chat", "--resume", "x"}},
			{"model short", []string{"-m", "y"}, []string{"chat", "-m", "y"}},
			{"resume then another word", []string{"--resume", "x", "run"},
				[]string{"chat", "--resume", "x", "run"}},
			{"empty", []string{}, []string{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				assert.Eq(t, tc.want, NormalizeArgs(tc.args))
			})
		}
	})

	t.Run("left as typed", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []string
		}{
			{"help", []string{"--help"}},
			{"help short", []string{"-h"}},
			{"version", []string{"-V"}},
			{"version long", []string{"--version"}},
			{"run", []string{"run", "--prompt", "hi"}},
			{"chat", []string{"chat", "--resume", "x"}},
			{"new groups", []string{"inspect", "trace", "s1"}},
			{"new data path", []string{"data", "gc", "--older-than", "30d"}},
			{"config", []string{"config", "get", "model"}},
			// An old command name is not rewritten either: it must fail.
			{"old trace", []string{"trace", "s1", "--json"}},
			{"old gc", []string{"gc", "--vacuum"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				assert.Eq(t, tc.args, NormalizeArgs(tc.args))
			})
		}
	})
}

// TestLegacyCommandHint covers the static table that only feeds the hint of an
// unknown command: every old form names the new spelling, everything else has
// no hint.
func TestLegacyCommandHint(t *testing.T) {
	t.Run("old forms name the new spelling", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []string
			old  string
			new  string
		}{
			{"trace", []string{"trace", "s1"}, "trace", "inspect trace"},
			{"stats", []string{"stats", "--since", "7d"}, "stats", "inspect stats"},
			{"export", []string{"export", "s1"}, "export", "inspect export"},
			{"manifest", []string{"manifest", "--json"}, "manifest", "inspect manifest"},
			{"backup", []string{"backup", "--output", "a.zip"}, "backup", "data backup"},
			{"restore", []string{"restore", "a.zip"}, "restore", "data restore"},
			{"gc", []string{"gc", "--older-than", "30d"}, "gc", "data gc"},
			{"gc shrink-fts", []string{"gc", "--shrink-fts", "--batch", "100"}, "gc --shrink-fts", "data shrink-fts"},
			{"gc vacuum", []string{"gc", "--vacuum"}, "gc --vacuum", "data vacuum"},
			{"gc compress-spill", []string{"gc", "--compress-spill", "--apply"}, "gc --compress-spill", "data compress-spill"},
			// The old precedence wins: shrink-fts before vacuum.
			{"gc precedence", []string{"gc", "--vacuum", "--shrink-fts"}, "gc --shrink-fts", "data shrink-fts"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				hint := legacyCommandHint(tc.args)
				assert.StrContains(t, hint, "提示：suga "+tc.old+" 已改为 suga "+tc.new)
				assert.StrContains(t, hint, "旧写法已在 v0.38 移除")
			})
		}
	})

	t.Run("no hint for current forms", func(t *testing.T) {
		for _, args := range [][]string{
			nil,
			{},
			{"inspect", "trace", "s1"},
			{"data", "gc", "--vacuum"},
			{"data", "bodies", "--status"},
			{"chat", "--resume", "x"},
			{"nope"},
		} {
			assert.Eq(t, "", legacyCommandHint(args))
		}
	})
}

// TestRemovedDataCommandHint checks the forms v0.39.0 deprecated and v0.41.0
// removed: they fail as unknown commands or unknown flags, the error names the
// replacement (or sends a pre-v0.36 ledger to v0.40) and the exit code is not 0.
func TestRemovedDataCommandHint(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"renamed command", []string{"data", "tool-bodies", "--status"}, "suga data tool-bodies 已在 v0.41.0 移除，改用 suga data bodies"},
		{"dedupe job", []string{"data", "dedupe-tool-results"}, "若需迁移旧库请用 v0.40"},
		{"shrink-fts job", []string{"data", "shrink-fts", "--status"}, "若需迁移旧库请用 v0.40"},
		{"slim job", []string{"data", "bodies", "--slim"}, "若需迁移旧库请用 v0.40"},
		{"rename-files job", []string{"data", "bodies", "--rename-files"}, "若需迁移旧库请用 v0.40"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := RunArgs(newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr), tc.args)
			requireErrContains(t, err, tc.want)
			assert.Eq(t, 1, ExitCode(err))
		})
	}

	t.Run("the kept jobs still run", func(t *testing.T) {
		assert.Eq(t, "", removedDataHint([]string{"data", "bodies", "--status"}))
		assert.Eq(t, "", removedDataHint([]string{"data", "vacuum"}))
	})
}

// TestRunArgsOldFormError checks the real dispatch path: an old top-level
// command reaches the command tree as typed, fails there as an unknown command,
// and the error names the new spelling.
func TestRunArgsOldFormError(t *testing.T) {
	t.Run("old form fails with the hint", func(t *testing.T) {
		var stdout, stderr strings.Builder
		err := RunArgs(newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr), []string{"trace", "s1", "--json"})
		requireErrContains(t, err, `input not exists command "trace"`)
		requireErrContains(t, err, "已改为 suga inspect trace")
		assert.Eq(t, 1, ExitCode(err))
		assert.Eq(t, "", stdout.String())
	})

	t.Run("unknown command without a hint", func(t *testing.T) {
		var stdout, stderr strings.Builder
		err := RunArgs(newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr), []string{"nope"})
		requireErrContains(t, err, `input not exists command "nope"`)
		assert.False(t, strings.Contains(err.Error(), "提示："))
	})

	t.Run("current command runs", func(t *testing.T) {
		var stdout, stderr strings.Builder
		err := RunArgs(newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr), []string{"version"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "suga dev")
	})
}

// requireErrContains asserts err is non-nil and its message holds want.
func requireErrContains(t *testing.T, err error, want string) {
	t.Helper()
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), want)
}
