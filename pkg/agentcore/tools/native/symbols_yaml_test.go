package native

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// yamlConfigFixture is the YAML extractor's fixture: a nested mapping, a
// sequence of mappings (whose keys share one path), an anchor with its alias, a
// list of scalars, an empty value and a quoted key. The line numbers in the test
// are this fixture's.
const yamlConfigFixture = `# a top comment
server:
  http:
    port: 8080
    hosts:
      - a.example
  name: "svc"
base: &base
  retry: 3
copy: *base
services:
  - name: api
    image: api:1
    env:
      DEBUG: "1"
  - name: web
list:
  - a
  - b
empty:
"quoted-key": 1
`

// TestExtractSymbolsYAML is the parsed path: goccy/go-yaml gives every key its
// line, and the key path (server.http.port, services[].name) is the name.
func TestExtractSymbolsYAML(t *testing.T) {
	syms, set := extractFixture(t, t.TempDir(), "config.yaml", yamlConfigFixture)
	assert.Eq(t, sourceYamlAST, set.Source)
	assert.False(t, set.Approx, "the parser read the file, so the result is not approx")
	assert.Eq(t, []string{
		"config.yaml:2 section server",
		"config.yaml:3 section server.http",
		"config.yaml:4 key server.http.port",
		"config.yaml:5 key server.http.hosts",
		"config.yaml:7 key server.name",
		"config.yaml:8 section base",
		"config.yaml:9 key base.retry",
		"config.yaml:10 key copy",
		"config.yaml:11 section services",
		"config.yaml:12 key services[].name",
		"config.yaml:13 key services[].image",
		"config.yaml:14 section services[].env",
		"config.yaml:15 key services[].env.DEBUG",
		"config.yaml:17 key list",
		"config.yaml:20 key empty",
		"config.yaml:21 key quoted-key",
	}, symbolsAsText(syms))

	t.Run("the signature is the value, a mapping or an array marked", func(t *testing.T) {
		for name, want := range map[string]string{
			"server":            configMappingPreview,
			"server.http":       configMappingPreview,
			"server.http.port":  "8080",
			"server.http.hosts": configArrayPreview,
			"server.name":       `"svc"`, // the value as written, quotes included
			"services":          configArrayPreview,
			"services[].env":    configMappingPreview,
			"list":              configArrayPreview,
		} {
			assert.Eq(t, want, symbolByName(t, syms, name).Signature)
		}
	})

	t.Run("an anchor is not followed, and its alias stays a leaf", func(t *testing.T) {
		assert.Eq(t, "{…}", symbolByName(t, syms, "base").Signature)
		assert.Eq(t, "*base", symbolByName(t, syms, "copy").Signature)
		// The anchor's key is listed once, where the anchor stands.
		assert.Eq(t, 1, strings.Count(strings.Join(symbolNames(syms), " "), "retry"))
	})

	t.Run("the entries of a sequence share one path with a repeat count", func(t *testing.T) {
		assert.Eq(t, "api ×2", symbolByName(t, syms, "services[].name").Signature)
		// The sequence's own key is declared once: no count, only the array mark.
		assert.Eq(t, configArrayPreview, symbolByName(t, syms, "services").Signature)
	})

	t.Run("the .yml extension is the same language", func(t *testing.T) {
		yml, ymlSet := extractFixture(t, t.TempDir(), "config.yml", "a:\n  b: 1\n")
		assert.Eq(t, sourceYamlAST, ymlSet.Source)
		assert.Eq(t, []string{"config.yml:1 section a", "config.yml:2 key a.b"}, symbolsAsText(yml))
	})
}

// TestExtractSymbolsYAMLMultiDocument covers a file with several documents: the
// document number prefixes every key path, so two documents declaring the same
// key do not read as one repeated key.
func TestExtractSymbolsYAMLMultiDocument(t *testing.T) {
	src := "server:\n  port: 1\n---\nserver:\n  port: 2\n---\nserver:\n  port: 3\n"
	syms, set := extractFixture(t, t.TempDir(), "multi.yaml", src)
	assert.False(t, set.Approx)
	assert.Eq(t, []string{
		"multi.yaml:1 section doc1.server",
		"multi.yaml:2 key doc1.server.port",
		"multi.yaml:4 section doc2.server",
		"multi.yaml:5 key doc2.server.port",
		"multi.yaml:7 section doc3.server",
		"multi.yaml:8 key doc3.server.port",
	}, symbolsAsText(syms))

	t.Run("a single-document file carries no document prefix", func(t *testing.T) {
		single, _ := extractFixture(t, t.TempDir(), "single.yaml", "server:\n  port: 1\n")
		assert.Eq(t, []string{"single.yaml:1 section server", "single.yaml:2 key server.port"}, symbolsAsText(single))
	})
}

// TestExtractSymbolsYAMLParserFallback covers the file the parser rejects (a tab
// used for indentation): the keys come from indentation detection and the result
// says so, instead of a failed call.
func TestExtractSymbolsYAMLParserFallback(t *testing.T) {
	src := "server:\n\tport: 8080\nservices:\n  - name: api\n    image: api:1\n"
	syms, set := extractFixture(t, t.TempDir(), "broken.yaml", src)
	assert.True(t, set.Approx, "the fallback recognises lines, not a parse")
	assert.Eq(t, sourceDeclLines, set.Source)
	assert.Eq(t, []string{
		"broken.yaml:1 section server",
		"broken.yaml:2 key server.port",
		"broken.yaml:3 section services",
		"broken.yaml:4 key services[].name",
		"broken.yaml:5 key services[].image",
	}, symbolsAsText(syms))
	assert.Eq(t, "8080", symbolByName(t, syms, "server.port").Signature)
	notes := strings.Join(set.Notes, "\n")
	assert.StrContains(t, notes, "this file did not parse")
	assert.StrContains(t, notes, "indentation detection and are approx")
}

// TestExtractSymbolsYAMLCaps covers the two scale limits: a key deeper than the
// depth cap and a file with more keys than the cap are left out, and the notes
// say what to use instead.
func TestExtractSymbolsYAMLCaps(t *testing.T) {
	t.Run("a key deeper than the depth cap is counted, not listed", func(t *testing.T) {
		syms, set := extractFixture(t, t.TempDir(), "deep.yaml", "a:\n  b:\n    c:\n      d:\n        e: 1\n")
		assert.Eq(t, []string{
			"deep.yaml:1 section a",
			"deep.yaml:2 section a.b",
			"deep.yaml:3 section a.b.c",
			"deep.yaml:4 section a.b.c.d",
		}, symbolsAsText(syms))
		assert.StrContains(t, strings.Join(set.Notes, "\n"),
			fmt.Sprintf("[yaml: only the first %d key levels are listed", maxConfigDepth))
	})

	t.Run("a file with more keys than the cap is cut and says how to read on", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < maxConfigKeys+5; i++ {
			fmt.Fprintf(&sb, "key%03d: %d\n", i, i)
		}
		syms, set := extractFixture(t, t.TempDir(), "big.yaml", sb.String())
		assert.Eq(t, maxConfigKeys, len(syms))
		assert.Eq(t, fmt.Sprintf("key%03d", maxConfigKeys-1), syms[len(syms)-1].Name)
		assert.StrContains(t, strings.Join(set.Notes, "\n"),
			fmt.Sprintf("[yaml: showing the first %d keys; for the rest use file_grep]", maxConfigKeys))
	})

	t.Run("a multi-line value is one line in the signature", func(t *testing.T) {
		syms, _ := extractFixture(t, t.TempDir(), "block.yaml", "log:\n  format: |\n    level: info\n")
		assert.Eq(t, []string{"block.yaml:1 section log", "block.yaml:2 key log.format"}, symbolsAsText(syms))
		signature := symbolByName(t, syms, "log.format").Signature
		assert.NotContains(t, signature, "\n")
		assert.True(t, len(signature) <= configPreviewBytes+len("…"), signature)
	})
}
