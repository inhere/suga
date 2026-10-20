package wiring

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// writeTestPNG writes a small opaque PNG and returns its path.
func writeTestPNG(t *testing.T, dir string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	assert.Require(t, assert.NoErr(t, png.Encode(&buf, img)))
	path := filepath.Join(dir, "shot.png")
	assert.Require(t, assert.NoErr(t, os.WriteFile(path, buf.Bytes(), 0o600)))
	return path
}

// TestReadImageAtOutsideTheWorkspace: a path the person typed (:image,
// run --image, send --image) may name a file outside the workspace, while the
// model's view_image stays confined to it.
func TestReadImageAtOutsideTheWorkspace(t *testing.T) {
	workspace, elsewhere := t.TempDir(), t.TempDir()
	outside := writeTestPNG(t, elsewhere)

	t.Run("typed path outside the workspace is read", func(t *testing.T) {
		part, _, err := ReadImageAt(workspace, outside)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 8, part.Width)
	})
	t.Run("the model's view_image still refuses it", func(t *testing.T) {
		_, _, err := native.ReadImageFile(workspace, nil, nil, outside)
		assert.Err(t, err)
	})
	t.Run("a relative path still resolves against the workspace", func(t *testing.T) {
		writeTestPNG(t, workspace)
		part, _, err := ReadImageAt(workspace, "shot.png")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 8, part.Width)
	})
}
