package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// TestFileRefsPHP is the tool over a small PHP workspace: the directory query
// reports the definition and the calls, a qualified query keeps what $this
// calls and the result carries the approx marker.
func TestFileRefsPHP(t *testing.T) {
	ws := t.TempDir()
	files := map[string]string{
		"src/Greeter.php": "<?php\n\nnamespace App;\n\nclass Greeter\n{\n    public function greet(): string\n    {\n        return 'hello';\n    }\n}\n",
		"src/Runner.php": "<?php\n\nnamespace App;\n\nclass Runner\n{\n    public function run(Greeter $greeter): string\n    {\n" +
			"        $this->log();\n        return $greeter->greet();\n    }\n\n    private function log(): void\n    {\n    }\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755)))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644)))
	}
	tool := &FileRefsTool{workspace: ws}

	t.Run("a directory reports the definition and the calls", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "greet", "path": "src"})
		assert.StrContains(t, got, "src/Greeter.php:7 ref public function greet(): string")
		assert.StrContains(t, got, "src/Runner.php:10 ref return $greeter->greet();")
		assert.Eq(t, 2, strings.Count(got, " ref "), got)
		assert.StrContains(t, got, "[approx: ")
		assert.StrContains(t, got, "PHP")
	})

	t.Run("a qualified query keeps what $this calls", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "this.log", "path": "src"})
		assert.StrContains(t, got, "src/Runner.php:9 ref $this->log();")
		assert.NotContains(t, got, "Runner.php:13")
		assert.StrContains(t, got, `[qualified by "this"`)
	})

	t.Run("one PHP file alone is searched as that file", func(t *testing.T) {
		got := runFileRefs(t, tool, map[string]any{"name": "greet", "path": "src/Greeter.php"})
		assert.StrContains(t, got, "src/Greeter.php:7 ref public function greet(): string")
		assert.StrContains(t, got, "[approx: ")
	})

	t.Run("a PHP file is a supported type for the file query", func(t *testing.T) {
		assert.True(t, codeintel.SupportsSymbols("src/Greeter.php"))
		assert.StrContains(t, codeintel.SupportedSymbolLanguages(), "PHP (.php)")
	})
}
