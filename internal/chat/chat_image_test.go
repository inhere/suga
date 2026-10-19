package chat

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/clipboardimage"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// fakeReadImage stands in for the CLI-built view_image pipeline: it loads its
// own name and fails for a "bad.png" argument (the real path rules and scaling
// live in native, and the command never branches on them).
func fakeReadImage() func(string) (agentcore.ImagePart, string, error) {
	return func(path string) (agentcore.ImagePart, string, error) {
		if path == "bad.png" {
			return agentcore.ImagePart{}, "", fmt.Errorf("bad.png: unsupported image format")
		}
		return agentcore.ImagePart{MediaType: "image/png", Width: 4, Height: 2, Data: []byte{1}}, path, nil
	}
}

// imageChatState builds enough of the loop state for :image dispatch.
func imageChatState(readImage func(string) (agentcore.ImagePart, string, error), vision bool) (*chatState, *bytes.Buffer) {
	var buf bytes.Buffer
	s := &chatState{ctx: context.Background(), modelInfo: ModelOption{Provider: "p", Model: "m"},
		readImage: readImage, vision: tools.NewVisionGate(vision), errOut: &buf}
	return s, &buf
}

// TestChatImageCommand dispatches :image through runChatCommand, the path chat
// commands take, and pins list / attach / clear plus the refusals.
func TestChatImageCommand(t *testing.T) {
	t.Run("attaches and lists in order", func(t *testing.T) {
		s, buf := imageChatState(fakeReadImage(), true)
		assert.Require(t, assert.True(t, s.runChatCommand(":image a.png")))
		assert.Require(t, assert.True(t, s.runChatCommand(":image b.jpg")))
		assert.Eq(t, 2, len(s.pendingImages))
		assert.Contains(t, buf.String(), "[已附图 1：4×2 image/png]")
		assert.Contains(t, buf.String(), "[已附图 2：4×2 image/png]")
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":image")))
		assert.Contains(t, buf.String(), "[已附图 1：4×2 image/png]")
	})
	t.Run("clear drops the queue", func(t *testing.T) {
		s, buf := imageChatState(fakeReadImage(), true)
		assert.Require(t, assert.True(t, s.runChatCommand(":image a.png")))
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":image clear")))
		assert.Eq(t, 0, len(s.pendingImages))
		assert.Contains(t, buf.String(), "已清空待发图片")
	})
	t.Run("an unreadable file is reported and not queued", func(t *testing.T) {
		s, buf := imageChatState(fakeReadImage(), true)
		assert.Require(t, assert.True(t, s.runChatCommand(":image bad.png")))
		assert.Eq(t, 0, len(s.pendingImages), "a failing load is not queued")
		assert.Contains(t, buf.String(), "读图失败")
		assert.Contains(t, buf.String(), "unsupported image format")
	})
	t.Run("a model without vision is refused", func(t *testing.T) {
		s, buf := imageChatState(fakeReadImage(), false)
		assert.Require(t, assert.True(t, s.runChatCommand(":image a.png")))
		assert.Eq(t, 0, len(s.pendingImages))
		assert.Contains(t, buf.String(), "不支持看图")
		assert.Contains(t, buf.String(), ":model")
	})
	t.Run("without a loader the command says so", func(t *testing.T) {
		s, buf := imageChatState(nil, true)
		assert.Require(t, assert.True(t, s.runChatCommand(":image a.png")))
		assert.Eq(t, 0, len(s.pendingImages))
		assert.Contains(t, buf.String(), "不可用")
	})
}

// fakeClipReader wraps a read function in the chat-side type (the real one is
// built by wiring over internal/clipboardimage).
func fakeClipReader(read func() (agentcore.ImagePart, string, error)) func() (agentcore.ImagePart, string, error) {
	return read
}

// clipboardChatState builds a chatState with a clipboard loader and no loader
// for paths.
func clipboardChatState(read func() (agentcore.ImagePart, string, error), vision bool) (*chatState, *bytes.Buffer) {
	s, buf := imageChatState(nil, vision)
	s.readClip = read
	return s, buf
}

// TestChatPasteClipboard covers Alt+V's chat half through real ReadLine binding
// (the key path is covered by the lineedit tests and the ptysmoke scenario).
func TestChatPasteClipboard(t *testing.T) {
	good := func() (agentcore.ImagePart, string, error) {
		return agentcore.ImagePart{MediaType: "image/png", Width: 4, Height: 2}, "剪贴板", nil
	}
	t.Run("pastes into the same queue as :image", func(t *testing.T) {
		s, buf := clipboardChatState(good, true)
		s.attachFromClipboard()
		assert.Contains(t, buf.String(), "[已附图 1：4×2 image/png]")
		assert.Eq(t, 1, len(s.pendingImages))
	})
	t.Run("no picture says so", func(t *testing.T) {
		s, buf := clipboardChatState(fakeClipReader(func() (agentcore.ImagePart, string, error) {
			return agentcore.ImagePart{}, "", clipboardimage.ErrNoClipboardImage
		}), true)
		s.attachFromClipboard()
		assert.Eq(t, 0, len(s.pendingImages))
		assert.Contains(t, buf.String(), "剪贴板里没有图片")
	})
	t.Run("a model without vision is refused", func(t *testing.T) {
		s, buf := clipboardChatState(good, false)
		s.attachFromClipboard()
		assert.Eq(t, 0, len(s.pendingImages))
		assert.Contains(t, buf.String(), ":model")
	})
	t.Run("without a reader the key says so", func(t *testing.T) {
		s, buf := clipboardChatState(nil, true)
		s.attachFromClipboard()
		assert.Contains(t, buf.String(), "不可用")
	})
}
func TestChatImageTakePending(t *testing.T) {
	s, _ := imageChatState(fakeReadImage(), true)
	assert.Nil(t, s.takePendingImages())
	assert.Require(t, assert.True(t, s.runChatCommand(":image a.png")))
	taken := s.takePendingImages()
	assert.Eq(t, 1, len(taken))
	assert.Eq(t, "image/png", taken[0].MediaType)
	assert.Nil(t, s.takePendingImages(), "the queue is emptied")
}
