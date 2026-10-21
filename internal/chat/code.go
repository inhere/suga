package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/inhere/suga/internal/output"
)

const codeCmd = ":code"

const (
	codeUsageHint    = "用法：:code 展开上一条回答里被折叠的代码块，:code N 展开第 N 个代码块"
	codeNoneHint     = "上一条回答里没有代码块"
	codeNoFoldedHint = "上一条回答没有折叠的代码块（chat.code_max_lines: 0 时不折叠）"
)

// isCodeCommand recognizes the :code command (and its ":code N" form): the
// answer's code blocks are kept by the markdown renderer, since a long one is
// folded after chat.code_max_lines lines.
func isCodeCommand(line string) bool { return line == codeCmd || strings.HasPrefix(line, codeCmd+" ") }

// ShowCode prints a code block of the last answer in full: without an argument
// the most recent folded one, with N the Nth block of that answer in order.
// The block goes through the markdown renderer again, so it is highlighted like
// it streamed (with no fold limit).
func (r *Renderer) ShowCode(arg string) {
	blocks := r.live.CodeBlocks()
	if len(blocks) == 0 {
		r.Hint(codeNoneHint)
		return
	}
	index, ok := r.pickCodeBlock(blocks, strings.TrimSpace(arg))
	if !ok {
		return
	}
	block := blocks[index]
	r.stopThinking()
	r.Hint(fmt.Sprintf("第 %d 个代码块（%s，共 %d 行）", index+1, codeBlockLang(block), len(block.Lines)))
	term := output.NewMDTerm(r.opts.Out, r.opts.Color)
	term.SetTheme(r.opts.Theme)
	// No fold limit: :code shows the whole block.
	term.SetCode(r.opts.CodeHighlight, 0)
	term.Print("```" + block.Lang + "\n" + strings.Join(block.Lines, "\n") + "\n```\n")
}

// pickCodeBlock reads the argument of :code and returns the index of the block
// to print; it prints the hint of an unusable argument or of a block that does
// not exist and reports false then.
func (r *Renderer) pickCodeBlock(blocks []output.CodeBlock, arg string) (int, bool) {
	if arg == "" {
		for i := len(blocks) - 1; i >= 0; i-- {
			if blocks[i].Folded > 0 {
				return i, true
			}
		}
		r.Hint(codeNoFoldedHint)
		return 0, false
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		r.Hint(codeUsageHint)
		return 0, false
	}
	if n > len(blocks) {
		r.Hint(fmt.Sprintf("上一条回答只有 %d 个代码块", len(blocks)))
		return 0, false
	}
	return n - 1, true
}

// codeBlockLang is the language shown in the :code label of a block.
func codeBlockLang(block output.CodeBlock) string {
	if block.Lang == "" {
		return "无语言"
	}
	return block.Lang
}
