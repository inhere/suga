package pump

import (
	"errors"
	"io"
	"sync"
	"time"
)

// DefaultHooks is a ready-made Hooks for a caller with no steering of its own (no
// separate read modes): Alt+Enter and the pasted-newline decision are on wherever
// the platform wait can be trusted, a lone Esc is a key only while EscActive says
// so, and the epoch is advanced by the caller.
//
// Use it as a pointer and fill the fields before the first Read. Wait is the
// byte-ready wait (WaitTerminalInput on the terminal descriptor); without one the
// pump leaves every Esc to the editor and every newline a submit. Every method is
// safe for concurrent use.
type DefaultHooks struct {
	// Wait reports whether more input is ready within the timeout, without reading
	// it (see Hooks.WaitInput). nil: no wait, so none of the byte-level decisions.
	Wait func(timeout time.Duration) bool
	// EscActive is evaluated before every read: true while a lone Esc is a key
	// (e.g. only while a turn is running). nil: never.
	EscActive func() bool
	// EscPressed runs for a lone Esc the pump declared. nil: ignored.
	EscPressed func()
	// Win32 reports whether the read gets win32-input-mode records. nil: no.
	Win32 func() bool
	// FirstByte runs when the first byte arrives (see Hooks.OnFirstByte). nil: none.
	FirstByte func() error

	mu    sync.Mutex
	epoch uint64
	eof   error
}

// AdvanceEpoch starts a new input epoch and returns it. Call it right after the
// input was flushed (FlushTerminalInput): every byte read before is dropped by
// HandOver instead of reaching the editor.
func (h *DefaultHooks) AdvanceEpoch() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.epoch++
	return h.epoch
}

func (h *DefaultHooks) waitTrusted() bool { return h.Wait != nil && WaitInputSupported }

// AltEnterWanted is on wherever the wait can be trusted.
func (h *DefaultHooks) AltEnterWanted() bool { return h.waitTrusted() }

// PasteNewlineWanted is on wherever the wait can be trusted.
func (h *DefaultHooks) PasteNewlineWanted() bool { return h.waitTrusted() }

// EscKeyWanted follows EscActive.
func (h *DefaultHooks) EscKeyWanted() bool {
	return h.Wait != nil && h.EscActive != nil && h.EscActive()
}

// Win32Records follows Win32.
func (h *DefaultHooks) Win32Records() bool { return h.Win32 != nil && h.Win32() }

// WaitInput uses Wait; without one it reports a byte as ready.
func (h *DefaultHooks) WaitInput(timeout time.Duration) bool {
	if h.Wait == nil {
		return true
	}
	return h.Wait(timeout)
}

// Epoch is the current epoch.
func (h *DefaultHooks) Epoch() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.epoch
}

// HandOver accepts a byte only from the current epoch.
func (h *DefaultHooks) HandOver(epoch uint64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.epoch == epoch
}

// StickyEOF is the io.EOF a read ended with (nil before).
func (h *DefaultHooks) StickyEOF() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.eof
}

// OnFirstByte runs FirstByte.
func (h *DefaultHooks) OnFirstByte() error {
	if h.FirstByte == nil {
		return nil
	}
	return h.FirstByte()
}

// OnConsumed does nothing.
func (h *DefaultHooks) OnConsumed() {}

// OnEOF remembers an input that ended with io.EOF.
func (h *DefaultHooks) OnEOF(err error) {
	if !errors.Is(err, io.EOF) {
		return
	}
	h.mu.Lock()
	if h.eof == nil {
		h.eof = err
	}
	h.mu.Unlock()
}

// OnEscPressed runs EscPressed.
func (h *DefaultHooks) OnEscPressed() {
	if h.EscPressed != nil {
		h.EscPressed()
	}
}

var _ Hooks = (*DefaultHooks)(nil)
