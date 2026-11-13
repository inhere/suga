package chat

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// TestHangingStopHookProcess is the fake hook command of the suga-b8ag test: the
// test binary re-runs itself with -test.run=^TestHangingStopHookProcess$. It
// answers SessionStart with an additionalContext (like gofer's hook does) and
// makes Stop wait far longer than the test, appending every event name it saw to
// SUGA_HANG_HOOK_LOG so the test knows the Stop wait really started.
func TestHangingStopHookProcess(t *testing.T) {
	logPath := os.Getenv("SUGA_HANG_HOOK_LOG")
	if logPath == "" {
		return
	}
	var event map[string]any
	_ = json.NewDecoder(bufio.NewReader(os.Stdin)).Decode(&event)
	name, _ := event["hook_event_name"].(string)
	if file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_, _ = file.WriteString(name + "\n")
		_ = file.Close()
	}
	switch name {
	case "SessionStart":
		_, _ = os.Stdout.WriteString(`{"hookSpecificOutput":{"additionalContext":"startup-context"}}`)
	case "Stop":
		// The gofer hook waits for external input; here a plain sleep stands in for
		// it, so only an interrupted wait lets chat exit.
		time.Sleep(60 * time.Second)
	}
	// Exit before the testing framework prints PASS on stdout.
	os.Exit(0)
}

// TestPersonalChatEOFEndsWaitingStopHook: an input that ends (a pipe closed after
// its last line, here "/exit", which is an ordinary message) while a Stop hook is
// still waiting must end chat instead of waiting for a read nobody asked for
// (suga-b8ag).
func TestPersonalChatEOFEndsWaitingStopHook(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{name: "a pipe closed after its last line", input: "/exit\n"},
		{name: "a pipe closed after an ordinary message", input: "hello\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gkconfig.Reset()
			t.Cleanup(gkconfig.Reset)
			t.Setenv("SUGA_HOME", t.TempDir())
			t.Setenv("SUGA_HOOKS", "")
			logPath := filepath.Join(t.TempDir(), "hook.log")
			t.Setenv("SUGA_HANG_HOOK_LOG", logPath)
			assert.NoErr(t, gkconfig.Set("hooks.command", []string{os.Args[0], "-test.run=^TestHangingStopHookProcess$"}))
			// The Stop bound the gofer hook gets: long enough that the hang is the
			// input handling, not the hook's own timeout.
			assert.NoErr(t, gkconfig.Set("hooks.stop_wait_sec", 60))
			hooks, err := sugahooks.New()
			assert.Require(t, assert.NoErr(t, err))
			assert.Require(t, assert.NotNil(t, hooks))

			runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Final("done")))
			stdin, input := io.Pipe()
			done := make(chan error, 1)
			var stdout, stderr strings.Builder
			go func() {
				done <- Run(testCtx(t), runner, Options{}, stdin, &stdout, &stderr, hooks)
			}()
			if tc.input != "" {
				if _, err := io.WriteString(input, tc.input); err != nil {
					t.Fatalf("write input: %v", err)
				}
			}
			// Only close the pipe once the Stop hook is waiting: that is the state the
			// EOF has to break out of.
			waitForHookLog(t, logPath, "Stop")
			_ = input.Close()

			select {
			case err := <-done:
				assert.NoErr(t, err)
			case <-time.After(20 * time.Second):
				t.Fatal("chat did not exit after stdin EOF while a Stop hook was waiting")
			}
		})
	}
}

// waitForHookLog blocks until the fake hook process logged the event name.
func waitForHookLog(t *testing.T, logPath, name string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		// Raw file read: this must not call t.FailNow from a polling loop.
		if data, _ := os.ReadFile(logPath); strings.Contains(string(data), name+"\n") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hook event %q never ran", name)
}
