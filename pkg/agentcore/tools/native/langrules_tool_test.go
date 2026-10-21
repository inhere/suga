package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// TestFileSymbolsUserLangRules is the end-to-end test of a language a user rule
// file adds (design docs/design/2026-10-22-lang-rules-design.md): a
// <workspace>/.suga/lang-rules.yaml teaches file_symbols Lua, an end_keyword
// language no Go code knows, and the tool description lists it.
func TestFileSymbolsUserLangRules(t *testing.T) {
	ws := t.TempDir()
	const source = `local function helper(x)
  return x + 1
end

function Store.reload(path)
  return path
end
`
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, "store.lua"), []byte(source), 0o644)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module example.com/store\n"), 0o644)))
	ruleFile := filepath.Join(ws, ".suga", "lang-rules.yaml")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(ruleFile), 0o755)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(ruleFile, []byte(`languages:
  - name: Lua
    exts: [.lua]
    test_commands: '\bbusted\b'
    syntax:
      line_comment: ['--']
      block_comment: [['--[[', ']]']]
      strings: ['"', "'"]
    blocks: end_keyword
    end_keyword:
      open: '^\s*(function|if|for|while)\b'
      close: '^\s*end\b'
    decls:
      - {pattern: '^\s*function\s+([A-Za-z_][\w.:]*)', kind: func, name: 1}
      - {pattern: '^\s*local\s+function\s+([A-Za-z_]\w*)', kind: func, name: 1}
`), 0o644)))

	// The wiring installs the loaded registry; the test does the same and puts the
	// previous one back for the other tests of the package.
	previous := codeintel.ActiveLangRules()
	t.Cleanup(func() { codeintel.InstallLangRules(previous) })
	assert.False(t, codeintel.SupportsSymbols("a.lua"), ".lua has no extractor before the rule file is installed")
	reg, errs := codeintel.LoadLangRules(ruleFile)
	assert.Eq(t, 0, len(errs), "errs=%v", errs)
	codeintel.InstallLangRules(reg)

	tool := &FileSymbolsTool{workspace: ws}
	res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tool.Name(), Arguments: map[string]any{"path": "store.lua"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, res.Content, "store.lua:1 func helper")
	assert.StrContains(t, res.Content, "store.lua:5 func Store.reload")
	assert.StrContains(t, tool.Description(), "C/C++, Lua, PHP and TOML")

	// The reference search masks Lua's comment the same way.
	refs := &FileRefsTool{workspace: ws}
	res, err = refs.Execute(context.Background(), api.ToolCall{CallID: "c", Name: refs.Name(), Arguments: map[string]any{"name": "reload", "path": "store.lua"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, res.Content, "store.lua:5 ref function Store.reload(path)")
}
