package output

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

// GFM tables of chat's markdown output: a table is a header line, a delimiter
// line (|---|:---:|) and body lines, every line starting with "|". The lines
// arrive while the answer streams, so the table is buffered until its end (a
// line that is not a table row, or the end of the message); it is then printed
// with every column aligned by display width (a CJK character is two columns).
// A block of "|" lines without the delimiter line is no table and stays exactly
// as the model wrote it, like before.

const (
	// mdTableBorder is the vertical border of a rendered table line.
	mdTableBorder = "│"
	// mdTablePipe is the ASCII cell separator of the markdown source.
	mdTablePipe = "|"
	// mdTablePad is the fill character of a padded cell.
	mdTablePad = " "
)

// mdTableSeparatorAligns reads the alignment of every column from the delimiter
// line ("---", ":---", "---:" and ":---:"); ok is false when a cell is no
// delimiter at all.
func mdTableSeparatorAligns(line string) (aligns []mdTableAlign, ok bool) {
	cells := mdTableCells(line)
	if len(cells) == 0 {
		return nil, false
	}
	aligns = make([]mdTableAlign, len(cells))
	for i, cell := range cells {
		left := strings.HasPrefix(cell, ":")
		right := strings.HasSuffix(cell, ":")
		body := strings.Trim(cell, ":")
		if body == "" || strings.Trim(body, "-") != "" {
			return nil, false
		}
		switch {
		case left && right:
			aligns[i] = mdAlignCenter
		case right:
			aligns[i] = mdAlignRight
		default:
			aligns[i] = mdAlignLeft
		}
	}
	return aligns, true
}

// mdTableAlign is how one table column pads its cells.
type mdTableAlign int

const (
	mdAlignLeft mdTableAlign = iota
	mdAlignRight
	mdAlignCenter
)

// mdTableCells splits one table line into its trimmed cells; the outer borders
// are optional, so both "| a | b |" and "a | b" work.
func mdTableCells(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, mdTablePipe)
	s = strings.TrimSuffix(s, mdTablePipe)
	if s == "" {
		return []string{""}
	}
	cells := strings.Split(s, mdTablePipe)
	for i, cell := range cells {
		cells[i] = strings.TrimSpace(cell)
	}
	return cells
}

// mdDisplayWidth is the column count of a rendered cell, without its ANSI
// styles: chat pads by what the terminal shows, not by the escape bytes.
func mdDisplayWidth(s string) int { return runewidth.StringWidth(mdStripANSI(s)) }

// mdStripANSI removes the SGR sequences of this renderer, so a styled cell can
// be measured and re-styled.
func mdStripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return mdANSIPattern.ReplaceAllString(s, "")
}

var mdANSIPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// mdPadCell pads a rendered cell to width columns according to align.
func mdPadCell(cell string, width int, align mdTableAlign) string {
	gap := width - mdDisplayWidth(cell)
	if gap <= 0 {
		return cell
	}
	switch align {
	case mdAlignRight:
		return strings.Repeat(mdTablePad, gap) + cell
	case mdAlignCenter:
		left := gap / 2
		return strings.Repeat(mdTablePad, left) + cell + strings.Repeat(mdTablePad, gap-left)
	default:
		return cell + strings.Repeat(mdTablePad, gap)
	}
}

// flushTable ends the buffered table lines and returns the lines to print: the
// aligned table when the second buffered line is a delimiter line, the lines as
// they came otherwise.
func (t *MDTerm) flushTable() []string {
	rows := t.table
	t.table = nil
	if len(rows) == 0 {
		return nil
	}
	if len(rows) < 2 {
		return rows
	}
	aligns, ok := mdTableSeparatorAligns(rows[1])
	if !ok {
		return rows
	}
	aligned, ok := t.alignTable(rows, aligns)
	if !ok {
		return rows
	}
	return aligned
}

// alignTable renders the buffered table rows: the header with its inline
// markers, one padded line per body row, a separator line of ─ characters and a
// bold header. ok is false when the rows do not form one table (a body row with
// another cell count) or when the table is wider than the terminal: it is then
// printed as it came, never truncated.
func (t *MDTerm) alignTable(rows []string, aligns []mdTableAlign) ([]string, bool) {
	cols := len(aligns)
	header := mdTableCells(rows[0])
	if len(header) != cols {
		return nil, false
	}
	body := make([][]string, 0, len(rows)-2)
	for _, row := range rows[2:] {
		cells := mdTableCells(row)
		if len(cells) != cols {
			return nil, false
		}
		body = append(body, cells)
	}
	// The display width of a column is the widest cell it holds, the header
	// included; every cell is rendered first, its inline markers and all.
	headerCells := make([]string, cols)
	widths := make([]int, cols)
	for i, cell := range header {
		headerCells[i] = t.inline(cell, nil)
		widths[i] = mdDisplayWidth(headerCells[i])
	}
	bodyCells := make([][]string, len(body))
	for r, row := range body {
		bodyCells[r] = make([]string, cols)
		for i, cell := range row {
			bodyCells[r][i] = t.inline(cell, nil)
			if w := mdDisplayWidth(bodyCells[r][i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	total := 4 // the border and a space on each side of the row
	for i, width := range widths {
		total += width
		if i > 0 {
			total += 3 // " │ " between two cells
		}
	}
	if t.width > 0 && total > t.width {
		return nil, false
	}
	lines := make([]string, 0, len(body)+2)
	// The header keeps its inline markers' rendering but is bold as a whole: a
	// reader tells it from the body at a glance.
	lines = append(lines, t.paint(t.theme.header, strings.Join([]string{
		mdTableBorder,
		mdTableLine(headerCells, widths, aligns, true),
		mdTableBorder,
	}, " ")))
	lines = append(lines, t.paint(t.theme.dim, strings.Repeat("─", total)))
	for _, cells := range bodyCells {
		lines = append(lines, strings.Join([]string{
			mdTableBorder,
			mdTableLine(cells, widths, aligns, false),
			mdTableBorder,
		}, " "))
	}
	return lines, true
}

// mdTableLine joins one row's rendered cells, every one padded to its column
// width; header rows drop the inline styles they may carry, since the whole
// header line is painted bold anyway.
func mdTableLine(cells []string, widths []int, aligns []mdTableAlign, header bool) string {
	parts := make([]string, len(cells))
	for i, cell := range cells {
		if header {
			cell = mdStripANSI(cell)
		}
		parts[i] = mdPadCell(cell, widths[i], aligns[i])
	}
	return strings.Join(parts, " "+mdTableBorder+" ")
}
