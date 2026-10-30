package native

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestCheckPathChars: a path this platform cannot open is refused with the
// offending character named and a way out, instead of the OS text
// (session sg_31nv13n28jb8: a model retried an invented path 16 times because
// "The filename, directory name, or volume label syntax is incorrect" said
// neither which character nor what to do).
func TestCheckPathChars(t *testing.T) {
	t.Run("a legal path passes", func(t *testing.T) {
		for _, path := range []string{"a.go", "dir/sub/b.go", `C:\work\a.go`, "a;b.go", "dir/../a.go", "中文/文件.md"} {
			assert.NoErr(t, checkPathChars(path), path)
		}
	})
	t.Run("a control character is refused everywhere", func(t *testing.T) {
		assert.ErrMsgContains(t, checkPathChars("a\x01b.go"), "控制字符")
		assert.ErrMsgContains(t, checkPathChars("a\x00b.go"), "NUL")
	})

	if !pathCharsStrict() {
		t.Skip("the name rules checked below are Windows-only")
	}
	t.Run("the offending character is named", func(t *testing.T) {
		err := checkPathChars("deploy-hub/docs/TODO.md;bad? nope")
		assert.ErrMsgContains(t, err, `"?"`)
		assert.ErrMsgContains(t, err, "file_list")
		assert.ErrMsgContains(t, err, "file_find")
	})
	t.Run("a wildcard points at file_find", func(t *testing.T) {
		err := checkPathChars("cmd/*.go")
		assert.ErrMsgContains(t, err, "通配符")
		assert.ErrMsgContains(t, err, "file_find")
	})
	t.Run("a trailing dot or space is refused", func(t *testing.T) {
		assert.ErrMsgContains(t, checkPathChars("deploy-hub/docs/TODO.md."), "静默去掉")
		assert.ErrMsgContains(t, checkPathChars("docs/a.go "), "静默去掉")
	})
	t.Run("the volume colon stays legal", func(t *testing.T) {
		assert.NoErr(t, checkPathChars(`D:\work\inhere\my-tools-dev\a.go`))
	})
}

// TestFileReadRefusesAnUnopenablePath runs the guard through the tool: the
// model reads our text, never the OS's.
func TestFileReadRefusesAnUnopenablePath(t *testing.T) {
	ws := t.TempDir()
	tool := &FileReadTool{workspace: ws}

	_, err := tool.Execute(t.Context(), callWith(map[string]any{"path": "deploy-hub/docs/TODO.md;bad? nope"}))
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "file_read")
	assert.NotContains(t, err.Error(), "volume label", "the OS wording is not what the model reads")
	if pathCharsStrict() {
		assert.ErrMsgContains(t, err, `"?"`)
	}

	// A clean path still goes through to the OS (and reports its own miss).
	_, err = tool.Execute(t.Context(), callWith(map[string]any{"path": "missing.txt"}))
	assert.Err(t, err)
}
