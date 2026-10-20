package output

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// codeLines builds a fenced block with n numbered lines.
func codeLines(n int) (body []string, fenced string) {
	for i := 1; i <= n; i++ {
		body = append(body, fmt.Sprintf("line %d", i))
	}
	return body, "```go\n" + strings.Join(body, "\n") + "\n```\n"
}

// TestMDCodeFold: past the fold limit the code lines are not printed, the block
// ends with a notice naming :code, and every line is still kept for the command.
func TestMDCodeFold(t *testing.T) {
	body, text := codeLines(45)
	t.Run("the block is folded after the limit", func(t *testing.T) {
		got, term := renderMDWithCode(text, false, true, 40)
		assert.Eq(t, strings.Join([]string{
			"─── go",
			strings.Join(body[:40], "\n"),
			"───",
			"… 还有 5 行（:code 查看全部）",
			"",
		}, "\n"), got)
		blocks := term.CodeBlocks()
		assert.Require(t, assert.Eq(t, 1, len(blocks)))
		assert.Eq(t, 45, len(blocks[0].Lines))
		assert.Eq(t, 5, blocks[0].Folded)
		assert.Eq(t, "go", blocks[0].Lang)
	})
	t.Run("code_max_lines 0 folds nothing", func(t *testing.T) {
		got, term := renderMDWithCode(text, false, true, 0)
		assert.Eq(t, "─── go\n"+strings.Join(body, "\n")+"\n───\n", got)
		blocks := term.CodeBlocks()
		assert.Require(t, assert.Eq(t, 1, len(blocks)))
		assert.Eq(t, 0, blocks[0].Folded)
	})
	t.Run("a block at the limit is not folded", func(t *testing.T) {
		body40, text40 := codeLines(40)
		got, _ := renderMDWithCode(text40, false, true, 40)
		assert.Eq(t, "─── go\n"+strings.Join(body40, "\n")+"\n───\n", got)
		assert.StrNotContains(t, got, ":code")
	})
	t.Run("an unclosed block is folded at the message end", func(t *testing.T) {
		body, _ := codeLines(43)
		got, term := renderMDWithCode("```go\n"+strings.Join(body, "\n")+"\n", false, true, 40)
		assert.Eq(t, "─── go\n"+strings.Join(body[:40], "\n")+"\n… 还有 3 行（:code 查看全部）\n", got)
		assert.Eq(t, 3, term.CodeBlocks()[0].Folded)
	})
	t.Run("several blocks are kept in order", func(t *testing.T) {
		_, first := codeLines(41)
		_, second := codeLines(42)
		_, term := renderMDWithCode(first+"\n"+second, false, true, 40)
		blocks := term.CodeBlocks()
		assert.Require(t, assert.Eq(t, 2, len(blocks)))
		assert.Eq(t, 41, len(blocks[0].Lines))
		assert.Eq(t, 42, len(blocks[1].Lines))
	})
	t.Run("only the last message with a block is kept", func(t *testing.T) {
		_, text := codeLines(41)
		got, term := renderMDWithCode(text, false, true, 40)
		assert.StrContains(t, got, ":code")
		// A later message without a block does not clear the kept one.
		term.Write("just text\n")
		term.Flush()
		assert.Eq(t, 41, len(term.CodeBlocks()[0].Lines))
	})
}
