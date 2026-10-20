package acpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

func editDiff(t *testing.T, cwd string, args map[string]any) []acp.ToolCallContent {
	t.Helper()
	return toolDiff(cwd, acp.ToolKindEdit, toolLocations(cwd, args), args)
}

func TestToolDiffFIFODoesNotBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mkfifo is unavailable on windows")
	}
	cwd := t.TempDir()
	if err := mkfifo(filepath.Join(cwd, "pipe"), 0o600); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}
	done := make(chan []acp.ToolCallContent, 1)
	go func() { done <- editDiff(t, cwd, map[string]any{"path": "pipe", "content": "x"}) }()
	select {
	case diff := <-done:
		assert.Empty(t, diff)
	case <-time.After(3 * time.Second):
		t.Fatal("toolDiff blocked on a FIFO")
	}
}

func TestToolPathMatchesNativeWritePath(t *testing.T) {
	cwd := t.TempDir()
	// An absolute path inside cwd is the file itself (not re-joined under cwd), so
	// its current content is the diff's oldText.
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, arg := range map[string]string{
		"abs inside":  filepath.Join(cwd, "a.txt"),
		"abs outside": "/etc/x",
		"relative":    "sub/b.txt",
	} {
		t.Run(name, func(t *testing.T) {
			args := map[string]any{"path": arg, "content": "new\n"}
			want, err := native.ResolveSandboxedPath(cwd, arg)
			if err != nil {
				// The write tool rejects this path (e.g. an absolute path outside
				// the workspace), so there is nothing to point at and no diff to offer.
				assert.Empty(t, toolLocations(cwd, args))
				assert.Empty(t, editDiff(t, cwd, args))
				return
			}
			locations := toolLocations(cwd, args)
			assert.Eq(t, 1, len(locations))
			assert.Eq(t, want, locations[0].Path)
			diff := editDiff(t, cwd, args)
			assert.Eq(t, 1, len(diff))
			assert.Eq(t, want, diff[0].Diff.Path)
			if name == "abs inside" {
				assert.Eq(t, "current\n", *diff[0].Diff.OldText)
			} else {
				assert.Nil(t, diff[0].Diff.OldText) // the other resolved targets do not exist
			}
		})
	}
}

func TestToolDiffNewFileInNewDirectory(t *testing.T) {
	cwd := t.TempDir()
	diff := editDiff(t, cwd, map[string]any{"path": "a/b/new.txt", "content": "hello\n"})
	assert.Eq(t, 1, len(diff))
	assert.Nil(t, diff[0].Diff.OldText)
	assert.Eq(t, "hello\n", diff[0].Diff.NewText)
}

func TestToolDiffEscapeStillBlockedBehindMissingLeaf(t *testing.T) {
	cwd, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cwd, "out")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	assert.Empty(t, editDiff(t, cwd, map[string]any{"path": "out/new.txt", "content": "x"}))
}

func TestToolDiffSkipsLargeAndBinary(t *testing.T) {
	cwd := t.TempDir()
	big := strings.Repeat("a", maxDiffBytes+1)
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(cwd, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("big.txt", []byte(big))
	write("bin.dat", []byte{0xff, 0xfe, 0x00, 0x01})
	write("nul.txt", []byte("a\x00b"))
	write("ok.txt", []byte("ok"))

	for _, tc := range []struct {
		name string
		args map[string]any
		want bool
	}{
		{"large old", map[string]any{"path": "big.txt", "content": "x"}, false},
		{"large new", map[string]any{"path": "ok.txt", "content": big}, false},
		{"binary old", map[string]any{"path": "bin.dat", "content": "x"}, false},
		{"nul old", map[string]any{"path": "nul.txt", "content": "x"}, false},
		{"binary new", map[string]any{"path": "ok.txt", "content": "a\xffb"}, false},
		{"small text", map[string]any{"path": "ok.txt", "content": "y"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, len(editDiff(t, cwd, tc.args)) == 1)
			assert.Eq(t, 1, len(toolLocations(cwd, tc.args))) // locations are kept regardless
		})
	}
}

func TestToolLocationOmitsLineWhenUnknown(t *testing.T) {
	cwd := t.TempDir()
	locations := toolLocations(cwd, map[string]any{"path": "a.txt"})
	assert.Nil(t, locations[0].Line)
	data, err := json.Marshal(locations[0])
	assert.NoErr(t, err)
	assert.False(t, strings.Contains(string(data), "line"))

	locations = toolLocations(cwd, map[string]any{"path": "a.txt", "line": float64(7)})
	assert.Eq(t, 7, *locations[0].Line)
}

func TestToolLocationsForReadPaths(t *testing.T) {
	cwd := t.TempDir()
	outside := filepath.Join(filepath.Dir(cwd), "outside.txt")
	locations := toolLocations(cwd, map[string]any{"paths": []any{"a.txt", outside, "sub/b.txt"}})
	assert.Eq(t, 2, len(locations)) // the path outside the workspace has nothing to point at
	assert.Eq(t, filepath.Join(cwd, "a.txt"), locations[0].Path)
	assert.Eq(t, filepath.Join(cwd, "sub", "b.txt"), locations[1].Path)
	assert.Empty(t, toolLocations(cwd, map[string]any{"paths": "[not json"}))
}

func TestToolUpdatesDoNotRepeatDiff(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "t.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := mock.NewProvider(mock.ToolCall("edit-1", "file_write", map[string]any{"path": "t.txt", "content": "after\n"}), mock.Final("done"))
	client := &testACPClient{selected: "allow"}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, &countingTool{writePath: path}), client)
	ctx := testCtx(t)
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("edit")}})
	assert.NoErr(t, err)

	client.mu.Lock()
	defer client.mu.Unlock()
	starts, statuses := 0, 0
	for _, n := range client.updates {
		if n.Update.ToolCall != nil {
			starts++
			assert.Eq(t, 1, len(n.Update.ToolCall.Content)) // gofer gets the diff here
		}
		if u := n.Update.ToolCallUpdate; u != nil {
			statuses++
			assert.Empty(t, u.Content)
			assert.Empty(t, u.Locations)
		}
	}
	assert.Eq(t, 1, starts)
	assert.True(t, statuses >= 2) // in_progress + completed
	assert.Eq(t, 1, len(client.requests))
	assert.Eq(t, 1, len(client.requests[0].ToolCall.Content)) // approver still sees the diff
}

func TestToolDiffOpenOnce(t *testing.T) {
	cwd := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("old\n"), 0o644))
	assert.NoErr(t, os.Mkdir(filepath.Join(cwd, "dir"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(cwd, "big.txt"), []byte(strings.Repeat("x", maxDiffBytes+1)), 0o644))

	t.Run("regular file previews old text", func(t *testing.T) {
		diff := editDiff(t, cwd, map[string]any{"path": "a.txt", "content": "new\n"})
		assert.Eq(t, 1, len(diff))
		assert.Eq(t, "old\n", *diff[0].Diff.OldText)
	})

	t.Run("missing file previews as new", func(t *testing.T) {
		diff := editDiff(t, cwd, map[string]any{"path": "none.txt", "content": "new\n"})
		assert.Eq(t, 1, len(diff))
		assert.Nil(t, diff[0].Diff.OldText)
	})

	t.Run("symlink inside workspace previews target", func(t *testing.T) {
		if err := os.Symlink("a.txt", filepath.Join(cwd, "ln.txt")); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		diff := editDiff(t, cwd, map[string]any{"path": "ln.txt", "content": "new\n"})
		assert.Eq(t, 1, len(diff))
		assert.Eq(t, "old\n", *diff[0].Diff.OldText)
	})

	t.Run("dangling symlink is not previewed", func(t *testing.T) {
		if err := os.Symlink("gone.txt", filepath.Join(cwd, "dangling.txt")); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		assert.Empty(t, editDiff(t, cwd, map[string]any{"path": "dangling.txt", "content": "new\n"}))
	})

	t.Run("directory is not previewed", func(t *testing.T) {
		assert.Empty(t, editDiff(t, cwd, map[string]any{"path": "dir", "content": "new\n"}))
	})

	t.Run("oversized file is not previewed", func(t *testing.T) {
		assert.Empty(t, editDiff(t, cwd, map[string]any{"path": "big.txt", "content": "new\n"}))
	})
}

func TestToolDiffFileEdit(t *testing.T) {
	cwd := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(cwd, "a.go"), []byte("a := 1\nb := 2\n"), 0o644))

	t.Run("new text is the file with the replacement applied", func(t *testing.T) {
		diff := editDiff(t, cwd, map[string]any{"path": "a.go", "old_string": "b := 2", "new_string": "b := 3"})
		assert.Require(t, assert.Len(t, diff, 1))
		assert.Eq(t, "a := 1\nb := 2\n", *diff[0].Diff.OldText)
		assert.Eq(t, "a := 1\nb := 3\n", diff[0].Diff.NewText)
	})
	t.Run("edits are all applied in the preview", func(t *testing.T) {
		diff := editDiff(t, cwd, map[string]any{"path": "a.go", "edits": []any{
			map[string]any{"old_string": "a := 1", "new_string": "a := 0"},
			map[string]any{"old_string": "b := 2", "new_string": "b := 3"},
		}})
		assert.Require(t, assert.Len(t, diff, 1))
		assert.Eq(t, "a := 0\nb := 3\n", diff[0].Diff.NewText)
	})
	t.Run("an edit that cannot apply has no diff", func(t *testing.T) {
		assert.Len(t, editDiff(t, cwd, map[string]any{"path": "a.go", "old_string": "nope", "new_string": "x"}), 0)
	})
	t.Run("a missing file has no diff", func(t *testing.T) {
		assert.Len(t, editDiff(t, cwd, map[string]any{"path": "new.go", "old_string": "a", "new_string": "b"}), 0)
	})
}

func TestCallTitle(t *testing.T) {
	if got := callTitle(agentcore.ToolCall{Name: "file_read"}); got != "file_read" {
		t.Fatalf("plain tool title = %q", got)
	}
	if got := callTitle(agentcore.ToolCall{Name: "agent", Arguments: map[string]any{"description": " find callers "}}); got != "agent: find callers" {
		t.Fatalf("agent title = %q", got)
	}
	if got := callTitle(agentcore.ToolCall{Name: "agent"}); got != "agent" {
		t.Fatalf("agent without description = %q", got)
	}
}
