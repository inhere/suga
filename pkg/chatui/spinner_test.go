package chatui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// syncBuf is a goroutine-safe output buffer: the frame goroutine writes while the
// test reads.
type syncBuf struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

// fakeClock drives the spinner without sleeping: Now is the fake time, ticks hands
// out the channel the test sends frames on.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	frames chan time.Time
	speed  time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), frames: make(chan time.Time, 16)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) ticks(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.speed = d
	return c.frames, func() {}
}

func (c *fakeClock) tick(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	c.frames <- now
}

func (c *fakeClock) interval() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.speed
}

func waitBuf(b *syncBuf, want string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), want) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

type statusLog struct {
	mu   sync.Mutex
	rows []string
}

func (l *statusLog) SetStatus(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = append(l.rows, s)
}

func (l *statusLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.rows...)
}

func (l *statusLog) last() string {
	rows := l.all()
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1]
}

type upperTr struct{}

func (upperTr) T(key string, _ ...map[string]any) string { return "T:" + key }

func TestSpinnerOwnLine(t *testing.T) {
	t.Run("frame, label and elapsed seconds with the nil-translator fallback", func(t *testing.T) {
		out, clock := &syncBuf{}, newFakeClock()
		s := NewSpinner(SpinnerConfig{Enabled: true, Out: out, Now: clock.Now, Ticks: clock.ticks})
		assert.False(t, s.Running())
		s.Start()
		assert.True(t, s.Running())
		clock.tick(3 * time.Second)
		assert.Require(t, waitBuf(out, "3s"))
		s.Stop()
		assert.False(t, s.Running())
		assert.Eq(t, SpinnerSpeed, clock.interval())
		assert.Eq(t, SpinnerClear+string(SpinnerChars[0])+" 思考中… 3s"+SpinnerClear, out.String())
		assert.False(t, s.StatusRow())
	})

	t.Run("disabled draws nothing and starts nothing", func(t *testing.T) {
		out, clock := &syncBuf{}, newFakeClock()
		s := NewSpinner(SpinnerConfig{Out: out, Now: clock.Now, Ticks: clock.ticks})
		s.Start()
		assert.False(t, s.Running())
		s.Stop()
		assert.Eq(t, "", out.String())
	})

	t.Run("stop without a drawn frame writes nothing", func(t *testing.T) {
		out, clock := &syncBuf{}, newFakeClock()
		s := NewSpinner(SpinnerConfig{Enabled: true, Out: out, Now: clock.Now, Ticks: clock.ticks})
		s.Start()
		s.Stop()
		assert.False(t, s.Running())
		assert.Eq(t, "", out.String())
	})

	t.Run("paint styles the frame and the label switch restarts the seconds", func(t *testing.T) {
		out, clock := &syncBuf{}, newFakeClock()
		s := NewSpinner(SpinnerConfig{
			Enabled: true, Out: out, Now: clock.Now, Ticks: clock.ticks,
			Paint: func(l string) string { return "<" + l + ">" },
		})
		s.Start()
		clock.tick(5 * time.Second)
		assert.Require(t, waitBuf(out, "思考中… 5s>"))
		s.SetLabel(ToolLabel(nil, "shell_exec"))
		assert.Require(t, waitBuf(out, "执行 shell_exec… 0s>"))
		s.Stop()
	})
}

func TestSpinnerStatusRow(t *testing.T) {
	t.Run("frames go to the status row and Stop clears it", func(t *testing.T) {
		out, clock, log := &syncBuf{}, newFakeClock(), &statusLog{}
		s := NewSpinner(SpinnerConfig{Enabled: true, Out: out, Status: log, Now: clock.Now, Ticks: clock.ticks})
		assert.True(t, s.StatusRow())
		s.Start()
		clock.tick(time.Second)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && log.last() == "" {
			time.Sleep(time.Millisecond)
		}
		assert.Eq(t, string(SpinnerChars[0])+" 思考中… 1s", log.last())
		s.Stop()
		assert.Eq(t, "", log.last())
		assert.Eq(t, "", out.String())
	})

	t.Run("Suspend hides the row and Resume shows it again", func(t *testing.T) {
		out, clock, log := &syncBuf{}, newFakeClock(), &statusLog{}
		s := NewSpinner(SpinnerConfig{Enabled: true, Out: out, Status: log, Now: clock.Now, Ticks: clock.ticks})
		s.Start()
		clock.tick(time.Second)
		for i := 0; i < 2000 && log.last() == ""; i++ {
			time.Sleep(time.Millisecond)
		}
		s.Suspend()
		assert.Eq(t, "", log.last())
		n := len(log.all())
		clock.tick(time.Second)
		time.Sleep(20 * time.Millisecond)
		assert.Eq(t, n, len(log.all()))
		s.Resume()
		assert.StrContains(t, log.last(), "思考中… 2s")
		s.Stop()
	})
}

func TestSpinnerTranslator(t *testing.T) {
	t.Run("an injected translator renders the labels", func(t *testing.T) {
		out, clock := &syncBuf{}, newFakeClock()
		s := NewSpinner(SpinnerConfig{Enabled: true, Out: out, Translator: upperTr{}, Now: clock.Now, Ticks: clock.ticks})
		s.Start()
		clock.tick(time.Second)
		assert.Require(t, waitBuf(out, "T:status.spinner.thinking 1s"))
		s.Stop()
		assert.Eq(t, "T:status.spinner.tool", ToolLabel(upperTr{}, "x"))
	})

	t.Run("ToolLabel falls back to Chinese without a translator", func(t *testing.T) {
		assert.Eq(t, "执行 shell_exec…", ToolLabel(nil, "shell_exec"))
	})
}
