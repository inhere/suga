package wiring

import (
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	capskills "github.com/inhere/suga/pkg/agentcore/skills"
)

// TestSkillInfosSources pins where :skills takes the source of a skill from:
// the root it was found in (workspace .agents/skills = 项目, SUGA_HOME/skills =
// 内置, the user's ~/.agents/skills = 用户), sorted by name.
func TestSkillInfosSources(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	sugaHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", sugaHome)
	writeSkill(t, filepath.Join(workspace, ".agents", "skills", "team-style"), "team-style", "Workspace conventions.")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "my-notes"), "my-notes", "Personal notes.")

	infos := SkillInfos(nil, buildDefaultSkillRegistry(workspace))
	byName := map[string]SkillInfo{}
	for _, info := range infos {
		byName[info.Name] = info
	}
	// The labels are the catalog values of SkillSourceProject/Builtin/User; a
	// wiring without an injected catalog keeps the Chinese text.
	assert.Eq(t, i18n.Fallback().T(SkillSourceProject), byName["team-style"].Source)
	assert.Eq(t, "Workspace conventions.", byName["team-style"].Description)
	assert.StrContains(t, byName["team-style"].Path, ".agents")
	assert.Eq(t, i18n.Fallback().T(SkillSourceUser), byName["my-notes"].Source)
	// The built-in guide is released into SUGA_HOME/skills on this run.
	assert.Eq(t, i18n.Fallback().T(SkillSourceBuiltin), byName["suga-guide"].Source)
	// Registry.List order (by name) is kept.
	for i := 1; i < len(infos); i++ {
		assert.Require(t, assert.True(t, infos[i-1].Name < infos[i].Name))
	}
	assert.Eq(t, 0, len(SkillInfos(nil, nil)))
	assert.Eq(t, 0, len(SkillInfos(nil, capskills.NewRegistry())))
}

// TestBuildContextReportParts pins the parts :context lists: the manifest parts
// in request order, the rule source files with what each contributed and the
// deferred tool counts from the visibility layer.
func TestBuildContextReportParts(t *testing.T) {
	rc := ResidentContext{
		SystemPrompt: "SYSTEM", Environment: "ENV", Rules: "RULES",
		RuleStats: personal.RuleStats{
			Files:     []personal.RuleFileStat{{Path: "AGENTS.md", Original: 5, Kept: 5, Text: "RULES", Included: true}},
			KeptBytes: 5,
		},
		MemoryIndex: "MEM", Skills: "SK", AppendPrompt: "APP", DeferredTools: "DEF",
	}
	out := BuildContextReport(NewResidentReport(rc, nil), 12, 2)
	names := make([]string, 0, len(out.Parts))
	for _, part := range out.Parts {
		names = append(names, part.Name)
	}
	assert.Eq(t, []string{"system_prompt", "environment", "rules", "memory_index", "skills", "append_prompt", "host_context", "deferred_tools"}, names)
	for _, part := range out.Parts {
		if part.Name == "host_context" {
			assert.Eq(t, 0, part.Bytes)
			continue
		}
		assert.Require(t, assert.True(t, part.Bytes > 0))
		assert.Require(t, assert.True(t, part.Tokens > 0))
	}
	rules := out.Parts[2]
	assert.Require(t, assert.Eq(t, 1, len(rules.Files)))
	assert.Eq(t, "AGENTS.md", rules.Files[0].Path)
	assert.Eq(t, true, rules.Files[0].Included)
	assert.Eq(t, 12, out.Deferred)
	assert.Eq(t, 2, out.Loaded)
}
