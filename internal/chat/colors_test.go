package chat

import (
	"testing"

	"github.com/gookit/color"
	"github.com/gookit/goutil/x/assert"
)

// colorRenderer returns a renderer whose lines are painted, with a palette built
// from settings: the color library is forced on so the assertions do not depend on
// the test environment's terminal.
func colorRenderer(t *testing.T, theme string, settings map[string]string) *Renderer {
	t.Helper()
	color.ForceColor()
	t.Cleanup(func() { color.RevertColorLevel() })
	pal, err := newPalette(theme, settings)
	if err != nil {
		t.Fatal(err)
	}
	return &Renderer{opts: rendererOptions{Color: true}, pal: pal}
}

// TestToolFamilySymbols: every tool line starts with the symbol of its family, so
// the families stay apart without colors too (a NO_COLOR terminal, a log file).
func TestToolFamilySymbols(t *testing.T) {
	for _, tc := range []struct{ tool, symbol string }{
		{"file_read", "⏺"}, {"file_list", "⏺"}, {"file_grep", "⏺"}, {"file_refs", "⏺"},
		{"file_symbols", "⏺"}, {"file_find", "⏺"}, {"view_image", "⏺"},
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
			assert.Eq(t, tc.symbol, familySymbols[toolFamilyOf(tc.tool)])
		})
	}
}

// TestToolCallLineStyles: chat.colors.style picks how much of a tool line the
// family color covers; the arguments are always dim, the symbol always shows.
func TestToolCallLineStyles(t *testing.T) {
	read := "\x1b[38;5;111m⏺ file_read\x1b[0m"
	dim := "\x1b[38;5;245m"
	t.Run("name is the default", func(t *testing.T) {
		r := colorRenderer(t, "", map[string]string{colorsKeyMode: colorMode256})
		assert.Eq(t, read+" "+dim+"a.go\x1b[0m", r.toolCallLine("file_read", "a.go"))
		assert.Eq(t, read, r.toolCallLine("file_read", ""))
	})
	t.Run("symbol paints the marker only", func(t *testing.T) {
		r := colorRenderer(t, "", map[string]string{colorsKeyMode: colorMode256, colorsKeyStyle: colorStyleSymbol})
		assert.Eq(t, "\x1b[38;5;111m⏺\x1b[0m "+dim+"file_read\x1b[0m "+dim+"a.go\x1b[0m", r.toolCallLine("file_read", "a.go"))
	})
	t.Run("line paints the whole call", func(t *testing.T) {
		r := colorRenderer(t, "", map[string]string{colorsKeyMode: colorMode256, colorsKeyStyle: colorStyleLine})
		assert.Eq(t, "\x1b[38;5;111m⏺ file_read a.go\x1b[0m", r.toolCallLine("file_read", "a.go"))
	})
	t.Run("a terminal without colors keeps the symbols", func(t *testing.T) {
		r := &Renderer{opts: rendererOptions{}, pal: mustPalette(t, "", nil)}
		assert.Eq(t, "✎ file_edit a.go", r.toolCallLine("file_edit", "a.go"))
	})
}

// TestPaletteFamiliesApart: the families get distinct colors, so a scroll back does
// not have to read a tool name to tell a read from a write.
func TestPaletteFamiliesApart(t *testing.T) {
	for _, theme := range []string{"", themeLight} {
		t.Run("theme "+theme, func(t *testing.T) {
			pal := mustPalette(t, theme, map[string]string{colorsKeyMode: colorMode256})
			seen := make(map[string]toolFamily, familyCount)
			for family := 0; family < familyCount; family++ {
				ink := pal.familyInk(toolFamily(family))
				assert.StrContains(t, ink, "\x1b[38;5;")
				if other, ok := seen[ink]; ok {
					t.Fatalf("families %s and %s share the color %q", familyNames[other], familyNames[family], ink)
				}
				seen[ink] = toolFamily(family)
			}
			assert.Eq(t, familyCount, len(seen))
		})
	}
}

// TestPaletteThemes: the light theme is its own set of colors - the dark ones
// nearly vanish on a light background - and ok/dim stay apart too.
func TestPaletteThemes(t *testing.T) {
	dark, light := mustPalette(t, "", nil), mustPalette(t, themeLight, nil)
	assert.Eq(t, "\x1b[38;5;111m", dark.familyInk(familyRead))
	assert.Eq(t, "\x1b[38;5;25m", light.familyInk(familyRead))
	for family := 0; family < familyCount; family++ {
		assert.True(t, dark.familyInk(toolFamily(family)) != light.familyInk(toolFamily(family)),
			familyNames[family], "keeps a palette of its own")
	}
	assert.True(t, dark.dim != dark.ok && light.dim != light.ok, "the ok label and the dim arguments stay apart")
}

// TestPaletteColorDepth: a named color always keeps its ANSI code, while a 256 or
// true color value degrades to the nearest color a narrower terminal has.
func TestPaletteColorDepth(t *testing.T) {
	t.Run("a 16 color name", func(t *testing.T) {
		for _, mode := range []string{colorModeANSI, colorMode256, colorModeTrue} {
			pal := mustPalette(t, "", map[string]string{colorsKeyMode: mode, "read": "blue"})
			assert.Eq(t, "\x1b[34m", pal.familyInk(familyRead), mode)
		}
	})
	t.Run("256 colors", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{colorsKeyMode: colorMode256, "read": "256:75"})
		assert.Eq(t, "\x1b[38;5;75m", pal.familyInk(familyRead))
	})
	t.Run("true color", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{colorsKeyMode: colorModeTrue, "read": "#87afff"})
		assert.Eq(t, "\x1b[38;2;135;175;255m", pal.familyInk(familyRead))
	})
	t.Run("true color on a 256 color terminal", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{colorsKeyMode: colorMode256, "read": "#87afff"})
		ink := pal.familyInk(familyRead)
		assert.StrContains(t, ink, "\x1b[38;5;")
		assert.StrNotContains(t, ink, "38;2;")
	})
	t.Run("everything degrades to 16 colors", func(t *testing.T) {
		for _, spec := range []string{"256:75", "#87afff"} {
			pal := mustPalette(t, "", map[string]string{colorsKeyMode: colorModeANSI, "read": spec})
			ink := pal.familyInk(familyRead)
			assert.StrNotContains(t, ink, "38;", spec)
			assert.True(t, len(ink) > 0 && ink[len(ink)-1] == 'm', spec, "is an ANSI code")
		}
	})
	t.Run("off writes no SGR", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{colorsKeyMode: colorModeOff})
		for family := 0; family < familyCount; family++ {
			assert.Eq(t, "", pal.familyInk(toolFamily(family)))
		}
		assert.Eq(t, "", pal.ok)
		assert.Eq(t, "", pal.errInk)
		assert.Eq(t, "", pal.dim)
	})
	t.Run("default keeps the terminal foreground", func(t *testing.T) {
		pal := mustPalette(t, "", map[string]string{colorsKeyMode: colorMode256, "dim": "default"})
		assert.Eq(t, "", pal.dim)
		assert.StrContains(t, pal.familyInk(familyRead), "38;5;")
	})
}

// TestPaletteErrors: a typo in chat.colors stops chat instead of leaving a line as
// it was.
func TestPaletteErrors(t *testing.T) {
	for name, settings := range map[string]map[string]string{
		"an unknown element": {"reed": "blue"},
		"an unknown style":   {colorsKeyStyle: "bold"},
		"an unknown mode":    {colorsKeyMode: "16"},
		"an unknown color":   {"read": "blurple"},
		"a short hex":        {"read": "#abc"},
		"a 256 index range":  {"read": "256:999"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newPalette("", settings); err == nil {
				t.Fatalf("%v should be refused", settings)
			}
		})
	}
}

// mustPalette builds a palette or stops the test.
func mustPalette(t *testing.T, theme string, settings map[string]string) *palette {
	t.Helper()
	pal, err := newPalette(theme, settings)
	if err != nil {
		t.Fatal(err)
	}
	return pal
}
