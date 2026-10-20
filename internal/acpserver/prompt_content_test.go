package acpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
)

// testPNG encodes a small 4×2 picture: big enough for the view_image pipeline
// to sniff, decode and re-encode without scaling.
func testPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(1, 1, color.RGBA{R: 0xf0, G: 0x10, B: 0x40, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// capturePromptProvider records every request the runtime sends, so a test
// sees the user message the prompt built (text, placeholders, image parts).
type capturePromptProvider struct {
	mu   sync.Mutex
	reqs []*agentcore.GenerateRequest
}

func (p *capturePromptProvider) Name() string { return "capture-prompt" }

func (p *capturePromptProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	return &agentcore.GenerateResult{Content: "好的", FinishReason: agentcore.FinishFinal}, nil
}

func (p *capturePromptProvider) last() *agentcore.GenerateRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.reqs) == 0 {
		return nil
	}
	return p.reqs[len(p.reqs)-1]
}

// driveImagePrompt opens one ACP session in cwd and sends the blocks through
// the real Prompt entry. It returns the last provider request.
func driveImagePrompt(t *testing.T, cwd string, noVision bool, blocks []acp.ContentBlock) *agentcore.GenerateRequest {
	t.Helper()
	provider := &capturePromptProvider{}
	client := &testACPClient{}
	conn := connectPersonalACP(t, testACPRuntimeWith(t, provider, nil, acpTestOptions{noVision: noVision}), client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: blocks})
	assert.Require(t, assert.NoErr(t, err))
	req := provider.last()
	assert.Require(t, req != nil, "the provider saw no request")
	return req
}

// lastUserMessage returns the newest user message of the request.
func lastUserMessage(req *agentcore.GenerateRequest) (agentcore.Message, bool) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i], true
		}
	}
	return agentcore.Message{}, false
}

// TestPromptImageBlocks drives session/prompt with image content blocks through
// the real Prompt handler (design 0.3, ACP second phase).
func TestPromptImageBlocks(t *testing.T) {
	data := testPNG()

	t.Run("text and image blocks keep their order and placeholders", func(t *testing.T) {
		cwd := t.TempDir()
		file := filepath.Join(cwd, "a.png")
		assert.Require(t, assert.NoErr(t, os.WriteFile(file, data, 0o600)))
		uri := file
		req := driveImagePrompt(t, cwd, false, []acp.ContentBlock{
			acp.TextBlock("看这张"),
			{Image: &acp.ContentBlockImage{Data: base64.StdEncoding.EncodeToString(testPNG()), MimeType: "image/png"}},
			{Image: &acp.ContentBlockImage{Type: "image", Uri: &uri}},
			acp.TextBlock("谢谢"),
		})
		msg, ok := lastUserMessage(req)
		assert.Require(t, assert.True(t, ok))
		assert.Contains(t, msg.Content, "看这张\n[图片 1]\n[图片 2]\n谢谢")
		assert.Eq(t, 2, len(msg.Images))
		for _, part := range msg.Images {
			assert.Require(t, len(part.Data) > 0, "the request carries the picture bytes")
			assert.Eq(t, 4, part.Width)
			assert.Eq(t, 2, part.Height)
			// Ref is filled when the turn is stored (image_files.go); the very
			// request of this turn still carries the in-memory part.
		}
	})

	t.Run("the picture is stored in the session images directory", func(t *testing.T) {
		cwd := t.TempDir()
		provider := &capturePromptProvider{}
		client := &testACPClient{}
		// The db lives under cwd/db/sessions.db, so the store keeps its session
		// files under cwd/sessions (deriveSessionsDir).
		open := testACPRuntimeWith(t, provider, nil, acpTestOptions{
			dbPath: filepath.Join(cwd, "db", "sessions.db"),
		})
		conn := connectPersonalACP(t, open, client)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
		assert.Require(t, assert.NoErr(t, err))
		_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{
			acp.TextBlock("看"),
			{Image: &acp.ContentBlockImage{Data: base64.StdEncoding.EncodeToString(testPNG()), MimeType: "image/png"}},
		}})
		assert.Require(t, assert.NoErr(t, err))
		req := provider.last()
		assert.Require(t, req != nil, "the provider saw no request")
		msg, ok := lastUserMessage(req)
		assert.Require(t, assert.True(t, ok))
		assert.Require(t, len(msg.Images) > 0)
		matches, err := filepath.Glob(filepath.Join(cwd, "sessions", string(created.SessionId), "images", "*"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(matches))
		stored, err := os.ReadFile(matches[0])
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, len(msg.Images[0].Data), len(stored))
	})
}

// TestPromptImageNoteWithoutVision: a text-only model still gets the pictures
// stored, but the client is told once that they turn into a text note.
func TestPromptImageNoteWithoutVision(t *testing.T) {
	provider := &capturePromptProvider{}
	client := &testACPClient{}
	conn := connectPersonalACP(t, testACPRuntimeWith(t, provider, nil, acpTestOptions{noVision: true}), client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{
		acp.TextBlock("看这张"),
		{Image: &acp.ContentBlockImage{Data: base64.StdEncoding.EncodeToString(testPNG()), MimeType: "image/png"}},
	}})
	assert.Require(t, assert.NoErr(t, err))
	var thoughts, answer strings.Builder
	client.mu.Lock()
	for _, u := range client.updates {
		if c := u.Update.AgentThoughtChunk; c != nil && c.Content.Text != nil {
			thoughts.WriteString(c.Content.Text.Text)
		}
		if c := u.Update.AgentMessageChunk; c != nil && c.Content.Text != nil {
			answer.WriteString(c.Content.Text.Text)
		}
	}
	defer client.mu.Unlock()
	assert.Contains(t, thoughts.String(), "不支持看图")
	assert.Contains(t, thoughts.String(), "1 张图片")
	assert.NotContains(t, answer.String(), "不支持看图")
	// The user message still carries the picture: the note replacement happens
	// in the provider layer for a model without vision.
	msg, ok := lastUserMessage(provider.last())
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, 1, len(msg.Images))
}
