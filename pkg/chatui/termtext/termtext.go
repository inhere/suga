// Package termtext removes terminal control sequences from text that came from
// a model, a tool or another external source before chat writes it to a terminal.
package termtext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type Cleaner struct {
	state   state
	pending string
}
type state uint8

const (
	stateText state = iota
	stateEscape
	stateCSI
	stateString
	stateStringEscape
)

// Write removes terminal controls incrementally; escape sequences may span deltas.
func (c *Cleaner) Write(value string) string {
	if c.pending != "" {
		value, c.pending = c.pending+value, ""
	}
	var out strings.Builder
	for i := 0; i < len(value); {
		if c.state != stateText {
			i = c.consumeSequence(value, i)
			continue
		}
		b := value[i]
		switch {
		case b == 0x1b:
			c.state = stateEscape
			i++
		case b >= 0x80 && b <= 0x9f:
			c.startC1(b)
			i++
		case b < 0x20 || b == 0x7f:
			if b == '\n' || b == '\t' {
				out.WriteByte(b)
			}
			i++
		case b < 0x80:
			out.WriteByte(b)
			i++
		default:
			r, size := utf8.DecodeRuneInString(value[i:])
			if r == utf8.RuneError && size == 1 {
				if !utf8.FullRuneInString(value[i:]) {
					c.pending = value[i:]
					return out.String()
				}
				out.WriteByte(b)
				i++
				continue
			}
			if !unicode.IsControl(r) {
				out.WriteString(value[i : i+size])
			}
			i += size
		}
	}
	return out.String()
}
func (c *Cleaner) Flush() string { c.state, c.pending = stateText, ""; return "" }
func Clean(value string) string  { var c Cleaner; return c.Write(value) + c.Flush() }
func (c *Cleaner) startC1(b byte) {
	switch b {
	case 0x9b:
		c.state = stateCSI
	case 0x9d, 0x90, 0x98, 0x9e, 0x9f:
		c.state = stateString
	default:
		c.state = stateText
	}
}
func (c *Cleaner) consumeSequence(value string, i int) int {
	b := value[i]
	switch c.state {
	case stateEscape:
		switch b {
		case '[':
			c.state = stateCSI
		case ']', 'P', 'X', '^', '_':
			c.state = stateString
		default:
			c.state = stateText
		}
	case stateCSI:
		if b == 0x9c || (b >= 0x40 && b <= 0x7e) {
			c.state = stateText
		} else if b == 0x1b {
			c.state = stateEscape
		}
	case stateString:
		if b == 0x07 || b == 0x9c {
			c.state = stateText
		} else if b == 0x1b {
			c.state = stateStringEscape
		}
	case stateStringEscape:
		if b == '\\' || b == 0x9c {
			c.state = stateText
		} else if b != 0x1b {
			c.state = stateString
		}
	default:
		c.state = stateText
		return i
	}
	return i + 1
}
