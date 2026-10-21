package codeintel

import "unicode/utf8"

// MaxGrepLineBytes trims the very long matching lines (minified files) one
// reference reports. It is the cap file_grep applies to the lines it shows, and
// the tool layer reads it from here so the two never drift apart.
const MaxGrepLineBytes = 300

// utf8Cut returns the largest cut not above the requested one that does not
// split a UTF-8 sequence, so a truncated line stays readable.
func utf8Cut(s string, cut int) int {
	if cut >= len(s) {
		return len(s)
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}
