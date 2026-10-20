package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// traceTranscriptFixture writes a session with the entries the transcript
// renders: a context update, a turn (user, assistant with reasoning and a tool
// call, a long tool result), a segment summary and a second turn. It also
// creates a session with the older id shape.
func traceTranscriptFixture(t *testing.T) *store.Store {
	t.Helper()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	assert.Require(t, assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "s_tx", Status: store.SessionOpen})))
	assert.Require(t, assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "sess_20260101000000001", Status: store.SessionOpen})))

	marshal := func(v any) json.RawMessage {
		raw, merr := json.Marshal(v)
		assert.Require(t, assert.NoErr(t, merr))
		return raw
	}
	long := strings.Repeat("z", 12000)
	_, err = repo.AppendEntries(ctx, "s_tx",
		store.NewEntry{TurnID: "t1", Type: store.EntryContextUpdate,
			JSON: json.RawMessage(`{"rules":"rule text","append_prompt":"p"}`)},
		store.NewEntry{TurnID: "t1", Type: store.EntryUser,
			JSON: marshal(map[string]any{"Role": "user", "Content": "第一轮的问题"})},
		store.NewEntry{TurnID: "t1", Type: store.EntryAssistant,
			JSON: marshal(map[string]any{"Role": "assistant", "Content": "先读文件", "Reasoning": "需要先看内容"})},
		store.NewEntry{TurnID: "t1", Type: store.EntryAssistant,
			JSON: marshal(map[string]any{"Role": "assistant", "ToolCall": map[string]any{
				"CallID": "c1", "Name": "file_read", "Arguments": map[string]any{"paths": []string{"a.go"}}}})},
		store.NewEntry{TurnID: "t1", Type: store.EntryTool,
			JSON: marshal(map[string]any{"Role": "tool", "ToolResult": map[string]any{
				"CallID": "c1", "Name": "file_read", "Content": long}})},
		store.NewEntry{TurnID: "t1", Type: store.EntryCompaction,
			JSON: json.RawMessage(`{"summary":"上一段摘要","first_kept_seq":2,"tokens_before":900,"tokens_after":200}`)},
		store.NewEntry{TurnID: "t2", Type: store.EntryUser,
			JSON: marshal(map[string]any{"Role": "user", "Content": "第二轮的问题"})},
	)
	assert.Require(t, assert.NoErr(t, err))
	_, err = repo.AppendEntries(ctx, "sess_20260101000000001",
		store.NewEntry{TurnID: "t9", Type: store.EntryUser,
			JSON: json.RawMessage(`{"Role":"user","Content":"旧会话"}`)})
	assert.Require(t, assert.NoErr(t, err))
	return repo
}

// TestTraceTranscriptCommand runs the real CLI path (`inspect trace
// --transcript`) against an isolated SUGA_HOME.
func TestTraceTranscriptCommand(t *testing.T) {
	t.Setenv("SUGA_HOME", shortCLIHome(t))
	traceTranscriptFixture(t)
	ctx := context.Background()
	run := func(args ...string) (string, error) {
		var stdout, stderr bytes.Buffer
		err := newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs(args)
		return stdout.String(), err
	}

	t.Run("blocks in stored order", func(t *testing.T) {
		out, err := run("inspect", "trace", "s_tx", "--transcript")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "# transcript of session s_tx")
		prev := -1
		for _, label := range []string{"[context update]", "[user]\n第一轮的问题", "[reasoning]\n需要先看内容",
			"[assistant]\n先读文件", "[assistant → tool_call file_read]\n{\"paths\":[\"a.go\"]}",
			"[tool_result file_read ok]", "[compaction summary]", "[user]\n第二轮的问题"} {
			at := strings.Index(out, label)
			assert.Require(t, assert.True(t, at > prev, "missing or out of order: %q", label))
			prev = at
		}
		// The long observation is cut by the renderer, and the marker names it.
		assert.StrContains(t, out, "…[transcript renderer: 截去 2000 字节，完整内容见 suga inspect trace s_tx --json 或 ref entry:s_tx:5]")
	})

	t.Run("--turn narrows the transcript", func(t *testing.T) {
		out, err := run("inspect", "trace", "s_tx", "--transcript", "--turn", "t2")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "第二轮的问题")
		assert.StrContains(t, out, "turn t2")
		assert.StrNotContains(t, out, "第一轮的问题")
		assert.StrNotContains(t, out, "[tool_result file_read ok]")
	})

	t.Run("--no-reasoning leaves the reasoning blocks out", func(t *testing.T) {
		out, err := run("inspect", "trace", "s_tx", "--transcript", "--no-reasoning")
		assert.NoErr(t, err)
		assert.StrNotContains(t, out, "[reasoning]")
		assert.StrNotContains(t, out, "需要先看内容")
		assert.StrContains(t, out, "[assistant]\n先读文件")
	})

	t.Run("a limit below the minimum is refused", func(t *testing.T) {
		_, err := run("inspect", "trace", "s_tx", "--transcript", "--max-observation", "9000")
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "--max-observation 9000 is below the 10000 byte minimum")
	})

	t.Run("the formats are mutually exclusive", func(t *testing.T) {
		_, err := run("inspect", "trace", "s_tx", "--transcript", "--json")
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "use one of them")
		_, err = run("inspect", "trace", "s_tx", "--transcript", "--analyze")
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "use one of them")
		_, err = run("inspect", "trace", "s_tx", "--transcript", "--web")
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "use one of them")
	})

	t.Run("-o writes the file instead of stdout", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "transcript.txt")
		out, err := run("inspect", "trace", "s_tx", "--transcript", "-o", path)
		assert.NoErr(t, err)
		assert.Eq(t, "", out)
		data, err := os.ReadFile(path)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, string(data), "[tool_result file_read ok]")
	})

	t.Run("the older session id shape works", func(t *testing.T) {
		out, err := run("inspect", "trace", "sess_20260101000000001", "--transcript")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "# transcript of session sess_20260101000000001")
		assert.StrContains(t, out, "[user]\n旧会话")
	})
}
