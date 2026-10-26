package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/schedule"
)

// idleFakeClock is a manual schedule.Clock: Advance runs due timers in the
// calling goroutine (like the scheduler's own test clock).
type idleFakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*idleFakeTimer
}

type idleFakeTimer struct {
	clock   *idleFakeClock
	at      time.Time
	fn      func()
	stopped bool
}

func (c *idleFakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *idleFakeClock) AfterFunc(d time.Duration, fn func()) schedule.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &idleFakeTimer{clock: c, at: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, t)
	return t
}
func (t *idleFakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

func (c *idleFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *idleFakeTimer
		for _, t := range c.timers {
			if !t.stopped && !t.at.After(target) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		next.stopped = true
		c.now = next.at
		c.mu.Unlock()
		next.fn()
	}
}

// idleTestProvider answers turns with a fixed reported input size; a turn can
// be held (holdTurn) and the first summary call can block until cancelled.
type idleTestProvider struct {
	input      int
	holdTurn   bool          // the first turn waits for release
	entered    chan struct{} // closed when the held turn started
	release    chan struct{}
	blockSum   bool          // the first summary call blocks until its ctx ends
	sumEntered chan struct{} // closed when that summary call started

	mu          sync.Mutex
	turns, sums int
}

func newIdleTestProvider(input int) *idleTestProvider {
	return &idleTestProvider{input: input, entered: make(chan struct{}), release: make(chan struct{}),
		sumEntered: make(chan struct{})}
}

func (*idleTestProvider) Name() string { return "idle-test" }
func (p *idleTestProvider) Generate(ctx context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.mu.Lock()
	if strings.HasPrefix(req.System, "你是会话交接助手") {
		p.sums++
		first := p.sums == 1
		p.mu.Unlock()
		if first && p.blockSum {
			close(p.sumEntered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &agentcore.GenerateResult{Content: "PKG-BODY", FinishReason: agentcore.FinishFinal}, nil
	}
	p.turns++
	n := p.turns
	p.mu.Unlock()
	if n == 1 && p.holdTurn {
		close(p.entered)
		<-p.release
	}
	return &agentcore.GenerateResult{Content: fmt.Sprintf("answer-%d", n), FinishReason: agentcore.FinishFinal,
		Usage: agentcore.Usage{InputTokens: p.input, OutputTokens: 1}}, nil
}

const idleTestAfter = time.Minute

type idleHarness struct {
	t     *testing.T
	seen  idleSeen
	clock *idleFakeClock
	repo  *store.Store
	in    *io.PipeWriter
	out   *syncOutput
	done  chan error
	sid   string
}

// startIdleChat runs chat on a pipe with the idle consolidation on a fake
// clock (idle_after = idleTestAfter, default threshold 112000 tokens). tune may
// adjust the chat options (the English catalog).
func startIdleChat(t *testing.T, prov *idleTestProvider, minRatio float64, tune ...func(*Options)) *idleHarness {
	t.Helper()
	runner, repo := newCLITestRunner(t, prov)
	h := &idleHarness{t: t, clock: &idleFakeClock{now: time.Unix(100, 0)}, repo: repo, out: &syncOutput{},
		done: make(chan error, 1), sid: "sess-idle"}
	reader, writer := io.Pipe()
	h.in = writer
	opts := Options{SessionID: h.sid, IdleAfter: idleTestAfter, IdleMinRatio: minRatio,
		scheduleOptions: []schedule.Option{schedule.WithClock(h.clock)}, scheduleObserver: h.seen.add}
	for _, apply := range tune {
		apply(&opts)
	}
	go func() { h.done <- Run(context.Background(), runner, opts, reader, h.out, io.Discard, nil) }()
	t.Cleanup(func() {
		_ = writer.Close()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			t.Error("chat did not exit")
		}
	})
	return h
}

// idleSeen counts scheduler events as they are emitted (synchronously, unlike
// the events table written in the background).
type idleSeen struct {
	mu     sync.Mutex
	counts map[schedule.EventKind]int
}

func (s *idleSeen) add(ev schedule.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counts == nil {
		s.counts = map[schedule.EventKind]int{}
	}
	s.counts[ev.Kind]++
}

func (s *idleSeen) count(kind schedule.EventKind) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[kind]
}

func (h *idleHarness) send(line string) {
	h.t.Helper()
	_, err := io.WriteString(h.in, line+"\n")
	assert.Require(h.t, assert.NoErr(h.t, err))
}

func (h *idleHarness) quit() {
	h.t.Helper()
	h.send(":quit")
	select {
	case err := <-h.done:
		assert.NoErr(h.t, err)
		h.done <- nil // for the cleanup
	case <-time.After(10 * time.Second):
		h.t.Fatal("chat did not quit")
	}
}

// scheduleEvents lists this session's scheduler events of one kind.
func (h *idleHarness) scheduleEvents(kind schedule.EventKind) []store.Event {
	evs, err := h.repo.ListEvents(context.Background(), store.EventFilter{SessionID: h.sid, Kinds: []string{store.EventSchedule}})
	assert.Require(h.t, assert.NoErr(h.t, err))
	var out []store.Event
	for _, e := range evs {
		if e.Attrs["event"] == string(kind) {
			out = append(out, e)
		}
	}
	return out
}

// waitEvents waits until n events of kind were emitted and written.
func (h *idleHarness) waitEvents(kind schedule.EventKind, n int) {
	h.t.Helper()
	waitUntil(h.t, fmt.Sprintf("%d %s event(s)", n, kind), func() bool {
		return h.seen.count(kind) >= n && len(h.scheduleEvents(kind)) >= n
	})
}

func (h *idleHarness) waitOutput(text string) {
	h.t.Helper()
	waitUntil(h.t, "output "+text, func() bool { return strings.Contains(h.out.String(), text) })
}

// segmentReasons lists the reasons of the session's segment records.
func (h *idleHarness) segmentReasons() []string {
	entries, err := h.repo.ListEntries(context.Background(), h.sid, store.EntryFilter{Types: []string{store.EntryCompaction}})
	assert.Require(h.t, assert.NoErr(h.t, err))
	var out []string
	for _, e := range entries {
		var rec struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(e.JSON, &rec)
		if rec.Reason != "start" {
			out = append(out, rec.Reason)
		}
	}
	return out
}

func TestChatIdleConsolidation(t *testing.T) {
	t.Run("not during a turn, after the turn ends", func(t *testing.T) {
		prov := newIdleTestProvider(100000) // ~0.89 of the threshold
		prov.holdTurn = true
		h := startIdleChat(t, prov, 0.5)
		h.send("hello")
		<-prov.entered
		// The turn runs far longer than idle_after: paused, nothing fires.
		h.clock.Advance(3 * idleTestAfter)
		assert.Eq(t, 0, h.seen.count(schedule.Fired))
		close(prov.release)
		h.waitEvents(schedule.Resumed, 1)
		// The quiet time counts from the turn end.
		h.clock.Advance(idleTestAfter - time.Second)
		assert.Eq(t, 0, h.seen.count(schedule.Fired))
		h.clock.Advance(time.Second)
		h.waitOutput("✦ 已整理上下文：")
		assert.StrContains(t, h.out.String(), "✦ 空闲整理中…")
		h.waitEvents(schedule.Done, 1)
		assert.Eq(t, []string{"idle"}, h.segmentReasons())
		// Input still works afterwards.
		h.send("again")
		h.waitOutput("answer-2")
		h.quit()
	})

	t.Run("context below the ratio is left alone", func(t *testing.T) {
		prov := newIdleTestProvider(1000) // ~0.01 of the threshold
		h := startIdleChat(t, prov, 0.5)
		h.send("hello")
		h.waitEvents(schedule.Resumed, 1)
		h.clock.Advance(idleTestAfter)
		h.waitEvents(schedule.Done, 1)
		assert.Eq(t, 1, len(h.scheduleEvents(schedule.Fired)))
		assert.False(t, strings.Contains(h.out.String(), "空闲整理"))
		assert.Eq(t, 0, len(h.segmentReasons()))
		prov.mu.Lock()
		assert.Eq(t, 0, prov.sums)
		prov.mu.Unlock()
		h.quit()
	})

	for _, by := range []string{"input", ":compact"} {
		t.Run(by+" preempts a running consolidation", func(t *testing.T) {
			prov := newIdleTestProvider(100000)
			prov.blockSum = true
			h := startIdleChat(t, prov, 0.5)
			h.send("hello")
			h.waitEvents(schedule.Resumed, 1)
			h.clock.Advance(idleTestAfter)
			<-prov.sumEntered // the consolidation is in its summary call
			h.waitOutput("空闲整理中…")
			if by == "input" {
				h.send("typed meanwhile")
				h.waitOutput("answer-2")
			} else {
				h.send(":compact")
				h.waitOutput("Compacted context")
			}
			// The consolidation gave way before its commit point: a cancelled
			// scheduler event, no idle segment record, no success line.
			h.waitEvents(schedule.Failed, 1)
			failed := h.scheduleEvents(schedule.Failed)
			assert.Eq(t, "cancelled", failed[0].Status)
			assert.StrContains(t, failed[0].Error, "preempted")
			assert.False(t, strings.Contains(h.out.String(), "已整理上下文"))
			want := []string(nil)
			if by == ":compact" {
				want = []string{"manual"}
			}
			assert.Eq(t, want, h.segmentReasons())
			h.quit()
		})
	}

	t.Run("off when idle_after is 0", func(t *testing.T) {
		runner, repo := newCLITestRunner(t, newIdleTestProvider(100000))
		err := Run(context.Background(), runner, Options{SessionID: "sess-off"}, strings.NewReader("hello\n:quit\n"),
			io.Discard, io.Discard, nil)
		assert.NoErr(t, err)
		evs, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: "sess-off", Kinds: []string{store.EventSchedule}})
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(evs))
	})
}
