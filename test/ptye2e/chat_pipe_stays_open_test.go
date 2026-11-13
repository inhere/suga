//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChatPipeStdinStaysOpenDuringStopWait is the process-level regression of the
// second path of suga-b8ag (reported from a Windows IDE feeder): stdin is a pipe
// that is *not* closed, the first prompt arrives as a CRLF line (a Windows feeder
// writes "\r\n"), and two hooks.commands entries run: a recorder and one that asks
// for a continuation on the first Stop. Before the fix the pump dropped the LF of
// the CRLF pair and waited for the next byte, so the line was not submitted until
// the pipe ended; the pending read then hit that EOF and cut the Stop wait short,
// the continuation never ran and the later ":quit" was never a command. Here the
// prompt submits at once, nothing interrupts the Stop wait, the continuation turn
// runs and the ":quit" written later ends chat with exit code 0.
func TestChatPipeStdinStaysOpenDuringStopWait(t *testing.T) {
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
	// The recorder runs first and sees every event; the second entry blocks once,
	// which is the continuation the pipeline has to deliver.
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
  stop_wait_sec: 60
  commands:
    - command: %s
      timeout_ms: 5000
    - command: %s
      timeout_ms: 5000
`, server.URL, hookCommand(self, "rec", "silent"), hookCommand(self, "cont", "block-once"))
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(testEnv(home),
		"SUGA_HOOKS=on", multiHookProcessEnv+"=1", multiHookLogEnv+"="+logPath)

	// Both ends of the pipe are files, so exec passes the handle to the process: a
	// copier goroutine would keep cmd.Wait blocked on the pipe this case leaves open.
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	cmd := exec.Command(binary, "chat", "--read-only")
	cmd.Dir, cmd.Env, cmd.Stdin = work, env, stdin
	// Stdout goes to a file for the same reason.
	stdoutPath := filepath.Join(t.TempDir(), "chat.out")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutFile.Close()
	cmd.Stdout, cmd.Stderr = stdoutFile, stdoutFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close() // the child holds its own handle now
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// A Windows feeder's line: CRLF, with the pipe left open behind it.
	if _, err := input.WriteString("hi\r\n"); err != nil {
		t.Fatal(err)
	}
	// The second entry's Stop answered block, so the continuation turn ran: its own
	// Stop is the second one the recorder sees.
	waitHookLog(t, logPath, "rec Stop start", 1)
	waitHookLog(t, logPath, "cont Stop start", 1)
	waitHookLog(t, logPath, "rec Stop start", 2)
	check(t, "the continuation turn shows the block reason", strings.Contains(chatOutput(stdoutPath), continueReason))
	check(t, "nothing cut the Stop wait short", !strings.Contains(readHookLogText(logPath), " Interrupt start"))

	// The input written after the wait is still read: chat leaves with 0, without
	// the pipe being closed.
	if _, err := input.WriteString(":quit\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("chat: %v", err)
		}
		check(t, "exit code 0", code == 0)
		check(t, "the later :quit was read as a command", !strings.Contains(chatOutput(stdoutPath), "\r:quit"))
	case <-time.After(25 * time.Second):
		t.Fatalf("chat did not read the :quit written after the Stop wait; hook log:\n%s\nchat output:\n%s",
			readHookLogText(logPath), chatOutput(stdoutPath))
	}
}

// chatOutput returns what the chat process wrote so far.
func chatOutput(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}
