package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// rulesDocFile is the skills reference the model reads before writing a rule
// file: resolved from this test file's location, because rulesCLIEnv changes
// the process working directory (t.Chdir) while the test runs.
func rulesDocFile(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "skills", "suga-guide", "references", "rules.md")
}

// rulesDocBlocks picks the ```yaml examples out of rules.md and tags each with
// the last `## ` heading before it, so each block is loaded by the loader of
// the rule kind the section documents. A doc example that no loader accepts
// (a renamed field, a broken regex) fails here instead of misleading a model.
func rulesDocBlocks(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(rulesDocFile(t))
	assert.Require(t, assert.NoErr(t, err), "read rules.md")
	blocks := map[string][]string{}
	heading := ""
	var current []string
	for line := range strings.Lines(string(data)) {
		text := strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(text, "## "):
			heading = strings.TrimSpace(strings.TrimPrefix(text, "## "))
		case strings.TrimSpace(text) == "```yaml":
			current = []string{}
		case strings.TrimSpace(text) == "```" && current != nil:
			blocks[heading] = append(blocks[heading], strings.Join(current, "\n"))
			current = nil
		case current != nil:
			current = append(current, text)
		}
	}
	if current != nil {
		t.Fatalf("rules.md ends inside an unterminated yaml block under %q", heading)
	}
	return blocks
}

// rulesDocKind maps a rules.md section heading to the block's rule kind.
func rulesDocKind(heading string) string {
	switch {
	case strings.Contains(heading, "审批"):
		return "approval"
	case strings.Contains(heading, "输出整理"):
		return "output"
	case strings.Contains(heading, "收工检查"):
		return "nudge"
	case strings.Contains(heading, "语言规则"):
		return "lang"
	default:
		return ""
	}
}

// TestRulesDocExamplesLoad parses every yaml example of references/rules.md
// with the loader of its section's rule kind: the doc must never drift away
// from what the loaders accept.
func TestRulesDocExamplesLoad(t *testing.T) {
	_, ws := rulesCLIEnv(t)
	blocks := rulesDocBlocks(t)
	loaded := 0
	for heading, blockList := range blocks {
		kind := rulesDocKind(heading)
		if kind == "" {
			continue
		}
		for _, block := range blockList {
			assert.Require(t, strings.Contains(block, "rules:\n") || strings.Contains(block, "languages:\n"),
				heading)
			path := filepath.Join(t.TempDir(), "doc-example.yaml")
			assert.NoErr(t, os.WriteFile(path, []byte(block), 0o644))
			switch kind {
			case "output":
				rules, errs := native.LoadOutputRules(path)
				assert.Require(t, assert.NoErr(t, firstErr(errs)), heading)
				assert.True(t, len(rules) > 0, heading)
			case "nudge":
				rules, errs := personal.LoadNudgeRules(path)
				assert.Require(t, assert.NoErr(t, firstErr(errs)), heading)
				assert.True(t, len(rules) >= 4, heading) // 内置四条加示例新增
				_ = rules
			case "lang":
				_, errs := codeintel.LoadLangRules(path)
				assert.Require(t, assert.NoErr(t, firstErr(errs)), heading)
			case "approval":
				// The approval example is a project-level file: write it there
				// and load through the wired, fail-closed loader.
				target := filepath.Join(ws, ".suga", "approval-rules.yaml")
				assert.NoErr(t, os.MkdirAll(filepath.Dir(target), 0o755))
				assert.NoErr(t, os.WriteFile(target, []byte(block), 0o644))
				snapshot, err := wiring.InspectApprovalRules(ws)
				assert.Require(t, assert.NoErr(t, err), heading)
				// 项目 allow 无信任时被忽略，deny 依然生效。
				assert.True(t, len(snapshot.EffectiveRules()) > 0, heading)
				assert.True(t, len(snapshot.IgnoredApprovalRules()) > 0, heading)
			}
			loaded++
		}
	}
	assert.True(t, loaded >= 4, "rules.md must keep one example per kind")
}
