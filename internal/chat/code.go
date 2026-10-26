package chat

import (
	"strconv"
	"strings"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/output"
)

const codeCmd = ":code"

const (
	codeNoneHintKey     = "cmd.code.none"
	codeNoFoldedHintKey = "cmd.code.no_folded"
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
		r.Hint(r.tr().T(codeNoneHintKey))
		return
	}
	index, ok := r.pickCodeBlock(blocks, strings.TrimSpace(arg))
	if !ok {
		return
	}
	block := blocks[index]
	r.stopThinking()
	r.Hint(r.tr().T("cmd.code.header", map[string]any{
		"index": index + 1, "lang": codeBlockLang(r.tr(), block), "lines": len(block.Lines),
	}))
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
		r.Hint(r.tr().T(codeNoFoldedHintKey))
		return 0, false
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		r.Hint(commandUsageText(r.tr(), codeCmd))
		return 0, false
	}
	if n > len(blocks) {
		r.Hint(r.tr().T("cmd.code.count", map[string]any{"count": len(blocks)}))
		return 0, false
	}
	return n - 1, true
}

// codeBlockLang is the language shown in the :code label of a block.
func codeBlockLang(tr i18n.Translator, block output.CodeBlock) string {
	if block.Lang == "" {
		return tr.T("cmd.code.no_lang")
	}
	return block.Lang
}
