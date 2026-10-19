//go:build linux

package clipboardimage

import (
	"fmt"
	"os/exec"
)

// Linux clipboard reading under Wayland first (wl-paste), then X11 (xclip);
// both read exactly the image/png type. When neither binary exists the reader
// says what to install instead of failing.
func platformReader() ImageReader { return readLinuxClipboard }

func readLinuxClipboard() ([]byte, string, error) {
	if data, err := readType("wl-paste", "--type", "image/png"); err == nil && len(data) > 0 {
		return data, "剪贴板", nil
	}
	if data, err := readType("xclip", "-selection", "clipboard", "-t", "image/png", "-o"); err == nil && len(data) > 0 {
		return data, "剪贴板", nil
	}
	// One of them exists but had no picture on the clipboard (or it failed for
	// another reason); reading the type twice only produced errors, and an
	// installation hint is worse than "no picture here" now.
	if installed("wl-paste") || installed("xclip") {
		return nil, "", ErrNoClipboardImage
	}
	return nil, "", fmt.Errorf("未找到 wl-paste 或 xclip：安装 wl-clipboard（Wayland）或 xclip（X11）后可用 Alt+V 贴图")
}

// readType runs one clipboard reader; a wayland/xdg session can fail for
// reasons of its own (no WAYLAND_DISPLAY), so callers fall through to the next.
func readType(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}

// installed reports whether a reader binary is on PATH.
func installed(name string) bool {
	return exec.Command("sh", "-c", "command -v "+name).Run() == nil
}
