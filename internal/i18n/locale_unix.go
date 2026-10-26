//go:build !windows

package i18n

import "os"

// systemLocale follows the POSIX locale precedence used by Unix tools.
func systemLocale() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
