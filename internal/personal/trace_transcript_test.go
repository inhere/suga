package personal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// transcriptText renders entries through the real WriteTranscript entry point.
func transcriptText(t *testing.T, entries []store.Entry, opts TranscriptOptions) string {
	t.Helper()
	var b strings.Builder
	assert.NoErr(t, WriteTranscript(&b, entries, opts))
	return b.String()
}

// transcriptMessage is one stored message entry (seq is what a marker points at).
func transcriptMessage(t *testing.T, seq int64, typ, sessionID string, m agentcore.Message) store.Entry {
	t.Helper()
	raw, err := store.EncodeMessage(m)
	assert.Require(t, assert.NoErr(t, err))
	return store.Entry{SessionID: sessionID, Seq: seq, Type: typ, JSON: raw}
}

// transcriptPosition asserts that the labels appear in the given order.
func transcriptPosition(t *testing.T, out string, labels ...string) {
	t.Helper()
	prev := -1
	for _, label := range labels {
		at := strings.Index(out, label)
		assert.Require(t, assert.True(t, at >= 0, "missing block %q in:\n%s", label, out))
		assert.Require(t, assert.True(t, at > prev, "block %q is out of order in:\n%s", label, out))
		prev = at
	}
}

func TestTranscriptRendersRoleBlocksInOrder(t *testing.T) {
	entries := []store.Entry{
		transcriptMessage(t, 1, store.EntryUser, "s_x", agentcore.Message{Role: "user", Content: "读一下 a.go"}),
		transcriptMessage(t, 2, store.EntryAssistant, "s_x", agentcore.Message{Role: "assistant", Content: "先读文件", Reasoning: "需要先看内容"}),
		transcriptMessage(t, 3, store.EntryAssistant, "s_x", agentcore.Message{Role: "assistant",
			ToolCall: &agentcore.ToolCall{CallID: "c1", Name: "file_read", Arguments: map[string]any{"paths": []any{"a.go", "b.go"}}}}),
		// No Name on the result: the name of the call it answers labels it.
		transcriptMessage(t, 4, store.EntryTool, "s_x", agentcore.Message{Role: "tool",
			ToolResult: &agentcore.ToolResult{CallID: "c1", Content: "package a"}}),
		transcriptMessage(t, 5, store.EntryTool, "s_x", agentcore.Message{Role: "tool",
			ToolResult: &agentcore.ToolResult{CallID: "c2", Name: "shell_exec", IsError: true, Content: "exit 1"}}),
		{SessionID: "s_x", Seq: 6, Type: store.EntryCompaction,
			JSON: json.RawMessage(`{"summary":"前文摘要","first_kept_seq":3,"tokens_before":1200,"tokens_after":300}`)},
		{SessionID: "s_x", Seq: 7, Type: store.EntryContextUpdate,
			JSON: json.RawMessage(`{"rules":"rule text","append_prompt":"p"}`)},
		transcriptMessage(t, 8, store.EntryUser, "s_x", agentcore.Message{Role: "user", Content: "看图",
			Images: []agentcore.ImagePart{{Ref: "images/ab.png", Width: 40, Height: 30}}}),
	}
	out := transcriptText(t, entries, TranscriptOptions{SessionID: "s_x"})

	transcriptPosition(t, out, "[user]", "[reasoning]", "[assistant]", "[assistant → tool_call file_read]",
		"[tool_result file_read ok]", "[tool_result shell_exec error]", "[compaction summary]",
		"[context update]", "[image: images/ab.png 40×30]")
	assert.StrContains(t, out, "# transcript of session s_x")
	assert.StrContains(t, out, "读一下 a.go")
	assert.StrContains(t, out, "先读文件")
	assert.StrContains(t, out, `{"paths":["a.go","b.go"]}`)
	assert.StrContains(t, out, "package a")
	assert.StrContains(t, out, "exit 1")
	assert.StrContains(t, out, "前文摘要")
	// One block per changed part, named with its size: the text itself repeats in
	// the segment messages that follow.
	assert.StrContains(t, out, "changed: rules 9B, append_prompt 1B")
	assert.Eq(t, 1, strings.Count(out, "[tool_result file_read ok]"))
}

func TestTranscriptReasoningToggle(t *testing.T) {
	entries := []store.Entry{
		transcriptMessage(t, 1, store.EntryAssistant, "s_x", agentcore.Message{Role: "assistant", Content: "答案", Reasoning: "先想一步"}),
	}
	t.Run("reasoning is rendered by default", func(t *testing.T) {
		out := transcriptText(t, entries, TranscriptOptions{SessionID: "s_x"})
		assert.StrContains(t, out, "[reasoning]\n先想一步\n")
		transcriptPosition(t, out, "[reasoning]", "[assistant]")
	})
	t.Run("--no-reasoning leaves the block out", func(t *testing.T) {
		out := transcriptText(t, entries, TranscriptOptions{SessionID: "s_x", NoReasoning: true})
		assert.StrNotContains(t, out, "[reasoning]")
		assert.StrNotContains(t, out, "先想一步")
		assert.StrContains(t, out, "[assistant]\n答案")
	})
}

func TestTranscriptObservationTruncation(t *testing.T) {
	render := func(t *testing.T, content string, opts TranscriptOptions) string {
		t.Helper()
		entries := []store.Entry{transcriptMessage(t, 1, store.EntryTool, "s_x", agentcore.Message{Role: "tool",
			ToolResult: &agentcore.ToolResult{CallID: "c1", Name: "shell_exec", Content: content}})}
		opts.SessionID = "s_x"
		return transcriptText(t, entries, opts)
	}
	t.Run("a result at the limit stays whole", func(t *testing.T) {
		out := render(t, strings.Repeat("z", DefaultTranscriptObservation), TranscriptOptions{})
		assert.StrNotContains(t, out, "transcript renderer")
		assert.Eq(t, DefaultTranscriptObservation, strings.Count(out, "z"))
	})
	t.Run("a longer result is cut and the renderer says so", func(t *testing.T) {
		out := render(t, strings.Repeat("z", 12000), TranscriptOptions{})
		assert.StrContains(t, out, "…[transcript renderer: 截去 2000 字节，"+
			"完整内容见 suga inspect trace s_x --json 或 ref entry:s_x:1]")
		assert.Eq(t, DefaultTranscriptObservation, strings.Count(out, "z"))
	})
	t.Run("the cut never splits a multi-byte character", func(t *testing.T) {
		out := render(t, strings.Repeat("工", 4000), TranscriptOptions{}) // 12000 bytes
		assert.True(t, utf8.ValidString(out), "output is not valid UTF-8")
		// 10000 lands inside a 3-byte rune: the cut falls back to 9999 bytes.
		body := out[strings.Index(out, "[tool_result shell_exec ok]")+len("[tool_result shell_exec ok]"):]
		assert.Eq(t, 3333, strings.Count(body, "工"))
		assert.StrContains(t, out, "截去 2001 字节")
	})
	t.Run("--max-observation raises the limit", func(t *testing.T) {
		out := render(t, strings.Repeat("z", 12000), TranscriptOptions{MaxObservation: 20000})
		assert.StrNotContains(t, out, "transcript renderer")
		assert.Eq(t, 12000, strings.Count(out, "z"))
	})
	t.Run("user and assistant text are never cut", func(t *testing.T) {
		big := strings.Repeat("b", 20000)
		entries := []store.Entry{
			transcriptMessage(t, 1, store.EntryUser, "s_x", agentcore.Message{Role: "user", Content: big}),
			transcriptMessage(t, 2, store.EntryAssistant, "s_x", agentcore.Message{Role: "assistant", Content: big}),
		}
		out := transcriptText(t, entries, TranscriptOptions{SessionID: "s_x"})
		assert.StrNotContains(t, out, "transcript renderer")
		assert.Eq(t, 40000, strings.Count(out, "b"))
	})
}

func TestTranscriptRejectsShortObservationLimit(t *testing.T) {
	entries := []store.Entry{transcriptMessage(t, 1, store.EntryUser, "s_x", agentcore.Message{Role: "user", Content: "hi"})}
	var b strings.Builder
	err := WriteTranscript(&b, entries, TranscriptOptions{SessionID: "s_x", MaxObservation: MinTranscriptObservation - 1})
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), "below the 10000 byte minimum")
	assert.Eq(t, "", b.String())
}

func TestTranscriptRestoresMovedBodies(t *testing.T) {
	// The store moves a tool body above the threshold into the session file; the
	// transcript must show the text, not the stub the row keeps.
	repo, err := store.Open(newStorePath(t), store.WithToolBodyInlineBytes(1024))
	assert.Require(t, assert.NoErr(t, err))
	defer repo.Close()
	ctx := context.Background()
	assert.Require(t, assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "s_move"})))
	content := strings.Repeat("回显 ", 1000) // 5000 bytes, far above the threshold
	_, err = repo.AppendEntries(ctx, "s_move", store.NewEntry{Type: store.EntryTool,
		JSON: mustMessageJSON(t, agentcore.Message{Role: "tool", ToolResult: &agentcore.ToolResult{
			CallID: "c1", Name: "shell_exec", Content: content}})})
	assert.Require(t, assert.NoErr(t, err))

	entries, err := repo.ListEntries(ctx, "s_move", store.EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	out := transcriptText(t, entries, TranscriptOptions{SessionID: "s_move"})
	assert.StrContains(t, out, "[tool_result shell_exec ok]")
	assert.Eq(t, 1000, strings.Count(out, "回显 "))
}

// mustMessageJSON encodes a message the way the store stores it.
func mustMessageJSON(t *testing.T, m agentcore.Message) json.RawMessage {
	t.Helper()
	raw, err := store.EncodeMessage(m)
	assert.Require(t, assert.NoErr(t, err))
	return raw
}
