package wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	capskills "github.com/inhere/suga/pkg/agentcore/skills"
)

// TestBuildManifestContext: every part reports its bytes and estimated tokens,
// the total sums them, and a part or the total over its soft threshold is
// flagged with the action that brings it down.
func TestBuildManifestContext(t *testing.T) {
	base := ResidentContext{
		SystemPrompt: "SYSTEM", Environment: "ENV", Rules: "RULES",
		RuleStats: personal.RuleStats{
			Files:         []personal.RuleFileStat{{Path: "/w/AGENTS.md", Original: 5, Kept: 5, Text: "RULES"}},
			OriginalBytes: 5, KeptBytes: 5,
		},
		MemoryIndex: "MEM", Skills: "SK", AppendPrompt: "APP", DeferredTools: "DEF",
	}

	t.Run("sizes and total", func(t *testing.T) {
		c := BuildManifestContext(base)
		assert.Eq(t, len(base.SystemPrompt), c.SystemPrompt.Bytes)
		assert.Eq(t, personal.EstimateTokens(base.SystemPrompt), c.SystemPrompt.Tokens)
		assert.Eq(t, len(base.Rules), c.Rules.Bytes)
		assert.Eq(t, len(base.MemoryIndex), c.MemoryIndex.Bytes)
		assert.Eq(t, len(base.Skills), c.Skills.Bytes)
		assert.Eq(t, len(base.AppendPrompt), c.AppendPrompt.Bytes)
		assert.Eq(t, len(base.DeferredTools), c.DeferredTools.Bytes)
		sum := len(base.SystemPrompt) + len(base.Environment) + len(base.Rules) + len(base.MemoryIndex) +
			len(base.Skills) + len(base.AppendPrompt) + len(base.DeferredTools)
		assert.Eq(t, sum, c.TotalBytes)
		assert.Eq(t, 0, len(c.OverBudget))
		assert.Require(t, assert.Len(t, c.Rules.Files, 1))
		assert.Eq(t, "/w/AGENTS.md", c.Rules.Files[0].Path)
		assert.Eq(t, 5, c.Rules.Files[0].Bytes)
		assert.Eq(t, personal.EstimateTokens("RULES"), c.Rules.Files[0].Tokens)
	})

	t.Run("soft thresholds", func(t *testing.T) {
		in := base
		in.Environment = strings.Repeat("e", 20*1024)
		in.Rules = strings.Repeat("r", ResidentRulesSoftBytes+1)
		in.MemoryIndex = strings.Repeat("m", ResidentMemoryIndexSoftBytes+1)
		in.Skills = strings.Repeat("s", ResidentSkillsSoftBytes+1)
		c := BuildManifestContext(in)
		assert.True(t, c.Rules.Over)
		assert.Eq(t, rulesSuggestion, c.Rules.Suggestion)
		assert.True(t, c.MemoryIndex.Over)
		assert.Eq(t, memorySuggestion, c.MemoryIndex.Suggestion)
		assert.True(t, c.Skills.Over)
		assert.Eq(t, skillsSuggestion, c.Skills.Suggestion)
		assert.Eq(t, []string{"rules", "memory_index", "skills", "total"}, c.OverBudget)
	})

	t.Run("truncated rules", func(t *testing.T) {
		in := base
		in.RuleStats.Truncated = true
		in.RuleStats.OriginalBytes = 200 * 1024
		in.RuleStats.KeptBytes = 128 * 1024
		c := BuildManifestContext(in)
		assert.Require(t, assert.NotNil(t, c.Rules.Truncated))
		assert.Eq(t, 200*1024, c.Rules.Truncated.OriginalBytes)
		assert.Eq(t, 128*1024, c.Rules.Truncated.KeptBytes)
	})
}

// skillRootsFixture writes one valid skill, one duplicate of it in a later root
// and one unparsable SKILL.md, and returns the roots in priority order.
func skillRootsFixture(t *testing.T) (first, second string) {
	t.Helper()
	write := func(root, name, content string) string {
		dir := filepath.Join(root, name)
		assert.NoErr(t, os.MkdirAll(dir, 0o755))
		path := filepath.Join(dir, "SKILL.md")
		assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644))
		return path
	}
	first, second = t.TempDir(), t.TempDir()
	write(first, "dup", "---\nname: dup\ndescription: first\n---\nbody\n")
	write(second, "dup", "---\nname: dup\ndescription: second\n---\nbody\n")
	write(second, "bad", "# no frontmatter\n")
	return first, second
}

// TestBuildManifestSkillsDiagnostics: skipped SKILL.md files and shadowed
// same-name skills reach the manifest (and its JSON), not only the log.
func TestBuildManifestSkillsDiagnostics(t *testing.T) {
	first, second := skillRootsFixture(t)
	reg := capskills.LoadRoots([]capskills.Root{{Dir: first}, {Dir: second}})
	ctxPart := BuildManifestContext(ResidentContext{Skills: strings.Repeat("s", ResidentSkillsSoftBytes+1)})
	m := BuildManifest(ManifestInput{Skills: reg, Context: ctxPart})

	t.Run("diagnostics", func(t *testing.T) {
		assert.Require(t, assert.Len(t, m.Skills.Skipped, 1))
		assert.StrContains(t, m.Skills.Skipped[0].Path, "bad")
		assert.True(t, m.Skills.Skipped[0].Err != "")
		assert.Require(t, assert.Len(t, m.Skills.Shadowed, 1))
		assert.Eq(t, "dup", m.Skills.Shadowed[0].Name)
		assert.StrContains(t, m.Skills.Shadowed[0].Winner, first)
		assert.StrContains(t, m.Skills.Shadowed[0].Loser, second)
	})

	t.Run("json fields", func(t *testing.T) {
		raw, err := json.Marshal(m)
		assert.Require(t, assert.NoErr(t, err))
		var decoded struct {
			Skills struct {
				Skipped []struct {
					Path string `json:"path"`
					Err  string `json:"err"`
				} `json:"skipped"`
				Shadowed []struct {
					Name   string `json:"name"`
					Winner string `json:"winner"`
					Loser  string `json:"loser"`
				} `json:"shadowed"`
			} `json:"skills"`
			Context struct {
				Rules struct {
					Bytes int `json:"bytes"`
				} `json:"rules"`
				TotalBytes int      `json:"total_bytes"`
				OverBudget []string `json:"over_budget"`
			} `json:"context"`
		}
		assert.Require(t, assert.NoErr(t, json.Unmarshal(raw, &decoded)))
		assert.Require(t, assert.Len(t, decoded.Skills.Skipped, 1))
		assert.Require(t, assert.Len(t, decoded.Skills.Shadowed, 1))
		assert.Eq(t, "dup", decoded.Skills.Shadowed[0].Name)
		assert.True(t, decoded.Context.TotalBytes >= decoded.Context.Rules.Bytes)
		assert.Eq(t, []string{"skills"}, decoded.Context.OverBudget)
	})
}

// TestContextNotices: each resident context problem becomes one line pointing
// at the manifest, and nothing is said when everything is in budget.
func TestContextNotices(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		assert.Len(t, ContextNotices(ManifestContext{}, ManifestSkills{}), 0)
	})

	t.Run("truncated and over budget and skills", func(t *testing.T) {
		c := BuildManifestContext(ResidentContext{
			Rules:     strings.Repeat("r", ResidentRulesSoftBytes+1),
			RuleStats: personal.RuleStats{Truncated: true, OriginalBytes: 200000, KeptBytes: 131072},
			Skills:    "SK",
		})
		sk := ManifestSkills{
			Skipped:  []ManifestSkillSkipped{{Path: "/s/bad/SKILL.md", Err: "x"}},
			Shadowed: []ManifestSkillShadow{{Name: "dup", Winner: "/a", Loser: "/b"}},
		}
		notices := ContextNotices(c, sk)
		assert.Require(t, assert.Len(t, notices, 3))
		for _, notice := range notices {
			assert.StrContains(t, notice, "suga inspect manifest")
		}
		assert.StrContains(t, notices[0], "原 200000 字节，保留 131072")
		assert.StrContains(t, notices[2], "1 个 SKILL.md 被跳过、1 个 skill 被同名遮蔽")
	})
}

// TestLoadManifestResidentContext: the standalone command measures the same
// system prompt and environment a session would send.
func TestLoadManifestResidentContext(t *testing.T) {
	manifestConfig(t)
	m, err := LoadManifest(ManifestOptions{})
	assert.Require(t, assert.NoErr(t, err))

	assert.Eq(t, len(basePrompt()), m.Context.SystemPrompt.Bytes)
	assert.True(t, m.Context.Environment.Bytes > 0)
	assert.True(t, m.Context.DeferredTools.Note != "")
	assert.Eq(t, m.Context.TotalBytes, m.Context.SystemPrompt.Bytes+m.Context.Environment.Bytes+m.Context.Rules.Bytes+
		m.Context.MemoryIndex.Bytes+m.Context.Skills.Bytes+m.Context.AppendPrompt.Bytes+m.Context.DeferredTools.Bytes)
	assert.True(t, m.Context.SystemPrompt.Tokens > 0)
}
