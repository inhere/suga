package output

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/llm"
)

func TestResultLineKeepsEmptyResult(t *testing.T) {
	line := ResultLine("s", personal.RunResult{RunID: "r", State: personal.RunCompleted, Output: ""})
	assert.Eq(t, "result", line["type"])
	assert.Eq(t, "", line["result"])
}

func TestEventLineToolEvents(t *testing.T) {
	t.Run("tool_call carries name and args summary", func(t *testing.T) {
		line, ok := EventLine(personal.Event{Kind: "tool_call", SessionID: "s", RunID: "r", ToolName: "file_read", Text: `{"path":"a.txt"}`})
		assert.True(t, ok)
		assert.Eq(t, map[string]any{
			"session_id": "s", "run_id": "r", "type": "tool_call", "name": "file_read", "args": `{"path":"a.txt"}`,
		}, line)
	})

	t.Run("tool_result summary is redacted before truncation", func(t *testing.T) {
		secret := "sk-abcdefghijklmnop"
		// The secret sits right at the truncation boundary: truncating first would leak a prefix.
		content := strings.Repeat("x", toolSummaryLimit-12) + " api_key=" + secret + " tail"
		cut, _ := EventLine(personal.Event{Kind: "tool_result", Text: content})
		assert.StrNotContains(t, cut["summary"].(string), "sk-")
		content = "api_key=" + secret + " " + strings.Repeat("x", 300)
		line, ok := EventLine(personal.Event{Kind: "tool_result", ToolName: "shell_exec", Text: content, IsError: true})
		assert.True(t, ok)
		assert.Eq(t, "tool_result", line["type"])
		assert.Eq(t, "shell_exec", line["name"])
		assert.Eq(t, true, line["is_error"])
		summary := line["summary"].(string)
		assert.StrNotContains(t, summary, "sk-")
		assert.StrContains(t, summary, "api_key=[REDACTED] xxx")
		assert.True(t, len([]rune(summary)) <= toolSummaryLimit)
	})

	t.Run("long result is truncated and collapsed to one line", func(t *testing.T) {
		got := ToolResultSummary("a\n\nb " + strings.Repeat("y", 500))
		assert.StrNotContains(t, got, "\n")
		assert.Eq(t, toolSummaryLimit, len([]rune(got)))
		assert.True(t, strings.HasSuffix(got, "…"))
	})
}

// TestVerifyLines covers the stream-json shape of the --verify delivery check:
// one "verify" line per run of the command, and the outcome on the closing
// result line (a run without --verify keeps that line as it was).
func TestVerifyLines(t *testing.T) {
	t.Run("a check line names the round, the command and the exit code", func(t *testing.T) {
		line := VerifyLine(1, "go test ./...", 1, false, 12300*time.Millisecond)
		assert.Eq(t, map[string]any{
			"type": "verify", "round": 1, "command": "go test ./...", "exit_code": 1,
			"passed": false, "duration_ms": int64(12300),
		}, line)
	})
	t.Run("the result line carries the outcome", func(t *testing.T) {
		line := ResultLine("s", personal.RunResult{RunID: "r", State: personal.RunCompleted, Output: "ok"})
		_, has := line["verify"]
		assert.False(t, has)
		line = WithVerify(line, true, 2)
		assert.Eq(t, map[string]any{"passed": true, "rounds": 2}, line["verify"])
	})
}

// TestErrorLineErrorKind: a failed turn names the kind of provider failure it hit
// (transient: a cut stream whose retries ran out; bad response: an unusable
// answer), so a caller such as gofer can decide whether to continue the session.
// A turn that failed for another reason carries no field.
func TestErrorLineErrorKind(t *testing.T) {
	runErr := errors.New("provider error: read openai stream: stream truncated: unexpected EOF")
	t.Run("a provider failure carries its kind", func(t *testing.T) {
		line := ErrorLine("s", runErr, personal.RunResult{ErrorKind: "provider_transient"})
		assert.Eq(t, "error", line["type"])
		assert.Eq(t, "provider_transient", line["error_kind"])
		assert.Eq(t, runErr.Error(), line["error"])
	})
	t.Run("another failure leaves the field out", func(t *testing.T) {
		line := ErrorLine("s", errors.New("boom"), personal.RunResult{})
		_, ok := line["error_kind"]
		assert.Eq(t, false, ok)
		_, ok = line["reset_at"]
		assert.Eq(t, false, ok)
	})
	t.Run("an exhausted quota carries its reset time", func(t *testing.T) {
		quota := fmt.Errorf("provider error: %w", llm.NewQuotaError(errors.New("weekly usage limit"), "2026-10-19T06:50:37Z"))
		line := ErrorLine("s", quota, personal.RunResult{ErrorKind: llm.ErrorKindQuota})
		assert.Eq(t, "provider_quota", line["error_kind"])
		assert.Eq(t, "2026-10-19T06:50:37Z", line["reset_at"])
	})
}
