package wiring

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// TestFileReadPictureRealAssembly pins the merge on the real assembly path: the
// capabilities buildPersonalCapabilities builds answer a picture from file_read
// with an image part, and the session store writes those bytes to
// images/<sha256>.png when the message is appended (design 2026-10-23 sections 2
// and "验证/单测": one test has to go through the real wiring and AppendMessages).
func TestFileReadPictureRealAssembly(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	writeTestPNG(t, workspace) // shot.png, 8×8

	capabilities, err := buildPersonalCapabilities(workspace, nil, false)
	assert.Require(t, assert.NoErr(t, err))
	res, err := capabilities.Registry.ExecuteTool(ctx, agentcore.ToolCall{
		CallID: "c1", Name: "file_read", Arguments: map[string]any{"path": "shot.png"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(res.Images)))
	assert.Contains(t, res.Content, "shot.png: PNG 8×8")

	// The real store: the tool itself writes nothing, AppendMessages stores the
	// bytes and keeps only the ref in the message.
	sessionsDir := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = s.Close() })
	assert.Require(t, assert.NoErr(t, s.CreateSession(ctx, store.Session{ID: "s1", Title: "picture"})))
	st := s.AgentStorage().WithSessionsDir(sessionsDir)
	assert.Require(t, assert.NoErr(t, s.CreateTurn(ctx, store.Turn{ID: "turn_img", SessionID: "s1"})))
	msg := api.Message{Role: "tool", Content: res.Content, ToolResult: res}
	msg.Images = res.Images
	assert.Require(t, assert.NoErr(t, st.AppendMessages(ctx, "turn_img", msg)))

	entries, err := os.ReadDir(filepath.Join(sessionsDir, "s1", "images"))
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(entries)))
	assert.Eq(t, ".jpg", filepath.Ext(entries[0].Name()), "an opaque picture is stored as JPEG")
	loaded, err := st.LoadMessages(ctx, "turn_img")
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(loaded)))
	assert.Require(t, assert.True(t, loaded[0].ToolResult != nil))
	assert.Require(t, assert.Eq(t, 1, len(loaded[0].ToolResult.Images)))
	assert.True(t, strings.HasPrefix(loaded[0].ToolResult.Images[0].Ref, "images/"),
		"the bytes became a ref, not a second copy")
	assert.Eq(t, 0, len(loaded[0].ToolResult.Images[0].Data))
}
