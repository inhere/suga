// Package pump is the byte pump a line editor (lineedit.Terminal) reads through,
// plus the terminal helpers around it (platform input wait, input flush, output
// flags, window size watching).
//
// The pump hands over one byte per Read (the editor keeps partial key sequences
// itself) and decides what the editor cannot tell by itself: whether a 0x1b byte
// is the Esc key on its own or the start of an escape sequence, whether a newline
// is pasted text or a submit, and whether ESC followed by Enter is Alt+Enter. What
// the pump may decide is not a static setting: the caller (Hooks) answers it before
// every read, because the same terminal is read in different modes over time (a
// prompt between turns, a steer read during a turn).
package pump
