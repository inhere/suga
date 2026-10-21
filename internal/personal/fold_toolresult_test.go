package personal

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
)

// foldCallStep builds one assistant tool step and its tool result of chars
// characters (a tool message carries its own result message).
func foldCallStep(msgs []agentcore.Message, name string, args map[string]any, chars int) []agentcore.Message {
	result := strings.Repeat("r", chars)
	call := &agentcore.ToolCall{Name: name, Arguments: args}
	return append(msgs,
		agentcore.Message{Role: "assistant", Reasoning: "think", ToolCall: call},
		agentcore.Message{Role: "tool", Content: result, ToolResult: &agentcore.ToolResult{
			Name: name, Content: result}})
}

// foldCallSteps is a user message followed by one step per call: the name and
// arguments, each result foldArgChars+1 characters long.
func foldCallSteps(calls ...foldStepArgs) []agentcore.Message {
	msgs := []agentcore.Message{{Role: "user", Content: "task"}}
	for _, c := range calls {
		msgs = foldCallStep(msgs, c.name, c.args, foldArgChars+1)
	}
	return msgs
}

// foldStepArgs is one step of foldCallSteps.
type foldStepArgs struct {
	name string
	args map[string]any
}

// foldReadArgs is a file_read argument map.
func foldReadArgs(path string, offset, limit int) map[string]any {
	args := map[string]any{"path": path}
	if offset > 0 {
		args["offset"] = offset
	}
	if limit > 0 {
		args["limit"] = limit
	}
	return args
}

// foldNoteOf returns the tool result text of the step-th step (1-based) of a
// view built by foldCallSteps: step i is message 2*i.
func foldNoteOf(msgs []agentcore.Message, step int) string {
	return msgs[2*step].ToolResult.Content
}

// foldReadRefoldable is the registry decision the tests without a real tool
// registry use: file_read and file_grep are re-runnable local reads.
func foldReadRefoldable(name string) bool {
	return name == foldReadName || name == "file_grep"
}

// TestFoldToolResults: an old result of a re-runnable read-only tool is
// replaced by a one-line note only when a later step of the view superseded it
// (design 2026-10-21, 0.3): the content was read again, or the read file was
// modified afterwards. Other old results stay whole and the call arguments stay.
func TestFoldToolResults(t *testing.T) {
	read := foldReadName
	long := strings.Repeat("r", foldArgChars+1)

	t.Run("a later read of the same content folds the old result", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{read, map[string]any{"path": "a.go"}},
		)
		out, st := foldToolResults(msgs, 3, foldReadRefoldable)
		assert.Eq(t, 2, st.results) // steps 1 and 2, the last read is kept
		assert.Eq(t, "[已折叠：后面第 2 步重新读取了同一内容]", foldNoteOf(out, 1))
		assert.Eq(t, "[已折叠：后面第 3 步重新读取了同一内容]", foldNoteOf(out, 2))
		assert.Eq(t, long, foldNoteOf(out, 3))
		assert.Eq(t, "[已折叠：后面第 2 步重新读取了同一内容]", out[2].Content)  // rendered content too
		assert.Eq(t, "a.go", out[1].ToolCall.Arguments["path"]) // arguments stay
		assert.Eq(t, long, foldNoteOf(msgs, 1))                 // the input is not modified
		assert.Eq(t, 2*(foldArgChars+1), st.charsBefore)
		assert.True(t, st.charsAfter < st.charsBefore, "note %d is not shorter than %d", st.charsAfter, st.charsBefore)
	})

	t.Run("a covering read window folds the old result", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, foldReadArgs("a.go", 10, 50)}, // 10-59
			foldStepArgs{read, foldReadArgs("a.go", 1, 100)}, // 1-100 covers it
			foldStepArgs{read, foldReadArgs("a.go", 0, 0)},   // 1-2000 (defaults)
			foldStepArgs{read, foldReadArgs("a.go", 10, 100)},
			foldStepArgs{read, foldReadArgs("b.go", 0, 0)},
		)
		out, st := foldToolResults(msgs, 5, foldReadRefoldable)
		assert.Eq(t, 2, st.results)
		assert.Eq(t, "[已折叠：后面第 2 步重新读取了同一内容]", foldNoteOf(out, 1))
		assert.Eq(t, "[已折叠：后面第 3 步重新读取了同一内容]", foldNoteOf(out, 2))
		assert.Eq(t, long, foldNoteOf(out, 3)) // 10-100 does not cover 1-2000
		assert.Eq(t, long, foldNoteOf(out, 4))
		assert.Eq(t, long, foldNoteOf(out, 5)) // another file
	})

	t.Run("a narrower window or another path does not fold the old result", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, foldReadArgs("a.go", 10, 50)}, // 10-59
			foldStepArgs{read, foldReadArgs("a.go", 10, 20)}, // 10-29
			foldStepArgs{read, foldReadArgs("b.go", 0, 0)},   // another file
			foldStepArgs{read, map[string]any{"path": "a.go", "offset": 10, "limit": 50, "outline": true}},
			foldStepArgs{read, map[string]any{"paths": []any{"a.go"}}},
			foldStepArgs{read, foldReadArgs("c.go", 0, 0)}, // one repeated read, so
			foldStepArgs{read, foldReadArgs("c.go", 0, 0)}, // the view does fold
		)
		out, st := foldToolResults(msgs, 7, foldReadRefoldable)
		assert.Eq(t, 1, st.results)
		for i := 1; i <= 5; i++ {
			assert.Eq(t, long, foldNoteOf(out, i))
		}
		assert.Eq(t, "[已折叠：后面第 7 步重新读取了同一内容]", foldNoteOf(out, 6))
		assert.Eq(t, long, foldNoteOf(out, 7))
	})

	t.Run("a later modification makes the read result stale", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{"file_edit", map[string]any{"path": "a.go"}},
			foldStepArgs{read, map[string]any{"path": "a.go"}}, // reads the new content
			foldStepArgs{"file_write", map[string]any{"path": "b.go"}},
		)
		out, st := foldToolResults(msgs, 4, foldReadRefoldable)
		assert.Eq(t, 1, st.results)
		// The modification at step 2 comes first: after it the re-read at step 3
		// returns other content, so it does not stand for the folded text.
		assert.Eq(t, "[已折叠：a.go 在后面第 2 步被修改，此处是修改前的内容]", foldNoteOf(out, 1))
		assert.Eq(t, long, foldNoteOf(out, 2))
		assert.Eq(t, long, foldNoteOf(out, 3))
		assert.Eq(t, long, foldNoteOf(out, 4))
	})

	t.Run("a modification of another file does not fold the read result", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{"file_remove", map[string]any{"path": "b.go"}},
			foldStepArgs{"shell_exec", map[string]any{"command": "sed -i s/x/y/ a.go"}},
		)
		out, st := foldToolResults(msgs, 3, foldReadRefoldable)
		assert.Eq(t, 0, st.results) // a shell write cannot be attributed to a path
		assert.Eq(t, long, foldNoteOf(out, 1))
	})

	t.Run("the same read tool with other arguments does not fold", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{"file_grep", map[string]any{"pattern": "foo"}},
			foldStepArgs{"file_grep", map[string]any{"pattern": "bar"}},
			foldStepArgs{"file_grep", map[string]any{"pattern": "foo"}},
		)
		out, st := foldToolResults(msgs, 3, foldReadRefoldable)
		assert.Eq(t, 1, st.results)
		assert.Eq(t, "[已折叠：后面第 3 步重新读取了同一内容]", foldNoteOf(out, 1))
		assert.Eq(t, long, foldNoteOf(out, 2)) // another pattern: another answer
		assert.Eq(t, long, foldNoteOf(out, 3))
	})

	t.Run("only results before the boundary are folded", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{read, map[string]any{"path": "a.go"}},
		)
		out, st := foldToolResults(msgs, 1, foldReadRefoldable)
		assert.Eq(t, 1, st.results)
		assert.Eq(t, "[已折叠：后面第 2 步重新读取了同一内容]", foldNoteOf(out, 1))
		// Step 2 is superseded by step 3 but lies after the boundary.
		assert.Eq(t, long, foldNoteOf(out, 2))
		assert.Eq(t, long, foldNoteOf(out, 3))
	})

	t.Run("error, small and non-read results stay", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{"shell_exec", map[string]any{"command": "ls"}},
			foldStepArgs{"shell_exec", map[string]any{"command": "ls"}},
			foldStepArgs{"web_fetch", map[string]any{"url": "https://example.test"}},
			foldStepArgs{"web_fetch", map[string]any{"url": "https://example.test"}},
		)
		msgs[4].ToolResult.IsError = true // step 2: its read failed
		out, st := foldToolResults(msgs, 6, foldReadRefoldable)
		assert.Eq(t, 1, st.results) // only the good read of step 1
		assert.Eq(t, "[已折叠：后面第 2 步重新读取了同一内容]", foldNoteOf(out, 1))
		assert.Eq(t, long, foldNoteOf(out, 2)) // error result kept
		// A small result is kept even when it was superseded.
		small := foldCallStep([]agentcore.Message{{Role: "user", Content: "task"}},
			read, map[string]any{"path": "s.go"}, 10)
		small = foldCallStep(small, read, map[string]any{"path": "s.go"}, 10)
		out, st = foldToolResults(small, 2, foldReadRefoldable)
		assert.Eq(t, 0, st.results)
		assert.Eq(t, 10, len(foldNoteOf(out, 1)))
	})
}

// TestFoldToolResultsByMetadata: the tool-result decision comes from the tool
// registry's metadata in the real compose path, and a later call after the
// boundary can supersede a result before it (fold_at 0 = fold by cadence from
// the start).
func TestFoldToolResultsByMetadata(t *testing.T) {
	b, _, _, _ := foldBuildSetup(t, Config{ContextWindow: 4000, FoldEvery: 2, FoldKeep: 1})
	msgs := []agentcore.Message{{Role: "user", Content: "task"}}
	long := strings.Repeat("r", foldArgChars+1)
	for _, name := range []string{"file_read", "shell_exec", "web_fetch", "file_read", "unknown_tool", "file_read"} {
		msgs = append(msgs,
			agentcore.Message{Role: "assistant", Reasoning: "think", ToolCall: &agentcore.ToolCall{Name: name, Arguments: map[string]any{"path": "a.go"}}},
			agentcore.Message{Role: "tool", Content: long, ToolResult: &agentcore.ToolResult{Name: name, Content: long}})
	}
	view, err := b.BuildMessages(foldCtx(b.sessionID), &agentcore.RunState{Messages: msgs})
	assert.Require(t, assert.NoErr(t, err))
	// 6 steps with every 2 / keep 1 fold the first 4. Only the two file_read
	// results before the boundary were read again later.
	assert.Eq(t, "[已折叠：后面第 4 步重新读取了同一内容]", view[2].ToolResult.Content)
	assert.Eq(t, long, view[2+2*1].ToolResult.Content) // shell_exec: not re-runnable
	assert.Eq(t, long, view[2+2*2].ToolResult.Content) // web_fetch: external
	assert.Eq(t, "[已折叠：后面第 6 步重新读取了同一内容]", view[2+2*3].ToolResult.Content)
	assert.Eq(t, long, view[2+2*4].ToolResult.Content) // unknown tool: metadata unknown
	assert.Eq(t, long, view[2+2*5].ToolResult.Content) // the last read is kept
}

// TestFoldSuperseded: the supersede decision reads only the request view order,
// tool names and arguments, and the first later step that invalidates a result
// wins.
func TestFoldSuperseded(t *testing.T) {
	read := foldReadName

	t.Run("the first invalidation wins", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{"file_edit", map[string]any{"path": "a.go"}},
			foldStepArgs{read, map[string]any{"path": "a.go"}},
		)
		got := foldSuperseded(msgs, len(msgs))
		assert.Eq(t, 1, len(got))
		assert.Eq(t, supersede{kind: supersedeModified, step: 2, path: "a.go"}, got[2])
	})

	t.Run("no later call supersedes anything", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{"file_grep", map[string]any{"pattern": "a"}},
		)
		assert.Eq(t, 0, len(foldSuperseded(msgs, len(msgs))))
		assert.Eq(t, 0, len(foldSuperseded(nil, 0)))
	})

	t.Run("a call after the cut still supersedes an earlier result", func(t *testing.T) {
		msgs := foldCallSteps(
			foldStepArgs{read, map[string]any{"path": "a.go"}},
			foldStepArgs{read, map[string]any{"path": "a.go"}},
		)
		cut := len(msgs) - 2 // the last step is kept whole
		got := foldSuperseded(msgs, cut)
		assert.Eq(t, supersede{kind: supersedeReread, step: 2}, got[2])
	})
}
