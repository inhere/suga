//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty"
	"github.com/gookit/gopty/ptytest"
)

// chatOpts describes one chat process the way scripts/ptysmoke/chat_approval.py
// write_home did: the fake provider is "fake/m1" and every extra goes verbatim
// into the config (Extra at the top level, the others under their own key).
type chatOpts struct {
	Home, Work, URL string
	ToolsExtra      string   // lines under tools: (two-space indented)
	MemoryExtra     string   // lines under memory:
	ProviderExtra   string   // lines under providers.fake:
	Extra           string   // top-level YAML appended after memory:
	Color           bool     // leave NO_COLOR unset so chat renders colors
	DropEnv         []string // inherited variables to remove (e.g. COLORFGBG)
	SetEnv          []string // KEY=VALUE appended last (e.g. SUGA_HOOKS=on)
}

// startChatOpts writes SUGA_HOME/config.yaml and starts the real chat in a pty.
// It returns the session and the child's environment (for `suga inspect trace`).
// Unlike startChatLanguage it does not switch off markdown, spinner or history:
// these scenarios run the defaults a person sees.
func startChatOpts(t *testing.T, binary string, o chatOpts) (*ptytest.Session, []string) {
	t.Helper()
	if err := os.MkdirAll(o.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`language: zh
model: fake/m1
providers:
  fake:
    type: openai
    base_url: %s
    api_key: k
%stools:
  allow_write: true
%smemory:
  project:
    backend: files
%s%s`, o.URL, o.ProviderExtra, o.ToolsExtra, o.MemoryExtra, o.Extra)
	if err := os.WriteFile(filepath.Join(o.Home, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	// An empty .env marks a home that was already set up (chat_onboarding covers
	// first runs); otherwise the first run prints a notice.
	if err := os.WriteFile(filepath.Join(o.Home, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, entry := range testEnv(o.Home) {
		key, _, _ := strings.Cut(entry, "=")
		if o.Color && key == "NO_COLOR" || containsString(o.DropEnv, key) {
			continue
		}
		// A variable the scenario sets itself replaces the default, never doubles.
		overridden := false
		for _, set := range o.SetEnv {
			if setKey, _, _ := strings.Cut(set, "="); setKey == key {
				overridden = true
			}
		}
		if overridden {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, o.SetEnv...)
	spec := gopty.Spec{Command: binary, Args: []string{"chat"}, Dir: o.Work, Env: env, Cols: 100, Rows: 40}
	return ptytest.StartSpec(t, spec), env
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// check records one named assertion without stopping the scenario, the way the
// Python scripts' s.check did: every item of a script is reported on its own.
func check(t *testing.T, name string, ok bool) {
	t.Helper()
	if !ok {
		t.Errorf("FAIL %s", name)
	}
}

// expectWithin is expect with a timeout of its own (idle consolidation waits
// longer than the usual 15s allows for a slow step).
func expectWithin(t *testing.T, s *ptytest.Session, pattern string, mark int, timeout time.Duration) {
	t.Helper()
	if !s.Expect(pattern, mark, timeout) {
		t.Fatalf("expected %q within %s; terminal tail:\n%s", pattern, timeout, tail(s.Text()))
	}
}

var resumeID = regexp.MustCompile(`--resume ((?:sess_|sg_|s_)\S+)`)

// ctrlCTwice presses Ctrl-C once for the hint and once more to leave, checks the
// resume line and returns the session id it names.
func ctrlCTwice(t *testing.T, s *ptytest.Session) string {
	t.Helper()
	time.Sleep(500 * time.Millisecond)
	mark := s.Mark()
	s.SendCtrlC()
	check(t, "ctrl-c once: hint", s.Expect(`再按一次 Ctrl\+C 退出`, mark, 15*time.Second))
	mark = s.Mark()
	s.SendCtrlC()
	expect(t, s, `续接：suga chat --resume (?:sess_|sg_|s_)`, mark)
	id := ""
	if m := resumeID.FindStringSubmatch(s.Since(mark)); m != nil {
		id = m[1]
	}
	waitExit(t, s)
	return id
}

// traceEvents runs `suga inspect trace <id> --json` and parses its JSON lines.
func traceEvents(t *testing.T, binary, work string, env []string, session string) []map[string]any {
	t.Helper()
	cmd := exec.Command(binary, "inspect", "trace", session, "--json")
	cmd.Dir, cmd.Env = work, env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("inspect trace: %v", err)
	}
	var events []map[string]any
	for _, line := range strings.Split(string(out), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) == nil {
			events = append(events, event)
		}
	}
	return events
}

// attrs is the attrs object of a trace event (nil-safe).
func attrs(event map[string]any) map[string]any {
	a, _ := event["attrs"].(map[string]any)
	return a
}

// findEvent returns the first event of the kind (and name, when given).
func findEvent(events []map[string]any, kind, name string) map[string]any {
	for _, e := range events {
		if e["kind"] == kind && (name == "" || e["name"] == name) {
			return e
		}
	}
	return nil
}

func regexpMatch(pattern, text string) bool { return regexp.MustCompile(pattern).MatchString(text) }

// rawContains waits for pattern in the raw output (escapes kept) after rawMark,
// an offset taken from len(s.RawText()).
func rawContains(s *ptytest.Session, pattern string, rawMark int, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if raw := s.RawText(); rawMark <= len(raw) && regexpMatch(pattern, raw[rawMark:]) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func expectRaw(t *testing.T, s *ptytest.Session, pattern string, rawMark int) {
	t.Helper()
	if !rawContains(s, pattern, rawMark, 15*time.Second) {
		t.Fatalf("expected raw %q; raw tail: %q", pattern, tail(s.RawText()))
	}
}
