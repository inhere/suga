package pump

import (
	"io"
	"time"

	"github.com/inhere/suga/pkg/chatui/lineedit"
)

// Hooks is what the pump asks of its caller. The three *Wanted methods are
// evaluated on every Read, never cached: the mode of the read in progress decides
// them. The remaining methods are the lifecycle of one read.
type Hooks interface {
	// AltEnterWanted reports whether the read in progress takes ESC followed by
	// Enter as "insert a newline in the input" and has the wait for the byte after
	// the ESC (a prompt read does, a steer read owns the lone Esc).
	AltEnterWanted() bool
	// PasteNewlineWanted reports whether the pump may take a newline for pasted
	// text (only where the byte-ready wait can be trusted).
	PasteNewlineWanted() bool
	// EscKeyWanted reports whether the read in progress takes a lone Esc as a key
	// press (a steer read does, a prompt read keeps x/term's behaviour).
	EscKeyWanted() bool
	// Win32Records reports whether the read in progress gets win32-input-mode
	// records instead of key bytes: the byte-level guesses are then off.
	Win32Records() bool
	// WaitInput reports whether more input is ready within timeout, without reading
	// it. A hook without a wait answers true (leave the Esc to the editor as a
	// sequence and the newline as a submit).
	WaitInput(timeout time.Duration) bool

	// Epoch is the input epoch a byte read right now belongs to. It is taken when
	// the byte is read and travels with the byte (also while the pump holds it).
	Epoch() uint64
	// HandOver is asked right before a byte goes to the editor: false means the
	// input was flushed after the byte was read, so the byte is dropped. A true
	// answer marks the byte as being handed to the editor.
	HandOver(epoch uint64) bool

	// StickyEOF returns the input's end once it has ended (nil before): the pump
	// answers it at once instead of blocking in a read that will not return.
	StickyEOF() error
	// OnFirstByte runs when the first byte of this pump has arrived, before that
	// byte reaches the editor (the activity handshake); an error ends the Read.
	OnFirstByte() error
	// OnConsumed runs at the start of every Read: the previous byte has been
	// processed by the editor.
	OnConsumed()
	// OnEOF is told about every read that returned no byte.
	OnEOF(err error)
	// OnEscPressed runs for a lone Esc the pump declared (lineedit.EscCallback). The
	// pump installs it on the terminal when the terminal has no EscCallback yet.
	OnEscPressed()
}

// EscWaitTimeout is how long a read waits for a byte after 0x1b before taking the
// Esc as a key press of its own (design D2).
const EscWaitTimeout = 50 * time.Millisecond

// PasteNewlineTimeout is how long a read waits for a byte after a newline before
// taking it as a submit where the terminal does not bracket pastes. A paste
// arrives as one chunk, so the byte after one of its line breaks is already
// readable within a few milliseconds, while a person's Enter is followed by
// nothing: no key press comes that fast (fastest typing and key repeat are
// around 30-50ms apart). Only platforms whose wait really reports a ready byte
// are asked (WaitInputSupported).
const PasteNewlineTimeout = 8 * time.Millisecond

// escByte is the Escape byte: on its own it is the Esc key, anything that follows
// makes it the start of an escape sequence (arrow keys, paste markers).
const escByte = 0x1b

// crByte and lfByte are the bytes a terminal hands over for Enter: CR in raw mode,
// LF for a line typed in cooked mode (ICRNL on Unix; the Windows console sends CRLF),
// which the line editor takes as Enter too (lineedit.keyLF).
const (
	crByte = 0x0d
	lfByte = 0x0a
)

// Pump is the io.Reader New returns. The extra method is for callers that must
// build the reader before the terminal exists (lineedit.NewTerminal takes the
// reader): create it with NewPump and Bind the terminal before the first Read.
type Pump struct {
	input io.Reader
	term  *lineedit.Terminal
	hooks Hooks

	activitySent bool
	// crHanded is true while the byte read last is a CR: the LF directly after it is
	// the LF half of a CRLF pair (a pre-typed line from the Windows console, a CRLF
	// paste), not an Enter of its own — x/term drops the same LF when both bytes
	// arrive in one read. Any other byte clears it.
	crHanded bool
	// held is a byte the pump read one step ahead while deciding whether a newline
	// is pasted text, with the epoch it was read in: it is handed to the editor by
	// the next Read like any other byte.
	held *heldByte
}

// heldByte is one byte of input read ahead of the editor (see Pump.held).
type heldByte struct {
	b     byte
	epoch uint64
}

// New returns the pump reading in through the hooks h. t may be nil (a read that
// is not on a terminal: nothing is declared to an editor then).
func New(in io.Reader, t *lineedit.Terminal, h Hooks) io.Reader {
	p := NewPump(in, h)
	p.Bind(t)
	return p
}

// NewPump is New without the terminal: call Bind before the first Read.
func NewPump(in io.Reader, h Hooks) *Pump {
	return &Pump{input: in, hooks: h}
}

// Bind sets the terminal the pump declares newlines and Esc keys to. It must be
// called before the first Read (and not concurrently with it).
func (r *Pump) Bind(t *lineedit.Terminal) {
	r.term = t
	if t != nil && t.EscCallback == nil {
		t.EscCallback = r.hooks.OnEscPressed
	}
}

// ResetFirstByte arms OnFirstByte again: call it when a new read of a line starts
// (before the editor's ReadLine), so the first byte of that read gets its own
// activity handshake. Without a call OnFirstByte runs once for the pump's life. It
// must not run concurrently with Read; the goroutine that starts the read calls it.
func (r *Pump) ResetFirstByte() { r.activitySent = false }

func (r *Pump) declareNewlineAsText() {
	if r.term != nil {
		r.term.SetNewlineAsText()
	}
}

func (r *Pump) declareEscLone() {
	if r.term != nil {
		r.term.SetEscAlone()
	}
}

func (r *Pump) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	h := r.hooks
	// Asking for more input means the previous byte has been processed.
	h.OnConsumed()
	// Records carry whole keys, so the byte-level guesses below are off while they
	// are what the editor reads (win32-input-mode).
	records := h.Win32Records()
	for {
		// An input that has already ended answers its EOF at once: a later request
		// must not block in a read that will not return.
		if err := h.StickyEOF(); err != nil {
			return 0, err
		}
		b, epoch, ok, err := r.nextByte()
		if err != nil {
			return 0, err
		}
		if !ok {
			// The LF of a CRLF pair belongs to the CR before it: it is dropped, so
			// the read goes on with the byte after the pair (see crHanded).
			continue
		}
		if !r.activitySent {
			r.activitySent = true
			if err := h.OnFirstByte(); err != nil {
				return 0, err
			}
		}
		if !records && b == escByte && h.AltEnterWanted() && h.WaitInput(EscWaitTimeout) {
			// A byte follows the ESC within the Esc window: Alt+Enter (ESC CR, or ESC LF
			// where the terminal turns Alt+Enter into that) asks for a newline inside
			// the input, not a submit. The ESC is dropped and the newline is declared as
			// text; anything else is an escape sequence, and the byte read ahead waits
			// for the next Read. Ctrl+J cannot be used for this: on Unix a pre-typed
			// line ends in a bare LF too (ICRNL), so it is ambiguous.
			next, nepoch, ok, err := r.nextByte()
			if err != nil {
				return 0, err
			}
			if ok && (next == crByte || next == lfByte) {
				r.declareNewlineAsText()
				if !h.HandOver(nepoch) {
					continue
				}
				p[0] = next
				return 1, nil
			}
			if ok {
				r.held = &heldByte{b: next, epoch: nepoch}
			}
		}
		if !records && b == lfByte && ctrlJIsInputNewline {
			// A bare LF is Ctrl+J on Windows (a console hands a cooked line over as
			// CRLF, whose LF the pump drops): a newline inside the input. On Unix ICRNL
			// turns Enter itself into a bare LF, so there it has to submit
			// (ctrlj_windows.go / ctrlj_other.go).
			r.declareNewlineAsText()
		} else if !records && (b == crByte || b == lfByte) {
			// The newline may be text of a paste instead of a submit, which a
			// terminal without bracketed paste gives no other sign of. Decide it
			// before handing the byte over, so the editor inserts the newline
			// instead of ending the read on it (lineedit.SetNewlineAsText).
			if r.newlineIsPaste(b) {
				r.declareNewlineAsText()
			}
		}
		if !records && b == escByte && h.EscKeyWanted() && !h.WaitInput(EscWaitTimeout) {
			// Nothing followed the 0x1b within EscWaitTimeout: it is the Esc key, not
			// the start of a sequence. Declare it before handing the byte over, so the
			// editor reports the key instead of waiting for the rest of a sequence.
			if !h.HandOver(epoch) {
				continue
			}
			r.declareEscLone()
			p[0] = b
			return 1, nil
		}
		// A byte held here (e.g. during the activity handshake, or the lone-Esc wait)
		// while a question flushed the input was typed before the question: drop it.
		if !h.HandOver(epoch) {
			continue
		}
		p[0] = b
		return 1, nil
	}
}

// nextByte returns the next byte to hand to the editor together with the epoch it
// was read in (the input may have been flushed since, and such a byte must not
// reach the editor). ok is false for a byte that belongs to the byte before it —
// the LF of a CRLF pair — the caller reading on instead of handing it over.
func (r *Pump) nextByte() (b byte, epoch uint64, ok bool, err error) {
	if h := r.held; h != nil {
		r.held = nil
		return h.b, h.epoch, true, nil
	}
	var one [1]byte
	n, err := r.input.Read(one[:])
	if n == 0 {
		r.hooks.OnEOF(err)
		return 0, 0, false, err
	}
	pairLF := one[0] == lfByte && r.crHanded
	r.crHanded = one[0] == crByte
	if pairLF {
		return 0, 0, false, nil
	}
	return one[0], r.hooks.Epoch(), true, nil
}

// newlineIsPaste reports whether the newline byte b is text of a paste rather
// than a submit: a paste goes on with the next byte within PasteNewlineTimeout,
// while a person's Enter is followed by nothing (see PasteNewlineTimeout).
//
// A CR followed by the LF of a CRLF pair is the one case that needs a second
// look: such a pair is a typed Enter on a Windows console, so it is a submit
// unless something follows the pair as well (then the pair is a line break of a
// pasted CRLF text). The byte looked at is held for the next Read.
//
// The decision reads the byte the wait promised, so a platform wait that reports a
// byte which is not there blocks this read — the same hazard the lone-Esc wait has
// (inputwait_*.go).
func (r *Pump) newlineIsPaste(b byte) bool {
	if !r.hooks.PasteNewlineWanted() || !r.hooks.WaitInput(PasteNewlineTimeout) {
		return false
	}
	if b != crByte {
		return true
	}
	next, epoch, ok, err := r.nextByte()
	if err != nil {
		// Nothing left to read: the input ended right after the newline, which is
		// what a submit looks like (the error is reported by the next Read).
		return false
	}
	if ok {
		r.held = &heldByte{b: next, epoch: epoch}
		return true
	}
	// The pair's LF stays dropped; what follows it decides.
	return r.hooks.WaitInput(PasteNewlineTimeout)
}
