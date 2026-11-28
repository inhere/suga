package personal

import (
	"context"

	"github.com/inhere/suga/pkg/agentcore"
)

// forkHeadHook fixes a fork's request head to the parent's seed. It runs before
// prefixHook so cache accounting observes the request the provider receives.
type forkHeadHook struct {
	agentcore.NopHook
	seed        *ForkSeed
	prompt      string
	maxInput    int
	instruction string
	first       bool
}

func (h forkHeadHook) BeforeModel(_ context.Context, event *agentcore.ModelEvent, ctrl *agentcore.Control) error {
	if event == nil || event.Request == nil || h.seed == nil || h.seed.Request == nil {
		return nil
	}
	request := cloneForkRequest(h.seed.Request)
	// Keep the model the child runtime put on the request (empty on the main loop:
	// the provider fills in its resolved model id). seed.Model is suga's model
	// reference, which an endpoint may not accept as a wire model id.
	request.Model = event.Request.Model
	request.System = h.seed.Request.System
	request.Tools = append([]agentcore.ToolSchema(nil), h.seed.Request.Tools...)
	// The runtime supplies ToolChoice only for its wrap-up retry. Keep that
	// transient setting, while ordinary fork steps leave it empty.
	request.ToolChoice = event.Request.ToolChoice

	// RunChild already placed its prompt in state.Messages. The seed carries the
	// fork directive, so remove only that first child prompt before appending the
	// child's own assistant/tool transcript. Never append to the seed slice.
	removed := false
	for _, message := range event.Request.Messages {
		if !removed && message.Role == "user" && message.Content == h.prompt {
			removed = true
			continue
		}
		request.Messages = append(request.Messages, message)
	}
	event.Request = request
	return nil
}

// AfterModel is intentionally separate from BeforeModel: usage belongs to the
// provider's preceding call, so the threshold is checked after that call.
func (h forkHeadHook) AfterModel(_ context.Context, event *agentcore.ModelEvent, ctrl *agentcore.Control) error {
	if event != nil && event.Result != nil && h.maxInput > 0 && event.Result.Usage.InputTokens >= h.maxInput && ctrl != nil {
		instruction := h.instruction
		if instruction == "" {
			instruction = forkTranslate(nil, "model.fork.wrapup_context")
		}
		ctrl.RequestWrapUp(StopContext, instruction)
	}
	return nil
}
