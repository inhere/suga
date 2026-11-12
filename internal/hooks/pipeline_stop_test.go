package hooks

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

func setPipelineTestHook(t *testing.T, hook func(pipelinePoint, int)) {
	t.Helper()
	previous := pipelineTestHook
	pipelineTestHook = hook
	t.Cleanup(func() { pipelineTestHook = previous })
}

func waitForPipelineLog(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		if hookLogHasLine(t, path, want) {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for hook log line %q; log=%v", want, readHookLog(t, path))
		}
	}
}

func TestPipelineStop(t *testing.T) {
	t.Run("R1 skips waiting and joins blocks", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, func() {
			assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 1))
		}, &hookSpec{label: "gofer", mode: "wait"}, hookSpec{label: "a", mode: "block"}, hookSpec{label: "b", mode: "block"})
		result := <-p.StartStop(context.Background(), Event{Name: "Stop"}).Done()
		assert.NoErr(t, result.Err)
		assert.Eq(t, "block", result.Response.Decision)
		assert.Eq(t, "a-reason\n\nb-reason", result.Response.Reason)
		assert.Eq(t, []string{"a", "b"}, startedLabels(t, logPath, "Stop"))
		assert.False(t, hookLogHasLine(t, logPath, "gofer Stop start"))
	})

	t.Run("a failed immediate entry does not discard a block", func(t *testing.T) {
		p, _ := newPipelineTestClient(t, nil, nil, hookSpec{label: "a", mode: "block"}, hookSpec{label: "b", mode: "fail"})
		result := <-p.StartStop(context.Background(), Event{Name: "Stop"}).Done()
		assert.NoErr(t, result.Err)
		assert.Eq(t, "block", result.Response.Decision)
		assert.Eq(t, "a-reason", result.Response.Reason)
	})

	t.Run("R2 runs a waiting compatible entry and returns its timeout", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, func() {
			assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 1))
		}, &hookSpec{label: "gofer", mode: "wait"}, hookSpec{label: "a", mode: "silent"})
		result := <-p.StartStop(context.Background(), Event{Name: "Stop"}).Done()
		assert.True(t, errors.Is(result.Err, ErrTimedOut), result.Err)
		assert.Eq(t, []string{"a", "gofer"}, startedLabels(t, logPath, "Stop"))
	})

	t.Run("an immediate compatible entry still receives stop args", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, func() {
			assert.NoErr(t, gkconfig.Set("hooks.stop_args", []string{"--wait", "7"}))
		}, &hookSpec{label: "compat", mode: "silent"}, hookSpec{label: "entry", mode: "silent"})
		result := <-p.StartStop(context.Background(), Event{Name: "Stop"}).Done()
		assert.NoErr(t, result.Err)
		assert.Eq(t, []string{"compat", "entry"}, startedLabels(t, logPath, "Stop"))
	})

	t.Run("notice follows execution order and done closes", func(t *testing.T) {
		p, _ := newPipelineNoticeClient(t, "a", "b")
		wait := p.StartStop(context.Background(), Event{Name: "Stop"})
		result := <-wait.Done()
		assert.Eq(t, "notice-a\nnotice-b", result.Response.Notice)
		_, open := <-wait.Done()
		assert.False(t, open)
	})

	t.Run("parent cancellation is R0", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p, logPath := newPipelineTestClient(t, nil, nil, hookSpec{label: "a", mode: "wait"})
		wait := p.StartStop(ctx, Event{Name: "Stop"})
		waitForPipelineLog(t, logPath, "a Stop start")
		cancel()
		result := <-wait.Done()
		assert.True(t, errors.Is(result.Err, context.Canceled), result.Err)
		assert.Eq(t, "", result.Response.Decision)
	})

	t.Run("cancel between entry exit and done is R0", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p, _ := newPipelineTestClient(t, nil, nil, hookSpec{label: "a", mode: "block"})
		entered := make(chan struct{})
		setPipelineTestHook(t, func(point pipelinePoint, entry int) {
			if point == pointEntryExited && entry == 1 {
				close(entered)
				cancel()
			}
		})
		result := <-p.StartStop(ctx, Event{Name: "Stop"}).Done()
		<-entered
		assert.True(t, errors.Is(result.Err, context.Canceled), result.Err)
		assert.Eq(t, "", result.Response.Decision)
	})

	t.Run("cancel in before done preserves R1", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p, _ := newPipelineTestClient(t, nil, nil, hookSpec{label: "a", mode: "block"})
		setPipelineTestHook(t, func(point pipelinePoint, entry int) {
			if point == pointBeforeDone {
				cancel()
			}
		})
		result := <-p.StartStop(ctx, Event{Name: "Stop"}).Done()
		assert.NoErr(t, result.Err)
		assert.Eq(t, "a-reason", result.Response.Reason)
	})

	t.Run("failure warns but cancellation and timeout debug", func(t *testing.T) {
		var logs strings.Builder
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
		t.Cleanup(func() { slog.SetDefault(previous) })
		p, logPath := newPipelineTestClient(t, func() {
			assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 1))
		}, &hookSpec{label: "gofer", mode: "ifail"}, hookSpec{label: "a", mode: "fail"})
		wait := p.StartStop(context.Background(), Event{Name: "Stop"})
		waitForPipelineLog(t, logPath, "gofer Stop start")
		wait.Interrupt(context.Background())
		assert.True(t, strings.Contains(logs.String(), "level=WARN"), logs.String())
	})
}

func TestPipelineInterrupt(t *testing.T) {
	t.Run("running waiting entry gets the only interrupt", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, func() {
			assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 1))
		}, &hookSpec{label: "gofer", mode: "wait"}, hookSpec{label: "a", mode: "silent"})
		wait := p.StartStop(context.Background(), Event{Name: "Stop"})
		waitForPipelineLog(t, logPath, "gofer Stop start")
		assert.Eq(t, "", wait.Interrupt(context.Background()))
		assert.Eq(t, []string{"a", "gofer"}, startedLabels(t, logPath, "Stop"))
		assert.True(t, hookLogHasLine(t, logPath, "gofer Interrupt start"))
		assert.False(t, hookLogHasLine(t, logPath, "a Interrupt start"))
	})

	t.Run("running child result is returned after freezing", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, nil, hookSpec{label: "a", mode: "block"})
		released := make(chan struct{})
		exited := make(chan struct{})
		var once sync.Once
		setPipelineTestHook(t, func(point pipelinePoint, entry int) {
			switch {
			case point == pointEntryExited && entry == 1:
				close(exited)
				<-released
			case point == pointInterruptFrozen && entry == 1:
				once.Do(func() { close(released) })
			}
		})
		wait := p.StartStop(context.Background(), Event{Name: "Stop"})
		<-exited
		reply := make(chan string, 1)
		go func() { reply <- wait.Interrupt(context.Background()) }()
		assert.Eq(t, "a-reason", <-reply)
		result := <-wait.Done()
		assert.Err(t, result.Err)
		assert.True(t, hookLogHasLine(t, logPath, "a Interrupt start"))
	})

	t.Run("between entries freezes without sending interrupt", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, func() {
			assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 1))
		}, &hookSpec{label: "gofer", mode: "wait"}, hookSpec{label: "a", mode: "block"}, hookSpec{label: "b", mode: "silent"})
		blocked := make(chan struct{})
		release := make(chan struct{})
		frozen := make(chan struct{})
		setPipelineTestHook(t, func(point pipelinePoint, entry int) {
			if point == pointBeforeStartNext && entry == 2 {
				close(blocked)
				<-release
			}
			if point == pointInterruptFrozen && entry == -1 {
				close(frozen)
			}
		})
		wait := p.StartStop(context.Background(), Event{Name: "Stop"})
		<-blocked
		reply := make(chan string, 1)
		go func() { reply <- wait.Interrupt(context.Background()) }()
		<-frozen
		close(release)
		assert.Eq(t, "a-reason", <-reply)
		result := <-wait.Done()
		assert.True(t, errors.Is(result.Err, context.Canceled), result.Err)
		assert.Eq(t, []int{1}, runEntries(result.Response.Runs))
		assert.False(t, hookLogHasLine(t, logPath, "b Stop start"))
		assert.False(t, hookLogHasLine(t, logPath, "gofer Stop start"))
		assert.False(t, hookLogHasLine(t, logPath, "a Interrupt start"))
	})

	t.Run("first start can be frozen", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, nil, hookSpec{label: "a", mode: "silent"})
		blocked := make(chan struct{})
		release := make(chan struct{})
		frozen := make(chan struct{})
		setPipelineTestHook(t, func(point pipelinePoint, entry int) {
			if point == pointBeforeStartNext && entry == 1 {
				close(blocked)
				<-release
			}
			if point == pointInterruptFrozen && entry == -1 {
				close(frozen)
			}
		})
		wait := p.StartStop(context.Background(), Event{Name: "Stop"})
		<-blocked
		reply := make(chan string, 1)
		go func() { reply <- wait.Interrupt(context.Background()) }()
		<-frozen
		close(release)
		assert.Eq(t, "", <-reply)
		result := <-wait.Done()
		assert.True(t, errors.Is(result.Err, context.Canceled), result.Err)
		assert.Nil(t, result.Response.Runs)
		assert.Nil(t, readHookLog(t, logPath))
	})

	t.Run("finished result still interrupts its last entry", func(t *testing.T) {
		p, logPath := newPipelineTestClient(t, nil, nil, hookSpec{label: "a", mode: "block"})
		wait := p.StartStop(context.Background(), Event{Name: "Stop"})
		result := <-wait.Done()
		assert.Eq(t, "a-reason", wait.Interrupt(context.Background()))
		assert.Eq(t, "a-reason", result.Response.Reason)
		assert.True(t, hookLogHasLine(t, logPath, "a Interrupt start"))
	})
}

func runEntries(runs []EntryRun) []int {
	entries := make([]int, 0, len(runs))
	for _, run := range runs {
		entries = append(entries, run.Entry)
	}
	return entries
}

func newPipelineNoticeClient(t *testing.T, labels ...string) (*pipeline, string) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "")
	t.Setenv("SUGA_NOTICE_HOOK", "1")
	logPath := filepath.Join(t.TempDir(), "notice.log")
	t.Setenv("SUGA_HOOK_LOG", logPath)
	entries := make([]*commandClient, 0, len(labels))
	for index, label := range labels {
		entries = append(entries, &commandClient{
			argv:    []string{os.Args[0], "-test.run=^TestPipelineStopNoticeProcess$", "--", label, "silent"},
			timeout: time.Second, stopTimeout: time.Second, entry: index + 1,
			program: filepath.Base(os.Args[0]),
		})
	}
	return newPipeline(nil, entries, false), logPath
}

func TestPipelineStopNoticeProcess(t *testing.T) {
	if os.Getenv("SUGA_NOTICE_HOOK") == "" {
		return
	}
	label, _ := hookProcessArgs()
	hookProcessLog(os.Getenv("SUGA_HOOK_LOG"), label+" Stop start")
	_, _ = os.Stderr.WriteString("notice-" + label)
	hookProcessLog(os.Getenv("SUGA_HOOK_LOG"), label+" Stop end")
	os.Exit(0)
}
