// Package clipboardimage reads a picture from the system clipboard for chat's
// Alt+V paste (image input design 0.3). Every platform keeps its own reader in
// a build-tagged file; the common piece dispatches and carries the test hook
// the ptysmoke scripts use. The bytes go straight into the view_image pipeline
// (validation, scaling, encoding) — nothing here decodes them.
package clipboardimage

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrNoClipboardImage is what a reader returns when this process could see a
// clipboard but no picture is on it (chat shows it verbatim).
var ErrNoClipboardImage = errors.New("剪贴板里没有图片")

// fakeClipboardEnv gives the pseudo-terminal smoke a fake clipboard: the
// environment names a picture file the reader hands over instead of touching
// the system clipboard (scripts/ptysmoke runs on headless containers).
const fakeClipboardEnv = "SUGA_FAKE_CLIPBOARD_IMAGE"

// ImageReader reads one picture from the clipboard: its bytes (any format the
// view_image pipeline accepts, PNG preferred), a name to report and an error.
// A clipboard without a picture comes back as ErrNoClipboardImage.
type ImageReader func() (data []byte, name string, err error)

// Reader returns this platform's reader; with the fake clipboard named in the
// environment it returns a file reader instead, so the smoke tests the whole
// Alt+V flow without a real clipboard. maxClipboardBytes bounds what a reader
// may hand back.
func Reader() ImageReader {
	if p := os.Getenv(fakeClipboardEnv); p != "" {
		return func() ([]byte, string, error) {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, "", err
			}
			return data, filepath.Base(p), nil
		}
	}
	return platformReader()
}

// maxClipboardBytes is the largest thing a clipboard read hands over (well
// above the 20MB view_image input limit, small enough to never stall a turn).
const maxClipboardBytes = 40 << 20
