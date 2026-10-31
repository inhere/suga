package pump

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/chatui/lineedit"
)

// bufInput is the fake keyboard: Read hands over one queued byte at a time and
// answers io.EOF once the queue is empty; wait answers "is a byte ready" from the
// same queue, like the platform wait does from the terminal.
type bufInput struct {
	mu  sync.Mutex
	buf []byte
}

func (b *bufInput) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.buf) == 0 {
		return 0, io.EOF
	}
	p[0] = b.buf[0]
	b.buf = b.buf[1:]
	return 1, nil
}

func (b *bufInput) wait(time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf) > 0
}

func (b *bufInput) push(s string) {
	b.mu.Lock()
	b.buf = append(b.buf, s...)
	b.mu.Unlock()
}

// modeHooks is a Hooks whose answers change between reads, as a caller with
// several read modes has them.
type modeHooks struct {
	DefaultHooks
	altEnter, escKey, pasteNewline bool
	handOverCalls                  int
	ignoreEpoch                    bool
	consumed                       int
	firstBytes                     int
}

func (h *modeHooks) AltEnterWanted() bool     { return h.altEnter }
func (h *modeHooks) EscKeyWanted() bool       { return h.escKey }
func (h *modeHooks) PasteNewlineWanted() bool { return h.pasteNewline }
func (h *modeHooks) OnConsumed()              { h.consumed++ }
func (h *modeHooks) OnFirstByte() error       { h.firstBytes++; return nil }
func (h *modeHooks) HandOver(epoch uint64) bool {
	h.handOverCalls++
	return h.ignoreEpoch || h.DefaultHooks.HandOver(epoch)
}

type rw struct {
	io.Reader
	io.Writer
}

func newEditor(in io.Reader, h Hooks) *lineedit.Terminal {
	p := NewPump(in, h)
	t := lineedit.NewTerminal(rw{p, io.Discard}, "")
	p.Bind(t)
	return t
}

func TestPump_ModeDecidedPerRead(t *testing.T) {
	t.Run("Alt+Enter is a newline while AltEnterWanted, an escape sequence after it flips", func(t *testing.T) {
		in := &bufInput{}
		h := &modeHooks{altEnter: true}
		h.Wait = in.wait
		term := newEditor(in, h)

		in.push("a\x1b\rb\r")
		line, err := term.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "a\nb", line)

		// The same pump, the next read in another mode: the answer is evaluated
		// again, so ESC CR is not Alt+Enter any more.
		h.altEnter = false
		in.push("c\x1b\rd\r")
		line, err = term.ReadLine()
		assert.NoErr(t, err)
		assert.NotEq(t, "c\nd", line)
	})

	t.Run("a lone Esc is a key only while EscKeyWanted", func(t *testing.T) {
		in := &bufInput{}
		h := &modeHooks{escKey: true}
		h.Wait = in.wait
		pressed := 0
		h.EscPressed = func() { pressed++ }
		term := newEditor(in, h)

		in.push("\x1b")
		_, _ = term.ReadLine() // ends on the EOF after the Esc key
		assert.Eq(t, 1, pressed)

		h.escKey = false
		in.push("\x1b")
		_, _ = term.ReadLine()
		assert.Eq(t, 1, pressed)
	})

	t.Run("EscCallback is installed when the terminal has none", func(t *testing.T) {
		in := &bufInput{}
		h := &modeHooks{}
		term := newEditor(in, h)
		assert.True(t, term.EscCallback != nil)
	})
}

func TestPump_PastedNewlines(t *testing.T) {
	in := &bufInput{}
	h := &modeHooks{pasteNewline: true}
	h.Wait = in.wait
	term := newEditor(in, h)

	t.Run("a newline followed by a byte is text", func(t *testing.T) {
		in.push("a\nb\r")
		line, err := term.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "a\nb", line)
	})

	t.Run("a CRLF pair is one newline and the LF is dropped", func(t *testing.T) {
		in.push("a\r\nb\r")
		line, err := term.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "a\nb", line)
	})

	t.Run("without PasteNewlineWanted a newline submits", func(t *testing.T) {
		h.pasteNewline = false
		in.push("a\rb\r")
		line, err := term.ReadLine()
		assert.NoErr(t, err)
		assert.Eq(t, "a", line)
	})
}

func TestPump_EpochDropsStaleBytes(t *testing.T) {
	t.Run("a held byte keeps the epoch it was read in", func(t *testing.T) {
		in := &bufInput{}
		in.push("\x1bx")
		h := &modeHooks{altEnter: true}
		h.Wait = in.wait
		p := NewPump(in, h)

		// ESC then 'x': the pump reads 'x' ahead (not an Enter) and holds it.
		buf := make([]byte, 1)
		n, err := p.Read(buf)
		assert.NoErr(t, err)
		assert.Eq(t, 1, n)
		assert.Eq(t, byte(escByte), buf[0])
		assert.True(t, p.held != nil)
		assert.Eq(t, h.Epoch(), p.held.epoch)

		// The input is flushed while the byte is held: it must not be handed over.
		h.AdvanceEpoch()
		n, err = p.Read(buf)
		assert.True(t, errors.Is(err, io.EOF))
		assert.Eq(t, 0, n)
	})

	t.Run("HandOver false drops the byte", func(t *testing.T) {
		in := &bufInput{}
		in.push("ab")
		h := &modeHooks{}
		p := NewPump(in, h)
		buf := make([]byte, 1)
		n, _ := p.Read(buf)
		assert.Eq(t, 1, n)
		assert.Eq(t, byte('a'), buf[0])

		h.AdvanceEpoch() // 'b' is read with the new epoch, so it passes
		n, _ = p.Read(buf)
		assert.Eq(t, byte('b'), buf[0])
		assert.Eq(t, 1, n)
	})
}

func TestPump_Lifecycle(t *testing.T) {
	t.Run("consumed on every Read, first byte once per arm", func(t *testing.T) {
		in := &bufInput{}
		in.push("abc")
		h := &modeHooks{}
		p := NewPump(in, h)
		buf := make([]byte, 1)
		for range 3 {
			_, err := p.Read(buf)
			assert.NoErr(t, err)
		}
		assert.Eq(t, 3, h.consumed)
		assert.Eq(t, 1, h.firstBytes)

		in.push("d")
		p.ResetFirstByte()
		_, err := p.Read(buf)
		assert.NoErr(t, err)
		assert.Eq(t, 2, h.firstBytes)
	})

	t.Run("an error from OnFirstByte ends the Read", func(t *testing.T) {
		in := &bufInput{}
		in.push("a")
		boom := errors.New("boom")
		h := &DefaultHooks{FirstByte: func() error { return boom }}
		_, err := New(in, nil, h).Read(make([]byte, 1))
		assert.True(t, errors.Is(err, boom))
	})

	t.Run("EOF is sticky", func(t *testing.T) {
		in := &bufInput{}
		h := &DefaultHooks{}
		p := New(in, nil, h)
		_, err := p.Read(make([]byte, 1))
		assert.True(t, errors.Is(err, io.EOF))
		assert.True(t, errors.Is(h.StickyEOF(), io.EOF))

		// Later input is not read: the sticky EOF answers first.
		in.push("x")
		_, err = p.Read(make([]byte, 1))
		assert.True(t, errors.Is(err, io.EOF))
		assert.Eq(t, 1, len(in.buf))
	})

	t.Run("no terminal: nothing is declared and bytes pass", func(t *testing.T) {
		in := &bufInput{buf: []byte("a\n")}
		got, err := io.ReadAll(New(in, nil, &DefaultHooks{}))
		assert.NoErr(t, err)
		assert.True(t, bytes.Equal([]byte("a\n"), got))
	})
}

func TestDefaultHooks(t *testing.T) {
	t.Run("without a wait no byte-level decision is taken", func(t *testing.T) {
		h := &DefaultHooks{EscActive: func() bool { return true }}
		assert.False(t, h.AltEnterWanted())
		assert.False(t, h.PasteNewlineWanted())
		assert.False(t, h.EscKeyWanted())
		assert.True(t, h.WaitInput(time.Millisecond))
	})

	t.Run("EscKey follows the caller's judgement on every call", func(t *testing.T) {
		active := false
		h := &DefaultHooks{Wait: func(time.Duration) bool { return false }, EscActive: func() bool { return active }}
		assert.False(t, h.EscKeyWanted())
		active = true
		assert.True(t, h.EscKeyWanted())
		assert.Eq(t, WaitInputSupported, h.AltEnterWanted())
	})

	t.Run("the epoch is advanced by the caller", func(t *testing.T) {
		h := &DefaultHooks{}
		old := h.Epoch()
		assert.True(t, h.HandOver(old))
		next := h.AdvanceEpoch()
		assert.Eq(t, next, h.Epoch())
		assert.False(t, h.HandOver(old))
		assert.True(t, h.HandOver(next))
	})
}

func TestWatchSize_NothingToWatch(t *testing.T) {
	stop := WatchSize(nil, -1, nil)
	stop()
	stop() // stop is safe to call again
}
