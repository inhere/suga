package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/process"
)

// sendHarness runs `suga send` against one live local chat process: every
// delivered message lands on the channel as "<source>:<text>".
type sendHarness struct {
	session   string
	delivered chan string
}

func newSendHarness(t *testing.T, sessionID string) *sendHarness {
	t.Helper()
	return newNamedSendHarness(t, sessionID, "send test")
}

// newNamedSendHarness is newSendHarness with an explicit short name, the address
// `suga send --session <name>` accepts.
func newNamedSendHarness(t *testing.T, sessionID, name string) *sendHarness {
	t.Helper()
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	delivered := make(chan string, 8)
	server, err := process.Start(filepath.Join(home, "run"), sessionID, name, t.TempDir(), func(mode, text, from string) (process.Delivery, error) {
		delivered <- from + ":" + text
		return process.Delivery{}, nil
	})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = server.Close() })
	return &sendHarness{session: sessionID, delivered: delivered}
}

// run executes `suga send --session <session> <args...>` with the given stdin
// and returns the stderr output plus the command error.
func (h *sendHarness) run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmdArgs := append([]string{"send", "--session", h.session}, args...)
	err := newCLI(context.Background(), strings.NewReader(stdin), &stdout, &stderr).RunWithArgs(cmdArgs)
	return stderr.String(), err
}

// next returns the message the chat process received next.
func (h *sendHarness) next(t *testing.T) string {
	t.Helper()
	select {
	case text := <-h.delivered:
		return text
	default:
		t.Fatal("no message was delivered to the chat process")
		return ""
	}
}

func TestSendAcceptsPositionalText(t *testing.T) {
	h := newSendHarness(t, "session-pos")
	t.Run("single argument", func(t *testing.T) {
		stderr, err := h.run(t, "", "你好")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:你好", h.next(t))
		assert.Eq(t, "delivered\n", stderr)
	})
	t.Run("multiple arguments joined with one space", func(t *testing.T) {
		_, err := h.run(t, "", "你好", "世界", "!")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:你好 世界 !", h.next(t))
	})
	t.Run("one argument keeps its inner spacing", func(t *testing.T) {
		_, err := h.run(t, "", "hello   world")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:hello   world", h.next(t))
	})
	t.Run("options may follow the text", func(t *testing.T) {
		_, err := h.run(t, "", "hi", "--source", "web-ui")
		assert.NoErr(t, err)
		assert.Eq(t, "web-ui:hi", h.next(t))
	})
	t.Run("dash text needs the -- separator", func(t *testing.T) {
		_, err := h.run(t, "", "--", "-hello")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:-hello", h.next(t))
	})
	t.Run("dash text without -- fails and sends nothing", func(t *testing.T) {
		_, err := h.run(t, "", "-hello")
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "-hello")
		assert.Eq(t, 1, ExitCode(err))
		assert.Eq(t, 0, len(h.delivered))
	})
}

func TestSendRejectsArgumentsTogetherWithText(t *testing.T) {
	h := newSendHarness(t, "session-both")
	t.Run("arguments and text", func(t *testing.T) {
		_, err := h.run(t, "", "hello", "world", "--text", "hi")
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 1, ExitCode(err))
		assert.StrContains(t, err.Error(), "mutually exclusive")
		assert.Eq(t, 0, len(h.delivered))
	})
	t.Run("text between arguments", func(t *testing.T) {
		_, err := h.run(t, "", "--text", "hi", "hello")
		assert.Err(t, err)
		assert.Eq(t, 1, ExitCode(err))
		assert.Eq(t, 0, len(h.delivered))
	})
	t.Run("blank argument is not a conflict", func(t *testing.T) {
		_, err := h.run(t, "", "", "--text", "hi")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:hi", h.next(t))
	})
}

func TestSendTextSourceFallbacks(t *testing.T) {
	h := newSendHarness(t, "session-src")
	t.Run("--text still works", func(t *testing.T) {
		_, err := h.run(t, "ignored stdin", "--text", "hi")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:hi", h.next(t))
	})
	t.Run("no text and no arguments reads stdin", func(t *testing.T) {
		stderr, err := h.run(t, "from stdin")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:from stdin", h.next(t))
		assert.Eq(t, "delivered\n", stderr)
	})
	t.Run("blank arguments fall back to stdin", func(t *testing.T) {
		_, err := h.run(t, "from stdin", "", " ")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:from stdin", h.next(t))
	})
	t.Run("session is required before stdin is read", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), unreadableStdin{}, &stdout, &stderr).RunWithArgs([]string{"send"})
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "--session is required")
	})
}

// unreadableStdin fails with a distinct message: `suga send` without --session
// must report the missing option instead of blocking on stdin.
type unreadableStdin struct{}

func (unreadableStdin) Read([]byte) (int, error) {
	return 0, errors.New("stdin must not be read without --session")
}

func TestSendResolvesByShortName(t *testing.T) {
	h := newNamedSendHarness(t, "sess_abc123", "ws-k7")
	t.Run("--session takes the short name from suga ps", func(t *testing.T) {
		h.session = "ws-k7"
		_, err := h.run(t, "", "你好")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:你好", h.next(t))
	})
	t.Run("the short name matches without case", func(t *testing.T) {
		h.session = "WS-K7"
		_, err := h.run(t, "", "你好")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:你好", h.next(t))
	})
	t.Run("the session ID still wins over a short name", func(t *testing.T) {
		h.session = "sess_abc123"
		_, err := h.run(t, "", "你好")
		assert.NoErr(t, err)
		assert.Eq(t, "cli:你好", h.next(t))
	})
	t.Run("an unknown short name exits 3", func(t *testing.T) {
		h.session = "other-aa"
		_, err := h.run(t, "", "你好")
		assert.Eq(t, 3, ExitCode(err))
	})
}

func TestSendHelpShowsBothTextForms(t *testing.T) {
	t.Setenv("SUGA_HOME", shortCLIHome(t))
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"send", "--help"})
	assert.NoErr(t, err)
	help := stderr.String()
	assert.StrContains(t, help, "suga send --session <id|name> [--text <text> | <text>...]")
	assert.StrContains(t, help, "suga send --session <id|name> hello world")
	assert.StrContains(t, help, "suga send --session <id|name> -- -hello")
	assert.StrContains(t, help, "arguments cannot be combined with --text")
	// --session documents the short names `suga ps` lists.
	assert.StrContains(t, help, "suga send --session suga-k7 hello world")
	assert.StrContains(t, help, "short name of a")
}
