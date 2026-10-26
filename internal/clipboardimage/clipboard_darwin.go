//go:build darwin

package clipboardimage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/inhere/suga/internal/i18n"
)

// macOS clipboard reading: osascript knows «class PNGf» and can write those
// bits to a file, which reads back as the PNG body (pbpaste is text-only). tr
// renders the reported name and the failure reasons.
func platformReader(tr i18n.Translator) ImageReader {
	return func() ([]byte, string, error) { return readDarwinClipboard(tr) }
}

func readDarwinClipboard(tr i18n.Translator) ([]byte, string, error) {
	t := trOrFallback(tr)
	f, err := os.CreateTemp("", "suga-clipboard-*.png")
	if err != nil {
		return nil, "", errors.New(t.T("host.clipboard.temp_create_failed", map[string]any{"err": err}))
	}
	name := f.Name()
	_ = f.Close()
	defer os.Remove(name)
	script := fmt.Sprintf(`set imgData to (the clipboard as «class PNGf»)
set fd to open for access (POSIX file %q) with write permission
try
	write imgData to fd
end try
close access fd`, name)
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "«class PNGf»") {
			// AppleScript phrases a screenshot-less clipboard as "the clipboard
			// as «class PNGf»" failing: no picture then, not a broken setup.
			return nil, "", ErrNoClipboardImage
		}
		return nil, "", errors.New(t.T("host.clipboard.osascript_failed", map[string]any{"err": strings.TrimSpace(string(out))}))
	}
	if info, err := os.Stat(name); err == nil && info.Size() > maxClipboardBytes {
		return nil, "", errors.New(t.T("host.clipboard.darwin_too_large", map[string]any{
			"bytes": info.Size(), "limit": maxClipboardBytes,
		}))
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, "", errors.New(t.T("host.clipboard.read_temp_failed", map[string]any{"err": err}))
	}
	if len(data) == 0 {
		return nil, "", ErrNoClipboardImage
	}
	return data, t.T("host.clipboard.name"), nil
}
