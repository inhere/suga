package agentcore

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/tools"
)

// TestRuntimeCarriesImages pins the image channel of a run: the input's images
// land on the user message, a tool result's images on its tool message (both in
// Message.Images and in ToolResult.Images, which is what providers and readers
// look at), and the state clones deep-copy them.
func TestRuntimeCarriesImages(t *testing.T) {
	ctx := context.Background()
	store := newRuntimeTestStorage()
	reg := tools.NewRegistry()
	reg.MustRegister(&runtimeTestTool{name: "view_image", result: &ToolResult{
		Content: "image ok",
		Images:  []ImagePart{{MediaType: "image/png", SHA256: "tool1", Width: 4, Height: 2}},
	}})
	provider := &runtimeTestProvider{results: []*GenerateResult{
		{ToolCalls: []ToolCall{{CallID: "call-1", Name: "view_image"}}, FinishReason: FinishToolCalls},
		{Content: "done", FinishReason: FinishFinal},
	}}
	inputImage := ImagePart{MediaType: "image/png", SHA256: "user1", Width: 8, Height: 6, Data: []byte{1, 2, 3}}

	result, err := New(Config{
		Provider:       provider,
		Storage:        store,
		Tools:          reg,
		PromptBuilder:  runtimeTestPromptBuilder{},
		ContextBuilder: runtimeTestContextBuilder{},
	}).Run(ctx, RunInput{RunID: "run-images", Instruction: "look", Images: []ImagePart{inputImage}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, RunCompleted, result.Status)

	msgs, err := store.LoadMessages(ctx, "run-images")
	assert.Require(t, assert.NoErr(t, err))
	var user, tool *Message
	for i := range msgs {
		switch msgs[i].Role {
		case "user":
			user = &msgs[i]
		case "tool":
			tool = &msgs[i]
		}
	}
	assert.Require(t, assert.NotNil(t, user))
	assert.Eq(t, "look", user.Content)
	assert.Eq(t, 1, len(user.Images))
	assert.Eq(t, "user1", user.Images[0].SHA256)
	assert.Eq(t, 8, user.Images[0].Width)
	// This double keeps the bytes in memory; the real store clears Data and
	// keeps only the reference (internal/personal/store TestEncodeMessageImages).
	assert.Eq(t, 3, len(user.Images[0].Data))

	assert.Require(t, assert.NotNil(t, tool))
	assert.Eq(t, 1, len(tool.Images))
	assert.Eq(t, "tool1", tool.Images[0].SHA256)
	assert.Require(t, assert.NotNil(t, tool.ToolResult))
	assert.Eq(t, 1, len(tool.ToolResult.Images))
	assert.Eq(t, "tool1", tool.ToolResult.Images[0].SHA256)

	t.Run("clones do not share image bytes", func(t *testing.T) {
		original := []ImagePart{{Data: []byte{1, 2}}}
		cloned := cloneImagesValue(original)
		assert.Eq(t, 1, len(cloned))
		original[0].Data[0] = 9
		assert.Eq(t, byte(1), cloned[0].Data[0])
		assert.Eq(t, ([]ImagePart)(nil), cloneImagesValue(nil))
	})
}

// TestResumePolicyAppendsImageOnlyInput: pictures without text are still a
// user turn, so they must not be dropped as an empty instruction.
func TestResumePolicyAppendsImageOnlyInput(t *testing.T) {
	policy := DefaultResumePolicy{}
	t.Run("images alone append the user message", func(t *testing.T) {
		assert.True(t, policy.ShouldAppendInstruction(RunInput{Images: []ImagePart{{SHA256: "a"}}}, &RunState{}))
	})
	t.Run("an empty input still appends nothing", func(t *testing.T) {
		assert.False(t, policy.ShouldAppendInstruction(RunInput{Instruction: "  "}, &RunState{}))
	})
}
