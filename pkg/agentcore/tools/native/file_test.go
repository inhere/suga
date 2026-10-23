package native

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

func TestFileWriteEncodingAndLineEndings(t *testing.T) {
	workspace := t.TempDir()
	tool := &FileWriteTool{workspace: workspace}
	// Mixed line endings, non-ASCII text and a shebang: everything must land on
	// disk byte for byte.
	content := "#!/usr/bin/env python3\nprint('中文')\r\nprint(1)\n"
	_, err := tool.Execute(context.Background(), api.ToolCall{
		CallID: "call-write", Name: "file_write",
		Arguments: map[string]any{"path": "tmp/script.py", "content": content},
	})
	assert.Require(t, assert.NoErr(t, err))

	raw, err := os.ReadFile(filepath.Join(workspace, "tmp", "script.py"))
	assert.Require(t, assert.NoErr(t, err))
	// UTF-8 without a BOM (the tool adds no byte of its own).
	assert.False(t, bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}))
	// LF and CRLF stay as given: no newline translation on any OS.
	assert.Eq(t, content, string(raw))
	assert.Eq(t, 3, strings.Count(string(raw), "\n"))

	// The description states what the test proves.
	desc := tool.Description()
	assert.StrContains(t, desc, "UTF-8 without a BOM")
	assert.StrContains(t, desc, "line endings")
}

// TestFileReadDescription pins the two usage hints of file_read's description:
// paths as the first choice when several places are wanted (with a cross-file
// example) and the concrete trigger of outline (research
// 2026-10-18-explore-efficiency section 9.7 candidate 3: only 3 of 10 runs used
// line-span reads and outline was used once).
func TestFileReadDescription(t *testing.T) {
	desc := (&FileReadTool{}).Description()
	assert.StrContains(t, desc, "To look at several places at once, prefer paths (up to 10) over path")
	assert.StrContains(t, desc, `paths: ["a.go:10-30", "b.go:40-60,90-110"]`)
	// The outline restatement moved to the parameter's own description
	// (2026-10-23 fixed-prefix report section 三 #14); the description keeps
	// only the "text only" caveat.
	assert.StrNotContains(t, desc, "Use outline=true only to see")
	assert.StrContains(t, desc, "offset, limit and outline apply to text only")
	props := (&FileReadTool{}).Schema().InputSchema["properties"].(map[string]any)
	assert.StrContains(t, props["outline"].(map[string]any)["description"].(string), "a source file over 300 lines")
}

func TestFileTools_ReturnToolResultsWithStableMetadata(t *testing.T) {
	workspace := t.TempDir()
	filePath := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(filePath, []byte("hello from file"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	readTool := &FileReadTool{workspace: workspace}
	writeTool := &FileWriteTool{workspace: workspace}

	t.Run("file_read returns content in ToolResult", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), api.ToolCall{
			CallID: "call-read",
			Name:   "file_read",
			Arguments: map[string]any{
				"path": "note.txt",
			},
		})
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if result.CallID != "call-read" || result.Name != "file_read" {
			t.Fatalf("result metadata = %#v, want preserved call metadata", result)
		}
		if result.Content != "1\thello from file" {
			t.Fatalf("content = %q, want file body", result.Content)
		}
	})

	t.Run("file_write returns write summary in ToolResult", func(t *testing.T) {
		result, err := writeTool.Execute(context.Background(), api.ToolCall{
			CallID: "call-write",
			Name:   "file_write",
			Arguments: map[string]any{
				"path":    "sub/out.txt",
				"content": "updated",
			},
		})
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if result.CallID != "call-write" || result.Name != "file_write" {
			t.Fatalf("result metadata = %#v, want preserved call metadata", result)
		}
		if result.Content == "" {
			t.Fatalf("content = %q, want non-empty write summary", result.Content)
		}
	})
}
