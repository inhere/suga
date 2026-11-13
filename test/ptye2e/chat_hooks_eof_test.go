//go:build ptye2e

package ptye2e

import (
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestChatPipeEOFExitsWithWaitingStopHook is the process-level regression of
// suga-b8ag: chat runs with a hook whose Stop waits (the gofer hook of the bug
// report), stdin is a pipe that ends after "/exit" (an ordinary message, so the
// turn runs and the Stop wait starts) and the process must end with exit code 0
// instead of hanging on the wait. The unit test
// TestPersonalChatEOFEndsWaitingStopHook covers the same path in package chat.
func TestChatPipeEOFExitsWithWaitingStopHook(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	logPath := filepath.Join(t.TempDir(), "hooks.log")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// The Stop hook waits far longer than this test: a chat that only exits when
	// the hook's own bound runs out must not pass.
	config := fmt.Sprintf(`language: zh
model: fake/m1
providers:
  fake:
    type: openai
    base_url: %s
    api_key: k
tools:
  allow_write: true
hooks:
  command: %s
  stop_wait_sec: 60
`, server.URL, hookCommand(self, "gofer", "wait"))
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(testEnv(home),
		"SUGA_HOOKS=on", multiHookProcessEnv+"=1", multiHookLogEnv+"="+logPath)

	stdin, input := io.Pipe()
	cmd := exec.Command(binary, "chat")
	cmd.Dir, cmd.Env, cmd.Stdin = work, env, stdin
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Whatever happens below, the process must not outlive the scenario.
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if _, err := io.WriteString(input, "/exit\n"); err != nil {
		t.Fatal(err)
	}
	// Close the pipe only once the Stop hook of that turn is waiting: that is the
	// state the end of the input has to break out of.
	waitHookLog(t, logPath, "gofer Stop start", 1)
	_ = input.Close()

	select {
	case err := <-done:
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("chat: %v", err)
		}
		check(t, "exit code 0", code == 0)
	case <-time.After(25 * time.Second):
		t.Fatalf("chat did not exit after stdin EOF while a Stop hook was waiting; hook log:\n%s", readHookLogText(logPath))
	}
}
