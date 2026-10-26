//go:build windows

package clipboardimage

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
)

// TestClipboardHintsEnglish: the Windows reader's failure reasons come from the
// host catalog, so an English session explains a broken bitmap in English. The
// conversion refuses a truncated header before it touches the clipboard, which
// makes the real call path testable without one.
func TestClipboardHintsEnglish(t *testing.T) {
	tr, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("english reasons", func(t *testing.T) {
		_, err := dibToPNG(tr, []byte{1, 2, 3})
		assert.ErrMsgContains(t, err, "the clipboard bitmap header is incomplete")

		// A BITMAPINFOHEADER claiming an impossible size is refused by name.
		data := make([]byte, 40)
		data[14] = 8                  // 8 bpp
		data[4], data[5] = 0xff, 0x7f // width 32767
		data[8], data[9] = 0xff, 0x7f // height 32767
		_, err = dibToPNG(tr, data)
		assert.ErrMsgContains(t, err, "unexpected clipboard bitmap size 32767x32767")
	})

	t.Run("without a catalog the chinese text stays", func(t *testing.T) {
		_, err := dibToPNG(nil, []byte{1, 2, 3})
		assert.ErrMsgContains(t, err, "剪贴板位图头不完整")
	})
}
