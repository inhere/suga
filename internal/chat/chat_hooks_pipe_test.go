package chat

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
)

// TestPersonalChatPipeStdinStaysOpenDuringStopWait is the second path of
// suga-b8ag: stdin is a pipe that is *not* closed (an IDE feeder writes a prompt
// and keeps writing later, `:quit` among it). While a configured Stop hook runs
// nothing new arrives, so the Stop wait must not be cut short: the first hook
// command has to see Stop, the second one's block reply has to run the
// continuation turn, and a `:quit` typed afterwards must still be read.
func TestPersonalChatPipeStdinStaysOpenDuringStopWait(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{name: "a CRLF line from a Windows feeder", input: "hi\r\n"},
		{name: "a LF line", input: "hi\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUGA_HOME", t.TempDir())
			// The recorder is the first entry (it sees every event), the hook that
			// asks for a continuation is the second one: the report's configuration.
			hooks, logPath := newMultiHookClient(t, func() {
				assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 30)))
			}, nil, multiHookSpec{label: "rec", mode: "silent"}, multiHookSpec{label: "cont", mode: "block"})
			provider := &hookTestProvider{}
			runner, _ := newCLITestRunner(t, provider)

			stdin, input := io.Pipe()
			var stdout, stderr strings.Builder
			done := make(chan error, 1)
			go func() {
				done <- Run(testCtx(t), runner, Options{}, stdin, &stdout, &stderr, hooks)
			}()
			// The pipe stays open for the whole case; nothing but :quit is written
			// before the Stop hook of the first turn has to finish.
			_, err := io.WriteString(input, tc.input)
			assert.Require(t, assert.NoErr(t, err))

			// The second command receives the Stop event, and its block reply runs
			// the continuation turn with the reason as the model's new message.
			waitMultiHookLine(t, logPath, "cont Stop start")
			waitPipeProviderCalls(t, provider, 2)
			provider.mu.Lock()
			second := append([]agentcore.Message(nil), provider.calls[1]...)
			provider.mu.Unlock()
			assert.True(t, hasUserText(second, "cont-reason"), "the continuation turn carries the block reason: %#v", second)
			assert.False(t, multiHookLogHas(t, logPath, "rec Interrupt start"), "no Stop wait was cut short without new input")

			// The input written after the wait is still read: chat leaves with 0.
			_, err = io.WriteString(input, ":quit\n")
			assert.Require(t, assert.NoErr(t, err))
			select {
			case err := <-done:
				assert.NoErr(t, err)
			case <-time.After(20 * time.Second):
				t.Fatalf("chat did not read the :quit written after the Stop wait; hook log:\n%s",
					strings.Join(multiHookLogLines(t, logPath), "\n"))
			}
			_ = input.Close()
		})
	}
}

// TestPersonalChatInputErrorEndsWaitingStopHook is the other half of the same
// deadlock: an input that fails (not an EOF) while a Stop hook is still waiting
// ends the read that produced the error, and chat reports the error instead of
// waiting for an event nobody produces (suga-b8ag).
func TestPersonalChatInputErrorEndsWaitingStopHook(t *testing.T) {
	t.Setenv("SUGA_HOME", t.TempDir())
	hooks, logPath := newMultiHookClient(t, func() {
		assert.Require(t, assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 30)))
	}, nil, multiHookSpec{label: "wait", mode: "stopwait", timeoutMS: 30000})
	runner, _ := newCLITestRunner(t, &hookTestProvider{})
	boom := errors.New("stdin broke")
	// The failing stdin is held back until the Stop hook runs: otherwise the error
	// races the hook's own start and the case would prove less than it claims.
	stdin := &gatedErrInput{buf: []byte("hi\n"), gate: make(chan struct{}), err: boom}
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(testCtx(t), runner, Options{}, stdin, &stdout, &stderr, hooks)
	}()
	// Only now does the read of the next prompt hit the error.
	waitMultiHookLine(t, logPath, "wait Stop start")
	stdin.release()
	select {
	case err := <-done:
		assert.True(t, errors.Is(err, boom), "chat reports the input error, got %v", err)
	case <-time.After(20 * time.Second):
		t.Fatalf("chat kept waiting for an event nobody produces after the input failed; hook log:%v stdout:%q stderr:%q",
			multiHookLogLines(t, logPath), stdout.String(), stderr.String())
	}
}

// gatedErrInput hands over buf one byte at a time and then waits for release: from
// then on every Read answers err (the failing stdin of a test).
type gatedErrInput struct {
	mu   sync.Mutex
	buf  []byte
	gate chan struct{}
	err  error
}

func (r *gatedErrInput) Read(p []byte) (int, error) {
	r.mu.Lock()
	if len(r.buf) > 0 {
		p[0] = r.buf[0]
		r.buf = r.buf[1:]
		r.mu.Unlock()
		return 1, nil
	}
	r.mu.Unlock()
	<-r.gate
	return 0, r.err
}

// release lets the reads that waited answer the error.
func (r *gatedErrInput) release() { close(r.gate) }

// waitPipeProviderCalls waits (20s) until the fake provider took count model
// requests: the spawned hook commands make the second call slower than the
// 3s helper of the other cases allows.
func waitPipeProviderCalls(t *testing.T, provider *hookTestProvider, count int) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		provider.mu.Lock()
		calls := len(provider.calls)
		provider.mu.Unlock()
		if calls >= count {
			return
		}
	}
	t.Fatalf("provider did not receive call %d", count)
}
