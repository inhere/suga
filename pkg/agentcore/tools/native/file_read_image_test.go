package native

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// newPictureFileRead builds file_read the way the host does: the picture
// pipeline of view_image bound to it, with the given vision gate and max side.
func newPictureFileRead(dir string, gate *tools.VisionGate, maxSide int) *FileReadTool {
	img := NewViewImageTool(dir, nil, nil, "").SetVision(gate)
	img.maxSide = maxSide
	return (&FileReadTool{workspace: dir}).SetImagePipeline(img)
}

// execFileRead runs one file_read call.
func execFileRead(t *testing.T, tool *FileReadTool, args map[string]any) *api.ToolResult {
	t.Helper()
	res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c1", Arguments: args})
	assert.Require(t, assert.NoErr(t, err))
	return res
}

// writeTestPicture encodes img with encode and writes it as name in dir.
func writeTestPicture(t *testing.T, dir, name string, encode func(*bytes.Buffer, image.Image) error, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	assert.Require(t, assert.NoErr(t, encode(&buf, img)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o600)))
}

// solidImage is a w×h opaque picture; transparent makes it an alpha one.
func solidImage(w, h int, transparent bool) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	a := uint8(255)
	if transparent {
		a = 0
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 180, G: 90, B: 40, A: a})
		}
	}
	return img
}

// TestFileReadPicture pins the center of the merge: a picture is recognized by
// its header and answered with one image part and a text description (design
// 2026-10-23-file-read-images sections 1-2).
func TestFileReadPicture(t *testing.T) {
	dir := t.TempDir()
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "shot.png"), encodeTestPNG(t, 100, 40), 0o600)))

	t.Run("a PNG comes back as an image part", func(t *testing.T) {
		res := execFileRead(t, newPictureFileRead(dir, nil, 0), map[string]any{"path": "shot.png"})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Require(t, assert.Eq(t, "file_read", res.Name))
		assert.Eq(t, "image/jpeg", res.Images[0].MediaType, "an opaque picture is sent as JPEG")
		assert.Contains(t, res.Content, "shot.png: PNG 100×40")
		assert.Contains(t, res.Content, "attached as an image part")
	})
	t.Run("images.max_side decides the long side", func(t *testing.T) {
		res := execFileRead(t, newPictureFileRead(dir, nil, 50), map[string]any{"path": "shot.png"})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Eq(t, 50, res.Images[0].Width)
		assert.Eq(t, 20, res.Images[0].Height)
	})
	t.Run("the retired max_side argument is accepted and ignored", func(t *testing.T) {
		res := execFileRead(t, newPictureFileRead(dir, nil, 50), map[string]any{"path": "shot.png", "max_side": 10})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Eq(t, 50, res.Images[0].Width, "the argument does not scale (design Q3)")
	})
	t.Run("JPEG and GIF by header, whatever the extension", func(t *testing.T) {
		writeTestPicture(t, dir, "photo.dat", func(b *bytes.Buffer, img image.Image) error {
			return jpeg.Encode(b, img, nil)
		}, solidImage(60, 30, false))
		writeTestPicture(t, dir, "anim.gif", func(b *bytes.Buffer, img image.Image) error {
			return gif.Encode(b, img, nil)
		}, solidImage(20, 10, false))
		res := execFileRead(t, newPictureFileRead(dir, nil, 0), map[string]any{"path": "photo.dat"})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Contains(t, res.Content, "JPEG 60×30")
		res = execFileRead(t, newPictureFileRead(dir, nil, 0), map[string]any{"path": "anim.gif"})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Contains(t, res.Content, "GIF 20×10")
	})
	t.Run("transparency stays PNG", func(t *testing.T) {
		var buf bytes.Buffer
		assert.Require(t, assert.NoErr(t, png.Encode(&buf, solidImage(8, 8, true))))
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "logo.png"), buf.Bytes(), 0o600)))
		res := execFileRead(t, newPictureFileRead(dir, nil, 0), map[string]any{"path": "logo.png"})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Eq(t, "image/png", res.Images[0].MediaType)
	})
	t.Run("WebP is recognized by its header", func(t *testing.T) {
		head := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
		_, format, err := sniffImage(head)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "WebP", format)
	})
}

// TestFileReadPictureWithoutVision pins the non-error note: a model that cannot
// see still learns the format and size and how to read the picture for real
// (design Q2, section 3).
func TestFileReadPictureWithoutVision(t *testing.T) {
	dir := t.TempDir()
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "shot.png"), encodeTestPNG(t, 100, 40), 0o600)))
	tool := newPictureFileRead(dir, tools.NewVisionGate(false), 0)

	res := execFileRead(t, tool, map[string]any{"path": "shot.png"})
	assert.Eq(t, 0, len(res.Images))
	assert.Contains(t, res.Content, "shot.png: PNG 100×40")
	assert.Contains(t, res.Content, "cannot see images")
	assert.Contains(t, res.Content, ":model")
	assert.True(t, !res.IsError)
	// A model switch needs no tool rebuild: the gate is read per call.
	tool.imagePipeline().SetVision(tools.NewVisionGate(true))
	res = execFileRead(t, tool, map[string]any{"path": "shot.png"})
	assert.Require(t, assert.Eq(t, 1, len(res.Images)))
}

// TestFileReadPictureCallShapes pins section 4: one picture per call, so paths,
// line spans and offset/limit/outline are answered with a note or an attached
// picture plus a note, never with more than one image.
func TestFileReadPictureCallShapes(t *testing.T) {
	dir := t.TempDir()
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "a.png"), encodeTestPNG(t, 20, 10), 0o600)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bravo\n"), 0o600)))
	tool := newPictureFileRead(dir, nil, 0)

	t.Run("paths keeps the picture as a note and reads the text", func(t *testing.T) {
		res := execFileRead(t, tool, map[string]any{"paths": []any{"a.png", "b.txt"}})
		assert.Eq(t, 0, len(res.Images))
		assert.Contains(t, res.Content, "[image: a.png, PNG 20×10")
		assert.Contains(t, res.Content, "read a picture with a single path call")
		assert.Contains(t, res.Content, "bravo")
	})
	t.Run("a line span only gives the note", func(t *testing.T) {
		res := execFileRead(t, tool, map[string]any{"path": "a.png:12-40"})
		assert.Eq(t, 0, len(res.Images))
		assert.Contains(t, res.Content, "not attached")
	})
	t.Run("outline attaches and says it does not apply", func(t *testing.T) {
		res := execFileRead(t, tool, map[string]any{"path": "a.png", "outline": true})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Contains(t, res.Content, "outline does not apply to a picture")
	})
	t.Run("offset and limit are ignored with a note", func(t *testing.T) {
		res := execFileRead(t, tool, map[string]any{"path": "a.png", "offset": 3, "limit": 5})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Contains(t, res.Content, "offset/limit do not apply to a picture")
	})
}

// TestFileReadPictureSessionRef pins section 6: a session image ref reads from
// the session's images directory resolved per call, and a ref without a session
// is a note, not a crash.
func TestFileReadPictureSessionRef(t *testing.T) {
	dir := t.TempDir()
	imagesDir := filepath.Join(dir, "sess", "images")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(imagesDir, 0o755)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(imagesDir, "abc.png"), encodeTestPNG(t, 12, 6), 0o600)))

	t.Run("the resolver finds the stored picture", func(t *testing.T) {
		tool := newPictureFileRead(dir, nil, 0)
		tool.imagePipeline().SetImagesDirResolver(func() string { return imagesDir })
		res := execFileRead(t, tool, map[string]any{"path": "images/abc.png"})
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Contains(t, res.Content, "images/abc.png: PNG 12×6")
	})
	t.Run("no session images directory is a note", func(t *testing.T) {
		res := execFileRead(t, newPictureFileRead(dir, nil, 0), map[string]any{"path": "images/abc.png"})
		assert.Eq(t, 0, len(res.Images))
		assert.Contains(t, res.Content, "not attached")
		assert.Contains(t, res.Content, "session images directory")
	})
	t.Run("the same ref inside paths is a note too", func(t *testing.T) {
		res := execFileRead(t, newPictureFileRead(dir, nil, 0), map[string]any{"paths": []any{"images/abc.png", "b.txt"}})
		assert.Eq(t, 0, len(res.Images))
		assert.Contains(t, res.Content, "not attached: session image images/abc.png")
	})
	t.Run("a ref that leaves the directory is refused", func(t *testing.T) {
		_, err := newPictureFileRead(dir, nil, 0).Execute(context.Background(),
			api.ToolCall{Arguments: map[string]any{"path": "../images/x.png"}})
		assert.Require(t, assert.Err(t, err))
	})
}

// TestFileReadPictureNotes pins the non-error notes for a file named like a
// picture whose header is not one, a corrupt picture and a picture over the
// input limit: what the file is, without a failed tool call.
func TestFileReadPictureNotes(t *testing.T) {
	dir := t.TempDir()
	tool := newPictureFileRead(dir, nil, 0)

	t.Run("a text file named .png keeps its text and gets the header hint", func(t *testing.T) {
		writeNamed(t, dir, "notes.png", []byte("alpha\nbeta\n"))
		res := execFileRead(t, tool, map[string]any{"path": "notes.png"})
		assert.Eq(t, 0, len(res.Images))
		assert.True(t, strings.HasPrefix(res.Content, "[notes.png: the file header is not a supported image format"))
		assert.Contains(t, res.Content, "1\talpha")
	})
	t.Run("a corrupt picture is a note", func(t *testing.T) {
		writeNamed(t, dir, "broken.png", append([]byte("\x89PNG\r\n\x1a\n"), []byte("not really a png")...))
		res := execFileRead(t, tool, map[string]any{"path": "broken.png"})
		assert.Eq(t, 0, len(res.Images))
		assert.Contains(t, res.Content, "not attached")
	})
	t.Run("a picture over the input limit is a note", func(t *testing.T) {
		file := filepath.Join(dir, "huge.png")
		assert.Require(t, assert.NoErr(t, os.WriteFile(file, []byte("\x89PNG\r\n\x1a\n"), 0o600)))
		assert.Require(t, assert.NoErr(t, os.Truncate(file, maxImageInputBytes+1)))
		res := execFileRead(t, tool, map[string]any{"path": "huge.png"})
		assert.Eq(t, 0, len(res.Images))
		assert.Contains(t, res.Content, "over the 20 MB input limit")
	})
}

// TestFileReadNotAPictureUnchanged is the fail-closed regression: text, binary
// files and directories are answered exactly as before the picture branch.
func TestFileReadNotAPictureUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeNamed(t, dir, "a.txt", []byte("one\ntwo\n"))
	writeNamed(t, dir, "a.bin", []byte("ab\x00cd"))
	assert.Require(t, assert.NoErr(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755)))
	tool := newPictureFileRead(dir, nil, 0)

	t.Run("text", func(t *testing.T) {
		res := execFileRead(t, tool, map[string]any{"path": "a.txt"})
		assert.Eq(t, "1\tone\n2\ttwo", res.Content)
		assert.Eq(t, 0, len(res.Images))
	})
	t.Run("binary", func(t *testing.T) {
		res := execFileRead(t, tool, map[string]any{"path": "a.bin"})
		assert.Eq(t, "[binary file: a.bin, 5 bytes, type application/octet-stream; not shown]", res.Content)
	})
	t.Run("directory", func(t *testing.T) {
		_, err := tool.Execute(context.Background(), api.ToolCall{Arguments: map[string]any{"path": "sub"}})
		assert.Require(t, assert.Err(t, err))
		assert.Contains(t, err.Error(), "is a directory; use file_list")
	})
	t.Run("a missing file is still an error", func(t *testing.T) {
		_, err := tool.Execute(context.Background(), api.ToolCall{Arguments: map[string]any{"path": "nope.txt"}})
		assert.Require(t, assert.Err(t, err))
	})
}

// writeNamed writes data as dir/name.
func writeNamed(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, name), data, 0o600)))
}
