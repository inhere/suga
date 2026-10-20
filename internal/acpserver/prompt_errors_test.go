package acpserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
)

// TestPromptContentErrors pins the per-image failures (bad base64, an
// unsupported format, a remote URI, an oversized file): each becomes a note in
// the text with its reason instead of failing the turn; only a prompt with no
// text and no usable image is refused.
func TestPromptContentErrors(t *testing.T) {
	t.Run("bad base64 names the decode failure", func(t *testing.T) {
		text, images, err := promptContent([]acp.ContentBlock{
			acp.TextBlock("看"),
			{Image: &acp.ContentBlockImage{Data: "not-base64!!", MimeType: "image/png"}},
		}, "/tmp")
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(images))
		assert.Contains(t, text, "[图片 1 不可用：base64 解码失败")
		assert.Contains(t, text, "看")
	})

	t.Run("a non-image payload is refused with the format list", func(t *testing.T) {
		text, _, err := promptContent([]acp.ContentBlock{
			{Image: &acp.ContentBlockImage{Data: "c28tZS10ZXh0", MimeType: "image/png"}},
			acp.TextBlock("文字"),
		}, "/tmp")
		assert.NoErr(t, err)
		assert.Contains(t, text, "[图片 1 不可用：unsupported image format")
		assert.Contains(t, text, "文字")
	})

	t.Run("a remote URI is refused", func(t *testing.T) {
		text, _, err := promptContent([]acp.ContentBlock{
			acp.TextBlock("看"),
			{Image: &acp.ContentBlockImage{Uri: uriPtr("https://example.com/a.png")}},
		}, "/tmp")
		assert.NoErr(t, err)
		assert.Contains(t, text, "远程图片不支持")
	})

	t.Run("an oversized source file is refused with the limit", func(t *testing.T) {
		cwd := t.TempDir()
		big := filepath.Join(cwd, "big.png")
		assert.Require(t, assert.NoErr(t, os.WriteFile(big, make([]byte, 21<<20), 0o600)))
		text, _, err := promptContent([]acp.ContentBlock{
			acp.TextBlock("看"),
			{Image: &acp.ContentBlockImage{Uri: uriPtr(big)}},
		}, cwd)
		assert.NoErr(t, err)
		assert.Contains(t, text, "[图片 1 不可用：")
		assert.Contains(t, text, "byte limit")
	})

	t.Run("no text and no usable image refuses the prompt", func(t *testing.T) {
		_, _, err := promptContent([]acp.ContentBlock{
			{Image: &acp.ContentBlockImage{Data: "not-base64!!", MimeType: "image/png"}},
		}, "/tmp")
		assert.Err(t, err)
	})
}

func uriPtr(s string) *string { return &s }
