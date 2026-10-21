package native

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// writeSourceFixture writes one fixture file into dir and returns its path: the
// extractors run on real files, not only on in-memory strings.
func writeSourceFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	return path
}

// The two configuration fixtures the tool test below lists through
// file_symbols. They are the extractors' fixtures as well (symbols_yaml_test.go
// and symbols_toml_test.go of the codeintel package): the tool's result is
// checked against the very files the extractor tests describe.

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
