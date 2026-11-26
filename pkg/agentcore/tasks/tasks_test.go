package tasks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// The test binary doubles as the task command: TASKS_HELPER selects what it
// does (see TestMain).
func TestMain(m *testing.M) {
	switch os.Getenv("TASKS_HELPER") {
	case "":
		os.Exit(m.Run())
	case "lines":
		n, _ := strconv.Atoi(os.Getenv("TASKS_LINES"))
		for i := 1; i <= n; i++ {
			fmt.Printf("line %d token=sk-secret\n", i)
		}
		fmt.Fprint(os.Stderr, "last without newline")
		code, _ := strconv.Atoi(os.Getenv("TASKS_EXIT"))
		os.Exit(code)
	case "sleep":
		fmt.Println("ready")
		time.Sleep(time.Minute)
	case "parent":
		// A child that keeps stdout open: only a tree kill ends it at once.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "TASKS_HELPER=sleep")
		child.Stdout = os.Stdout
		_ = child.Start()
		fmt.Println("parent ready")
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func helper(mode string, env ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(append(os.Environ(), "TASKS_HELPER="+mode), env...)
	return cmd
}

func newTestManager(t *testing.T, limits Limits, onExit func(Info)) (*Manager, context.Context) {
	t.Helper()
	dir := t.TempDir()
	m := NewManager(Config{Limits: limits, OnNotice: onExit, Output: func(sid, id string) (string, string, error) {
		return filepath.Join(dir, sid+"-"+id+".txt"), "out:" + sid + ":" + id, nil
	}})
	t.Cleanup(m.Close)
	return m, api.WithSessionID(context.Background(), "s1")
}

// waitFor polls cond for up to 10s.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met in time")
}

func TestManager(t *testing.T) {
	clean := func(b []byte) string { return strings.ReplaceAll(string(b), "sk-secret", "[REDACTED]") }

	t.Run("runs, writes cleaned lines, waits", func(t *testing.T) {
		m, ctx := newTestManager(t, Limits{}, nil)
		info, err := m.Start(ctx, Spec{Kind: "shell", Summary: "lines", Cmd: helper("lines", "TASKS_LINES=3", "TASKS_EXIT=3"), Clean: clean})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "t1", info.ID)
		assert.Eq(t, "out:s1:t1", info.Ref)
		info, done, err := m.Wait(ctx, "s1", "t1", 10*time.Second)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, WaitEnded, done)
		assert.Eq(t, Exited, info.Status)
		assert.Eq(t, 3, info.ExitCode)
		assert.Eq(t, 4, info.Lines) // the last one without a newline too
		assert.Eq(t, "last without newline", info.Tail[len(info.Tail)-1])
		data, _ := os.ReadFile(info.Path)
		assert.StrContains(t, string(data), "line 2 token=[REDACTED]\n")
		assert.NotContains(t, string(data), "sk-secret")
		assert.Empty(t, m.TakeNotices("s1")) // Wait saw the end
		lines, total, more, err := ReadLines(info.Path, 2, 2)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"line 2 token=[REDACTED]", "line 3 token=[REDACTED]"}, lines)
		assert.Eq(t, 4, total)
		assert.True(t, more)
		hits, matched, err := GrepLines(info.Path, `line [13]`, 10)
		assert.NoErr(t, err)
		assert.Eq(t, 2, matched)
		assert.Eq(t, "3: line 3 token=[REDACTED]", hits[1])
	})
	t.Run("an unseen end leaves one notice and calls OnNotice", func(t *testing.T) {
		var exits atomic.Int32
		m, ctx := newTestManager(t, Limits{}, func(Info) { exits.Add(1) })
		_, err := m.Start(ctx, Spec{Kind: "shell", Cmd: helper("lines", "TASKS_LINES=1")})
		assert.Require(t, assert.NoErr(t, err))
		waitFor(t, func() bool { return m.HasNotices("s1") })
		notices := m.TakeNotices("s1")
		assert.Require(t, assert.Len(t, notices, 1))
		assert.Eq(t, Exited, notices[0].Status)
		assert.Empty(t, m.TakeNotices("s1"))
		waitFor(t, func() bool { return exits.Load() == 1 })
	})
	t.Run("kill ends the whole tree at once", func(t *testing.T) {
		m, ctx := newTestManager(t, Limits{}, nil)
		info, err := m.Start(ctx, Spec{Kind: "shell", Cmd: helper("parent")})
		assert.Require(t, assert.NoErr(t, err))
		waitFor(t, func() bool { i, _ := m.Get("s1", info.ID); return i.Lines >= 2 }) // parent and child are up
		start := time.Now()
		info, err = m.Kill("s1", info.ID)
		assert.NoErr(t, err)
		assert.Eq(t, Killed, info.Status)
		// A child left alive would hold the pipe until WaitDelay (2s).
		assert.True(t, time.Since(start) < 1500*time.Millisecond, time.Since(start))
		assert.Empty(t, m.TakeNotices("s1"))
	})
	t.Run("limits: running count, duration, output size", func(t *testing.T) {
		m, ctx := newTestManager(t, Limits{MaxRunning: 1, MaxDuration: 300 * time.Millisecond, MaxOutputBytes: 100}, nil)
		_, err := m.Start(ctx, Spec{Kind: "shell", Cmd: helper("sleep")})
		assert.Require(t, assert.NoErr(t, err))
		_, err = m.Start(ctx, Spec{Kind: "shell", Cmd: helper("sleep")})
		assert.ErrMsgContains(t, err, "already running")
		info, done, err := m.Wait(ctx, "s1", "t1", 10*time.Second)
		assert.NoErr(t, err)
		assert.Eq(t, WaitEnded, done)
		assert.Eq(t, TimedOut, info.Status)

		info, err = m.Start(ctx, Spec{Kind: "shell", Cmd: helper("lines", "TASKS_LINES=50")})
		assert.Require(t, assert.NoErr(t, err))
		info, _, _ = m.Wait(ctx, "s1", info.ID, 10*time.Second)
		assert.True(t, info.Truncated)
		assert.True(t, info.Bytes <= 100)
		data, _ := os.ReadFile(info.Path)
		assert.StrContains(t, string(data), "[output cut at 100 bytes; 51 lines in all]")
	})
	t.Run("sessions are separate; close kills everything", func(t *testing.T) {
		m, ctx := newTestManager(t, Limits{}, nil)
		info, err := m.Start(ctx, Spec{Kind: "shell", Cmd: helper("sleep")})
		assert.Require(t, assert.NoErr(t, err))
		_, err = m.Get("s2", info.ID)
		assert.ErrIs(t, err, ErrUnknownTask)
		_, err = m.Start(context.Background(), Spec{Kind: "shell", Cmd: helper("sleep")})
		assert.ErrMsgContains(t, err, "need a session")
		m.Close()
		got, _ := m.Get("s1", info.ID)
		assert.Eq(t, Killed, got.Status)
		_, err = m.Start(ctx, Spec{Kind: "shell", Cmd: helper("sleep")})
		assert.ErrMsgContains(t, err, "shut down")
	})
}

func TestFunctionTasks(t *testing.T) {
	t.Run("writes output, reports progress and counters, ends with its exit code", func(t *testing.T) {
		m, ctx := newTestManager(t, Limits{}, nil)
		info, err := m.Start(ctx, Spec{Kind: "watch", Summary: "fn", Run: func(_ context.Context, out io.Writer, r Reporter) (int, error) {
			fmt.Fprintln(out, "run 1")
			r.Progress("run 1, exit 0")
			r.Count("runs", 1)
			return 3, nil
		}})
		assert.Require(t, assert.NoErr(t, err))
		// A Wait while it runs could return at its Progress (WaitUpdated); wait
		// for the end first.
		waitFor(t, func() bool { got, _ := m.Get("s1", info.ID); return got.Status != Running })
		ended, done, err := m.Wait(ctx, "s1", info.ID, 5*time.Second)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, WaitEnded, done)
		assert.Eq(t, Exited, ended.Status)
		assert.Eq(t, 3, ended.ExitCode)
		assert.Eq(t, []string{"run 1"}, ended.Tail)
		assert.Eq(t, map[string]int{"runs": 1}, ended.Counters)
		assert.StrContains(t, Describe(ended), "run 1, exit 0")
		assert.Empty(t, m.TakeNotices("s1")) // Wait saw the end
	})
	t.Run("an error marks it failed", func(t *testing.T) {
		m, ctx := newTestManager(t, Limits{}, nil)
		info, err := m.Start(ctx, Spec{Kind: "hook", Run: func(context.Context, io.Writer, Reporter) (int, error) {
			return -1, errors.New("boom")
		}})
		assert.Require(t, assert.NoErr(t, err))
		ended, _, _ := m.Wait(ctx, "s1", info.ID, 5*time.Second)
		assert.Eq(t, Failed, ended.Status)
		assert.Eq(t, "boom", ended.Error)
		assert.StrContains(t, Describe(ended), "failed after")
		assert.StrContains(t, Describe(ended), ": boom")
	})
	t.Run("kill and the time limit cancel its context", func(t *testing.T) {
		m, ctx := newTestManager(t, Limits{MaxDuration: 300 * time.Millisecond}, nil)
		// Both only cancel the ctx, so Killed is what tells them apart.
		block := func(said chan<- bool) RunFunc {
			return func(ctx context.Context, _ io.Writer, r Reporter) (int, error) {
				<-ctx.Done()
				said <- r.Killed()
				return -1, nil
			}
		}
		killedSaid, timedSaid := make(chan bool, 1), make(chan bool, 1)
		killed, err := m.Start(ctx, Spec{Run: block(killedSaid)})
		assert.Require(t, assert.NoErr(t, err))
		timed, err := m.Start(ctx, Spec{Run: block(timedSaid)})
		assert.Require(t, assert.NoErr(t, err))
		info, err := m.Kill("s1", killed.ID)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, Killed, info.Status)
		assert.True(t, <-killedSaid)
		info, done, _ := m.Wait(ctx, "s1", timed.ID, 5*time.Second)
		assert.Eq(t, WaitEnded, done)
		assert.Eq(t, TimedOut, info.Status)
		assert.False(t, <-timedSaid)
	})
	t.Run("running notices: the newest replaces a pending one, the end notice stays apart", func(t *testing.T) {
		var notified atomic.Int32
		m, ctx := newTestManager(t, Limits{}, func(Info) { notified.Add(1) })
		step := make(chan struct{})
		info, err := m.Start(ctx, Spec{Kind: "watch", Run: func(_ context.Context, _ io.Writer, r Reporter) (int, error) {
			r.Notify("changed: exit 0 -> 1")
			r.Notify("changed: exit 1 -> 0")
			<-step
			r.Notify("changed: exit 0 -> 2")
			return 0, nil // it ends by itself, unseen
		}})
		assert.Require(t, assert.NoErr(t, err))
		waitFor(t, func() bool { return notified.Load() == 2 })
		got := m.TakeNotices("s1")
		assert.Require(t, assert.Len(t, got, 1))
		assert.Eq(t, info.ID, got[0].ID)
		assert.Eq(t, "changed: exit 1 -> 0", got[0].Notice)
		assert.Eq(t, Running, got[0].Status)

		close(step)
		waitFor(t, func() bool { return notified.Load() == 4 }) // the notice and the end
		got = m.TakeNotices("s1")
		assert.Require(t, assert.Len(t, got, 2))
		assert.Eq(t, "changed: exit 0 -> 2", got[0].Notice)
		assert.Eq(t, "", got[1].Notice)
		assert.Eq(t, Exited, got[1].Status)
	})
	t.Run("a spec needs exactly one of Cmd and Run", func(t *testing.T) {
		m, ctx := newTestManager(t, Limits{}, nil)
		_, err := m.Start(ctx, Spec{})
		assert.Err(t, err)
		_, err = m.Start(ctx, Spec{Cmd: helper("sleep"), Run: func(context.Context, io.Writer, Reporter) (int, error) { return 0, nil }})
		assert.Err(t, err)
	})
}

func TestNoEndNotice(t *testing.T) {
	m, ctx := newTestManager(t, Limits{}, nil)
	info, err := m.Start(ctx, Spec{Kind: "hook", NoEndNotice: true, Run: func(_ context.Context, _ io.Writer, r Reporter) (int, error) {
		r.Notify("asks to continue")
		return 0, nil
	}})
	assert.Require(t, assert.NoErr(t, err))
	waitFor(t, func() bool { got, _ := m.Get("s1", info.ID); return got.Status == Exited })
	got := m.TakeNotices("s1")
	assert.Require(t, assert.Len(t, got, 1))
	assert.Eq(t, "asks to continue", got[0].Notice)
}

// TestReturnNotices: notices a host took for a turn that never delivered them go
// back into the pending queue, in their original order and before the notices
// that arrived meanwhile, so the next TakeNotices hands each of them out once
// more (issue suga-o26e).
func TestReturnNotices(t *testing.T) {
	m, ctx := newTestManager(t, Limits{}, nil)
	// t1 leaves its end notice before the host takes it, t2 ends while it is out.
	first, err := m.Start(ctx, Spec{Kind: "shell", Cmd: helper("lines", "TASKS_LINES=1")})
	assert.Require(t, assert.NoErr(t, err))
	waitFor(t, func() bool { return m.HasNotices("s1") })
	taken := m.TakeNotices("s1")
	assert.Require(t, assert.Len(t, taken, 1))
	assert.Eq(t, first.ID, taken[0].ID)

	second, err := m.Start(ctx, Spec{Kind: "shell", Cmd: helper("lines", "TASKS_LINES=1")})
	assert.Require(t, assert.NoErr(t, err))
	waitFor(t, func() bool { return m.HasNotices("s1") })

	m.ReturnNotices("s1", taken)
	again := m.TakeNotices("s1")
	assert.Require(t, assert.Len(t, again, 2))
	// The returned notice comes first, the one that arrived meanwhile after it.
	assert.Eq(t, []string{first.ID, second.ID}, []string{again[0].ID, again[1].ID})
	assert.Empty(t, m.TakeNotices("s1")) // each of them once

	t.Run("a newer notice of the same task wins", func(t *testing.T) {
		var notified atomic.Int32
		m2, ctx2 := newTestManager(t, Limits{}, func(Info) { notified.Add(1) })
		step := make(chan struct{})
		_, err := m2.Start(ctx2, Spec{Kind: "watch", Run: func(_ context.Context, _ io.Writer, r Reporter) (int, error) {
			r.Notify("run 1: exit 0")
			<-step
			r.Notify("run 2: exit 1")
			return 0, nil
		}})
		assert.Require(t, assert.NoErr(t, err))
		waitFor(t, func() bool { return notified.Load() == 1 })
		out := m2.TakeNotices("s1")
		assert.Require(t, assert.Len(t, out, 1))
		assert.Eq(t, "run 1: exit 0", out[0].Notice)

		// The task reports again and ends while its first notice is out: both the
		// newer report and the end notice are queued (each OnNotice call).
		close(step)
		waitFor(t, func() bool { return notified.Load() == 3 })
		m2.ReturnNotices("s1", out)
		back := m2.TakeNotices("s1")
		assert.Require(t, assert.Len(t, back, 2))
		assert.Eq(t, "run 2: exit 1", back[0].Notice)
		assert.Eq(t, "", back[1].Notice) // the end of the same task
	})
}

func TestWaitOnFunctionTask(t *testing.T) {
	m, ctx := newTestManager(t, Limits{}, nil)
	step := make(chan struct{})
	info, err := m.Start(ctx, Spec{Kind: "watch", Run: func(ctx context.Context, _ io.Writer, r Reporter) (int, error) {
		<-step
		r.Notify("run 2: exit 1 -> 0")
		r.Progress("run 2, last exit 0")
		<-ctx.Done()
		return 0, nil
	}})
	assert.Require(t, assert.NoErr(t, err))
	t.Run("no progress: the timeout", func(t *testing.T) {
		_, how, err := m.Wait(ctx, "s1", info.ID, 100*time.Millisecond)
		assert.NoErr(t, err)
		assert.Eq(t, WaitTimedOut, how)
	})
	t.Run("progress returns it and takes the notice", func(t *testing.T) {
		go func() { time.Sleep(50 * time.Millisecond); close(step) }()
		got, how, err := m.Wait(ctx, "s1", info.ID, 5*time.Second)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, WaitUpdated, how)
		assert.Eq(t, Running, got.Status)
		assert.Eq(t, "run 2, last exit 0", got.Progress)
		assert.Eq(t, "run 2: exit 1 -> 0", got.Notice)
		assert.Empty(t, m.TakeNotices("s1")) // not noticed again
	})
}

// TestSeenEndLeavesNoNotice: an end the caller saw through Kill or Wait never
// leaves a notice behind, however the task's own end and the caller race
// (the notice used to be queued after done was closed).
func TestSeenEndLeavesNoNotice(t *testing.T) {
	m, ctx := newTestManager(t, Limits{MaxRunning: 1000}, nil)
	// It ends by itself while Kill or Wait already waits for it.
	quick := func(context.Context, io.Writer, Reporter) (int, error) { time.Sleep(time.Millisecond); return 0, nil }
	for i := 0; i < 300; i++ {
		info, err := m.Start(ctx, Spec{Run: quick})
		assert.Require(t, assert.NoErr(t, err))
		if i%2 == 0 {
			_, _ = m.Kill("s1", info.ID)
		} else {
			_, _, _ = m.Wait(ctx, "s1", info.ID, 5*time.Second)
		}
		if got := m.TakeNotices("s1"); len(got) != 0 {
			t.Fatalf("run %d: %s's end was seen, yet left a notice", i, got[0].ID)
		}
	}
}
