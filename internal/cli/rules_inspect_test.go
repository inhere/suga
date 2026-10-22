package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// rulesCLIEnv isolates SUGA_HOME, workspace and provider the way
// manifestCLIEnv does: the command must never write into SUGA_HOME and never
// contact the provider.
func rulesCLIEnv(t *testing.T) (home, ws string) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home = filepath.Join(t.TempDir(), "suga-home") // must not exist before the command runs
	t.Setenv("SUGA_HOME", home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	ws = t.TempDir()
	t.Chdir(ws)
	assert.NoErr(t, gkconfig.Set("workspace", ws))
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	setTestProvider(t, "http://127.0.0.1:1/v1") // nothing listens: a request would fail the command
	return home, ws
}

func runInspectRules(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()
	return runCLI(t, ctx, append([]string{"inspect", "rules"}, args...)...)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644))
}

// builtinReport is the `--builtin --json` response of the command.
type builtinReport struct {
	Builtin []struct {
		File    string `json:"file"`
		Content string `json:"content"`
	} `json:"builtin"`
}

func TestInspectRulesCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("output lists the built-in rules with their layer", func(t *testing.T) {
		_, _ = rulesCLIEnv(t)
		out, err := runInspectRules(t, ctx, "output")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "go-build")
		assert.StrContains(t, out, "source builtin")
		assert.StrContains(t, out, "go-test")
	})
	t.Run("user then project layer merge and name their source file", func(t *testing.T) {
		home, ws := rulesCLIEnv(t)
		writeFile(t, filepath.Join(home, "output-rules.yaml"), `
rules:
  - name: user-check
    match: 'user'
  - name: go-test
    enabled: false
`)
		writeFile(t, filepath.Join(ws, ".suga", "output-rules.yaml"), `
rules:
  - name: project-check
    match: 'project'
`)
		out, err := runInspectRules(t, ctx, "output")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "user-check")
		assert.StrContains(t, out, filepath.Join(home, "output-rules.yaml"))
		assert.StrContains(t, out, "project-check")
		assert.StrContains(t, out, filepath.Join(ws, ".suga", "output-rules.yaml"))
		// the disabled built-in rule is out of the effective set (its name may
		// still appear as a substring of another rule's match, so pin the line)
		assert.StrNotContains(t, out, "\n  go-test (match")
	})
	t.Run("json exposes rule entries and skipped files", func(t *testing.T) {
		_, ws := rulesCLIEnv(t)
		userRules := filepath.Join(ws, ".suga", "output-rules.yaml")
		writeFile(t, userRules, "rules:\n  - match: 'x'\n")
		out, err := runInspectRules(t, ctx, "output", "--json")
		assert.Require(t, assert.NoErr(t, err))
		var report map[string]any
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(out), &report)))
		assert.Eq(t, "output", report["kind"])
		rules, ok := report["rules"].([]any)
		assert.Require(t, assert.True(t, ok && len(rules) > 0))
		first := rules[0].(map[string]any)
		assert.Eq(t, "builtin", first["source"])
		skipped, ok := report["skipped"].([]any)
		assert.Require(t, assert.True(t, ok && len(skipped) == 1))
		skip := skipped[0].(map[string]any)
		assert.Eq(t, userRules, skip["file"])
		assert.StrContains(t, skip["err"].(string), "name is required")
	})
	t.Run("nudge effective rules name their layer", func(t *testing.T) {
		home, _ := rulesCLIEnv(t)
		writeFile(t, filepath.Join(home, "nudge-rules.yaml"), `
rules:
  - name: promise-new
    kind: promise
    patterns: ['^let us just continue\b']
`)
		out, err := runInspectRules(t, ctx, "nudge")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "promise-zh: promise (builtin)")
		assert.StrContains(t, out, "promise-new: promise (")
		assert.StrContains(t, out, filepath.Join(home, "nudge-rules.yaml"))
	})
	t.Run("lang lists the language layers", func(t *testing.T) {
		_, _ = rulesCLIEnv(t)
		out, err := runInspectRules(t, ctx, "lang")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "Python: .py")
		assert.StrContains(t, out, "source=builtin")
	})
	t.Run("approval lists the effective rules of both files", func(t *testing.T) {
		home, ws := rulesCLIEnv(t)
		assert.NoErr(t, gkconfig.Set("tools.approval_rules.enabled", true))
		writeFile(t, filepath.Join(home, "approval-rules.yaml"), `
rules:
  - tool: web_fetch
    action: allow
`)
		writeFile(t, filepath.Join(ws, ".suga", "approval-rules.yaml"), `
rules:
  - tool: terraform
    action: ask
`)
		out, err := runInspectRules(t, ctx, "approval")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "(enabled=true)")
		assert.StrContains(t, out, "web_fetch")
		assert.StrContains(t, out, "terraform")
	})
	t.Run("builtin output prints the shipped YAML per file", func(t *testing.T) {
		_, _ = rulesCLIEnv(t)
		out, err := runInspectRules(t, ctx, "output", "--builtin")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "# rules/go.yaml")
		assert.StrContains(t, out, "name: go-test")
	})
	t.Run("approval has no builtin rules", func(t *testing.T) {
		_, _ = rulesCLIEnv(t)
		out, err := runInspectRules(t, ctx, "approval", "--builtin")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, out, "has no built-in rules")
	})
	t.Run("unknown kind and extra arguments fail", func(t *testing.T) {
		_, _ = rulesCLIEnv(t)
		_, err := runInspectRules(t, ctx, "wrong")
		assert.Err(t, err)
		_, err = runInspectRules(t, ctx, "output", "extra")
		assert.Err(t, err)
		_, err = runInspectRules(t, ctx)
		assert.Err(t, err)
	})
	t.Run("the command writes nothing into SUGA_HOME", func(t *testing.T) {
		home, _ := rulesCLIEnv(t)
		for _, kind := range []string{"approval", "output", "nudge", "lang", "output"} {
			_, err := runInspectRules(t, ctx, kind)
			assert.NoErr(t, err)
		}
		if entries, readErr := os.ReadDir(home); readErr == nil {
			t.Logf("SUGA_HOME entries after run (%d):", len(entries))
			for _, entry := range entries {
				t.Logf("  - %s", entry.Name())
			}
		}
		// Only the inspect read ran: SUGA_HOME must not even exist.
		_, statErr := os.Stat(home)
		assert.True(t, os.IsNotExist(statErr))
	})
}

// TestBuiltinRulesRoundTrip feeds the `--builtin` report back through the
// loaders: the shipped content must parse with the same functions that load
// user files, or a template printed by --builtin could not become a rule file.
func TestBuiltinRulesRoundTrip(t *testing.T) {
	_, _ = rulesCLIEnv(t)
	ctx := context.Background()

	t.Run("output files parse", func(t *testing.T) {
		out, err := runInspectRules(t, ctx, "output", "--builtin", "--json")
		assert.Require(t, assert.NoErr(t, err))
		var report builtinReport
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(out), &report)))
		assert.Require(t, assert.True(t, len(report.Builtin) > 0))
		names := 0
		for _, file := range report.Builtin {
			path := filepath.Join(t.TempDir(), "output-rules.yaml")
			assert.NoErr(t, os.WriteFile(path, []byte(file.Content), 0o644))
			loaded, errs := native.LoadOutputRules(path)
			assert.NoErr(t, firstErr(errs))
			names += len(loaded)
		}
		assert.True(t, names > 20)
	})
	t.Run("lang files parse", func(t *testing.T) {
		out, err := runInspectRules(t, ctx, "lang", "--builtin", "--json")
		assert.Require(t, assert.NoErr(t, err))
		var report builtinReport
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(out), &report)))
		assert.Require(t, assert.True(t, len(report.Builtin) > 0))
		for _, file := range report.Builtin {
			path := filepath.Join(t.TempDir(), "lang-rules.yaml")
			assert.NoErr(t, os.WriteFile(path, []byte(file.Content), 0o644))
			_, errs := codeintel.LoadLangRules(path)
			assert.NoErr(t, firstErr(errs))
		}
	})
	t.Run("nudge builtin parses", func(t *testing.T) {
		out, err := runInspectRules(t, ctx, "nudge", "--builtin")
		assert.Require(t, assert.NoErr(t, err))
		// The print starts with one header line; the rest is the YAML document.
		_, body, cut := strings.Cut(out, "\n")
		assert.Require(t, assert.True(t, cut && strings.Contains(body, "unverified-write")))
		path := filepath.Join(t.TempDir(), "nudge-rules.yaml")
		assert.NoErr(t, os.WriteFile(path, []byte(body), 0o644))
		rules, errs := personal.LoadNudgeRules(path)
		assert.Require(t, assert.NoErr(t, firstErr(errs)))
		assert.True(t, len(rules) >= 4) // the three built-in rules plus plan-open
	})
}

// TestManifestListsOutputRules: the manifest joins the nudge and lang summary
// with the output rules (and no skip when no rule file is present).
func TestManifestListsOutputRules(t *testing.T) {
	_, ws := rulesCLIEnv(t)
	out, err := runCLI(t, context.Background(), "inspect", "manifest", "--json")
	assert.Require(t, assert.NoErr(t, err))
	var m wiring.Manifest
	assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(out), &m)))
	assert.Require(t, assert.True(t, len(m.OutputRules) > 0))
	assert.Eq(t, "go-build", m.OutputRules[0].Name)
	assert.Eq(t, "builtin", m.OutputRules[0].Source)
	assert.Len(t, m.OutputRuleSkips, 0)

	// A broken project file is skipped and reported, like the lang skips.
	writeFile(t, filepath.Join(ws, ".suga", "output-rules.yaml"), "rules:\n  - match: 'x'\n")
	out, err = runCLI(t, context.Background(), "inspect", "manifest", "--json")
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, json.Unmarshal([]byte(out), &m))
	assert.Require(t, assert.Len(t, m.OutputRuleSkips, 1))
	assert.Eq(t, filepath.Join(ws, ".suga", "output-rules.yaml"), m.OutputRuleSkips[0].File)
}

func firstErr(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return errs[0]
}
