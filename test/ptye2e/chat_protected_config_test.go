//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty"
	"github.com/gookit/gopty/ptytest"
)

// The protected settings notice of plan 2026-11-12 T5, against the real binary:
// the banner reports the keys another writer changed, once, and `config set`
// takes a protected key from a terminal while a piped stdin is refused.

// protectedNoticeHead is the start of the zh notice line ("安全相关配置自上次确认
// 以来有变化：…"), enough to tell "a notice was shown" from "no notice".
const protectedNoticeHead = "安全相关配置自上次确认以来有变化"

// protectedReadRoots is the protected key the scenarios change from outside.
const protectedReadRoots = "tools.read_roots"

// TestChatProtectedConfigBanner: chat's banner names the protected settings that
// changed since the user last confirmed them, and only once. The first start
// creates the snapshot of a config without tools.read_roots and shows nothing;
// the second start writes a config that has it (startChatOpts rewrites the file,
// i.e. a change made outside suga), shows the notice, and its first turn records
// one kind=config trace event; the third start rewrites the same config, which
// the second start's banner confirmed, so nothing is reported again.
func TestChatProtectedConfigBanner(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 100 * time.Millisecond, Reply: "好的"})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	// Two-space indented lines under tools:, the way chatOpts.ToolsExtra is placed.
	extra := "  read_roots:\n    - .\n"

	first, _ := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL})
	mark := first.Mark()
	until(t, first, "first start: prompt", `❯ `, mark)
	check(t, "first start: no notice", !strings.Contains(unspaced(first.Since(mark)), protectedNoticeHead))
	exitChat(t, first)

	second, env := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, ToolsExtra: extra})
	mark = second.Mark()
	until(t, second, "second start: prompt", `❯ `, mark)
	check(t, "second start: notice shown", strings.Contains(unspaced(second.Since(mark)), protectedNoticeHead))
	check(t, "second start: notice names the changed key",
		strings.Contains(unspaced(second.Since(mark)), protectedReadRoots))
	// One turn, so the session's trace has the config event of a turn that ran on
	// the changed settings.
	turn := second.Mark()
	second.SendLine("hi")
	until(t, second, "second start: reply", `好的`, turn)
	id := exitAfterTurn(t, second, turn)
	check(t, "second start: session id", id != "")
	if id == "" {
		return
	}

	events := traceEvents(t, binary, work, env, id)
	configs := 0
	for _, e := range events {
		if e["kind"] == "config" {
			configs++
		}
	}
	check(t, "trace: one kind=config event", configs == 1)
	keys, _ := attrs(findEvent(events, "config", ""))["keys"].([]any)
	named := false
	for _, key := range keys {
		if key == protectedReadRoots {
			named = true
		}
	}
	check(t, "trace: the event names the changed key", named)

	third, _ := startChatOpts(t, binary, chatOpts{Home: home, Work: work, URL: server.URL, ToolsExtra: extra})
	mark = third.Mark()
	until(t, third, "third start: prompt", `❯ `, mark)
	check(t, "third start: the notice is not repeated",
		!strings.Contains(unspaced(third.Since(mark)), protectedNoticeHead))
	exitChat(t, third)
}

// TestConfigSetProtectedInTerminal: a changed protected key is writable from a
// terminal (`suga config set` in a pty) and refused when stdin is a pipe (the
// model running it through a shell tool): exit code 2, the refusal message, and
// the config file keeps the value the terminal wrote.
func TestConfigSetProtectedInTerminal(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 100 * time.Millisecond})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// The scenario writes the home itself: `config set` needs no chat fixture, and
	// an empty .env marks a home that was already set up.
	config := "language: zh\nmodel: fake/m1\nproviders:\n  fake:\n    type: openai\n    base_url: " +
		server.URL + "\n    api_key: k\ntools:\n  allow_write: true\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := testEnv(home)
	configPath := filepath.Join(home, "config.yaml")

	spec := gopty.Spec{
		Command: binary,
		Args:    []string{"config", "set", protectedReadRoots, `["../shared"]`},
		Dir:     work,
		Env:     env,
		Cols:    100,
		Rows:    40,
	}
	s := ptytest.StartSpec(t, spec)
	mark := s.Mark()
	until(t, s, "terminal: the write is reported", `已写入`, mark)
	waitExit(t, s)

	stdout, _, code := sugaRun(t, binary, work, env, "config", "get", protectedReadRoots)
	check(t, "terminal: the value is stored", code == 0 && strings.Contains(stdout, "../shared"))

	_, stderr, code := sugaRun(t, binary, work, env, "config", "set", protectedReadRoots, `["../piped"]`)
	check(t, "piped stdin: exit code 2", code == 2)
	check(t, "piped stdin: the message names the key", strings.Contains(stderr, protectedReadRoots))
	check(t, "piped stdin: the message names the terminal rule",
		strings.Contains(stderr, "只能由你在 suga 之外的终端里修改"))
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "piped stdin: the config file keeps the terminal's value",
		strings.Contains(string(data), "../shared") && !strings.Contains(string(data), "../piped"))
}
