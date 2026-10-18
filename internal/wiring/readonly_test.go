package wiring

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
)

func TestPersonalReadOnlyToolsCannotWrite(t *testing.T) {
	workspace := t.TempDir()
	capabilities, err := buildPersonalCapabilities(workspace, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := capabilities.Registry.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, 6, len(schemas)) // file_read, file_list, file_grep, file_find, file_symbols, web_fetch
	_, err = capabilities.Registry.ExecuteTool(context.Background(), agentcore.ToolCall{
		CallID: "write", Name: "file_write", Arguments: map[string]any{"path": "blocked.txt", "content": "secret"},
	})
	assert.Eq(t, true, err != nil)
	_, err = os.Stat(filepath.Join(workspace, "blocked.txt"))
	assert.Eq(t, true, errors.Is(err, os.ErrNotExist))
	_, err = capabilities.Registry.ExecuteTool(context.Background(), agentcore.ToolCall{
		CallID: "shell", Name: "shell_exec", Arguments: map[string]any{"command": "echo blocked"},
	})
	assert.Eq(t, true, err != nil)
}
