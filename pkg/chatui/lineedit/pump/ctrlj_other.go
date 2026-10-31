//go:build !windows

package pump

// ctrlJIsInputNewline: on Unix ICRNL turns Enter into a bare LF, so a pre-typed
// line ends in one as well and Ctrl+J cannot be told from that Enter - there a bare
// LF has to submit. Alt+Enter is the key for a newline in the input.
const ctrlJIsInputNewline = false
