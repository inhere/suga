package personal

import (
	"context"
	"fmt"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore"
)

// ForkSeed is the in-memory request snapshot used to start a read-only fork.
type ForkSeed struct {
	Request      *agentcore.GenerateRequest
	Model        string
	ProviderName string
	BaseTurn     string
	Source       string
	Reuse        string
	ReuseNote    string
}

// ForkUnavailableError is returned when the current session cannot provide a
// trustworthy complete request for a fork. Hint is already translated for the
// host; the reason remains stable for command and trace diagnostics.
type ForkUnavailableError struct {
	Reason string
	hint   string
}

func (e *ForkUnavailableError) Error() string {
	if e == nil {
		return "fork unavailable"
	}
	return fmt.Sprintf("fork unavailable: %s", e.Reason)
}

// Hint returns the translated explanation, when the caller should show one.
func (e *ForkUnavailableError) Hint() string {
	if e == nil {
		return ""
	}
	return e.hint
}

// forkStep is the latest model reply and the tool results completed in that
// reply. It is deliberately separate from the persisted transcript: a fork
// needs the final in-memory calls while hooks may still have rewritten them.
type forkStep struct {
	Result    agentcore.GenerateResult
	Done      map[string]agentcore.ToolResult
	TurnID    string
	Completed bool
}

func (t *prefixTracker) observeStep(sessionID, turnID string, res *agentcore.GenerateResult) {
	if t == nil || sessionID == "" || res == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.steps[sessionID] = forkStep{Result: cloneForkResult(res), TurnID: turnID, Done: make(map[string]agentcore.ToolResult)}
}

func (t *prefixTracker) observeToolResult(sessionID, callID string, res agentcore.ToolResult) {
	if t == nil || sessionID == "" || callID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	step := t.steps[sessionID]
	if step.Done == nil {
		step.Done = make(map[string]agentcore.ToolResult)
	}
	step.Done[callID] = cloneForkToolResult(res)
	t.steps[sessionID] = step
}

func (t *prefixTracker) observeTurnEnd(sessionID, turnID string, completed bool) {
	if t == nil || sessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	step, ok := t.steps[sessionID]
	if !ok || (turnID != "" && step.TurnID != turnID) {
		return
	}
	step.Completed = completed
	t.steps[sessionID] = step
}

func (t *prefixTracker) lastStep(sessionID string) (forkStep, bool) {
	if t == nil || sessionID == "" {
		return forkStep{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	step, ok := t.steps[sessionID]
	if !ok {
		return forkStep{}, false
	}
	step.Result = cloneForkResultPtr(&step.Result)
	step.Done = cloneForkDone(step.Done)
	return step, true
}

// forkStepHook records just enough of the running turn to construct a fork
// without changing the persisted transcript or the model-facing request.
type forkStepHook struct {
	agentcore.NopHook
	tracker *prefixTracker
}

func (h forkStepHook) AfterModel(ctx context.Context, event *agentcore.ModelEvent, _ *agentcore.Control) error {
	if event != nil {
		h.tracker.observeStep(observeScopeFrom(ctx).sessionID, observeScopeFrom(ctx).turnID, event.Result)
	}
	return nil
}

func (h forkStepHook) AfterTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event != nil && event.Result != nil && event.Call != nil {
		h.tracker.observeToolResult(observeScopeFrom(ctx).sessionID, event.Call.CallID, *event.Result)
	}
	return nil
}

func cloneForkRequest(req *agentcore.GenerateRequest) *agentcore.GenerateRequest {
	if req == nil {
		return nil
	}
	clone := *req
	clone.Messages = append([]agentcore.Message(nil), req.Messages...)
	for i := range clone.Messages {
		clone.Messages[i].Metadata = cloneForkMetadata(req.Messages[i].Metadata)
		if req.Messages[i].ToolCall != nil {
			call := cloneForkCall(*req.Messages[i].ToolCall)
			clone.Messages[i].ToolCall = &call
		}
		if req.Messages[i].ToolResult != nil {
			result := cloneForkToolResult(*req.Messages[i].ToolResult)
			clone.Messages[i].ToolResult = &result
		}
	}
	clone.Tools = append([]agentcore.ToolSchema(nil), req.Tools...)
	clone.Metadata = cloneForkMetadata(req.Metadata)
	return &clone
}

func cloneForkResult(res *agentcore.GenerateResult) agentcore.GenerateResult {
	if res == nil {
		return agentcore.GenerateResult{}
	}
	clone := *res
	clone.ToolCalls = make([]agentcore.ToolCall, len(res.ToolCalls))
	for i, call := range res.ToolCalls {
		clone.ToolCalls[i] = cloneForkCall(call)
	}
	clone.Metadata = cloneForkMetadata(res.Metadata)
	clone.Artifacts = append([]agentcore.Artifact(nil), res.Artifacts...)
	return clone
}

func cloneForkResultPtr(res *agentcore.GenerateResult) agentcore.GenerateResult {
	return cloneForkResult(res)
}

func cloneForkCall(call agentcore.ToolCall) agentcore.ToolCall {
	call.Arguments = cloneForkMetadata(call.Arguments)
	return call
}

func cloneForkToolResult(result agentcore.ToolResult) agentcore.ToolResult {
	result.Metadata = cloneForkMetadata(result.Metadata)
	result.Artifacts = append([]agentcore.Artifact(nil), result.Artifacts...)
	return result
}

func cloneForkDone(done map[string]agentcore.ToolResult) map[string]agentcore.ToolResult {
	if len(done) == 0 {
		return nil
	}
	clone := make(map[string]agentcore.ToolResult, len(done))
	for id, result := range done {
		clone[id] = cloneForkToolResult(result)
	}
	return clone
}

func cloneForkMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// ForkSeedForCall snapshots the current request plus the model reply that
// produced callID. Model-triggered forks always use the current prefix; host
// hooks are intentionally not consulted here (ACP's turn gate still applies).
func (r *Runner) ForkSeedForCall(ctx context.Context, sessionID, callID, task string) (ForkSeed, error) {
	if r == nil {
		return ForkSeed{}, &ForkUnavailableError{Reason: "no_request"}
	}
	base := r.rec.prefix.lastRequest(sessionID)
	step, ok := r.rec.prefix.lastStep(sessionID)
	if base == nil || !ok {
		return ForkSeed{}, &ForkUnavailableError{Reason: "no_request"}
	}
	index := -1
	for i, call := range step.Result.ToolCalls {
		if call.CallID == callID {
			index = i
			break
		}
	}
	if index < 0 {
		return ForkSeed{}, &ForkUnavailableError{Reason: "no_request"}
	}

	request := cloneForkRequest(base)
	assistant := agentcore.ExpandToolCalls(&step.Result)
	for i, message := range assistant {
		request.Messages = append(request.Messages, message)
		call := step.Result.ToolCalls[i]
		content := ""
		if i < index {
			content = step.Done[call.CallID].Content
		} else if i == index {
			content = forkTranslate(r.cfg.Translator, "model.fork.directive", map[string]any{"task": task})
		} else {
			content = forkTranslate(r.cfg.Translator, "model.fork.not_run")
		}
		result := agentcore.ToolResult{CallID: call.CallID, Name: call.Name, Content: content}
		request.Messages = append(request.Messages, agentcore.Message{Role: "tool", Content: content, ToolCall: &call, ToolResult: &result})
	}
	if forkRequestTokens(request) > r.forkThreshold() {
		return ForkSeed{}, &ForkUnavailableError{Reason: "context", hint: forkTranslate(r.cfg.Translator, "model.fork.too_large")}
	}
	return ForkSeed{Request: request, Model: r.cfg.Model, ProviderName: r.cfg.ProviderName,
		BaseTurn: step.TurnID, Source: "call", Reuse: "prefix"}, nil
}

func (r *Runner) forkThreshold() int {
	window, reserve := r.compactLimits()
	return thresholdOf(window, reserve)
}

func forkRequestTokens(req *agentcore.GenerateRequest) int {
	if req == nil {
		return 0
	}
	return tokensOfText(req.System) + tokensOfTools(req.Tools) + tokensOfMessages(req.Messages)
}

func forkTranslate(tr i18n.Translator, key string, args ...map[string]any) string {
	if tr == nil {
		tr = i18n.Fallback()
	}
	return tr.T(key, args...)
}

// Keep the seed assembly independent of the runner's persisted transcript.
