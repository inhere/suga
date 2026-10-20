package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// writeSkill writes root/<name>/SKILL.md with the given frontmatter fields.
func writeSkill(t *testing.T, root, name, frontmatter string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	assert.NoErr(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "SKILL.md")
	assert.NoErr(t, os.WriteFile(path, []byte(frontmatter), 0o644))
	return path
}

// TestLoadRootsDiagnostics: a SKILL.md that cannot be parsed is reported as
// skipped with its reason, and a same-name skill of a later root as shadowed
// by the file that won.
func TestLoadRootsDiagnostics(t *testing.T) {
	t.Run("skipped and shadowed", func(t *testing.T) {
		first, second := t.TempDir(), t.TempDir()
		winner := writeSkill(t, first, "dup", "---\nname: dup\ndescription: first\n---\nbody\n")
		loser := writeSkill(t, second, "dup", "---\nname: dup\ndescription: second\n---\nbody\n")
		bad := writeSkill(t, second, "bad", "# no frontmatter\n")
		writeSkill(t, second, "other", "---\nname: other\ndescription: d\n---\n")

		reg := LoadRoots([]Root{{Dir: first}, {Dir: second}})
		assert.Eq(t, 2, reg.Len())

		skipped := reg.Skipped()
		assert.Require(t, assert.Len(t, skipped, 1))
		assert.Eq(t, bad, skipped[0].Path)
		assert.True(t, skipped[0].Err != "")

		shadowed := reg.Shadowed()
		assert.Require(t, assert.Len(t, shadowed, 1))
		assert.Eq(t, "dup", shadowed[0].Name)
		assert.Eq(t, winner, shadowed[0].Winner)
		assert.Eq(t, loser, shadowed[0].Loser)
	})

	t.Run("clean roots report nothing", func(t *testing.T) {
		root := t.TempDir()
		writeSkill(t, root, "a", "---\nname: a\ndescription: d\n---\n")
		reg := LoadRoots([]Root{{Dir: root}})
		assert.Len(t, reg.Skipped(), 0)
		assert.Len(t, reg.Shadowed(), 0)
	})
}
