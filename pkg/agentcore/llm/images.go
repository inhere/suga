package llm

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// Image input (design 0.3). The host fills ImagePart.Data before a request
// (the store keeps only a reference), so this layer encodes what it is given
// and never reads a file.
//
// The mappings:
//
//	provider   user message                  tool result
//	claude     image block, text after it     tool_result.content blocks
//	openai     content array image_url        a user message after the batch
//	responses  input_image item               a user message after the outputs
//
// Claude is the only API that takes an image inside a tool result; the other
// two get the pictures in a deterministic user message built during assembly,
// so a replay produces the same bytes.
//
// A provider or model without vision (NoImages) does not get the bytes at all:
// every image becomes a text note in the message that carried it, which is also
// what a fallback model of a vision-capable main model sees.

// messageImages returns the images of a message. A tool message mirrors its
// result's images into Message.Images, so that field wins and the result is the
// fallback for a message written by an older path.
func messageImages(m agentcoreapi.Message) []agentcoreapi.ImagePart {
	if len(m.Images) > 0 {
		return m.Images
	}
	if m.ToolResult != nil {
		return m.ToolResult.Images
	}
	return nil
}

// imageMimeType is the media type sent with an image (PNG when unknown: the
// image tools always set it).
func imageMimeType(part agentcoreapi.ImagePart) string {
	if part.MediaType != "" {
		return part.MediaType
	}
	return "image/png"
}

// imageDataURL is the data: URL both OpenAI APIs take as image_url.
func imageDataURL(part agentcoreapi.ImagePart) string {
	return "data:" + imageMimeType(part) + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
}

// claudeImageBlock is the Anthropic image content block.
func claudeImageBlock(part agentcoreapi.ImagePart) map[string]any {
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": imageMimeType(part),
			"data":       base64.StdEncoding.EncodeToString(part.Data),
		},
	}
}

// openAIImageBlock is the Chat Completions content part.
func openAIImageBlock(part agentcoreapi.ImagePart) map[string]any {
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageDataURL(part)}}
}

// responsesImageBlock is the Responses input_image item.
func responsesImageBlock(part agentcoreapi.ImagePart) map[string]any {
	return map[string]any{"type": "input_image", "image_url": imageDataURL(part)}
}

// imageNote is the text that stands in for an image the model cannot see.
func imageNote(part agentcoreapi.ImagePart) string {
	name := filepath.Base(part.Ref)
	if part.Ref == "" {
		name = imageMimeType(part)
	}
	size := ""
	if part.Width > 0 && part.Height > 0 {
		size = fmt.Sprintf(" %d×%d", part.Width, part.Height)
	}
	return fmt.Sprintf("[图片 %s%s：当前模型不支持看图，未发送；可切换到支持看图的模型", name, size)
}

// imageNotes renders the notes of a message's images ("" when there are none).
func imageNotes(images []agentcoreapi.ImagePart) []string {
	notes := make([]string, 0, len(images))
	for _, part := range images {
		notes = append(notes, imageNote(part))
	}
	return notes
}

// withImageNotes appends the notes of the images left out to a message body.
func withImageNotes(content string, images []agentcoreapi.ImagePart) string {
	if len(images) == 0 {
		return content
	}
	lines := make([]string, 0, len(images)+1)
	if content != "" {
		lines = append(lines, content)
	}
	lines = append(lines, imageNotes(images)...)
	return strings.Join(lines, "\n")
}

// sendableImages returns the images that carry bytes to send, and the ones
// that cannot be sent (no bytes read back by the host) as notes are added to
// the message text.
func sendableImages(images []agentcoreapi.ImagePart) (send []agentcoreapi.ImagePart, missing []agentcoreapi.ImagePart) {
	for _, part := range images {
		if len(part.Data) > 0 {
			send = append(send, part)
			continue
		}
		missing = append(missing, part)
	}
	return send, missing
}

// toolImageNoteText introduces the user message that carries the pictures of a
// tool result on the APIs that cannot put them in the result itself.
func toolImageNoteText(m agentcoreapi.Message) string {
	name, callID := "", ""
	if m.ToolResult != nil {
		name, callID = m.ToolResult.Name, m.ToolResult.CallID
	} else if m.ToolCall != nil {
		name, callID = m.ToolCall.Name, m.ToolCall.CallID
	}
	if callID == "" {
		return fmt.Sprintf("以下是工具 %s 返回的图片", name)
	}
	return fmt.Sprintf("以下是工具 %s（%s）返回的图片", name, callID)
}
