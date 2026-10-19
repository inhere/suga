package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// TestFileReadBareLineNumber pins the read of "path:N", the way rg, file_grep
// and stack traces name a position: lineSpanWindow lines from N, cut at the
// end of the file without an error.
func TestFileReadBareLineNumber(t *testing.T) {
	ws := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, "a.txt"), []byte(b.String()), 0o644)))
	tool := &FileReadTool{workspace: ws}
	read := func(path string) string {
		res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: tool.Name(), Arguments: map[string]any{"path": path}})
		assert.Require(t, assert.NoErr(t, err))
		return res.Content
	}

	t.Run("reads a window from the line", func(t *testing.T) {
		got := read("a.txt:10")
		assert.StrContains(t, got, "10\tline 10\n")
		assert.StrContains(t, got, "49\tline 49")
		assert.NotContains(t, got, "50\tline 50")
		assert.NotContains(t, got, "9\tline 9\n")
	})

	t.Run("a window past the end stops at the last line", func(t *testing.T) {
		got := read("a.txt:55")
		assert.StrContains(t, got, "55\tline 55\n")
		assert.StrContains(t, got, "60\tline 60")
	})
}
