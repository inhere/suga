package native

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// TestBareRefClass pins the text guess of the fallback classes: a declaration
// keyword before the name is a def, the name before "(" a call, else a ref.
func TestBareRefClass(t *testing.T) {
	cases := []struct{ line, want string }{
		{"    pub fn file_type(&self) -> fs::FileType {", codeintel.RefDef},
		{"pub struct WalkDir {", codeintel.RefDef},
		{"{% macro file_type(x) %}", codeintel.RefDef},
		{"        if dent.file_type().is_symlink() {", codeintel.RefCall},
		{"    let v = parse::<u8>(s);", codeintel.RefRef},
		{"    let t = Vec::file_type::<u8>(s);", codeintel.RefCall},
		{"name = \"file_type\"", codeintel.RefRef},
		{"    my_file_type()", codeintel.RefRef},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			name := "file_type"
			if c.want == codeintel.RefDef && c.line == "pub struct WalkDir {" {
				name = "WalkDir"
			}
			if c.line == "    let v = parse::<u8>(s);" {
				name = "v"
			}
			assert.Eq(t, c.want, codeintel.BareRefClass(c.line, name))
		})
	}
}

// TestFallbackSkipsTestDirs pins that a fallback file under a test directory
// (Rust keeps its tests in tests/ or src/tests/) is left out unless
// include_tests is set, like the test files of the supported languages.
func TestFallbackSkipsTestDirs(t *testing.T) {
	ws := t.TempDir()
	files := map[string]string{
		"src/lib.rs":           "pub fn file_type() {}\nfn walk() { file_type(); }\n",
		"src/tests/walk.rs":    "fn t() { file_type(); }\n",
		"tests/integration.rs": "fn t() { file_type(); }\n",
	}
	for name, content := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}
	tool := &FileRefsTool{workspace: ws}

	t.Run("test directories are skipped and counted", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "file_type"})
		assert.StrContains(t, got, "src/lib.rs:1 def pub fn file_type() {}")
		assert.StrContains(t, got, "src/lib.rs:2 call fn walk() { file_type(); }")
		assert.NotContains(t, got, "src/tests/walk.rs")
		assert.NotContains(t, got, "tests/integration.rs")
		assert.StrContains(t, got, "[skipped 2 test file(s)")
	})

	t.Run("include_tests searches them", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "file_type", "include_tests": true})
		assert.StrContains(t, got, "src/tests/walk.rs:1 call")
		assert.StrContains(t, got, "tests/integration.rs:1 call")
	})
}
