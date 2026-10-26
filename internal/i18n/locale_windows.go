//go:build windows

package i18n

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemLocale uses the user's default locale and falls back to preferred UI
// languages when the kernel32 API is unavailable or fails.
func systemLocale() string {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")
	// Find first: Call panics when the procedure cannot be loaded.
	if proc.Find() == nil {
		buf := make([]uint16, 85) // LOCALE_NAME_MAX_LENGTH
		if ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); ret != 0 {
			if locale := windows.UTF16ToString(buf); locale != "" {
				return locale
			}
		}
	}
	locales, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err == nil && len(locales) > 0 {
		return strings.TrimSpace(locales[0])
	}
	return ""
}
