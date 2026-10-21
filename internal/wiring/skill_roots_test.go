package wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// resetSkillConfig turns off the machine-detected roots (dependency and
// standards sources), so the skill-root assertions see only what the test
// created.
func resetSkillConfig(t *testing.T) {
	t.Helper()
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("tools.dependency_roots", false))
	assert.NoErr(t, gkconfig.Set("tools.standards_roots", false))
}

// skillRootEnv isolates every directory the skill roots come from: SUGA_HOME,
// the user home (os.UserHomeDir reads USERPROFILE on Windows) and the
// workspace. The skill directories are created with a skill inside, so they
// exist like they do on a real machine.
func skillRootEnv(t *testing.T) (home, sugaHome, workspace string) {
	t.Helper()
	home, sugaHome, workspace = t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", sugaHome)
	skills := filepath.Join(sugaHome, "skills", "y")
	assert.NoErr(t, os.MkdirAll(skills, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte("# y\n"), 0o644))
	agents := filepath.Join(home, ".agents", "skills", "x", "references")
	assert.NoErr(t, os.MkdirAll(agents, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(agents, "a.md"), []byte("reference\n"), 0o644))
	return home, sugaHome, workspace
}

// skillRootPaths returns the two skill directories outside the workspace.
func skillRootPaths(home, sugaHome string) (agents, builtin string) {
	return filepath.Join(home, ".agents", "skills"), filepath.Join(sugaHome, "skills")
}

// findReadRoot reports whether paths holds dir (case-cleaned comparison).
func findReadRoot(paths []string, dir string) bool {
	for _, p := range paths {
		if filepath.Clean(p) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// skillTestAsker records every question; any question means a call needed an
// approval, so "no question" is the read-only-roots assertion.
type skillTestAsker struct{ asked []tools.Question }

func (a *skillTestAsker) Ask(_ context.Context, q tools.Question) (string, error) {
	a.asked = append(a.asked, q)
	return "y", nil
}

func TestSkillReadRoots(t *testing.T) {
	t.Run("outside skill roots become read roots", func(t *testing.T) {
		resetSkillConfig(t)
		home, sugaHome, workspace := skillRootEnv(t)
		assert.NoErr(t, os.MkdirAll(filepath.Join(workspace, "skills"), 0o755))

		paths := nativeEnv(workspace, nil).ReadRoots
		agents, builtin := skillRootPaths(home, sugaHome)
		assert.True(t, findReadRoot(paths, agents))
		assert.True(t, findReadRoot(paths, builtin))
		// The workspace's own skill roots are already reachable by the file
		// tools: adding them again would only duplicate them.
		assert.False(t, findReadRoot(paths, filepath.Join(workspace, "skills")))
	})

	t.Run("missing skill directories add no root", func(t *testing.T) {
		resetSkillConfig(t)
		home, sugaHome, workspace := skillRootEnv(t)
		assert.NoErr(t, os.RemoveAll(filepath.Join(home, ".agents")))
		assert.NoErr(t, os.RemoveAll(filepath.Join(sugaHome, "skills")))
		assert.Empty(t, nativeEnv(workspace, nil).ReadRoots)
	})

	t.Run("the switch turns the skill roots off", func(t *testing.T) {
		resetSkillConfig(t)
		_, _, workspace := skillRootEnv(t)
		assert.NoErr(t, gkconfig.Set("tools.skill_roots", false))
		assert.Empty(t, nativeEnv(workspace, nil).ReadRoots)
	})

	t.Run("a read under the skill roots is not outside", func(t *testing.T) {
		resetSkillConfig(t)
		home, _, workspace := skillRootEnv(t)
		agents, _ := skillRootPaths(home, "")
		ref := filepath.Join(agents, "x", "references", "a.md")
		_, ok := native.OutsideOf(workspace, nativeEnv(workspace, nil).ReadRoots, nil, "file_read", map[string]any{"path": ref})
		assert.False(t, ok)
		// A write into the same directory is still an authorization: the roots
		// are read-only for the write tools.
		_, ok = native.OutsideOf(workspace, nativeEnv(workspace, nil).ReadRoots, nil, "file_write", map[string]any{"path": ref})
		assert.True(t, ok)
	})

	t.Run("the approval layer reads a skill reference without a question", func(t *testing.T) {
		resetSkillConfig(t)
		home, _, _ := skillRootEnv(t)
		agents, _ := skillRootPaths(home, "")
		ref := filepath.Join(agents, "x", "references", "a.md")
		asker := &skillTestAsker{}
		c := newOutsideCase(t, asker, outsideCaseOptions{
			// The tool side gets the roots from the configuration (the helper
			// turns the automatic roots off); the approver side needs them here.
			config:    func(t *testing.T) { assert.NoErr(t, gkconfig.Set("tools.skill_roots", true)) },
			readRoots: []string{agents},
		})
		res, err := c.read(context.Background(), ref)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "reference")
		assert.Eq(t, 0, len(asker.asked))
	})

	t.Run("a write into the skill roots still needs an approval", func(t *testing.T) {
		resetSkillConfig(t)
		home, _, _ := skillRootEnv(t)
		agents, _ := skillRootPaths(home, "")
		asker := &skillTestAsker{}
		c := newOutsideCase(t, asker, outsideCaseOptions{
			config:    func(t *testing.T) { assert.NoErr(t, gkconfig.Set("tools.skill_roots", true)) },
			readRoots: []string{agents},
		})
		write := filepath.Join(agents, "x", "references", "new.md")
		_, err := c.approval.ExecuteTool(context.Background(),
			api.ToolCall{CallID: "write", Name: "file_write", Arguments: map[string]any{"path": write, "content": "x"}})
		assert.Err(t, err)
		assert.Eq(t, 1, len(asker.asked))
	})
}

func TestSkillRootsInManifest(t *testing.T) {
	manifestConfig(t)
	assert.NoErr(t, gkconfig.Set("tools.dependency_roots", false))
	assert.NoErr(t, gkconfig.Set("tools.standards_roots", false))
	home, sugaHome, workspace := skillRootEnv(t)
	assert.NoErr(t, gkconfig.Set("workspace", workspace))

	m, err := LoadManifest(ManifestOptions{})
	assert.Require(t, assert.NoErr(t, err))
	agents, builtin := skillRootPaths(home, sugaHome)
	assert.Len(t, m.ReadRoots, 2)
	assert.Eq(t, ManifestReadRoot{Label: "skills", Path: builtin}, m.ReadRoots[0])
	assert.Eq(t, ManifestReadRoot{Label: "skills", Path: agents}, m.ReadRoots[1])
}
