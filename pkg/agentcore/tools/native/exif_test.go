package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// jpegWithOrientation returns a width×height JPEG whose APP1 segment declares
// the EXIF orientation, as a camera does: the pixels stay as encoded and the
// viewer is expected to rotate them.
func jpegWithOrientation(t *testing.T, img image.Image, orientation int) []byte {
	t.Helper()
	var buf bytes.Buffer
	assert.Require(t, assert.NoErr(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})))
	encoded := buf.Bytes()

	// APP1 payload: "Exif\0\0" + a little-endian TIFF block with one IFD0 entry
	// (orientation, SHORT, 1) and no next IFD.
	tiff := make([]byte, 26)
	copy(tiff[0:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)  // IFD0 right after the header
	binary.LittleEndian.PutUint16(tiff[8:10], 1) // one entry
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3) // SHORT
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], uint16(orientation))
	// tiff[20:22] keeps the value field padding, tiff[22:26] the next IFD (0).
	payload := append([]byte("Exif\x00\x00"), tiff...)

	segment := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	segment = append(segment, payload...)

	// SOI + APP1 + the rest of the JPEG (the APP0 JFIF segment follows it).
	out := make([]byte, 0, len(encoded)+len(segment))
	out = append(out, encoded[:2]...)
	out = append(out, segment...)
	return append(out, encoded[2:]...)
}

// TestExifOrientation pins the oriented JPEG path: the pixels are rotated before
// scaling, so the reported size is the upright one.
func TestExifOrientation(t *testing.T) {
	dir := t.TempDir()
	// A 40×20 picture with a red top-left block, so a rotation can be checked by
	// the corner that ends up where (big enough that JPEG chroma subsampling
	// does not grey the block out).
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			c := color.RGBA{R: 20, G: 200, B: 40, A: 255}
			if x < 10 && y < 5 {
				c = color.RGBA{R: 220, G: 20, B: 20, A: 255}
			}
			img.Set(x, y, c)
		}
	}

	t.Run("the tag is read from the APP1 segment", func(t *testing.T) {
		assert.Eq(t, 6, exifOrientation(jpegWithOrientation(t, img, 6)))
		assert.Eq(t, 1, exifOrientation(jpegWithOrientation(t, img, 1)))
	})
	t.Run("a picture without EXIF keeps its orientation", func(t *testing.T) {
		var buf bytes.Buffer
		assert.Require(t, assert.NoErr(t, jpeg.Encode(&buf, img, nil)))
		assert.Eq(t, 1, exifOrientation(buf.Bytes()))
		assert.Eq(t, 1, exifOrientation([]byte("not a jpeg")))
		assert.Eq(t, 1, exifOrientation(nil))
	})
	t.Run("orientation 6 turns the picture a quarter clockwise", func(t *testing.T) {
		path := filepath.Join(dir, "rotated.jpg")
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, jpegWithOrientation(t, img, 6), 0o600)))
		res, err := NewViewImageTool(dir, nil, nil, "").Execute(context.Background(),
			api.ToolCall{Arguments: map[string]any{"path": "rotated.jpg"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		// The stored 40×20 becomes 20×40.
		assert.Eq(t, 20, res.Images[0].Width)
		assert.Eq(t, 40, res.Images[0].Height)
		assert.Contains(t, res.Content, "EXIF orientation 6")
		// The encoded picture carries the rotation: the red top-left block of the
		// stored picture is now a block in the top-right corner.
		sent, _, err := image.Decode(bytes.NewReader(res.Images[0].Data))
		assert.Require(t, assert.NoErr(t, err))
		bounds := sent.Bounds()
		assert.Eq(t, 20, bounds.Dx())
		assert.Eq(t, 40, bounds.Dy())
		r, g, b, _ := sent.At(bounds.Max.X-2, bounds.Min.Y+2).RGBA()
		assert.True(t, r > 3*g && r > 3*b)
	})
	t.Run("a quarter-turned picture is scaled after the rotation", func(t *testing.T) {
		wide := image.NewRGBA(image.Rect(0, 0, 40, 10))
		path := filepath.Join(dir, "wide.jpg")
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, jpegWithOrientation(t, wide, 8), 0o600)))
		res, err := NewViewImageTool(dir, nil, nil, "").Execute(context.Background(),
			api.ToolCall{Arguments: map[string]any{"path": "wide.jpg", "max_side": 5}})
		assert.Require(t, assert.NoErr(t, err))
		// Stored 40×10 becomes 10×40 upright; its long side is scaled to 5.
		assert.Eq(t, 1, res.Images[0].Width)
		assert.Eq(t, 5, res.Images[0].Height)
	})
}

// TestApplyOrientation pins the pixel mapping of every EXIF orientation: the
// marker pixel of the stored picture ends up where the tag says.
func TestApplyOrientation(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.RGBA{R: 255, A: 255}) // the marker
	isMarker := func(img image.Image, x, y int) bool {
		r, _, _, _ := img.At(x, y).RGBA()
		return r > 0x8000
	}
	// Stored 3×2: a marker at (0,0) moves to these coordinates.
	tests := []struct {
		orientation, x, y int
	}{
		{1, 0, 0}, {2, 2, 0}, {3, 2, 1}, {4, 0, 1},
		{5, 0, 0}, {6, 1, 0}, {7, 1, 2}, {8, 0, 2},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("orientation %d", tc.orientation), func(t *testing.T) {
			out := applyOrientation(src, tc.orientation)
			assert.Eq(t, tc.orientation >= 5, out.Bounds().Dx() == 2)
			assert.True(t, isMarker(out, tc.x, tc.y))
		})
	}
}
