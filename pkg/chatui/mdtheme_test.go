package chatui

import (
	"bytes"
	"testing"

	gcolor "github.com/gookit/color"

	"github.com/gookit/goutil/x/assert"
)

// The palettes of the two themes (chat.theme): which SGR style each element is
// painted in. dark is the palette chat had before the option existed - the
// existing tests assert it - and light is the one for a light background, where
// the dark values are barely readable (see mdterm.go).
const (
	themeHeadingSGR = "\x1b[1;36m" // dark heading: bold cyan
	themeCodeSGR    = "\x1b[93m"   // dark inline code: bright yellow
	themeDimSGR     = "\x1b[90m"   // dark quote/fence/plain code: gray
)

// renderTheme renders text with code highlighting on, on the named theme; like
// the other output tests it forces color, so the SGR does not depend on the ENV
// or on test order.
func renderTheme(t *testing.T, theme, text string) string {
	t.Helper()
	gcolor.ForceColor()
	var out bytes.Buffer
	term := NewMDTerm(&out, true)
	term.SetCode(true, 0)
	term.SetTheme(theme)
	term.Write(text)
	return out.String()
}

// TestMDThemeDark: the default theme of a new MDTerm is the one chat had before
// the theme option existed, byte for byte.
func TestMDThemeDark(t *testing.T) {
	t.Run("headings and inline code keep their SGR", func(t *testing.T) {
		got := renderTheme(t, "dark", "# Title\n\nrun `make smoke`\n")
		assert.StrContains(t, got, themeHeadingSGR+"Title")
		assert.StrContains(t, got, themeCodeSGR+"make smoke")
	})
	t.Run("quote, fences and plain code keep the gray", func(t *testing.T) {
		got := renderTheme(t, "dark", "> quoted\n\n```\nx := 1\n```\n")
		assert.StrContains(t, got, themeDimSGR+"│ ")
		assert.StrContains(t, got, themeDimSGR+"───")
		assert.StrContains(t, got, themeDimSGR+"x := 1")
	})
	t.Run("an unknown theme keeps dark", func(t *testing.T) {
		got := renderTheme(t, "no-such-theme", "# Title\n")
		assert.StrContains(t, got, themeHeadingSGR+"Title")
	})
}

// TestMDThemeLight: the light theme paints the same markdown elements with
// SGR colors that stay legible on a light terminal background: a bold blue
// heading, a magenta inline code (the bright yellow of dark is invisible
// there), the quote/fence/plain-code gray in the normal foreground color, and
// the code highlighter's keyword in blue instead of the weak light cyan.
func TestMDThemeLight(t *testing.T) {
	t.Run("heading and inline code", func(t *testing.T) {
		got := renderTheme(t, "light", "# Title\n\nrun `make smoke`\n")
		assert.StrContains(t, got, "\x1b[1;34mTitle")
		assert.StrContains(t, got, "\x1b[35mmake smoke")
		assert.StrNotContains(t, got, themeHeadingSGR)
		assert.StrNotContains(t, got, themeCodeSGR)
	})
	t.Run("quote, fences and plain code lose the light gray", func(t *testing.T) {
		got := renderTheme(t, "light", "> quoted\n\n```\nx := 1\n```\n")
		assert.StrContains(t, got, "\x1b[30m│ ")
		assert.StrContains(t, got, "\x1b[30m───")
		assert.StrContains(t, got, "\x1b[30mx := 1")
		assert.StrNotContains(t, got, themeDimSGR)
	})
	t.Run("code highlight keeps gray comments and blue keywords", func(t *testing.T) {
		got := renderTheme(t, "light", "```go\nfunc a() { // note\nx := 1\n}\n```\n")
		assert.StrContains(t, got, "\x1b[90m// note")
		assert.StrContains(t, got, "\x1b[34mfunc")
		assert.StrNotContains(t, got, "\x1b[36m")
	})
	t.Run("the highlighter shares the term's palette", func(t *testing.T) {
		// startCode hands its theme palette to the highlighter (newMDCode), so a
		// light terminal colors a keyword with the light palette's keyword too.
		gcolor.ForceColor()
		var out bytes.Buffer
		term := NewMDTerm(&out, true)
		term.SetCode(true, 0)
		term.SetTheme("light")
		term.Write("```go\nfunc a() // c\n```\n")
		assert.StrContains(t, out.String(), "\x1b[34mfunc")
	})
}
