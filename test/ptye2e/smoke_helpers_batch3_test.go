//go:build ptye2e

package ptye2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// Helpers added by the third smoke migration batch (suga-hb8v); kept apart from
// smoke_helpers_test.go so the shared file stays as the earlier batches left it.

// promptReady waits until a fresh prompt is drawn after mark: a "❯" at the start
// of a line, or right after the steer prompt "↳ " a finished turn leaves (it draws
// the prompt over that with cursor moves, which Text strips). The echo of a line
// just sent ("❯ text") has no line break before it, and ConPTY may drop the
// prompt's trailing space, so neither is matched on. Send the next line only after
// this: input typed ahead of the line editor is lost (suga-uxmu), and during a
// turn it would be taken as a steer.
func promptReady(t *testing.T, s *ptytest.Session, mark int) {
	t.Helper()
	if !until(t, s, "next prompt is up", freshPrompt, mark) {
		t.Logf("next prompt is up: text tail %q", tail(s.Since(mark)))
	}
}

// oscRun matches any run of OSC sequences (ESC ] ... BEL or ST).
const oscRun = `(?:\x1b\][^\a\x1b]*(?:\a|\x1b\\))*`

// promptGap matches what may sit between the line break (or the steer prompt "↳ ")
// and the ❯ of a fresh prompt: the OSC titles ConPTY writes (oscRun one sequence at
// a time, so the repetition never matches the empty string) and the carriage
// returns of a repaint. The line editor returns to column 0 before drawing a
// prompt whose predecessor started a different column, so after a turn the bytes
// can read "↳ \r❯" instead of "↳ ❯" - the same prompt on the same row (suga-itjl on
// a slow windows-latest runner: the screen said ready, the bytes did not match).
const promptGap = `(?:\r|\x1b\][^\a\x1b]*(?:\a|\x1b\\))*`

// freshPrompt is a prompt drawn on a row of its own: after a line break, or after
// the steer prompt of a turn that just ended (promptReady).
const freshPrompt = `(?:\n|↳ )` + promptGap + `❯`

// TestFreshPromptPattern keeps promptReady's pattern honest against the byte orders
// a repaint produces: the tail below is the one recorded on a windows-latest runner
// (suga-itjl) where the prompt WAS up but the pattern of that time missed it.
func TestFreshPromptPattern(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"a prompt on a fresh line", "❯ 你好\r\n▣ 完成\r\nfake/m1 · in 10 / out 2 tokens · ctx 0% · 326ms · 11:16\r\n❯ ", true},
		{"a prompt drawn over the steer prompt", "你好\r\n↳ ❯ ", true},
		{"a prompt after a repaint's carriage return (suga-itjl)", "你好\r\n↳ \r❯", true},
		{"a carriage return before the line break's prompt", "tokens · 1s\r\n\r❯ ", true},
		{"a console title between the break and the prompt", "tokens · 1s\r\n\x1b]0;Administrator: pwsh\x07❯ ", true},
		{"the turn is still running: bare steer prompt", "你好\r\n↳ ", false},
		{"the echo of the line just sent", "❯ echo hi", false},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			assert(t, c.name, regexp.MustCompile(freshPrompt).MatchString(c.text) == c.want)
		})
	}
}

var anySpace = regexp.MustCompile(`\s+`)

// unspaced is text with every run of whitespace removed, for matching a long line
// the terminal may have wrapped: CR LF, or CR CR LF while a running turn's read
// keeps output processing on, and ConPTY may add padding spaces at the wrap.
func unspaced(text string) string { return anySpace.ReplaceAllString(text, "") }

var chatSessionID = regexp.MustCompile(`session id: ((?:sess_|sg_|s_)\S+)`)

// str is v as a string when it is one ("" otherwise): trace attrs are untyped.
func str(v any) string {
	s, _ := v.(string)
	return s
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

// outputLine waits for pattern to be a whole line of the chat output: the local
// command's own output, not the echo of the line that was typed (the editor echoes
// that after the prompt). A line may end in extra CRs: a read with output
// processing back on adds one (the editor writes CR LF itself).
func outputLine(t *testing.T, s *ptytest.Session, name, pattern string, mark int) bool {
	t.Helper()
	// The line may start with OSC sequences (ESC ] ... BEL or ST): on the
	// windows-latest runner pwsh sets the console title ("Administrator: ...") and
	// ConPTY writes it on the output's line, which Text keeps. On a miss the text
	// and raw tail are logged so the cause shows in CI.
	ok := s.Expect(`(?m)^`+oscRun+pattern+`\r*$`, mark, 15*time.Second)
	check(t, name, ok)
	if !ok {
		t.Logf("%s: text tail %q", name, tail(s.Since(mark)))
		t.Logf("%s: raw tail %q", name, tail(s.RawText()))
	}
	return ok
}

// waitRequest polls the fake model's request log from index from on and returns
// the first request ok accepts (false when none arrives within timeout).
func waitRequest(f *fakeLLM, from int, ok func(fakeRequest) bool, timeout time.Duration) (fakeRequest, bool) {
	end := time.Now().Add(timeout)
	for {
		reqs := f.Requests()
		for i := from; i < len(reqs); i++ {
			if ok(reqs[i]) {
				return reqs[i], true
			}
		}
		if time.Now().After(end) {
			return fakeRequest{}, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sessionFromText is the session id named by the resume line chat prints on exit
// ("" when the text has none).
func sessionFromText(text string) string {
	if m := resumeID.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// sugaSend is a `suga send` that is safe to start from a goroutine (it never
// calls t.Fatal): it returns the exit code (124 on timeout) and stdout+stderr.
func sugaSend(binary, work string, env []string, timeout time.Duration, args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append([]string{"send"}, args...)...)
	cmd.Dir, cmd.Env = work, env
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return 124, string(out) + "timeout"
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		return -1, err.Error()
	}
	return 0, string(out)
}

// chatPID is the process id of the chat serving session, from `suga ps --json`
// (the session's run record). ptytest does not expose the child's pid.
func chatPID(t *testing.T, binary, work string, env []string, session string) int {
	t.Helper()
	out, _, _ := sugaRun(t, binary, work, env, "ps", "--json")
	var records []struct {
		PID       int    `json:"pid"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(out), &records); err != nil {
		t.Fatalf("suga ps --json: %v: %q", err, out)
	}
	for _, r := range records {
		if r.SessionID == session {
			return r.PID
		}
	}
	t.Fatalf("no chat process for session %s in %q", session, out)
	return 0
}

// killChat ends the chat process hard (SIGKILL on Unix, TerminateProcess on
// Windows): nothing in chat runs on the way out, so a session left running stays
// running, like after a crash. Session.Close is not the same on Windows: closing
// the pseudo console sends the process a close event and chat shuts down cleanly.
func killChat(t *testing.T, pid int) {
	t.Helper()
	proc, err := os.FindProcess(pid)
	if err == nil {
		err = proc.Kill()
	}
	if err != nil {
		t.Fatalf("kill chat %d: %v", pid, err)
	}
}

// pathForms is p and, when it differs, p with symlinks and Windows 8.3 short
// names resolved (filepath.EvalSymlinks expands both): a path the test built
// from a temp dir may be printed by suga in its canonical form.
func pathForms(p string) []string {
	forms := []string{p}
	if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
		forms = append(forms, real)
	}
	return forms
}
