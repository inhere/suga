package personal

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// captureDefaultLogger points the process logger at a buffer for one test and
// returns it; the previous logger is restored afterwards.
func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// TestTurnLoggerCarriesSessionAndTurn: records of a turn carry session_id and
// turn_id, so a log line can be lined up with the session store.
func TestTurnLoggerCarriesSessionAndTurn(t *testing.T) {
	buf := captureDefaultLogger(t)

	ctx := withTurnLogger(context.Background(), "sess-1", "turn-1")
	turnLogger(ctx).Warn("tool call failed", "tool", "read")

	line := buf.String()
	assert.StrContains(t, line, "tool call failed")
	assert.StrContains(t, line, "session_id=sess-1")
	assert.StrContains(t, line, "turn_id=turn-1")

	buf.Reset()
	// Outside a turn the process logger stays untouched.
	turnLogger(context.Background()).Warn("outside a turn")
	assert.Eq(t, false, strings.Contains(buf.String(), "session_id="))
}

// TestToolEventHookLogsWithTurnScope: the tool event hook is on the turn path,
// so its warning line carries the turn's session and turn id.
func TestToolEventHookLogsWithTurnScope(t *testing.T) {
	buf := captureDefaultLogger(t)
	hook := toolEventHook{
		sink:      func(context.Context, Event) error { return errors.New("sink closed") },
		sessionID: "sess-2", turnID: "turn-2", runID: "turn-2",
	}

	ctx := withTurnLogger(context.Background(), "sess-2", "turn-2")
	hook.publish(ctx, Event{Kind: "tool_result", ToolName: "read"})

	line := buf.String()
	assert.StrContains(t, line, "emit tool event")
	assert.StrContains(t, line, "session_id=sess-2")
	assert.StrContains(t, line, "turn_id=turn-2")
	assert.StrContains(t, line, "tool=read")
}
