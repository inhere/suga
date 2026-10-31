package chatui

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gookit/cliui/progress"
)

const (
	// SpinnerSpeed is the interval between two frames of the waiting line.
	// It is cliui's default spinner speed: fast enough to look alive, slow
	// enough not to flood the terminal.
	SpinnerSpeed = 100 * time.Millisecond
	// SpinnerClear moves the cursor to the start of the line and erases it:
	// the overwrite sequence cliui's progress widgets use for a dynamic line.
	// Every frame starts with it (the elapsed seconds change anyway) and Stop
	// writes it once more, so the output that follows starts at column 0 with
	// no trace of the hint left on screen.
	SpinnerClear = "\r\x1b[2K"
)

// ToolLabel is what the status line says while a tool of the turn runs: the tool's
// name in between ("执行 shell_exec…" / "running shell_exec…"), the elapsed
// seconds follow it.
func ToolLabel(t Translator, name string) string {
	return spinnerText(t).T("status.spinner.tool", map[string]any{"tool": name})
}

// StatusSetter is where the waiting line goes while chat keeps an input line on
// screen (chat.steering): the row above it (lineedit.Terminal.SetStatus), which the
// line editor clears and redraws around every write of its own - a line of its own
// would fight that input line for the row it is on (r3 M1). nil without a status
// line.
type StatusSetter interface{ SetStatus(s string) }

// SpinnerChars is cliui's dots loading theme (progress.CharsThemes[20]): the
// animation of the waiting line.
var SpinnerChars = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Spinner shows one self-updating line while chat waits for the model
// ("⠋ 思考中… 3s", dimmed on a colored terminal), or what the turn does right now
// while a tool runs ("⠋ 执行 shell_exec… 3s"). The model is asked for its next
// message at every turn start and after every tool result; the line ends with the
// model's first output or the first line chat prints itself.
//
// Where the line goes depends on the input line: a terminal chat keeps one on
// screen for the whole turn (chat.steering), so the frames go to the row above it
// (status); without one they overwrite a row of their own (SpinnerClear).
//
// It does not use cliui's progress.SpinnerFactory (Spinner/LoadingSpinner): that
// widget renders from a goroutine it owns with an internal time.Sleep, so a test
// cannot drive its clock, Stop returns without waiting for that goroutine (a
// frame could land between the erase and the renderer's next line) and nothing
// reports that the goroutine is gone. This type keeps cliui's animation
// (progress.LoadingWidget) and its overwrite convention but owns the frame loop.
type Spinner struct {
	enabled bool
	out     io.Writer
	// status is the status line above the input line while chat keeps one on
	// screen (chat.steering); nil writes the line itself (out).
	status StatusSetter
	// paint styles a frame (a colored terminal); nil keeps it plain.
	paint func(string) string
	// speed is the frame interval; now and ticks are injectable, so a test can
	// drive the line without sleeping (nil: the real clock and a time.Ticker).
	speed time.Duration
	now   func() time.Time
	ticks func(time.Duration) (<-chan time.Time, func())
	// frame draws the animation character of the next frame: cliui's loading
	// widget, which ignores its progress argument.
	frame progress.WidgetFunc
	// label is what the line says right now (SetLabel).
	label string

	mu      sync.Mutex // guards the state below, the frames and Stop's erase
	active  bool       // a frame goroutine is running
	drew    bool       // a frame was written: Stop must erase the line
	started time.Time  // the line appeared at; the elapsed seconds count from it
	// suspended is set by Suspend while something else owns the input line (an
	// approval question asks on it): no frame is drawn until Resume.
	suspended bool
	stopCh    chan struct{}
	done      chan struct{}
	cancel    func() // releases the ticker of the running frame goroutine
}

// SpinnerConfig configures a Spinner.
type SpinnerConfig struct {
	Enabled bool
	Out     io.Writer
	Status  StatusSetter
	Paint   func(string) string
	Speed   time.Duration
	Now     func() time.Time
	Ticks   func(time.Duration) (<-chan time.Time, func())
	// Translator renders the line's text; nil takes the built-in Chinese fallback
	// (spinnerText), like every other unwired chat text.
	Translator Translator
}

// NewSpinner builds a spinner; its first label is the catalog's thinking
// line (status.spinner.thinking).
func NewSpinner(cfg SpinnerConfig) *Spinner {
	label := spinnerText(cfg.Translator).T("status.spinner.thinking")
	s := &Spinner{
		enabled: cfg.Enabled, out: cfg.Out, status: cfg.Status, paint: cfg.Paint, speed: cfg.Speed,
		now: cfg.Now, ticks: cfg.Ticks, frame: progress.LoadingWidget(SpinnerChars),
		label: label,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.ticks == nil {
		s.ticks = realTicks
	}
	if s.speed <= 0 {
		s.speed = SpinnerSpeed
	}
	return s
}

// realTicks is the default frame cadence: a real ticker, released by the
// returned func.
func realTicks(d time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(d)
	return ticker.C, ticker.Stop
}

// StatusRow reports whether the line goes to the row above the input line
// (chat.steering) instead of onto a row of its own (chat.spinner without an input
// line): what the renderer's writes have to do about the line depends on it.
func (s *Spinner) StatusRow() bool { return s.status != nil }

// SetLabel switches what the line says: the model is thinking
// (status.spinner.thinking) or a tool runs right now (ToolLabel). The elapsed
// seconds count from there on (the line reports how long the current state
// lasts), and a line that is already up changes in place. A no-op without a
// status line (the line of its own only ever says 思考中…) and while the line is
// not running.
func (s *Spinner) SetLabel(label string) {
	if !s.enabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.label == label {
		return
	}
	s.label = label
	s.started = s.now()
	if s.active {
		s.drawLocked()
	}
}

// Suspend takes the status line off screen while another part of chat owns the
// input line (an approval question asks on it): the frames of the running turn
// stop drawing until Resume, which shows the line again with what the turn waits
// for. Both are no-ops without a status line.
func (s *Spinner) Suspend() {
	if !s.enabled || s.status == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return
	}
	s.suspended = true
	s.status.SetStatus("")
}

// Resume is the counterpart of Suspend.
func (s *Spinner) Resume() {
	if !s.enabled || s.status == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.suspended {
		return
	}
	s.suspended = false
	if s.active {
		s.drawLocked()
	}
}

// Start shows the waiting line. It does nothing while the spinner is off (not a
// terminal, NO_COLOR or chat.spinner: false) or the line is already shown, so
// it may be called again after every tool result.
func (s *Spinner) Start() {
	if !s.enabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return
	}
	s.active, s.drew, s.started = true, false, s.now()
	s.suspended = false
	s.stopCh, s.done = make(chan struct{}), make(chan struct{})
	tick, cancel := s.ticks(s.speed)
	s.cancel = cancel
	go s.run(tick, s.stopCh, s.done)
}

// Stop erases the waiting line and waits for the frame goroutine to be gone, so
// its caller can print immediately afterwards knowing that nothing will overwrite
// it and no goroutine is left behind. It is safe to call when nothing is shown.
func (s *Spinner) Stop() {
	if !s.enabled {
		return
	}
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	stopCh, done, cancel, drew := s.stopCh, s.done, s.cancel, s.drew
	s.stopCh, s.done, s.cancel, s.drew = nil, nil, nil, false
	s.mu.Unlock()

	close(stopCh)
	if cancel != nil {
		cancel()
	}
	<-done // no frame can follow this erase

	if drew {
		// Holding mu keeps a frame of an immediately following Start from
		// slipping in between.
		s.mu.Lock()
		if s.status != nil && drew {
			// The row above the input line goes away with the turn that owned it.
			s.status.SetStatus("")
		} else {
			fmt.Fprint(s.out, SpinnerClear)
		}
		s.mu.Unlock()
	}
}

// run draws one frame per tick until Stop closes stopCh.
func (s *Spinner) run(tick <-chan time.Time, stopCh, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-stopCh:
			return
		case <-tick:
			s.draw()
		}
	}
}

// draw rewrites the line. mu is held for the whole write, so a frame never
// slips between Stop's erase and the renderer's next output.
func (s *Spinner) draw() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drawLocked()
}

// drawLocked writes one frame of the line where it belongs. t.mu must be held.
func (s *Spinner) drawLocked() {
	if !s.active || s.suspended {
		// Stopped or suspended while this frame was in flight (its tick was
		// already delivered): the text must not appear after the erase.
		return
	}
	if s.status != nil {
		// The line above the input line: the line editor keeps that row clear of
		// every write of its own, so the frame does not have to erase anything.
		s.status.SetStatus(s.line())
	} else {
		fmt.Fprint(s.out, SpinnerClear+s.line())
	}
	s.drew = true
}

// line is the text of one frame: the animation character, what the turn does right
// now (label) and the seconds it has been doing it.
func (s *Spinner) line() string {
	elapsed := int(s.now().Sub(s.started).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	text := fmt.Sprintf("%s %s %ds", s.frame(nil), s.label, elapsed)
	if s.paint != nil {
		text = s.paint(text)
	}
	return text
}

// Running reports whether the frame goroutine is alive: a stopped spinner has
// closed its done channel, one that never started has none.
func (s *Spinner) Running() bool {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// spinnerFallback is the Chinese text of the waiting line for a host that
// injects no Translator (the catalog's status.spinner.* entries).
type spinnerFallback struct{}

func (spinnerFallback) T(key string, args ...map[string]any) string {
	var a map[string]any
	if len(args) > 0 {
		a = args[0]
	}
	switch key {
	case "status.spinner.thinking":
		return "思考中…"
	case "status.spinner.tool":
		return fmt.Sprintf("执行 %v…", a["tool"])
	}
	return key
}

// spinnerText returns t, or the Chinese fallback when no translator was injected.
func spinnerText(t Translator) Translator {
	if t == nil {
		return spinnerFallback{}
	}
	return t
}
