package personal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore"
)

// imageTestBuilder returns a context builder that assembles requests of session
// sid with images below sessionsDir.
func imageTestBuilder(sessionsDir, sid string, every, keep int) *contextBuilder {
	return &contextBuilder{
		r:         &Runner{cfg: Config{SessionsDir: sessionsDir, FoldEvery: every, FoldKeep: keep}},
		sessionID: sid,
	}
}

// writeTestImage stores data as the image ref of a session and returns the part
// pointing at it.
func writeTestImage(t *testing.T, sessionsDir, sid string, data []byte) agentcore.ImagePart {
	t.Helper()
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	ref := "images/" + sha + ".png"
	dir := filepath.Join(sessionsDir, sid, "images")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(dir, 0o700)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, sha+".png"), data, 0o600)))
	return agentcore.ImagePart{MediaType: "image/png", Ref: ref, SHA256: sha, Width: 100, Height: 50}
}

// TestPrepareImagesReadsBytesBack pins the retrieval side: a stored reference
// gets its bytes from the session directory, a missing file or a sha mismatch
// becomes a text note, and the input view is not modified.
func TestPrepareImagesReadsBytesBack(t *testing.T) {
	sessionsDir := t.TempDir()
	b := imageTestBuilder(sessionsDir, "s1", 0, 0)
	data := []byte("png-bytes")

	t.Run("a stored image carries its bytes in the request", func(t *testing.T) {
		part := writeTestImage(t, sessionsDir, "s1", data)
		view := []agentcore.Message{{Role: "user", Content: "look", Images: []agentcore.ImagePart{part}}}
		out := b.prepareImages(view)
		assert.Require(t, assert.Eq(t, 1, len(out)))
		assert.Require(t, assert.Eq(t, 1, len(out[0].Images)))
		assert.Eq(t, string(data), string(out[0].Images[0].Data))
		// The caller's view keeps the reference only.
		assert.Eq(t, 0, len(view[0].Images[0].Data))
	})

	t.Run("a missing file becomes a note", func(t *testing.T) {
		part := writeTestImage(t, sessionsDir, "s1", data)
		assert.Require(t, assert.NoErr(t, os.Remove(filepath.Join(sessionsDir, "s1", part.Ref))))
		view := []agentcore.Message{{Role: "user", Content: "look", Images: []agentcore.ImagePart{part}}}
		out := b.prepareImages(view)
		assert.Eq(t, 0, len(out[0].Images))
		assert.True(t, strings.Contains(out[0].Content, "图片缺失"))
		assert.True(t, strings.Contains(out[0].Content, part.Ref))
	})

	t.Run("a sha mismatch becomes a note", func(t *testing.T) {
		part := writeTestImage(t, sessionsDir, "s1", data)
		part.SHA256 = strings.Repeat("0", 64)
		view := []agentcore.Message{{Role: "user", Content: "look", Images: []agentcore.ImagePart{part}}}
		out := b.prepareImages(view)
		assert.Eq(t, 0, len(out[0].Images))
		assert.True(t, strings.Contains(out[0].Content, "图片缺失"))
	})

	t.Run("a reference outside the images directory is refused", func(t *testing.T) {
		view := []agentcore.Message{{Role: "user", Images: []agentcore.ImagePart{{Ref: "../secrets.png", SHA256: "x"}}}}
		out := b.prepareImages(view)
		assert.Eq(t, 0, len(out[0].Images))
		assert.True(t, strings.Contains(out[0].Content, "图片缺失"))
	})
}

// TestPrepareImagesHistoryAndCaps pins the two cost rules: images older than the
// last fold boundary become notes, and one request carries at most
// ImageRequestCount images (the newest win).
func TestPrepareImagesHistoryAndCaps(t *testing.T) {
	t.Run("images before the fold boundary are omitted", func(t *testing.T) {
		sessionsDir := t.TempDir()
		part := writeTestImage(t, sessionsDir, "s1", []byte("old"))
		// 4 assistant steps, fold every 2 keeping 1: boundary 2, so the first two
		// assistant steps are folded and the image before them is omitted.
		b := imageTestBuilder(sessionsDir, "s1", 2, 1)
		view := []agentcore.Message{
			{Role: "user", Content: "old", Images: []agentcore.ImagePart{part}},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "middle"},
			{Role: "assistant", Content: "a2"},
			{Role: "user", Content: "new", Images: []agentcore.ImagePart{part}},
			{Role: "assistant", Content: "a3"},
			{Role: "assistant", Content: "a4"},
		}
		out := b.prepareImages(view)
		assert.Eq(t, 0, len(out[0].Images))
		assert.True(t, strings.Contains(out[0].Content, "已省略"))
		// The note points at the tool that reads pictures now (design 2026-10-23
		// section 6); view_image is only a hidden alias.
		assert.Contains(t, out[0].Content, "需要时 file_read images/")
		assert.Eq(t, 1, len(out[4].Images), "the newest image stays")
	})

	t.Run("the count cap keeps the newest images", func(t *testing.T) {
		sessionsDir := t.TempDir()
		part := writeTestImage(t, sessionsDir, "s1", []byte("x"))
		b := imageTestBuilder(sessionsDir, "s1", 0, 0)
		var view []agentcore.Message
		for i := 0; i < appconfig.DefaultImageRequestCount+2; i++ {
			view = append(view, agentcore.Message{Role: "user", Content: "m", Images: []agentcore.ImagePart{part}})
		}
		out := b.prepareImages(view)
		kept := 0
		for _, m := range out {
			kept += len(m.Images)
		}
		assert.Eq(t, appconfig.DefaultImageRequestCount, kept)
		assert.True(t, strings.Contains(out[0].Content, "超出单请求图片上限"))
		assert.Eq(t, 1, len(out[len(out)-1].Images), "the newest image stays")
	})

	t.Run("the byte cap keeps the newest images", func(t *testing.T) {
		sessionsDir := t.TempDir()
		part := writeTestImage(t, sessionsDir, "s1", []byte("xx"))
		part.Data = []byte("xx")
		view := []agentcore.Message{
			{Role: "user", Images: []agentcore.ImagePart{part}},
			{Role: "user", Images: []agentcore.ImagePart{part}},
		}
		out := limitRequestImages(view, 8, 3)
		assert.Eq(t, 0, len(out[0].Images))
		assert.Eq(t, 1, len(out[1].Images))
	})

	t.Run("images.history_turns keeps the pictures of the newest steps", func(t *testing.T) {
		// Folding off (every = 0): the images of the last `turns` assistant steps
		// stay, an older one becomes a note (images.history_turns).
		sessionsDir := t.TempDir()
		part := writeTestImage(t, sessionsDir, "s1", []byte("png-bytes"))
		view := []agentcore.Message{
			{Role: "user", Content: "old", Images: []agentcore.ImagePart{part}},
			{Role: "assistant", Content: "step 1"},
			{Role: "assistant", Content: "step 2"},
			{Role: "assistant", Content: "step 3"},
			{Role: "assistant", Content: "step 4"},
			{Role: "user", Content: "new", Images: []agentcore.ImagePart{part}},
		}
		b := imageTestBuilder(sessionsDir, "s1", 0, 0)
		b.r.cfg.ImageHistoryTurns = 2
		out := b.prepareImages(view)
		assert.Eq(t, 0, len(out[0].Images))
		assert.Contains(t, out[0].Content, "已省略")
		assert.Eq(t, 1, len(out[5].Images))
	})
}

// TestTokensOfMessageCountsImages pins the image share of the window estimate:
// about width×height/750 per picture.
func TestTokensOfMessageCountsImages(t *testing.T) {
	t.Run("a picture costs about its pixels over 750", func(t *testing.T) {
		msg := agentcore.Message{Role: "user", Images: []agentcore.ImagePart{{Width: 1500, Height: 750}}}
		assert.Eq(t, 1500, tokensOfMessage(msg))
	})
	t.Run("a message without images is unchanged", func(t *testing.T) {
		assert.Eq(t, tokensOfText("hello"), tokensOfMessage(agentcore.Message{Role: "user", Content: "hello"}))
	})
}
