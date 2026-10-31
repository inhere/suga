package chatui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gookit/color"
	"github.com/inhere/suga/pkg/chatui/termtext"
)

// chat.colors is the styling of chat's own lines - a tool call, its result, a plan,
// a hint. The model's answer keeps the terminal's default foreground and is painted
// by the markdown palette (chat.theme, pkg/chatui), so the two never share a color
// and an answer cannot be taken for a tool line.

// ToolFamily groups the tools by what a call does, so one tool line says at a
// glance whether it read, wrote, ran or fetched something. A tool without an entry
// in toolFamilies (an MCP tool) gets FamilyMeta.
type ToolFamily uint8

const (
	FamilyRead  ToolFamily = iota // reads the workspace
	FamilyWrite                   // writes or removes a file
	FamilyShell                   // runs a command
	FamilyNet                     // reaches the network
	FamilyAgent                   // runs a sub-agent
	FamilyMeta                    // session, memory, skills, plans
	FamilyCount = 6
)

// FamilySymbols are the markers of the tool lines, one per family, in FamilyRead..
// FamilyMeta order. They carry the distinction without colors too: a NO_COLOR
// terminal, a log file, a recording or a person who cannot tell these hues apart
// still reads a write apart from a read. A read tool keeps a triangle and not the
// record symbol ⏺ a call used to be marked with: many terminal fonts draw that one
// as a filled square, which is the answer's own marker ▣, so a tool line and an
// answer were told apart by their color alone.
var FamilySymbols = [FamilyCount]string{"▸", "✎", "$", "⇄", "◆", "✦"}

// FamilyNames are the chat.colors keys of the families, in the same order.
var FamilyNames = [FamilyCount]string{"read", "write", "shell", "net", "agent", "meta"}

// toolFamilies maps the built-in tools to their family. The tool line's symbol and
// color then say what a call does before its arguments are read.
var toolFamilies = map[string]ToolFamily{
	"file_read": FamilyRead, "file_list": FamilyRead, "file_find": FamilyRead,
	"file_grep": FamilyRead, "file_symbols": FamilyRead, "file_refs": FamilyRead,
	"view_image": FamilyRead,
	"file_write": FamilyWrite, "file_edit": FamilyWrite, "file_remove": FamilyWrite,
	"shell_exec": FamilyShell,
	"web_fetch":  FamilyNet, "web_search": FamilyNet,
	"agent":           FamilyAgent,
	"session_message": FamilyMeta, "session_search": FamilyMeta, "recall": FamilyMeta,
	"skill": FamilyMeta, "tool_search": FamilyMeta, "memory": FamilyMeta,
	"update_plan": FamilyMeta,
}

// ToolFamilyOf returns the family of a tool name; an unknown tool (an MCP tool, a
// tool a plugin added) keeps the neutral one.
func ToolFamilyOf(name string) ToolFamily {
	if family, ok := toolFamilies[name]; ok {
		return family
	}
	return FamilyMeta
}

// The keys of the chat.colors block: style is how much of a tool line the family
// color covers, mode is the color depth chat writes, and every other key names one
// color. A color is a 16 color name (blue, bright-cyan, purple), 256:NNN, #rrggbb
// or default for the terminal's own foreground.
const (
	ColorsKeyMode  = "mode"
	ColorsKeyStyle = "style"

	ColorModeAuto = "auto"
	ColorModeANSI = "ansi"
	ColorMode256  = "256"
	ColorModeTrue = "truecolor"
	ColorModeOff  = "off"

	// ColorStyleSymbol paints the family symbol only, ColorStyleName the symbol and
	// the tool name (the default), ColorStyleLine the whole call line.
	ColorStyleSymbol = "symbol"
	ColorStyleName   = "name"
	ColorStyleLine   = "line"

	ThemeLight = "light"
)

// colorElements are the chat.colors keys that name a color, in the order an error
// message lists them.
var colorElements = []string{"read", "write", "shell", "net", "agent", "meta", "ok", "error", "dim"}

// defaultColorSpecs is the built-in palette of one theme (chat.theme), in the
// chat.colors syntax: one hue per family, so the families stay apart, plus the ok
// label, the error line and the dim arguments. The light palette is the same set
// darker, because the dark hues nearly vanish on a light background.
func defaultColorSpecs(theme string) map[string]string {
	if theme == ThemeLight {
		return map[string]string{
			"read": "256:25", "write": "256:136", "shell": "256:127",
			"net": "256:30", "agent": "256:91", "meta": "256:28",
			"ok": "256:22", "error": "256:160", "dim": "256:240",
		}
	}
	return map[string]string{
		"read": "256:111", "write": "256:179", "shell": "256:170",
		"net": "256:80", "agent": "256:141", "meta": "256:114",
		"ok": "256:71", "error": "256:203", "dim": "256:245",
	}
}

// colorDepth is how many colors chat writes to this terminal.
type colorDepth uint8

const (
	depthANSI colorDepth = iota // the 16 colors every colored terminal has
	depth256                    // 256:NNN
	depthTrue                   // #rrggbb
	depthNone                   // no SGR at all (chat.colors.mode off)
)

// colorDepthFor resolves chat.colors.mode. auto keeps 256 colors when the terminal
// reports them (gookit/color reads TERM and COLORTERM) and falls back to the 16
// every colored terminal understands; off writes no SGR, which keeps the symbols
// and drops the colors, like a NO_COLOR terminal.
func colorDepthFor(mode string) (colorDepth, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ColorModeANSI:
		return depthANSI, nil
	case ColorMode256:
		return depth256, nil
	case ColorModeTrue:
		return depthTrue, nil
	case ColorModeOff:
		return depthNone, nil
	case "", ColorModeAuto:
		if color.IsSupport256Color() {
			return depth256, nil
		}
		return depthANSI, nil
	}
	return depthANSI, fmt.Errorf("chat.colors.mode must be auto, ansi, 256, truecolor or off, got %q", mode)
}

// Palette is the resolved styling of chat's own lines for this terminal: one SGR
// prefix per element, "" printing the terminal's default foreground. It is built
// once per chat, so the terminal's color depth is asked once.
type Palette struct {
	style    string
	families [FamilyCount]string
	ok       string
	errInk   string
	dim      string
	// plain turns every Paint into plain text (a host whose output is not a
	// colored terminal); the zero value paints.
	plain bool
}

// WithColor returns a copy of the palette that paints (true) or writes plain
// text (false). The tool lines of a host that has no colored terminal keep their
// symbols and drop the SGR.
func (p *Palette) WithColor(on bool) *Palette {
	c := *p
	c.plain = !on
	return &c
}

// Style is how much of a tool line the family color covers (ColorStyleSymbol,
// ColorStyleName or ColorStyleLine).
func (p *Palette) Style() string { return p.style }

// OKInk, ErrInk and DimInk are the SGR prefixes of the ok label, the error line
// and the dim arguments.
func (p *Palette) OKInk() string  { return p.ok }
func (p *Palette) ErrInk() string { return p.errInk }
func (p *Palette) DimInk() string { return p.dim }

// Paint paints s with one resolved chat.colors style (an SGR prefix, "" for the
// terminal's default foreground). Like the paint() above it writes no SGR when the
// color library is off for this process (NO_COLOR, a terminal without colors, a
// test that pins plain text).
func (p *Palette) Paint(sgr, s string) string {
	// Clean external text before adding our own SGR style, so plain and colored output are safe.
	s = termtext.Clean(s)
	if p.plain || sgr == "" || !color.Enable || !color.SupportColor() {
		return s
	}
	return paintSGR(sgr, s)
}

// PaintDim dims a line (the arguments and the notices around a tool call) on a
// colored terminal.
func (p *Palette) PaintDim(s string) string { return p.Paint(p.dim, s) }

// FamilyInk is the SGR prefix of one tool family.
func (p *Palette) FamilyInk(family ToolFamily) string { return p.families[family] }

// NewPalette resolves chat.colors for one chat: the defaults of theme (empty: the
// dark ones), overridden by the block's own values. An unknown key, style, mode or
// color is an error - a typo would otherwise silently leave a line as it was.
func NewPalette(theme string, settings map[string]string) (*Palette, error) {
	specs := defaultColorSpecs(theme)
	style, mode := ColorStyleName, ColorModeAuto
	for key, value := range settings {
		name := strings.ToLower(strings.TrimSpace(key))
		switch name {
		case ColorsKeyMode:
			mode = value
		case ColorsKeyStyle:
			style = value
		default:
			if _, ok := specs[name]; !ok {
				return nil, fmt.Errorf("chat.colors: unknown key %q, use one of %s", key,
					strings.Join(append([]string{ColorsKeyMode, ColorsKeyStyle}, colorElements...), ", "))
			}
			specs[name] = value
		}
	}
	style = strings.ToLower(strings.TrimSpace(style))
	switch style {
	case ColorStyleSymbol, ColorStyleName, ColorStyleLine:
	default:
		return nil, fmt.Errorf("chat.colors.style must be symbol, name or line, got %q", style)
	}
	depth, err := colorDepthFor(mode)
	if err != nil {
		return nil, err
	}
	inks := make(map[string]string, len(specs))
	for element, spec := range specs {
		if inks[element], err = sgrFor(element, spec, depth); err != nil {
			return nil, err
		}
	}
	p := &Palette{style: style, ok: inks["ok"], errInk: inks["error"], dim: inks["dim"]}
	for family := 0; family < FamilyCount; family++ {
		p.families[family] = inks[FamilyNames[family]]
	}
	return p, nil
}

// sgrFor turns one chat.colors value into the SGR prefix chat writes before a line,
// or "" for the terminal's default foreground.
func sgrFor(element, spec string, depth colorDepth) (string, error) {
	parsed, err := parseColorSpec(spec)
	if err != nil {
		return "", fmt.Errorf("chat.colors.%s: %w", element, err)
	}
	return parsed.sgr(depth), nil
}

// inkKind is the form a chat.colors value was written in.
type inkKind uint8

const (
	inkDefault inkKind = iota // default: the terminal's own foreground
	inkBasic                  // a 16 color name
	ink256                    // 256:NNN
	inkRGB                    // #rrggbb
)

// colorSpec is one parsed chat.colors value.
type colorSpec struct {
	kind  inkKind
	basic uint8
	index uint8
	rgb   [3]uint8
}

// basicColorNames are the 16 color names chat.colors accepts: the ANSI eight, the
// bright eight, and the aliases people write them with.
var basicColorNames = map[string]uint8{
	"black": 0, "red": 1, "green": 2, "yellow": 3, "blue": 4, "magenta": 5, "cyan": 6, "white": 7,
	"gray": 8, "grey": 8, "dark-gray": 8, "bright-black": 8,
	"bright-red": 9, "bright-green": 10, "bright-yellow": 11, "bright-blue": 12,
	"purple": 13, "bright-magenta": 13, "bright-cyan": 14, "bright-white": 15,
}

func parseColorSpec(spec string) (colorSpec, error) {
	value := strings.ToLower(strings.TrimSpace(spec))
	switch {
	case value == "" || value == "default":
		return colorSpec{kind: inkDefault}, nil
	case strings.HasPrefix(value, "#"):
		n, err := strconv.ParseUint(strings.TrimPrefix(value, "#"), 16, 32)
		if err != nil || len(strings.TrimPrefix(value, "#")) != 6 {
			return colorSpec{}, fmt.Errorf("%q: a hex color needs six hex digits, like #87afff", spec)
		}
		return colorSpec{kind: inkRGB, rgb: [3]uint8{uint8(n >> 16), uint8(n >> 8), uint8(n)}}, nil
	case strings.HasPrefix(value, "256:"):
		n, err := strconv.ParseUint(strings.TrimPrefix(value, "256:"), 10, 8)
		if err != nil {
			return colorSpec{}, fmt.Errorf("%q: 256:NNN needs a number from 0 to 255", spec)
		}
		return colorSpec{kind: ink256, index: uint8(n)}, nil
	}
	if basic, ok := basicColorNames[value]; ok {
		return colorSpec{kind: inkBasic, basic: basic}, nil
	}
	return colorSpec{}, fmt.Errorf("unknown color %q, use a name like blue, 256:111, #87afff or default", spec)
}

// sgr renders the prefix for one depth. A 16 color name always keeps its ANSI code,
// so a named color follows the terminal's own palette; a 256 or true color value
// degrades to the nearest color a narrower terminal has.
func (s colorSpec) sgr(depth colorDepth) string {
	if s.kind == inkDefault || depth == depthNone {
		return ""
	}
	switch s.kind {
	case inkBasic:
		return basicSGR(s.basic)
	case ink256:
		if depth == depthANSI {
			return basicSGR(nearestBasic(color.C256ToRgb(s.index)))
		}
		return "\x1b[38;5;" + strconv.Itoa(int(s.index)) + "m"
	case inkRGB:
		switch depth {
		case depthANSI:
			return basicSGR(nearestBasic(s.rgb[:]))
		case depth256:
			return "\x1b[38;5;" + strconv.Itoa(int(color.RgbTo256(s.rgb[0], s.rgb[1], s.rgb[2]))) + "m"
		}
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", s.rgb[0], s.rgb[1], s.rgb[2])
	}
	return ""
}

// basicSGR is the SGR of one of the 16 colors: 30-37 for the first eight and 90-97
// for the bright ones.
func basicSGR(basic uint8) string {
	if basic < 8 {
		return "\x1b[" + strconv.Itoa(30+int(basic)) + "m"
	}
	return "\x1b[" + strconv.Itoa(90+int(basic)-8) + "m"
}

// nearestBasic picks the closest of the 16 colors, for a terminal with none of the
// wider ones.
func nearestBasic(rgb []uint8) uint8 {
	if len(rgb) < 3 {
		return 7
	}
	return color.Rgb2basic(rgb[0], rgb[1], rgb[2], false)
}

// paintSGR wraps s in the prefix and a reset: the reset also closes whatever the
// line painted before, so a style never runs on into the next line.
func paintSGR(sgr, s string) string {
	if sgr == "" {
		return s
	}
	return sgr + s + "\x1b[0m"
}
