//go:build linux

package clipboardimage

import (
	"bytes"
	"fmt"
	"io"
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
// The output is read through maxClipboardBytes: a clipboard holding something
// huge is refused instead of being buffered whole.
func readType(name string, args ...string) ([]byte, error) {
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
		return nil, fmt.Errorf("剪贴板数据超过 %d 字节，不读取", maxClipboardBytes)
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
