package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gookit/color"
	"github.com/inhere/suga/internal/diffview"
	"github.com/inhere/suga/internal/output/termtext"
)

const diffCmd = ":diff"

const (
	diffNoneHint     = "本次会话还没有文件改动"
	diffDisabledHint = "文件改动的 diff 已显示关闭（chat.diff: false）"
	defaultDiffLines = 20
)

// diffUsageHint answers a :diff line whose argument does not parse; the text is
// the :diff entry of the command table (chat_usage.go).
var diffUsageHint = commandUsageText(diffCmd)

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
func buildDiffLines(change *diffview.Change, limit, index int) []LayoutLine {
	if change == nil || change.Rel == "" {
		return nil
	}
	number := ""
	rel := diffPathText(change.Rel)
	if index > 0 {
		number = fmt.Sprintf("#%d ", index)
	}
	if change.Tool == diffview.ToolRemove {
		return []LayoutLine{{Text: fmt.Sprintf("  %s删除 %s（%d 行）", number, rel, change.RemovedLines), Color: color.Gray}}
	}
	body := change.Lines()
	if change.NewFile() {
		empty := ""
		body = (&diffview.Change{Old: &empty, New: change.New}).Lines()
	}
	adds, dels := diffCounts(body)
	head := fmt.Sprintf("  %sdiff %s（+%d -%d）", number, rel, adds, dels)
	if change.NewFile() {
		head = fmt.Sprintf("  %s新文件 %s（共 %d 行）", number, rel, adds)
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
		lines = append(lines, LayoutLine{Text: fmt.Sprintf("  … 还有 %d 行（%s 查看全部）", omitted, diffCmd), Color: color.Gray})
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
	printLayout(r.opts.Out, r.opts.Color, buildDiffLines(change, r.diffLimit(), 0))
}

// ApprovalDiff returns the pending file change before the approval question.
func (r *Renderer) ApprovalDiff(qTool string) string {
	if !r.diffOn() || r.pending == nil || r.pending.Tool != qTool {
		return ""
	}
	var b strings.Builder
	for _, line := range buildDiffLines(r.pending, r.diffLimit(), 0) {
		b.WriteString(line.Paint(r.opts.Color))
		b.WriteByte('\n')
	}
	return b.String()
}

// ShowDiff prints a remembered change in full; :diff N selects the Nth change.
func (r *Renderer) ShowDiff(arg string) {
	if !r.diffOn() {
		r.Hint(diffDisabledHint)
		return
	}
	index, ok := diffIndex(arg)
	if !ok {
		r.Hint(diffUsageHint)
		return
	}
	if len(r.changes) == 0 {
		r.Hint(diffNoneHint)
		return
	}
	if index == 0 {
		index = len(r.changes)
	}
	if index > len(r.changes) {
		r.Hint(fmt.Sprintf("本次会话只有 %d 次文件改动", len(r.changes)))
		return
	}
	r.stopThinking()
	printLayout(r.opts.Out, r.opts.Color, buildDiffLines(r.changes[index-1], 0, index))
}

func (r *Renderer) diffOn() bool { return r.opts.Diff }
