package native

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// tomlConfigFixture is the TOML extractor's fixture: two tables, a dotted key
// inside a table, an inline table, a multi-line array, both multi-line string
// forms, an array of tables and comments (one of them looks like a key). The
// line numbers in the test are this fixture's.
const tomlConfigFixture = `# a comment: title = "nope"
title = "demo"

[server]
port = 8080
hosts = ["a", "b"]

[server.http]
timeout = 30

[extra]
extra.stylesheets = ["/a.css"]
inline = { a = 1, b = 2 }
stylesheets = [
  "/x.css", # port = 1 in a comment
  "/y.css",
]
note = """
port = 2 in a string
"""
literal = '''
other = 3
'''

[[products]]
name = "hammer"

[[products]]
name = "nail"
`

// TestExtractSymbolsTOML is the line recogniser: table headers are sections, the
// assignments are keys, and the table prefixes the key path.
func TestExtractSymbolsTOML(t *testing.T) {
	syms, set := extractFixture(t, t.TempDir(), "config.toml", tomlConfigFixture)
	assert.Eq(t, sourceDeclLines, set.Source)
	assert.True(t, set.Approx, "a line-based result is marked approx")
	assert.Eq(t, []string{
		"config.toml:2 key title",
		"config.toml:4 section server",
		"config.toml:5 key server.port",
		"config.toml:6 key server.hosts",
		"config.toml:8 section server.http",
		"config.toml:9 key server.http.timeout",
		"config.toml:11 section extra",
		"config.toml:12 key extra.extra.stylesheets",
		"config.toml:13 key extra.inline",
		"config.toml:14 key extra.stylesheets",
		"config.toml:18 key extra.note",
		"config.toml:21 key extra.literal",
		"config.toml:25 section products[]",
		"config.toml:26 key products[].name",
	}, symbolsAsText(syms))

	t.Run("a table or array value is marked, a scalar is previewed", func(t *testing.T) {
		for name, want := range map[string]string{
			"title":                   `"demo"`,
			"server":                  configMappingPreview,
			"server.port":             "8080",
			"server.hosts":            configArrayPreview,
			"extra.inline":            configMappingPreview, // an inline table: only the outer key is listed
			"extra.stylesheets":       configArrayPreview,
			"extra.extra.stylesheets": configArrayPreview,
		} {
			assert.Eq(t, want, symbolByName(t, syms, name).Signature)
		}
	})

	t.Run("comments and multi-line strings hold no keys", func(t *testing.T) {
		names := strings.Join(symbolNames(syms), " ")
		// The comment on line 1 writes "title = ..." as well: it may not add a
		// second key of that name.
		assert.Eq(t, 1, strings.Count(names, "title"))
		// The array's trailing comment and the two multi-line strings each look
		// like an assignment of a key of the extra table: none of them is one.
		assert.NotContains(t, names, "extra.port")
		assert.NotContains(t, names, "extra.other")
		// The inner key of the inline table is not listed either: its line's own
		// key is previewed as a table instead.
		assert.NotContains(t, names, "inline.a")
		assert.Eq(t, `"""`, symbolByName(t, syms, "extra.note").Signature)
		assert.Eq(t, "'''", symbolByName(t, syms, "extra.literal").Signature)
	})

	t.Run("an array of tables is one path with its entry count", func(t *testing.T) {
		assert.Eq(t, configArrayPreview+" ×2", symbolByName(t, syms, "products[]").Signature)
		assert.Eq(t, `"hammer" ×2`, symbolByName(t, syms, "products[].name").Signature)
		assert.StrContains(t, strings.Join(set.Notes, "\n"),
			"[toml: 2 key path(s) repeat")
	})
}

// TestExtractSymbolsTOMLCaps covers the lines the recogniser must not read as a
// declaration, and the two scale limits.
func TestExtractSymbolsTOMLCaps(t *testing.T) {
	t.Run("a continuation line or a value's own key is no declaration", func(t *testing.T) {
		src := "list = [\n" + // the value's continuation lines
			"  { name = \"inner\" },\n" +
			"  \"a=b\",\n" +
			"]\n"
		syms, _ := extractFixture(t, t.TempDir(), "arr.toml", src)
		assert.Eq(t, []string{"arr.toml:1 key list"}, symbolsAsText(syms))
	})

	t.Run("a key deeper than the depth cap is counted, not listed", func(t *testing.T) {
		syms, set := extractFixture(t, t.TempDir(), "deep.toml", "[a.b.c.d.e]\nx = 1\n[one.two.three]\ny = 2\n")
		assert.Eq(t, []string{"deep.toml:3 section one.two.three", "deep.toml:4 key one.two.three.y"}, symbolsAsText(syms))
		assert.StrContains(t, strings.Join(set.Notes, "\n"),
			fmt.Sprintf("[toml: only the first %d key levels are listed", maxConfigDepth))
	})

	t.Run("a file with more keys than the cap is cut and says how to read on", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < maxConfigKeys+5; i++ {
			fmt.Fprintf(&sb, "key%03d = %d\n", i, i)
		}
		syms, set := extractFixture(t, t.TempDir(), "big.toml", sb.String())
		assert.Eq(t, maxConfigKeys, len(syms))
		assert.StrContains(t, strings.Join(set.Notes, "\n"),
			fmt.Sprintf("[toml: showing the first %d keys; for the rest use file_grep]", maxConfigKeys))
	})
}
