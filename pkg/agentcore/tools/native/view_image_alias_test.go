package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// TestViewImageAliasMatchesFileRead pins design 2026-10-23 Q4: view_image is
// kept one version as hidden alias of file_read's picture branch, so an old
// session that calls it by name gets the same picture and text, and the registry
// does not list it.
func TestViewImageAliasMatchesFileRead(t *testing.T) {
	dir := t.TempDir()
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "shot.png"), encodeTestPNG(t, 30, 20), 0o600)))
	read := newPictureFileRead(dir, nil, 0)
	alias := NewViewImageTool(dir, nil, nil, "")

	readRes := execFileRead(t, read, map[string]any{"path": "shot.png"})
	aliasRes, err := alias.Execute(context.Background(), api.ToolCall{CallID: "c1", Arguments: map[string]any{"path": "shot.png"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(aliasRes.Images)))
	assert.Require(t, assert.Eq(t, 1, len(readRes.Images)))
	assert.Eq(t, readRes.Images[0].MediaType, aliasRes.Images[0].MediaType)
	assert.Eq(t, readRes.Images[0].Width, aliasRes.Images[0].Width)
	assert.Eq(t, len(readRes.Images[0].Data), len(aliasRes.Images[0].Data), "one pipeline, one encoding")

	t.Run("the alias is registered and hidden", func(t *testing.T) {
		reg := tools.NewRegistry()
		reg.MustRegister(read)
		reg.MustRegister(alias)
		names := make([]string, 0, 2)
		for _, schema := range reg.Schemas() {
			names = append(names, schema.Name)
		}
		assert.Eq(t, []string{"file_read"}, names, "a hidden alias is not listed")
		_, err := reg.Get("view_image")
		assert.NoErr(t, err, "but it stays callable by name")
	})
}
