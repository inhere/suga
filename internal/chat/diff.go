package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gookit/color"
	"github.com/inhere/suga/internal/diffview"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/output/termtext"
)

const diffCmd = ":diff"

const (
	diffNoneHintKey     = "cmd.diff.none"
	diffDisabledHintKey = "cmd.diff.disabled"
	defaultDiffLines    = 20
)

func isDiffCommand(line string) bool { return line == diffCmd || strings.HasPrefix(line, diffCmd+" ") }

func diffIndex(arg string) (int, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, true
	}
	n, err := strconv.Atoi(arg)
	return n, err == nil && n >= 1
}

// buildDiffLines prepares the bounded preview used below tool results and approvals.
func buildDiffLines(tr i18n.Translator, change *diffview.Change, limit, index int) []LayoutLine {
	if change == nil || change.Rel == "" {
		return nil
	}
	number := ""
	rel := diffPathText(change.Rel)
	if index > 0 {
		number = fmt.Sprintf("#%d ", index)
	}
	if change.Tool == diffview.ToolRemove {
		head := tr.T("cmd.diff.removed", map[string]any{"number": number, "path": rel, "lines": change.RemovedLines})
		return []LayoutLine{{Text: "  " + head, Color: color.Gray}}
	}
	body := change.Lines()
	if change.NewFile() {
		empty := ""
		body = (&diffview.Change{Old: &empty, New: change.New}).Lines()
	}
	adds, dels := diffCounts(body)
	head := "  " + tr.T("cmd.diff.head", map[string]any{"number": number, "path": rel, "adds": adds, "dels": dels})
	if change.NewFile() {
		head = "  " + tr.T("cmd.diff.new_file", map[string]any{"number": number, "path": rel, "lines": adds})
	}
	shown, omitted := body, 0
	bodyLimit := limit
	if change.NewFile() && bodyLimit > 0 {
		bodyLimit++
	}
	if bodyLimit > 0 && len(body) > bodyLimit {
		shown, omitted = body[:bodyLimit], len(body)-bodyLimit
	}
	lines := make([]LayoutLine, 0, len(shown)+2)
	lines = append(lines, LayoutLine{Text: head, Color: color.Gray})
	width := diffNumberWidth(shown)
	if change.NewFile() {
		width = 1
	}
	for _, line := range shown {
		lines = append(lines, diffPaintLine(line, width))
	}
	if omitted > 0 {
		lines = append(lines, LayoutLine{Text: "  " + tr.T("cmd.diff.omitted", map[string]any{"count": omitted, "command": diffCmd}), Color: color.Gray})
	}
	return lines
}

// diffPaintLine keeps the sign separate from the aligned line-number column.
func diffPaintLine(line diffview.Line, width int) LayoutLine {
	text := termtext.Clean(line.Text)
	if line.Kind == diffview.Hunk {
		return LayoutLine{Text: "  " + text, Color: color.Gray}
	}
	if line.Kind == diffview.Context {
		return LayoutLine{Text: fmt.Sprintf("  %*s%d %s", width, "", line.Number(), text), Color: color.Gray}
	}
	sign, paint := "", color.Gray
	if line.Kind == diffview.Add {
		sign, paint = "+", color.Green
	}
	if line.Kind == diffview.Del {
		sign, paint = "-", color.Red
	}
	return LayoutLine{Text: fmt.Sprintf("  %s %*d %s", sign, width, line.Number(), text), Color: paint}
}

func diffPathText(path string) string { return strings.Join(strings.Fields(termtext.Clean(path)), " ") }

func diffNumberWidth(lines []diffview.Line) int {
	width := 2
	for _, line := range lines {
		if line.Kind != diffview.Hunk {
			if n := len(strconv.Itoa(line.Number())); n > width {
				width = n
			}
		}
	}
	return width
}

func diffCounts(lines []diffview.Line) (adds, dels int) {
	for _, line := range lines {
		if line.Kind == diffview.Add {
			adds++
		}
		if line.Kind == diffview.Del {
			dels++
		}
	}
	return
}

func (r *Renderer) diffLimit() int {
	if r.opts.DiffLines > 0 {
		return r.opts.DiffLines
	}
	return defaultDiffLines
}
func (r *Renderer) noteToolCall(change *diffview.Change) { r.pending = change }

func (r *Renderer) showToolResult(change *diffview.Change, tool string) {
	if !r.diffOn() || change == nil || change.Tool != tool {
		return
	}
	r.changes = append(r.changes, change)
	printLayout(r.opts.Out, r.opts.Color, buildDiffLines(r.tr(), change, r.diffLimit(), 0))
}

// ApprovalDiff returns the pending file change before the approval question.
func (r *Renderer) ApprovalDiff(qTool string) string {
	if !r.diffOn() || r.pending == nil || r.pending.Tool != qTool {
		return ""
	}
	var b strings.Builder
	for _, line := range buildDiffLines(r.tr(), r.pending, r.diffLimit(), 0) {
		b.WriteString(line.Paint(r.opts.Color))
		b.WriteByte('\n')
	}
	return b.String()
}

// ShowDiff prints a remembered change in full; :diff N selects the Nth change.
func (r *Renderer) ShowDiff(arg string) {
	if !r.diffOn() {
		r.Hint(r.tr().T(diffDisabledHintKey))
		return
	}
	index, ok := diffIndex(arg)
	if !ok {
		r.Hint(commandUsageText(r.tr(), diffCmd))
		return
	}
	if len(r.changes) == 0 {
		r.Hint(r.tr().T(diffNoneHintKey))
		return
	}
	if index == 0 {
		index = len(r.changes)
	}
	if index > len(r.changes) {
		r.Hint(r.tr().T("cmd.diff.count", map[string]any{"count": len(r.changes)}))
		return
	}
	r.stopThinking()
	printLayout(r.opts.Out, r.opts.Color, buildDiffLines(r.tr(), r.changes[index-1], 0, index))
}

func (r *Renderer) diffOn() bool { return r.opts.Diff }
