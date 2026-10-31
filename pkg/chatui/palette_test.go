package chatui

import (
	"testing"

	"github.com/gookit/color"
	"github.com/gookit/goutil/x/assert"
)

// colorPalette builds a palette from settings and forces the color library on,
// so the assertions do not depend on the test environment's terminal.
func colorPalette(t *testing.T, theme string, settings map[string]string) *Palette {
	t.Helper()
	color.ForceColor()
	t.Cleanup(func() { color.RevertColorLevel() })
	return mustPalette(t, theme, settings)
}

// callLine renders one tool call through the palette.
func callLine(p *Palette, name, args string) string {
	return ToolCallLine(nil, p, ToolEvent{Name: name, Text: args}, "")
}

// TestToolFamilySymbols: every tool line starts with the symbol of its family, so
// the families stay apart without colors too (a NO_COLOR terminal, a log file).
func TestToolFamilySymbols(t *testing.T) {
	for _, tc := range []struct{ tool, symbol string }{
		{"file_read", "▸"}, {"file_list", "▸"}, {"file_grep", "▸"}, {"file_refs", "▸"},
		{"file_symbols", "▸"}, {"file_find", "▸"}, {"view_image", "▸"},
		{"file_write", "✎"}, {"file_edit", "✎"}, {"file_remove", "✎"},
		{"shell_exec", "$"},
		{"web_fetch", "⇄"}, {"web_search", "⇄"},
		{"agent", "◆"},
		{"session_message", "✦"}, {"session_search", "✦"}, {"recall", "✦"},
		{"skill", "✦"}, {"tool_search", "✦"}, {"memory", "✦"}, {"update_plan", "✦"},
		// A tool chat does not know (an MCP tool) keeps the neutral family.
		{"mcp__github__create_issue", "✦"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			assert.Eq(t, tc.symbol, FamilySymbols[ToolFamilyOf(tc.tool)])
		})
	}
}

// TestToolCallLineStyles: chat.colors.style picks how much of a tool line the
// family color covers; the arguments are always dim, the symbol always shows.
func TestToolCallLineStyles(t *testing.T) {
	read := "\x1b[38;5;111m▸ file_read\x1b[0m"
	dim := "\x1b[38;5;245m"
	t.Run("name is the default", func(t *testing.T) {
		p := colorPalette(t, "", map[string]string{ColorsKeyMode: ColorMode256})
		assert.Eq(t, read+" "+dim+"a.go\x1b[0m", callLine(p, "file_read", "a.go"))
		assert.Eq(t, read, callLine(p, "file_read", ""))
	})
	t.Run("symbol paints the marker only", func(t *testing.T) {
		p := colorPalette(t, "", map[string]string{ColorsKeyMode: ColorMode256, ColorsKeyStyle: ColorStyleSymbol})
		assert.Eq(t, "\x1b[38;5;111m▸\x1b[0m "+dim+"file_read\x1b[0m "+dim+"a.go\x1b[0m", callLine(p, "file_read", "a.go"))
	})
	t.Run("line paints the whole call", func(t *testing.T) {
		p := colorPalette(t, "", map[string]string{ColorsKeyMode: ColorMode256, ColorsKeyStyle: ColorStyleLine})
		assert.Eq(t, "\x1b[38;5;111m▸ file_read a.go\x1b[0m", callLine(p, "file_read", "a.go"))
	})
	t.Run("a terminal without colors keeps the symbols", func(t *testing.T) {
		p := mustPalette(t, "", nil).WithColor(false)
		assert.Eq(t, "✎ file_edit a.go", callLine(p, "file_edit", "a.go"))
	})
}

// TestPaletteFamiliesApart: the families get distinct colors, so a scroll back does
// not have to read a tool name to tell a read from a write.
func TestPaletteFamiliesApart(t *testing.T) {
	for _, theme := range []string{"", ThemeLight} {
		t.Run("theme "+theme, func(t *testing.T) {
			pal := mustPalette(t, theme, map[string]string{ColorsKeyMode: ColorMode256})
			seen := make(map[string]ToolFamily, FamilyCount)
			for family := 0; family < FamilyCount; family++ {
				ink := pal.FamilyInk(ToolFamily(family))
				assert.StrContains(t, ink, "\x1b[38;5;")
				if other, ok := seen[ink]; ok {
					t.Fatalf("families %s and %s share the color %q", FamilyNames[other], FamilyNames[family], ink)
				}
				seen[ink] = ToolFamily(family)
			}
			assert.Eq(t, FamilyCount, len(seen))
		})
	}
}

// TestPaletteThemes: the light theme is its own set of colors - the dark ones
// nearly vanish on a light background - and ok/dim stay apart too.
func TestPaletteThemes(t *testing.T) {
	// The 256 color mode is set explicitly: with no setting the depth follows the
	// terminal, and a CI runner without a 256 color terminal degrades the codes.
	mode := map[string]string{ColorsKeyMode: ColorMode256}
	dark, light := mustPalette(t, "", mode), mustPalette(t, ThemeLight, mode)
	assert.Eq(t, "\x1b[38;5;111m", dark.FamilyInk(FamilyRead))
	assert.Eq(t, "\x1b[38;5;25m", light.FamilyInk(FamilyRead))
	for family := 0; family < FamilyCount; family++ {
		assert.True(t, dark.FamilyInk(ToolFamily(family)) != light.FamilyInk(ToolFamily(family)),
			FamilyNames[family], "keeps a palette of its own")
	}
	assert.True(t, dark.DimInk() != dark.OKInk() && light.DimInk() != light.OKInk(), "the ok label and the dim arguments stay apart")
}

// TestPaletteColorDepth: a named color always keeps its ANSI code, while a 256 or
// true color value degrades to the nearest color a narrower terminal has.
func TestPaletteColorDepth(t *testing.T) {
	t.Run("a 16 color name", func(t *testing.T) {
		for _, mode := range []string{ColorModeANSI, ColorMode256, ColorModeTrue} {
			pal := mustPalette(t, "", map[string]string{ColorsKeyMode: mode, "read": "blue"})
			assert.Eq(t, "\x1b[34m", pal.FamilyInk(FamilyRead), mode)
		}
	})
	t.Run("256 colors", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{ColorsKeyMode: ColorMode256, "read": "256:75"})
		assert.Eq(t, "\x1b[38;5;75m", pal.FamilyInk(FamilyRead))
	})
	t.Run("true color", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{ColorsKeyMode: ColorModeTrue, "read": "#87afff"})
		assert.Eq(t, "\x1b[38;2;135;175;255m", pal.FamilyInk(FamilyRead))
	})
	t.Run("true color on a 256 color terminal", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{ColorsKeyMode: ColorMode256, "read": "#87afff"})
		ink := pal.FamilyInk(FamilyRead)
		assert.StrContains(t, ink, "\x1b[38;5;")
		assert.StrNotContains(t, ink, "38;2;")
	})
	t.Run("everything degrades to 16 colors", func(t *testing.T) {
		for _, spec := range []string{"256:75", "#87afff"} {
			pal := mustPalette(t, "", map[string]string{ColorsKeyMode: ColorModeANSI, "read": spec})
			ink := pal.FamilyInk(FamilyRead)
			assert.StrNotContains(t, ink, "38;", spec)
			assert.True(t, len(ink) > 0 && ink[len(ink)-1] == 'm', spec, "is an ANSI code")
		}
	})
	t.Run("off writes no SGR", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{ColorsKeyMode: ColorModeOff})
		for family := 0; family < FamilyCount; family++ {
			assert.Eq(t, "", pal.FamilyInk(ToolFamily(family)))
		}
		assert.Eq(t, "", pal.OKInk())
		assert.Eq(t, "", pal.ErrInk())
		assert.Eq(t, "", pal.DimInk())
	})
	t.Run("default keeps the terminal foreground", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{ColorsKeyMode: ColorMode256, "dim": "default"})
		assert.Eq(t, "", pal.DimInk())
		assert.StrContains(t, pal.FamilyInk(FamilyRead), "38;5;")
	})
}

// TestPaletteErrors: a typo in chat.colors stops chat instead of leaving a line as
// it was.
func TestPaletteErrors(t *testing.T) {
	for name, settings := range map[string]map[string]string{
		"an unknown element": {"reed": "blue"},
		"an unknown style":   {ColorsKeyStyle: "bold"},
		"an unknown mode":    {ColorsKeyMode: "16"},
		"an unknown color":   {"read": "blurple"},
		"a short hex":        {"read": "#abc"},
		"a 256 index range":  {"read": "256:999"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewPalette("", settings); err == nil {
				t.Fatalf("%v should be refused", settings)
			}
		})
	}
}

// mustPalette builds a palette or stops the test.
func mustPalette(t *testing.T, theme string, settings map[string]string) *Palette {
	t.Helper()
	pal, err := NewPalette(theme, settings)
	if err != nil {
		t.Fatal(err)
	}
	return pal
}
