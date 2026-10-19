package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// TestAgentStorageStoresImages pins the write side of image input: the bytes go
// to <sessions>/<sid>/images/<sha256>.<ext> before the message is encoded, the
// stored message keeps the reference, and the same picture is written once.
func TestAgentStorageStoresImages(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	newSession(t, s, "s1")
	assert.NoErr(t, s.CreateTurn(ctx, Turn{ID: "turn_img", SessionID: "s1"}))
	sessionsDir := t.TempDir()
	st := s.AgentStorage().WithSessionsDir(sessionsDir)

	data := []byte("\x89PNG\r\n\x1a\nnot-a-real-image")
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	msg := api.Message{Role: "user", Content: "look", Images: []api.ImagePart{{
		MediaType: "image/png", Width: 2, Height: 3, Data: data,
	}}}
	assert.NoErr(t, st.AppendMessages(ctx, "turn_img", msg))

	t.Run("the file holds the sent bytes", func(t *testing.T) {
		path := filepath.Join(sessionsDir, "s1", "images", sha+".png")
		stored, err := os.ReadFile(path)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, string(data), string(stored))
	})

	t.Run("the stored message keeps the reference and no bytes", func(t *testing.T) {
		msgs, err := st.LoadMessages(ctx, "turn_img")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, 1, len(msgs)))
		assert.Require(t, assert.Eq(t, 1, len(msgs[0].Images)))
		part := msgs[0].Images[0]
		assert.Eq(t, "images/"+sha+".png", part.Ref)
		assert.Eq(t, sha, part.SHA256)
		assert.Eq(t, "image/png", part.MediaType)
		assert.Eq(t, 2, part.Width)
		assert.Eq(t, 0, len(part.Data))
	})

	t.Run("a second copy of the same picture writes one file", func(t *testing.T) {
		assert.NoErr(t, st.AppendMessages(ctx, "turn_img", msg))
		entries, err := os.ReadDir(filepath.Join(sessionsDir, "s1", "images"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(entries))
	})

	t.Run("a tool result's images are stored too", func(t *testing.T) {
		assert.NoErr(t, s.CreateTurn(ctx, Turn{ID: "turn_tool", SessionID: "s1"}))
		tool := api.Message{Role: "tool", Content: "image ok", ToolResult: &api.ToolResult{
			CallID: "c1", Name: "view_image", Content: "image ok",
			Images: []api.ImagePart{{MediaType: "image/png", Data: data}},
		}}
		tool.Images = tool.ToolResult.Images
		assert.NoErr(t, st.AppendMessages(ctx, "turn_tool", tool))
		msgs, err := st.LoadMessages(ctx, "turn_tool")
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Eq(t, 1, len(msgs)))
		assert.Require(t, assert.NotNil(t, msgs[0].ToolResult))
		assert.Eq(t, "images/"+sha+".png", msgs[0].ToolResult.Images[0].Ref)
		assert.Eq(t, 0, len(msgs[0].ToolResult.Images[0].Data))
	})
}

// TestImageRefAndMediaType pins the naming rules the read side relies on.
func TestImageRefAndMediaType(t *testing.T) {
	assert.Eq(t, "images/ab.png", ImageRef("ab", "image/png"))
	assert.Eq(t, "images/ab.jpg", ImageRef("ab", "image/jpeg"))
	assert.Eq(t, "images/ab.bin", ImageRef("ab", "image/tiff"))
	assert.Eq(t, "image/webp", MediaTypeForRef("images/ab.webp"))
	assert.Eq(t, "", MediaTypeForRef("images/ab.bin"))
}
