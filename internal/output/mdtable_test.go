package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// renderMDWidth renders text plain (no colors) for a terminal of width columns.
func renderMDWidth(text string, width int) string {
	var out bytes.Buffer
	term := NewMDTerm(&out, false)
	term.SetWidth(width)
	term.Write(text)
	term.Flush()
	return out.String()
}

// TestMDTableAlign: a GFM table (header, delimiter row, body) is printed with
// every column as wide as its widest cell, a ─ separator and a bold header.
func TestMDTableAlign(t *testing.T) {
	t.Run("chinese, inline code and bold", func(t *testing.T) {
		got := renderMDWidth("| 名称 | 说明 |\n| --- | --- |\n| `a` | **b** |\n| 中文 | long |\n", 0)
		assert.Eq(t, strings.Join([]string{
			"│ 名称 │ 说明 │",
			"───────────────",
			"│ a    │ b    │",
			"│ 中文 │ long │",
			"",
		}, "\n"), got)
	})
	t.Run("the alignment markers of the delimiter row", func(t *testing.T) {
		got := renderMDWidth("| a |\n| :---: |\n| bbbb |\n", 0)
		assert.Eq(t, strings.Join([]string{
			"│  a   │",
			"────────",
			"│ bbbb │",
			"",
		}, "\n"), got)
		right := renderMDWidth("| a |\n| ---: |\n| bbbb |\n", 0)
		assert.Eq(t, strings.Join([]string{
			"│    a │",
			"────────",
			"│ bbbb │",
			"",
		}, "\n"), right)
	})
	t.Run("a line with a pipe but no delimiter stays", func(t *testing.T) {
		const text = "a | b\n1 | 2\n"
		assert.Eq(t, text, renderMDWidth(text, 0))
	})
	t.Run("the table ends with the message", func(t *testing.T) {
		// No newline after the last row: the message end prints the table.
		assert.Eq(t, "│ a │ b │\n─────────\n│ 1 │ 2 │\n", renderMDWidth("| a | b |\n| --- | --- |\n| 1 | 2 |", 0))
	})
	t.Run("a table followed by text", func(t *testing.T) {
		assert.Eq(t, "│ a │\n─────\n│ 1 │\ntail\n", renderMDWidth("| a |\n| --- |\n| 1 |\ntail\n", 0))
	})
}

// TestMDTableStreaming: the rows are buffered until the table ends, so a row
// split over several deltas is aligned like a whole one.
func TestMDTableStreaming(t *testing.T) {
	const text = "before\n| a | b |\n| --- | --- |\n| 1 | 2 |\nafter\n"
	whole := renderMDWidth(text, 0)
	assert.Eq(t, "before\n│ a │ b │\n─────────\n│ 1 │ 2 │\nafter\n", whole)
	for cut := 1; cut < len(text); cut++ {
		var out bytes.Buffer
		term := NewMDTerm(&out, false)
		term.Write(text[:cut]) // a delta may cut a table row in half
		term.Write(text[cut:])
		term.Flush()
		assert.Eq(t, whole, out.String(), cut)
	}
}

// TestMDTableWide: a table wider than the terminal is printed exactly as it came
// (never truncated) and a block of "|" lines without the delimiter row is no
// table.
func TestMDTableWide(t *testing.T) {
	t.Run("wider than the terminal", func(t *testing.T) {
		const text = "| 名称 | 说明 |\n| --- | --- |\n| 中文 | long |\n"
		assert.Eq(t, text, renderMDWidth(text, 12))
	})
	t.Run("no delimiter row", func(t *testing.T) {
		const text = "| a | b |\n| c | d |\n"
		assert.Eq(t, text, renderMDWidth(text, 0))
	})
	t.Run("a row with another cell count", func(t *testing.T) {
		const text = "| a | b |\n| --- | --- |\n| 1 |\n"
		assert.Eq(t, text, renderMDWidth(text, 0))
	})
}
