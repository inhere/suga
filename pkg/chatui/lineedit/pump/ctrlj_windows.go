//go:build windows

package pump

// ctrlJIsInputNewline: a bare LF inserts a newline in the input on Windows, which
// covers Ctrl+J and Ctrl+Enter - a console hands a cooked line over as CRLF (the pump
// drops that LF), and Windows Terminal 1.24 sends Ctrl+Enter as a bare LF (measured
// with tmp/keyprobe). Shift+Enter arrives as CR there, i.e. like Enter itself, so it
// cannot be told apart without a terminal-side binding. Ctrl+Enter is the pair of keys
// that is free - and Windows Terminal keeps Alt+Enter for fullscreen.
const ctrlJIsInputNewline = true
