package skills

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	capskills "github.com/inhere/suga/pkg/agentcore/skills"
)

// The embedded guide is the built-in skill released into <SUGA_HOME>/skills:
// keep it loadable (SKILL.md present, frontmatter with a name and a
// description) and make sure the references SKILL.md points to are embedded too.
func TestEmbeddedGuideIsALoadableSkill(t *testing.T) {
	data, err := fs.ReadFile(FS, "suga-guide/SKILL.md")
	assert.Require(t, assert.NoErr(t, err))

	skill, body, err := capskills.Parse(data)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "suga-guide", skill.Name)
	assert.NotEmpty(t, skill.Description)
	assert.NotEmpty(t, strings.TrimSpace(body))

	for _, ref := range []string{"references/config.md", "references/providers.md", "references/gofer.md",
		"references/chat.md", "references/tools.md", "references/rules.md", "references/sessions.md", "references/observability.md"} {
		t.Run(ref, func(t *testing.T) {
			assert.StrContains(t, body, ref) // SKILL.md tells the model to read it
			data, err := fs.ReadFile(FS, "suga-guide/"+ref)
			assert.Require(t, assert.NoErr(t, err))
			assert.NotEmpty(t, len(data))
		})
	}
}

// Every embedded file must live below a skill directory (the release writes one
// directory per top-level entry, and the scanner looks for SKILL.md there).
func TestEmbeddedLayoutIsOneDirectoryPerSkill(t *testing.T) {
	var paths []string
	assert.NoErr(t, fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	}))
	assert.NotEmpty(t, paths)
	for _, p := range paths {
		parts := strings.Split(p, "/")
		assert.True(t, len(parts) >= 2, p)
		assert.Eq(t, "suga-guide", parts[0])
	}
	sort.Strings(paths)
	assert.Eq(t, "suga-guide/SKILL.md", paths[0])
}
