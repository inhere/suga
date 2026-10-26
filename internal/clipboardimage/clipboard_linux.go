//go:build linux

package clipboardimage

import (
	"bytes"
	"errors"
	"io"
	"os/exec"

	"github.com/inhere/suga/internal/i18n"
)

// Linux clipboard reading under Wayland first (wl-paste), then X11 (xclip);
// both read exactly the image/png type. When neither binary exists the reader
// says what to install instead of failing; tr renders the name and the hints.
func platformReader(tr i18n.Translator) ImageReader {
	return func() ([]byte, string, error) { return readLinuxClipboard(tr) }
}

func readLinuxClipboard(tr i18n.Translator) ([]byte, string, error) {
	t := trOrFallback(tr)
	if data, err := readType(tr, "wl-paste", "--type", "image/png"); err == nil && len(data) > 0 {
		return data, t.T("host.clipboard.name"), nil
	}
	if data, err := readType(tr, "xclip", "-selection", "clipboard", "-t", "image/png", "-o"); err == nil && len(data) > 0 {
		return data, t.T("host.clipboard.name"), nil
	}
	// One of them exists but had no picture on the clipboard (or it failed for
	// another reason); reading the type twice only produced errors, and an
	// installation hint is worse than "no picture here" now.
	if installed("wl-paste") || installed("xclip") {
		return nil, "", ErrNoClipboardImage
	}
	return nil, "", errors.New(t.T("host.clipboard.linux.missing"))
}

// readType runs one clipboard reader; a wayland/xdg session can fail for
// reasons of its own (no WAYLAND_DISPLAY), so callers fall through to the next.
// The output is read through maxClipboardBytes: a clipboard holding something
// huge is refused instead of being buffered whole.
func readType(tr i18n.Translator, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	n, readErr := io.Copy(&buf, io.LimitReader(out, maxClipboardBytes+1))
	if n > maxClipboardBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New(trOrFallback(tr).T("host.clipboard.linux.too_large", map[string]any{"limit": maxClipboardBytes}))
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	return buf.Bytes(), nil
}

// installed reports whether a reader binary is on PATH.
func installed(name string) bool {
	return exec.Command("sh", "-c", "command -v "+name).Run() == nil
}
