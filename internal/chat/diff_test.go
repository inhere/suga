package chat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/gookit/color"
	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/diffview"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// changeOf builds the change of one call: oldText becomes newText.
func textPtr(text string) *string { return &text }

func changeOf(tool, rel, oldText, newText string) *diffview.Change {
	return &diffview.Change{Path: "/ws/" + rel, Rel: rel, Tool: tool,
		Old: &oldText, New: &newText}
}

// replacedLines is a change of n replaced lines: n removed, n added, nothing in
// common between the two sides.
func replacedLines(tool, rel string, n int) *diffview.Change {
	oldLines, newLines := make([]string, n), make([]string, n)
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("old %d", i+1)
		newLines[i] = fmt.Sprintf("new %d", i+1)
	}
	return changeOf(tool, rel, strings.Join(oldLines, "\n")+"\n", strings.Join(newLines, "\n")+"\n")
}

// diffShown renders the lines of one preview the way chat writes them.
func diffShown(change *diffview.Change, limit, index int, colorOn bool) string {
	var b strings.Builder
	for _, line := range buildDiffLines(testTranslator, change, limit, index) {
		b.WriteString(line.Paint(colorOn))
		b.WriteString("\n")
	}
	return b.String()
}

// TestBuildDiffLines: the lines of one change, whole, with the plain text every
// case pins (colors are checked in TestDiffColors).
func TestBuildDiffLines(t *testing.T) {
	t.Run("an edit shows the header, the hunk and its lines", func(t *testing.T) {
		got := diffShown(changeOf("file_edit", "a.go", "a\nb\nc\n", "a\nB\nc\n"), 0, 0, false)
		assert.Eq(t, "  diff a.go（+1 -1）\n"+
			"  @@ -1,3 +1,3 @@\n"+
			"    1 a\n"+
			"  -  2 b\n"+
			"  +  2 B\n"+
			"    3 c\n", got)
	})

	t.Run("numbers are padded to the widest line of the change", func(t *testing.T) {
		// 12 lines, the last one changed: the numbers reach two digits.
		lines := make([]string, 12)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i+1)
		}
		oldText := strings.Join(lines, "\n") + "\n"
		lines[11] = "last"
		got := diffShown(changeOf("file_edit", "a.go", oldText, strings.Join(lines, "\n")+"\n"), 0, 0, false)
		assert.StrContains(t, got, "  + 12 last\n")
		assert.StrContains(t, got, "    9 line 9\n")
	})

	t.Run("more lines than the limit are cut with a hint", func(t *testing.T) {
		got := diffShown(replacedLines("file_edit", "a.go", 30), 20, 0, false)
		assert.StrContains(t, got, "  diff a.go（+30 -30）\n")
		assert.StrContains(t, got, "  @@ -1,30 +1,30 @@\n")
		assert.StrContains(t, got, "  … 还有 41 行（:diff 查看全部）\n")
		// 20 diff lines, then the hint naming :diff.
		assert.Eq(t, 22, strings.Count(got, "\n"))
	})

	t.Run("a change within the limit has no hint", func(t *testing.T) {
		got := diffShown(changeOf("file_edit", "a.go", "a\nb\nc\n", "a\nB\nc\n"), 20, 0, false)
		assert.StrNotContains(t, got, "还有")
	})

	t.Run("a new file shows its first lines and how many it has", func(t *testing.T) {
		got := diffShown(&diffview.Change{Rel: "new.txt", Tool: diffview.ToolWrite, New: textPtr("one\ntwo\nthree\n")}, 2, 0, false)
		assert.Eq(t, "  新文件 new.txt（共 3 行）\n"+
			"  @@ -0,0 +1,3 @@\n"+
			"  + 1 one\n"+
			"  + 2 two\n"+
			"  … 还有 1 行（:diff 查看全部）\n", got)
	})

	t.Run("a removal shows how many lines it takes away", func(t *testing.T) {
		change := &diffview.Change{Path: "/ws/a.go", Rel: "a.go", Tool: "file_remove",
			Old: diffview.Ptr("a\nb\nc\n"), RemovedLines: 3}
		assert.Eq(t, "  删除 a.go（3 行）\n", diffShown(change, 2, 0, false))
	})

	t.Run("the number of the change is shown with :diff", func(t *testing.T) {
		got := diffShown(changeOf("file_edit", "a.go", "a\n", "b\n"), 0, 2, false)
		assert.StrContains(t, got, "  #2 diff a.go（+1 -1）\n")
	})

	t.Run("a change without a file has no lines", func(t *testing.T) {
		assert.Empty(t, buildDiffLines(testTranslator, nil, 20, 0))
	})
}

// TestDiffColors: an added line is green, a removed one red, the hunk header,
// the line numbers and the file header grey - and nothing of that on a terminal
// that takes no colors (a pipe, a redirected output, NO_COLOR).
func TestDiffColors(t *testing.T) {
	change := changeOf("file_edit", "a.go", "a\nb\nc\n", "a\nB\nc\n")

	t.Run("colored", func(t *testing.T) {
		color.ForceColor()
		t.Cleanup(func() { color.RevertColorLevel() })
		got := diffShown(change, 0, 0, true)
		assert.StrContains(t, got, "\x1b[90m  diff a.go（+1 -1）\x1b[0m\n")
		assert.StrContains(t, got, "\x1b[90m  @@ -1,3 +1,3 @@\x1b[0m\n")
		assert.StrContains(t, got, "\x1b[31m  -  2 b\x1b[0m\n")
		assert.StrContains(t, got, "\x1b[32m  +  2 B\x1b[0m\n")
		assert.StrContains(t, got, "\x1b[90m    1 a\x1b[0m\n")
	})

	t.Run("plain", func(t *testing.T) {
		assert.StrNotContains(t, diffShown(change, 0, 0, false), "\x1b[")
	})
}

// TestRendererShowsChangeUnderResult: the tool result line of a file call is
// followed by the diff of what it changed; a failed call and a chat with
// chat.diff off show nothing.
func TestRendererShowsChangeUnderResult(t *testing.T) {
	change := changeOf("file_edit", "a.go", "a\nb\nc\n", "a\nB\nc\n")
	call := func(r *Renderer) {
		r.Handle(personal.Event{Kind: "tool_call", ToolName: "file_edit", Text: `{"path":"a.go"}`, Change: change})
	}
	result := func(r *Renderer, isError bool) {
		r.Handle(personal.Event{Kind: "tool_result", ToolName: "file_edit", Text: "edited a.go", IsError: isError})
	}

	t.Run("the diff follows the ok line", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Diff: true, DiffLines: 20})
		r.BeginTurn()
		call(r)
		result(r, false)
		assert.StrContains(t, out.String(), "  ⎿ ok: edited a.go\n  diff a.go（+1 -1）\n  @@ -1,3 +1,3 @@\n")
	})

	t.Run("nothing is shown for a failed call", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Diff: true, DiffLines: 20})
		r.BeginTurn()
		call(r)
		result(r, true)
		assert.StrNotContains(t, out.String(), "diff a.go")
	})

	t.Run("nothing is shown with chat.diff off", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Diff: false, DiffLines: 20})
		r.BeginTurn()
		call(r)
		result(r, false)
		assert.StrNotContains(t, out.String(), "diff a.go")
		assert.Empty(t, r.changes)
	})

	t.Run("a result of another tool shows nothing", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Diff: true, DiffLines: 20})
		r.BeginTurn()
		call(r)
		r.Handle(personal.Event{Kind: "tool_result", ToolName: "file_write", Text: "wrote"})
		assert.StrNotContains(t, out.String(), "diff a.go")
	})

	t.Run("a tool call without a change leaves no pending one", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Diff: true, DiffLines: 20})
		r.BeginTurn()
		r.Handle(personal.Event{Kind: "tool_call", ToolName: "shell_exec", Text: `{"command":"ls"}`})
		r.Handle(personal.Event{Kind: "tool_result", ToolName: "shell_exec", Text: "ok"})
		assert.StrNotContains(t, out.String(), "diff ")
		assert.Empty(t, r.changes)
	})
}

// TestApprovalDiff: the pending change of the call being approved is shown
// before the question, cut like any other preview; another tool's question, and
// chat.diff off, show nothing.
func TestApprovalDiff(t *testing.T) {
	change := changeOf("file_write", "a.go", "a\nb\nc\n", "a\nB\nc\n")
	newRendererWith := func(t *testing.T, diff bool) (*Renderer, *bytes.Buffer) {
		t.Helper()
		var out bytes.Buffer
		return newRenderer(rendererOptions{Out: &out, Diff: diff, DiffLines: 20}), &out
	}

	t.Run("the pending change of the tool is shown", func(t *testing.T) {
		r, _ := newRendererWith(t, true)
		r.noteToolCall(change)
		assert.Eq(t, "  diff a.go（+1 -1）\n  @@ -1,3 +1,3 @@\n    1 a\n  -  2 b\n  +  2 B\n    3 c\n",
			r.ApprovalDiff("file_write"))
	})

	t.Run("another tool's question shows nothing", func(t *testing.T) {
		r, _ := newRendererWith(t, true)
		r.noteToolCall(change)
		assert.Eq(t, "", r.ApprovalDiff("shell_exec"))
	})

	t.Run("without a pending change nothing is shown", func(t *testing.T) {
		r, _ := newRendererWith(t, true)
		assert.Eq(t, "", r.ApprovalDiff("file_write"))
	})

	t.Run("with chat.diff off nothing is shown", func(t *testing.T) {
		r, _ := newRendererWith(t, false)
		r.noteToolCall(change)
		assert.Eq(t, "", r.ApprovalDiff("file_write"))
	})
}

// TestShowDiffCommand: :diff shows the whole change of this session - the last
// one, or the one its number names - and a chat without one, a number without
// one and an argument that is not a number only print a hint.
func TestShowDiffCommand(t *testing.T) {
	small := changeOf("file_edit", "a.go", "a\nb\nc\n", "a\nB\nc\n")
	big := replacedLines("file_edit", "b.go", 30)
	withChanges := func(t *testing.T, diff bool) (*Renderer, *bytes.Buffer) {
		t.Helper()
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Diff: diff, DiffLines: 2})
		r.showToolResult(small, "file_edit")
		r.showToolResult(big, "file_edit")
		out.Reset()
		return r, &out
	}

	t.Run("no argument shows the last change in full", func(t *testing.T) {
		r, out := withChanges(t, true)
		r.ShowDiff("")
		assert.StrContains(t, out.String(), "  #2 diff b.go（+30 -30）\n")
		assert.StrContains(t, out.String(), "+ 30 new 30\n") // the preview cut this one off
		assert.StrNotContains(t, out.String(), "还有")
	})

	t.Run("a number shows that change", func(t *testing.T) {
		r, out := withChanges(t, true)
		r.ShowDiff(" 1 ")
		assert.Eq(t, "  #1 diff a.go（+1 -1）\n"+
			"  @@ -1,3 +1,3 @@\n"+
			"    1 a\n"+
			"  -  2 b\n"+
			"  +  2 B\n"+
			"    3 c\n", out.String())
	})

	t.Run("a number without a change only hints", func(t *testing.T) {
		r, out := withChanges(t, true)
		r.ShowDiff("3")
		assert.Eq(t, "本次会话只有 2 次文件改动\n", out.String())
	})

	t.Run("an argument that is not a number prints the usage", func(t *testing.T) {
		r, out := withChanges(t, true)
		r.ShowDiff("x")
		assert.Eq(t, commandUsageText(testTranslator, diffCmd)+"\n", out.String())
	})

	t.Run("a chat without a change says so", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Diff: true}).ShowDiff("")
		assert.Eq(t, testTranslator.T(diffNoneHintKey)+"\n", out.String())
	})

	t.Run("with chat.diff off the command says so", func(t *testing.T) {
		var out bytes.Buffer
		newRenderer(rendererOptions{Out: &out, Diff: false}).ShowDiff("")
		assert.Eq(t, testTranslator.T(diffDisabledHintKey)+"\n", out.String())
	})
}

func TestDiffCommandParsing(t *testing.T) {
	for line, want := range map[string]bool{
		":diff":         true,
		":diff 2":       true,
		":diff 2 extra": true,
		":diff2":        false,
		":different":    false,
		"diff":          false,
	} {
		t.Run(line, func(t *testing.T) {
			assert.Eq(t, want, isDiffCommand(line))
		})
	}
	for arg, want := range map[string]int{
		"": 0, "3": 3, " 3 ": 3,
	} {
		t.Run("index "+arg, func(t *testing.T) {
			index, ok := diffIndex(arg)
			assert.True(t, ok)
			assert.Eq(t, want, index)
		})
	}
	for _, arg := range []string{"0", "-1", "x", "1.5"} {
		t.Run("bad index "+arg, func(t *testing.T) {
			_, ok := diffIndex(arg)
			assert.False(t, ok)
		})
	}
}

// TestChatDiffCommandInLoop: a typed ":diff" answers on the chat output and
// starts no turn; a ":diff" a Stop hook delivered is an ordinary message (the
// keyboard-only guard of the command switch), so it reaches the model.
func TestChatDiffCommandInLoop(t *testing.T) {
	t.Run("typed: answers without a turn", func(t *testing.T) {
		runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
		var stdout, stderr bytes.Buffer
		// One model step only: a turn started by ":diff" would leave the "hello"
		// turn without an answer.
		input := ":diff\nhello\n:quit\n"
		assert.NoErr(t, Run(context.Background(), runner, Options{Diff: true, DiffLines: 20},
			strings.NewReader(input), &stdout, &stderr, nil))
		assert.StrContains(t, stdout.String(), testTranslator.T(diffNoneHintKey))
		assert.StrContains(t, stdout.String(), "MODEL_ANSWER")
	})

	t.Run("delivered by a hook: an ordinary message", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		t.Setenv("SUGA_HOME", t.TempDir())
		provider := &hookTestProvider{}
		runner, _ := newCLITestRunner(t, provider)
		hooks := newFakeHookClient()
		hooks.stopReason = ":diff"

		reader, writer := io.Pipe()
		var stdout, stderr strings.Builder
		done := make(chan error, 1)
		go func() {
			done <- Run(context.Background(), runner, Options{Diff: true, DiffLines: 20}, reader, &stdout, &stderr, hooks)
		}()
		if _, err := io.WriteString(writer, "first prompt\n"); err != nil {
			t.Fatal(err)
		}
		waitProviderCalls(t, provider, 2) // the reply started a turn of its own
		if _, err := io.WriteString(writer, ":quit\n"); err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
		assert.NoErr(t, <-done)

		assert.StrNotContains(t, stdout.String(), testTranslator.T(diffNoneHintKey))
		provider.mu.Lock()
		defer provider.mu.Unlock()
		assert.Require(t, assert.Eq(t, 2, len(provider.calls)))
		var found bool
		for _, message := range provider.calls[1] {
			if strings.Contains(message.Content, ":diff") {
				found = true
			}
		}
		assert.True(t, found, "the hook's reply should reach the model as a user message")
	})
}
