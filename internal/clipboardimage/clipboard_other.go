//go:build !windows && !darwin && !linux

package clipboardimage

import "fmt"

// platformReader on an OS without a clipboard reader: chat says so instead of
// failing.
func platformReader() ImageReader {
	return func() ([]byte, string, error) {
		return nil, "", fmt.Errorf("当前系统没有可用的剪贴板读取器")
	}
}
