package orders

import "strings"

// Normalize trims the line and folds its region field to lower case.
func Normalize(line string) string {
	return strings.ToLower(strings.TrimSpace(line))
}

// RegionOf reads the region= field of one log line, or "" when it is absent.
func RegionOf(line string) string {
	for _, field := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(field, "region="); ok {
			return v
		}
	}
	return ""
}

// IsError reports whether the line carries status=error.
func IsError(line string) bool {
	for _, field := range strings.Fields(line) {
		if field == "status=error" {
			return true
		}
	}
	return false
}
