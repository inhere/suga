// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lineedit is a copy of the VT100 line editor (Terminal) from
// golang.org/x/term v0.29.0 (terminal.go), made display-width aware.
//
// x/term counts every rune as one terminal column, so East Asian wide runes
// (CJK, fullwidth forms, most emoji: two columns) break backspace, cursor
// movement and line wrapping. Local modifications, all width related:
//   - visualLength sums display columns (runeWidth, width.go) instead of runes;
//   - cursor positions are computed by laying out the line cell by cell
//     (posToXY/cellIndex); a wide rune that would start in the last column is
//     moved to the next row, the skipped cell being written as a space;
//   - writeLine, setLine, eraseNPreviousChars and Ctrl-K blank cells by
//     columns, not by rune count;
//   - Backspace, Ctrl-D and Left/Right step over zero-width runes (combining
//     marks) together with their base rune;
//   - readPasswordLine (unused here) is dropped; stRingBuffer moved to history.go.
//
// Additions for asking a question during a pending read (take.go): TakeLine,
// SetPendingLine, ReadLineNoHistory and AbortLine; a Ctrl-C/Ctrl-D that ends
// a read is consumed instead of staying in the input remainder.
//
// Addition for a lone Esc the caller already decided on (esc.go): SetEscAlone
// and EscCallback.
//
// Addition for taking a line back into the editor: WithdrawCallback, called when
// Up is pressed on an empty input line, so a caller can fill the line in (chat
// taking a message still waiting in the running turn back for editing).
//
// Addition for the status line chat shows above the input line while a turn runs
// (status.go): SetStatus. The status line's row belongs to the block the editor
// keeps on screen - the prompt and the line being edited - so every repaint
// (Write) clears it with them and draws it again from the top of the block.
//
// Addition for Ctrl-C on a non-empty line (SetCtrlCClearsLine): it drops the line
// and goes on reading instead of ending the read, so a stray key press does not
// throw a half-typed message away; chat turns it on for the person's own input
// line (the prompt, and the read a running turn keeps) and leaves it off where
// Ctrl-C means "cancel" (the answer of an approval question).
//
// Change to bracketed pastes (paste.go): a newline inside a paste is inserted
// into the line instead of ending the read, so a pasted multi-line text lands on
// the input line as one piece; SetNewlineAsText lets a caller declare the same
// for a terminal that does not bracket pastes. Upstream's ErrPasteIndicator (a
// line that consists only of pasted data) is gone with it: no paste ends a read.
//
// This file holds the terminal itself and the read loop; key parsing is in
// keys.go, the cursor and repainting helpers are in render.go.
//
// For ASCII input the output is byte-for-byte identical to x/term.
package lineedit

import (
	"io"
	"strings"
	"sync"
	"unicode/utf8"
)

// Terminal contains the state for running a VT100 terminal that is capable of
// reading lines of input.
type Terminal struct {
	// AutoCompleteCallback, if non-null, is called for each keypress with
	// the full input line and the current position of the cursor (in
	// bytes, as an index into |line|). If it returns ok=false, the key
	// press is processed normally. Otherwise it returns a replacement line
	// and the new cursor position.
	AutoCompleteCallback func(line string, pos int, key rune) (newLine string, newPos int, ok bool)

	// Escape contains a pointer to the escape codes for this terminal.
	// It's always a valid pointer, although the escape codes themselves
	// may be empty if the terminal doesn't support them.
	Escape *EscapeCodes

	// lock protects the terminal and the state in this object from
	// concurrent processing of a key press and a Write() call.
	lock sync.Mutex

	c      io.ReadWriter
	prompt []rune

	// line is the current line being entered.
	line []rune
	// pos is the logical position of the cursor in line
	pos int
	// echo is true if local echo is enabled
	echo bool
	// pasteActive is true iff there is a bracketed paste operation in
	// progress: a newline read while it is set is text of the paste, not a
	// submit (paste.go).
	pasteActive bool
	// newlineAsText is set by SetNewlineAsText: the next newline key is text of a
	// paste, not a submit. A terminal that does not bracket pastes gives the same
	// bytes for a pasted and a typed newline, so a caller that can tell them apart
	// declares it (paste.go).
	newlineAsText bool

	// cursorX contains the current X value of the cursor where the left
	// edge is 0. cursorY contains the row number where the first row of
	// the current line is 0.
	cursorX, cursorY int
	// maxLine is the greatest value of cursorY so far.
	maxLine int

	termWidth, termHeight int

	// outBuf contains the terminal data to be sent.
	outBuf []byte
	// remainder contains the remainder of any partial key sequences after
	// a read. It aliases into inBuf.
	remainder []byte
	inBuf     [256]byte

	// history contains previously entered commands so that they can be
	// accessed with the up and down keys.
	history stRingBuffer
	// historyIndex stores the currently accessed history entry, where zero
	// means the immediately previous entry.
	historyIndex int
	// When navigating up and down the history it's possible to return to
	// the incomplete, initial line. That value is stored in
	// historyPending.
	historyPending  string
	reverseActive   bool
	reverseQuery    string
	reverseIndex    int
	reverseOriginal string
	reversePrompt   string

	// Local additions (see take.go): reading is set while ReadLine runs,
	// noHistory keeps the current read out of history and pendingLine
	// prefills the next read.
	reading     bool
	noHistory   bool
	pendingLine string
	// escAlone is set by SetEscAlone: the next Esc byte is the Esc key on its own
	// rather than the start of a sequence (esc.go).
	escAlone bool
	// EscCallback, when non-nil, is called for a lone Esc press the caller declared
	// with SetEscAlone. It is called from inside ReadLine with the terminal lock
	// released (like AutoCompleteCallback), so it must not use the terminal; a
	// declared Esc without a callback is dropped like any other key the editor has no
	// use for.
	EscCallback func()
	// WithdrawCallback, when non-nil, is called from inside ReadLine when Up is
	// pressed on an empty input line. ok=true means the caller filled a line in -
	// text it took back from somewhere, which the editor then shows with the cursor
	// at its end - so the key is consumed; ok=false keeps Up's usual meaning
	// (history). It is called with the terminal lock released (like EscCallback and
	// AutoCompleteCallback), so it may write to the terminal; an empty line always
	// keeps Up's usual meaning.
	WithdrawCallback func() (string, bool)
	// PasteImageCallback, when non-nil, runs on Alt+V (design 0.3): the chat
	// appends the clipboard's picture to its pending queue and prints the
	// "[已附图 …]" note above the input line. Called with the terminal lock
	// released (like WithdrawCallback), so it may write to the terminal.
	PasteImageCallback func()
	// ctrlCClearsLine is set by SetCtrlCClearsLine: a Ctrl-C on a non-empty line
	// drops what was typed and the read goes on, instead of ending it with
	// ErrInterrupt. An empty line always ends the read.
	ctrlCClearsLine bool
	// inputStyle paints a submitted line (highlight.go); zero by default.
	inputStyle InputStyle
	// statusText is the text SetStatus was given and status the runes actually on
	// screen (cut to the terminal width, so the two differ); statusOn says the
	// status line's row is on screen, i.e. that the block the cursor belongs to
	// starts one row above the prompt row (status.go).
	statusText string
	status     []rune
	statusOn   bool
}

// DefaultWidth is the terminal width the editor lays lines out with until it is
// told the real one (SetSize).
const DefaultWidth = 80

// NewTerminal runs a VT100 terminal on the given ReadWriter. If the ReadWriter is
// a local terminal, that terminal must first have been put into raw mode.
// prompt is a string that is written at the start of each input line (i.e.
// "> ").
func NewTerminal(c io.ReadWriter, prompt string) *Terminal {
	return &Terminal{
		Escape:       &vt100EscapeCodes,
		c:            c,
		prompt:       []rune(prompt),
		termWidth:    DefaultWidth,
		termHeight:   24,
		echo:         true,
		historyIndex: -1,
	}
}

// ReadPassword temporarily changes the prompt and reads a password, without
// echo, from the terminal.
func (t *Terminal) ReadPassword(prompt string) (line string, err error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	oldPrompt := t.prompt
	t.prompt = []rune(prompt)
	t.echo = false

	line, err = t.readLine()

	t.prompt = oldPrompt
	t.echo = true

	return
}

// ReadLine returns a line of input from the terminal.
func (t *Terminal) ReadLine() (line string, err error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	return t.readLine()
}

// RestoreEcho gives the read in progress its echo back and records it. The turn's
// steer read echoes like the prompt read (chat.steering keeps the person's input
// line on screen for the whole turn), so nothing turns echoing off while a turn
// runs; the method is the counterpart of a read that changes hands - an approval
// question asking on the turn's own read gives it back with RestoreEcho - and a
// caller that wants a silent read reads a password (ReadPassword) instead. It
// draws nothing by itself: the caller sets and redraws the prompt (UpdatePrompt)
// or lets the next read do it.
func (t *Terminal) RestoreEcho() {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.echo = true
}

func (t *Terminal) readLine() (line string, err error) {
	// t.lock must be held at this point

	t.reading = true
	defer func() { t.reading, t.noHistory = false, false }()
	prefilled := t.applyPendingLine()
	if !t.lineShown() {
		// Nothing of the block is on screen yet: draw it - the status line, while
		// one is set (chat may set it before this read draws anything), above the
		// prompt - the way every repaint draws it (paintBlock).
		t.statusOn = len(t.status) > 0
		t.paintBlock()
		t.c.Write(t.outBuf)
		t.outBuf = t.outBuf[:0]
	} else if prefilled {
		// The prompt is already on screen with the old line: repaint it.
		t.repaintLocked()
	}

	for {
		rest := t.remainder
		lineOk := false
		for !lineOk {
			var key rune
			key, rest = bytesToKey(rest, t.pasteActive)
			if key == utf8.RuneError {
				if t.reverseActive && len(rest) > 0 && rest[len(rest)-1] == keyEscape {
					rest = rest[:len(rest)-1]
					t.cancelReverseSearch()
					continue
				}
				if !t.takeEscAlone(&rest) {
					break
				}
				// A lone Esc the caller declared (SetEscAlone): report the key now
				// instead of waiting for the rest of a sequence (esc.go).
				if escCallback := t.EscCallback; escCallback != nil {
					t.lock.Unlock()
					escCallback()
					t.lock.Lock()
				}
				continue
			}
			if !t.pasteActive {
				// Local change: the key is consumed (kept out of t.remainder),
				// so a later ReadLine does not see the same Ctrl-C/Ctrl-D again.
				if key == keyCtrlD {
					if len(t.line) == 0 {
						t.keepRemainder(rest)
						return "", io.EOF
					}
				}
				if key == keyCtrlC {
					if t.ctrlCClearsLine && len(t.line) > 0 {
						// Local change: a non-empty line is what Ctrl-C drops at an input
						// line the caller edits this way (chat: the prompt, and the input
						// line of a running turn). The text goes away, the read goes on and
						// the caller sees none of it.
						t.clearInputLine()
						continue
					}
					t.keepRemainder(rest)
					return "", ErrInterrupt
				}
				if key == keyPasteStart {
					t.pasteActive = true
					continue
				}
			} else if key == keyPasteEnd {
				t.pasteActive = false
				continue
			}
			line, lineOk = t.handleKey(key)
		}
		t.keepRemainder(rest)
		t.c.Write(t.outBuf)
		t.outBuf = t.outBuf[:0]
		if lineOk {
			if t.echo {
				t.historyIndex = -1
				if !t.noHistory {
					t.history.Add(line)
				}
			}
			return
		}

		// t.remainder is a slice at the beginning of t.inBuf
		// containing a partial key sequence
		readBuf := t.inBuf[len(t.remainder):]
		var n int

		t.lock.Unlock()
		n, err = t.c.Read(readBuf)
		t.lock.Lock()

		if err != nil {
			return
		}

		t.remainder = t.inBuf[:n+len(t.remainder)]
	}
}

// SetHistory replaces the editor history with entries ordered oldest to newest.
func (t *Terminal) SetHistory(entries []string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.history = stRingBuffer{}
	for _, entry := range entries {
		t.history.Add(entry)
	}
	t.historyIndex = -1
}

// reverseSearch implements bash-like incremental search over the loaded history.
func (t *Terminal) reverseSearch() {
	if !t.reverseActive {
		t.reverseActive = true
		t.reverseQuery = string(t.line)
		t.reverseOriginal = string(t.line)
		t.reverseIndex = 0
		t.reversePrompt = string(t.prompt)
	} else {
		t.reverseIndex++
	}
	t.reversePromptForState()
	t.findReverseMatch()
}

func (t *Terminal) reversePromptForState() {
	if t.reverseQuery == "" {
		t.prompt = []rune("(reverse-i-search)`': ")
	} else {
		t.prompt = []rune("(reverse-i-search)`" + t.reverseQuery + "': ")
	}
}

// findReverseMatch redraws the complete input block so the search prompt and the
// matched line stay in sync, including when a status line is present.
func (t *Terminal) findReverseMatch() {
	for i := t.reverseIndex; ; i++ {
		entry, ok := t.history.NthPreviousEntry(i)
		if !ok {
			break
		}
		if strings.Contains(entry, t.reverseQuery) {
			runes := []rune(entry)
			t.setLine(runes, len(runes))
			t.reverseIndex = i
			_ = t.repaintLocked()
			return
		}
	}
	t.prompt = []rune("(failed reverse-i-search)`" + t.reverseQuery + "': ")
	_ = t.repaintLocked()
}

func (t *Terminal) reverseSearchBackspace() {
	if len(t.reverseQuery) == 0 {
		return
	}
	runes := []rune(t.reverseQuery)
	t.reverseQuery = string(runes[:len(runes)-1])
	t.reverseIndex = 0
	t.reversePromptForState()
	t.findReverseMatch()
}

func (t *Terminal) cancelReverseSearch() {
	if !t.reverseActive {
		return
	}
	t.setLine([]rune(t.reverseOriginal), len([]rune(t.reverseOriginal)))
	t.endReverseSearch()
	_ = t.repaintLocked()
}

func (t *Terminal) endReverseSearch() {
	if !t.reverseActive {
		return
	}
	t.reverseActive = false
	t.prompt = []rune(t.reversePrompt)
}

// SetPrompt sets the prompt to be used when reading subsequent lines.
func (t *Terminal) SetPrompt(prompt string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.prompt = []rune(prompt)
}

// UpdatePrompt replaces the prompt and, when a prompt is on screen (a ReadLine
// is in progress), repaints it together with the line being edited. SetPrompt
// alone only takes effect on the next redraw. (Local addition.)
func (t *Terminal) UpdatePrompt(prompt string) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	if string(t.prompt) == prompt {
		return nil
	}
	t.prompt = []rune(prompt)
	// A read in progress may have no line on the screen (a turn's read runs with
	// echoing off): drawing the prompt is then exactly what the caller wants - the
	// next prompt after a turn, or an approval question borrowing the read.
	return t.repaintLocked()
}

// ErrInterrupt is returned by ReadLine for Ctrl-C. It matches io.EOF too,
// so callers that only know "input ended" keep treating it that way, while
// chat can tell it apart from Ctrl-D to ask for a second Ctrl-C.
var ErrInterrupt error = interruptError{}

type interruptError struct{}

func (interruptError) Error() string        { return "interrupted (Ctrl-C)" }
func (interruptError) Is(target error) bool { return target == io.EOF }

// SetCtrlCClearsLine makes Ctrl-C on a non-empty line drop that line and go on
// reading instead of ending the read with ErrInterrupt; an empty line always ends
// it. It is off by default: a reader that wants Ctrl-C as "cancel what I am asking"
// (chat's approval question, ask.go) leaves it off, while the reader of the
// person's own input line turns it on (chat's prompt and its read during a turn),
// so a stray key press does not throw a half-typed message away.
func (t *Terminal) SetCtrlCClearsLine(on bool) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.ctrlCClearsLine = on
}

// SetBracketedPasteMode requests that the terminal bracket paste operations
// with markers. Not all terminals support this; where it is supported a paste
// reaches the editor as one chunk with its own start and end markers, which is
// what tells its newlines from a typed Enter (paste.go).
func (t *Terminal) SetBracketedPasteMode(on bool) {
	if on {
		io.WriteString(t.c, "\x1b[?2004h")
	} else {
		io.WriteString(t.c, "\x1b[?2004l")
	}
}
