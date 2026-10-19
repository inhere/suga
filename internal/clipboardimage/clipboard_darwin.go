//go:build darwin

package clipboardimage

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// macOS clipboard reading: osascript knows «class PNGf» and can write those
// bits to a file, which reads back as the PNG body (pbpaste is text-only).
func platformReader() ImageReader { return readDarwinClipboard }

func readDarwinClipboard() ([]byte, string, error) {
	f, err := os.CreateTemp("", "suga-clipboard-*.png")
	if err != nil {
		return nil, "", fmt.Errorf("无法创建剪贴板暂存文件：%w", err)
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
		return nil, "", fmt.Errorf("读取剪贴板失败（osascript）：%s", strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, "", fmt.Errorf("读取剪贴板暂存文件：%w", err)
	}
	if len(data) == 0 {
		return nil, "", ErrNoClipboardImage
	}
	return data, "剪贴板", nil
}
