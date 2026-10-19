package cli

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestRewriteLegacyArgs covers the argument rewrite of the v0.36 command-group
// move (design 2026-10-19-cli-command-groups-design.md): every old top-level
// command reaches its new group, `suga inspect trace …` is rewritten without a notice,
// and the forms that must NOT be rewritten stay as typed.
func TestRewriteLegacyArgs(t *testing.T) {
	t.Run("moved top-level commands", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			args   []string
			want   []string
			notice bool
		}{
			{"manifest", []string{"manifest", "--json"}, []string{"inspect", "manifest", "--json"}, true},
			{"stats", []string{"stats", "--since", "7d"}, []string{"inspect", "stats", "--since", "7d"}, true},
			{"export", []string{"export", "s1"}, []string{"inspect", "export", "s1"}, true},
			{"backup", []string{"backup", "--output", "a.zip"}, []string{"data", "backup", "--output", "a.zip"}, true},
			{"restore", []string{"restore", "a.zip"}, []string{"data", "restore", "a.zip"}, true},
			// Q2: trace is used a lot, its rewrite stays silent.
			{"trace is silent", []string{"trace", "s1", "--json"}, []string{"inspect", "trace", "s1", "--json"}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, notice := RewriteLegacyArgs(tc.args)
				assert.Eq(t, tc.want, got)
				assert.Eq(t, tc.notice, notice != "")
				if tc.notice {
					assert.StrContains(t, notice, "已改为 suga "+joinArgs(tc.want[:2])+"，旧写法将在 v0.38 移除")
				}
			})
		}
	})

	t.Run("gc splits into data subcommands", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []string
			want []string
			note string
		}{
			{"older-than", []string{"gc", "--older-than", "30d", "--apply"},
				[]string{"data", "gc", "--older-than", "30d", "--apply"}, "gc"},
			{"retention", []string{"gc", "--retention"}, []string{"data", "gc", "--retention"}, "gc"},
			{"shrink-fts", []string{"gc", "--shrink-fts", "--batch", "100"},
				[]string{"data", "shrink-fts", "--batch", "100"}, "gc --shrink-fts"},
			{"shrink-fts status", []string{"gc", "--status", "--shrink-fts"},
				[]string{"data", "shrink-fts", "--status"}, "gc --shrink-fts"},
			{"abort", []string{"gc", "--abort", "--shrink-fts"}, []string{"data", "shrink-fts", "--abort"}, "gc --shrink-fts"},
			{"vacuum", []string{"gc", "--vacuum"}, []string{"data", "vacuum"}, "gc --vacuum"},
			{"compress-spill", []string{"gc", "--compress-spill", "--apply"},
				[]string{"data", "compress-spill", "--apply"}, "gc --compress-spill"},
			// The old precedence wins: shrink-fts before vacuum before compress-spill.
			{"precedence", []string{"gc", "--vacuum", "--shrink-fts"}, []string{"data", "shrink-fts", "--vacuum"}, "gc --shrink-fts"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, notice := RewriteLegacyArgs(tc.args)
				assert.Eq(t, tc.want, got)
				assert.StrContains(t, notice, "提示：suga "+tc.note+" 已改为 suga ")
				assert.StrContains(t, notice, "旧写法将在 v0.38 移除")
			})
		}
	})

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
				got, notice := RewriteLegacyArgs(tc.args)
				assert.Eq(t, tc.want, got)
				assert.Eq(t, "", notice)
			})
		}
	})

	t.Run("no rewrite", func(t *testing.T) {
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
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, notice := RewriteLegacyArgs(tc.args)
				assert.Eq(t, tc.args, got)
				assert.Eq(t, "", notice)
			})
		}
	})
}

// joinArgs renders a short command path for the expected notice.
func joinArgs(args []string) string {
	out := ""
	for i, arg := range args {
		if i > 0 {
			out += " "
		}
		out += arg
	}
	return out
}
