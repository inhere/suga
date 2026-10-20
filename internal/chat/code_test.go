package chat

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// codeAnswer is a fenced code block with n numbered lines, between plain text.
func codeAnswer(n int, lang string) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("// line %d", i+1)
	}
	return "before\n```" + lang + "\n" + strings.Join(lines, "\n") + "\n```\nafter\n"
}

// streamCode renders text through the renderer's streamed path (the one chat
// uses) and returns the renderer and its output.
func streamCode(opts rendererOptions, text string) (*Renderer, string) {
	var out bytes.Buffer
	opts.Out = &out
	opts.Markdown = true
	opts.Color = true
	r := newRenderer(opts)
	r.Handle(personal.Event{Kind: "text_delta", Text: text})
	r.Finish()
	return r, out.String()
}

// TestRendererShowCode: a folded code block is printed again in full by :code,
// and :code N selects the Nth block of the answer.
func TestRendererShowCode(t *testing.T) {
	t.Run("no argument takes the folded block", func(t *testing.T) {
		r, got := streamCode(rendererOptions{CodeMaxLines: 40}, codeAnswer(45, "go"))
		assert.StrContains(t, got, "… 还有 5 行（:code 查看全部）")
		var out bytes.Buffer
		r.opts.Out = &out
		r.ShowCode("")
		assert.StrContains(t, plainSGR(out.String()), "// line 45")
		assert.StrContains(t, plainSGR(out.String()), "第 1 个代码块（go，共 45 行）")
		assert.StrNotContains(t, plainSGR(out.String()), ":code 查看全部")
	})
	t.Run("a number takes the Nth block", func(t *testing.T) {
		r, _ := streamCode(rendererOptions{CodeMaxLines: 40},
			"```go\nfirst\n```\n```go\nsecond\n```\n")
		var out bytes.Buffer
		r.opts.Out = &out
		r.ShowCode("2")
		assert.StrContains(t, plainSGR(out.String()), "second")
		assert.StrNotContains(t, plainSGR(out.String()), "first")
	})
	t.Run("a number that does not exist only hints", func(t *testing.T) {
		r, _ := streamCode(rendererOptions{CodeMaxLines: 40}, "```go\nfirst\n```\n")
		var out bytes.Buffer
		r.opts.Out = &out
		r.ShowCode("3")
		assert.Eq(t, "上一条回答只有 1 个代码块\n", plainSGR(out.String()))
	})
	t.Run("an argument that is not a number prints the usage", func(t *testing.T) {
		r, _ := streamCode(rendererOptions{CodeMaxLines: 40}, "```go\nfirst\n```\n")
		var out bytes.Buffer
		r.opts.Out = &out
		r.ShowCode("x")
		assert.Eq(t, codeUsageHint+"\n", plainSGR(out.String()))
	})
	t.Run("an answer without a code block says so", func(t *testing.T) {
		r, _ := streamCode(rendererOptions{CodeMaxLines: 40}, "just text\n")
		var out bytes.Buffer
		r.opts.Out = &out
		r.ShowCode("")
		assert.Eq(t, codeNoneHint+"\n", plainSGR(out.String()))
	})
	t.Run("without a folded block the command says so", func(t *testing.T) {
		r, _ := streamCode(rendererOptions{CodeMaxLines: 0}, codeAnswer(45, "go"))
		var out bytes.Buffer
		r.opts.Out = &out
		r.ShowCode("")
		assert.Eq(t, codeNoFoldedHint+"\n", plainSGR(out.String()))
	})
	t.Run("a chat without markdown has no blocks", func(t *testing.T) {
		var out bytes.Buffer
		// Markdown off: the text streams as it is, so nothing is kept.
		newRenderer(rendererOptions{Out: &out, Color: true}).ShowCode("")
		assert.Eq(t, codeNoneHint+"\n", plainSGR(out.String()))
	})
}

// TestCodeCommandParsing: only ":code" and its argument are the command.
func TestCodeCommandParsing(t *testing.T) {
	for line, want := range map[string]bool{
		":code":        true,
		":code 2":      true,
		":code 2 text": true,
		":code2":       false,
		":codes":       false,
		"code":         false,
	} {
		t.Run(line, func(t *testing.T) {
			assert.Eq(t, want, isCodeCommand(line))
		})
	}
}

// TestChatCodeCommandInLoop: a typed ":code" answers on the chat output and
// starts no turn. The block itself is covered by TestRendererShowCode: a chat
// output that is no terminal renders no markdown, so it keeps no block (see the
// loop of TestChatDiffCommandInLoop for the delivered-message guard).
func TestChatCodeCommandInLoop(t *testing.T) {
	runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	var stdout, stderr bytes.Buffer
	// One model step only: a turn started by ":code" would leave "hello" without
	// an answer.
	input := ":code\nhello\n:quit\n"
	assert.NoErr(t, Run(context.Background(), runner, Options{Markdown: true, CodeMaxLines: 40},
		strings.NewReader(input), &stdout, &stderr, nil))
	assert.StrContains(t, plainSGR(stdout.String()), codeNoneHint)
	assert.StrContains(t, stdout.String(), "MODEL_ANSWER")
}

// sgrPattern matches the SGR styles of a styled line.
var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// plainSGR drops those styles, so a check compares the visible text.
func plainSGR(s string) string { return sgrPattern.ReplaceAllString(s, "") }
