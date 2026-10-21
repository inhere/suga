package native

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// The characterization tests of the symbol and reference analysis (design
// docs/design/2026-10-22-lang-rules-design.md, section 7): every language's
// file_symbols and file_refs output is recorded byte for byte in
// testdata/golden, so moving the analysis into another package (and, later,
// replacing the built-in recognisers by rule files) is proven to keep the
// behaviour. Run `go test ./pkg/agentcore/tools/native -run TestCharacterization
// -update` to regenerate after an intended change.

// updateGolden rewrites the golden files instead of comparing them.
var updateGolden = flag.Bool("update", false, "rewrite the characterization golden files")

// goldenCase is one recorded call: the tool, its arguments, and the name of the
// golden file under testdata/golden.
type goldenCase struct {
	name string
	tool string // "file_symbols" or "file_refs"
	args map[string]any
}

// sampleFiles are the sample sources, relative to testdata/samples and to the
// temporary workspace the tools run over.
var sampleFiles = []string{
	"go/store.go", "go/store_test.go",
	"ts/loader.ts", "ts/loader.test.ts",
	"py/loader.py", "py/test_loader.py",
	"java/Store.java", "java/StoreTest.java",
	"php/Store.php", "php/StoreTest.php",
	"yaml/config.yaml", "toml/config.toml",
	"rs/lib.rs", "docs/guide.md",
}

// goldenCases records one symbols and two references calls per language, the two
// directory walks, and the refusals: the whole surface the move must keep.
func goldenCases() []goldenCase {
	cases := []goldenCase{
		{name: "symbols-dir-all", tool: "file_symbols", args: map[string]any{"path": "."}},
		{name: "symbols-dir-all-name", tool: "file_symbols", args: map[string]any{"path": ".", "name": "reload", "kind": "method,class"}},
		{name: "refs-dir-reload", tool: "file_refs", args: map[string]any{"name": "reload", "path": "."}},
		{name: "refs-dir-reload-context", tool: "file_refs", args: map[string]any{"name": "reload", "path": ".", "context": 1}},
		{name: "refs-dir-reload-qualified", tool: "file_refs", args: map[string]any{"name": "loader.reload", "path": "."}},
		{name: "symbols-refused-rs", tool: "file_symbols", args: map[string]any{"path": "rs/lib.rs"}},
		{name: "refs-dir-tests", tool: "file_refs", args: map[string]any{"name": "reload", "path": ".", "include_tests": true}},
	}
	for _, file := range sampleFiles {
		slug := strings.ReplaceAll(strings.TrimSuffix(file, filepath.Ext(file)), "/", "-")
		cases = append(cases, goldenCase{name: "symbols-" + slug, tool: "file_symbols", args: map[string]any{"path": file}})
	}
	// One references query per language sample, plus a context variant: the
	// masked approximate match, the Go parser, the configuration key labelling
	// and the bare text fallback all appear.
	for _, c := range []struct{ name, file, ident string }{
		{"go", "go/store.go", "Reload"},
		{"ts", "ts/loader.ts", "reload"},
		{"py", "py/loader.py", "reload"},
		{"java", "java/Store.java", "reload"},
		{"php", "php/Store.php", "reload"},
		{"yaml", "yaml/config.yaml", "port"},
		{"toml", "toml/config.toml", "port"},
		{"rs", "rs/lib.rs", "store"},
	} {
		cases = append(cases,
			goldenCase{name: "refs-" + c.name, tool: "file_refs", args: map[string]any{"name": c.ident, "path": c.file}},
			goldenCase{name: "refs-" + c.name + "-context", tool: "file_refs", args: map[string]any{"name": c.ident, "path": c.file, "context": 2}},
		)
	}
	return cases
}

// TestCharacterization runs every recorded call and compares the whole result
// with its golden file.
func TestCharacterization(t *testing.T) {
	quietCtags(t)
	ws := sampleWorkspace(t)
	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			assertGolden(t, c.name, runGolden(t, ws, c))
		})
	}
}

// TestCharacterizationLanguageRegistry records the language registry (name,
// markers, dependency-API clause, test commands) and what the two trace
// patterns match: the base prompt's dependency sentence and the trace analysis
// read these, so their bytes must survive the move too.
func TestCharacterizationLanguageRegistry(t *testing.T) {
	var b strings.Builder
	for _, lang := range codeintel.Languages() {
		fmt.Fprintf(&b, "%s markers=%q api=%q tests=%q focused=%q\n",
			lang.Name, lang.Markers, lang.DependencyAPI, lang.TestCommands, lang.FocusedFlags)
	}
	b.WriteString("test-commands:\n")
	for _, cmd := range []string{
		"go test ./...", "go test -run TestX ./pkg", "mvn -o test", "./mvnw verify", "gradlew :app:test",
		"gradle --tests Foo test", "cargo test", "cargo test --test integration",
		"npm test", "pnpm run test", "yarn test", "npx jest -t foo", "vitest run",
		"pytest -q", "python3 -m pytest tests -k foo", "vendor/bin/phpunit --filter Foo", "pest",
		"make test", "ctest", "rspec spec/models", "dotnet test", "ls -la", "go build ./...",
	} {
		fmt.Fprintf(&b, "  %-40s test=%v focused=%v\n", cmd, codeintel.TestCommandPattern().MatchString(cmd), codeintel.FocusedTestPattern().MatchString(cmd))
	}
	assertGolden(t, "language-registry", b.String())
}

// sampleWorkspace copies testdata/samples into a fresh temporary directory, so a
// result shows workspace-relative names and nothing of the checkout's path.
func sampleWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	assert.Require(t, assert.NoErr(t, os.CopyFS(ws, os.DirFS(filepath.Join("testdata", "samples")))))
	return ws
}

// quietCtags turns universal-ctags off for a recorded run: the golden files must
// not depend on whether the machine running the tests has ctags installed.
func quietCtags(t *testing.T) {
	t.Helper()
	t.Cleanup(codeintel.PinBuiltinExtractors())
}

// runGolden executes one recorded call and returns the tool's content; a failing
// call is recorded as its error text, so the refusals are characterized too.
func runGolden(t *testing.T, ws string, c goldenCase) string {
	t.Helper()
	var res *api.ToolResult
	var err error
	switch c.tool {
	case "file_symbols":
		tool := &FileSymbolsTool{workspace: ws}
		res, err = tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tool.Name(), Arguments: c.args})
	case "file_refs":
		tool := &FileRefsTool{workspace: ws}
		res, err = tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tool.Name(), Arguments: c.args})
	default:
		t.Fatalf("unknown tool %q", c.tool)
	}
	if err != nil {
		return "ERROR: " + err.Error() + "\n"
	}
	if res == nil {
		t.Fatalf("%s returned no result and no error", c.tool)
	}
	return res.Content + "\n"
}

// assertGolden compares got with the golden file of name, or rewrites the file
// with -update.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".txt")
	if *updateGolden {
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(got), 0o644)))
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file %s is missing (%v); run with -update", path, err)
	}
	if string(data) != got {
		t.Fatalf("output differs from %s:\n%s", path, goldenDiff(string(data), got))
	}
}

// goldenDiff shows the differing lines of want and got, which is enough to see
// what a change of an extractor did.
func goldenDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		switch {
		case wl == gl:
			fmt.Fprintf(&b, " %3d %s\n", i+1, wl)
		default:
			fmt.Fprintf(&b, "-%3d %s\n+%3d %s\n", i+1, wl, i+1, gl)
		}
	}
	return b.String()
}
