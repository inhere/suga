package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// encodeTestPNG writes a width×height opaque PNG.
func encodeTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	assert.Require(t, assert.NoErr(t, png.Encode(&buf, img)))
	return buf.Bytes()
}

// TestViewImageScalesAndSends pins the core of the tool: a big PNG comes back
// scaled to the long side, as an image part and a text description.
func TestViewImageScalesAndSends(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "shot.png")
	assert.Require(t, assert.NoErr(t, os.WriteFile(file, encodeTestPNG(t, 100, 40), 0o600)))
	tool := NewViewImageTool(dir, nil, nil, "")

	res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c1", Arguments: map[string]any{"path": "shot.png", "max_side": 50}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(res.Images)))
	assert.Eq(t, 50, res.Images[0].Width)
	assert.Eq(t, 20, res.Images[0].Height)
	assert.Eq(t, "image/jpeg", res.Images[0].MediaType, "opaque pictures are sent as JPEG")
	assert.True(t, len(res.Images[0].Data) > 0)
	assert.Contains(t, res.Content, "shot.png")
	assert.Contains(t, res.Content, "100×40")
}

// TestViewImageKeepsTransparency pins the PNG path: a picture with an alpha
// channel is re-encoded as PNG so the transparency survives.
func TestViewImageKeepsTransparency(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "logo.png")
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{A: 0})
	var buf bytes.Buffer
	assert.Require(t, assert.NoErr(t, png.Encode(&buf, img)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(file, buf.Bytes(), 0o600)))

	res, err := NewViewImageTool(dir, nil, nil, "").Execute(context.Background(), api.ToolCall{Arguments: map[string]any{"path": "logo.png"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "image/png", res.Images[0].MediaType)
}

// TestViewImageRefuses pins the guards: a text file, a path outside the
// workspace, a picture over the pixel limit and a model without vision.
func TestViewImageRefuses(t *testing.T) {
	dir := t.TempDir()
	tool := NewViewImageTool(dir, nil, nil, "")

	t.Run("a text file is not an image", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "a.png"), []byte("not an image"), 0o600)))
		_, err := tool.Execute(context.Background(), api.ToolCall{Arguments: map[string]any{"path": "a.png"}})
		assert.Require(t, assert.Err(t, err))
		assert.Contains(t, err.Error(), "unsupported image format")
	})
	t.Run("a path outside the workspace is refused", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "x.png")
		assert.Require(t, assert.NoErr(t, os.WriteFile(outside, encodeTestPNG(t, 2, 2), 0o600)))
		_, err := tool.Execute(context.Background(), api.ToolCall{Arguments: map[string]any{"path": outside}})
		assert.Require(t, assert.Err(t, err))
	})
	t.Run("an image over the pixel limit is refused before decoding", func(t *testing.T) {
		// The header claims 40000×40000 pixels (1.6GP): the config check refuses
		// it without decoding the whole picture.
		data := imageWithHugeHeader(t)
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "bomb.png"), data, 0o600)))
		_, err := tool.Execute(context.Background(), api.ToolCall{Arguments: map[string]any{"path": "bomb.png"}})
		assert.Require(t, assert.Err(t, err))
		assert.Contains(t, err.Error(), "pixel limit")
	})
	t.Run("a model without vision gets an explanation", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "ok.png"), encodeTestPNG(t, 2, 2), 0o600)))
		_, err := NewViewImageTool(dir, nil, nil, "").WithoutVision(true).Execute(context.Background(),
			api.ToolCall{Arguments: map[string]any{"path": "ok.png"}})
		assert.Require(t, assert.Err(t, err))
		assert.Contains(t, err.Error(), ":model")
	})
}

// TestViewImageGIFFirstFrame pins the GIF path: the first frame is sent.
func TestViewImageGIFFirstFrame(t *testing.T) {
	dir := t.TempDir()
	img := image.NewPaletted(image.Rect(0, 0, 6, 4), []color.Color{color.Black, color.White})
	var buf bytes.Buffer
	assert.Require(t, assert.NoErr(t, gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{img}, Delay: []int{0}})))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "a.gif"), buf.Bytes(), 0o600)))
	res, err := NewViewImageTool(dir, nil, nil, "").Execute(context.Background(), api.ToolCall{Arguments: map[string]any{"path": "a.gif"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 6, res.Images[0].Width)
	assert.Eq(t, 4, res.Images[0].Height)
}

// TestIsSessionImageRef pins what counts as a session image reference.
func TestIsSessionImageRef(t *testing.T) {
	assert.True(t, isSessionImageRef("images/ab12.png"))
	assert.True(t, isSessionImageRef(`images\ab12.jpg`))
	assert.False(t, isSessionImageRef("images/../secrets.png"))
	assert.False(t, isSessionImageRef("dir/images/ab.png"))
	assert.False(t, isSessionImageRef("images"))
}

// imageWithHugeHeader returns a PNG whose IHDR claims 40000×40000 pixels with
// no pixel data at all, i.e. what a decompression bomb looks like.
func imageWithHugeHeader(t *testing.T) []byte {
	t.Helper()
	hdr := make([]byte, 13)
	binary.BigEndian.PutUint32(hdr[0:4], 40000)
	binary.BigEndian.PutUint32(hdr[4:8], 40000)
	hdr[8], hdr[9] = 8, 2 // bit depth, color type RGB
	chunk := append([]byte("IHDR"), hdr...)
	crc := crc32.ChecksumIEEE(chunk)
	data := []byte("\x89PNG\r\n\x1a\n")
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, 13)
	data = append(data, length...)
	data = append(data, chunk...)
	sum := make([]byte, 4)
	binary.BigEndian.PutUint32(sum, crc)
	return append(data, sum...)
}
