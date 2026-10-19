package agentcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

func stateForSaveStateFailure(preState *RunState, currentState *RunState, err error) *RunState {
	var memoryErr *HistoryError
	if errors.As(err, &memoryErr) {
		return currentState
	}
	return preState
}

type runtimeResolvedControlError struct {
	result *RunResult
}

func (e runtimeResolvedControlError) Error() string {
	return "runtime control resolved"
}

// defaultStorage keeps runs in memory when the host passes no storage.
func defaultStorage(storage Storage) Storage {
	if storage != nil {
		return storage
	}
	return NewMemoryStorage()
}

func defaultTools(tools ToolRegistry) ToolRegistry {
	if tools != nil {
		return tools
	}
	return noTools{}
}

// noTools is the registry of a runtime configured without tools: it lists
// none and rejects every call as unregistered.
type noTools struct{}

func (noTools) ListTools(context.Context) ([]ToolSchema, error) { return nil, nil }

func (noTools) ExecuteTool(_ context.Context, call ToolCall) (*ToolResult, error) {
	return nil, &ToolError{Err: fmt.Errorf("tool %q not registered", call.Name)}
}

func defaultHistory(history History) History {
	if history != nil {
		return history
	}
	return noopHistory{}
}

func defaultPromptBuilder(builder PromptBuilder) PromptBuilder {
	if builder != nil {
		return builder
	}
	return defaultStaticPromptBuilder{}
}

func defaultResumePolicy(policy ResumePolicy) ResumePolicy {
	if policy != nil {
		return policy
	}
	return DefaultResumePolicy{}
}

func defaultToolExecutionPolicy(policy ToolExecutionPolicy, maxCallsPerStep int) ToolExecutionPolicy {
	if policy != nil {
		return policy
	}
	return DefaultToolExecutionPolicy{MaxCallsPerStep: maxCallsPerStep}
}

func defaultErrorPolicy(policy ErrorPolicy) ErrorPolicy {
	if policy != nil {
		return policy
	}
	return DefaultErrorPolicy{}
}

func defaultContextBuilder(history History) ContextBuilder {
	return defaultContextBuilderImpl{history: history}
}

func newRunState(runID string, input RunInput) *RunState {
	return &RunState{
		RunID:    runID,
		AgentID:  input.AgentID,
		Status:   RunRunning,
		Metadata: cloneMetadata(input.Metadata),
	}
}

func newRunID() string {
	return fmt.Sprintf("run-%d", time.Now().UnixNano())
}

func wrapProviderError(err error) error {
	if err == nil {
		return nil
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return err
	}
	return &ProviderError{Err: err}
}

func wrapStorageError(err error) error {
	if err == nil {
		return nil
	}
	var storageErr *StorageError
	if errors.As(err, &storageErr) {
		return err
	}
	return &StorageError{Err: err}
}

func wrapHistoryError(err error) error {
	if err == nil {
		return nil
	}
	var memoryErr *HistoryError
	if errors.As(err, &memoryErr) {
		return err
	}
	return &HistoryError{Err: err}
}

func wrapHookError(err error) error {
	if err == nil {
		return nil
	}
	var hookErr *HookError
	if errors.As(err, &hookErr) {
		return err
	}
	return &HookError{Err: err}
}

func isStorageMiss(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

func classifyRuntimeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.As(err, new(*HookError)):
		return err
	case errors.As(err, new(*ProviderError)):
		return err
	case errors.As(err, new(*StorageError)):
		return err
	case errors.As(err, new(*HistoryError)):
		return err
	case errors.As(err, new(*ToolError)):
		return err
	case errors.Is(err, ErrToolCallsNotImplemented):
		return err
	case errors.Is(err, ErrRunNotResumable):
		return err
	default:
		return err
	}
}

func normalizeToolResult(call ToolCall, result *ToolResult) *ToolResult {
	if result == nil {
		result = &ToolResult{}
	}
	cloned := cloneToolResultValue(result)
	if cloned == nil {
		cloned = &ToolResult{}
	}
	if cloned.CallID == "" {
		cloned.CallID = call.CallID
	}
	if cloned.Name == "" {
		cloned.Name = call.Name
	}
	return cloned
}

func toolErrorAsResult(call ToolCall, err error) *ToolResult {
	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		err = &ToolError{Err: err}
	}
	return &ToolResult{
		CallID:  call.CallID,
		Name:    call.Name,
		Content: err.Error(),
		IsError: true,
	}
}

func cloneRunInput(input RunInput) *RunInput {
	cloned := input
	cloned.Metadata = cloneMetadata(input.Metadata)
	return &cloned
}

func cloneRunStateValue(state *RunState) *RunState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.Messages = cloneMessagesValue(state.Messages)
	if len(state.Artifacts) > 0 {
		cloned.Artifacts = make([]Artifact, len(state.Artifacts))
		copy(cloned.Artifacts, state.Artifacts)
	}
	cloned.Metadata = cloneMetadata(state.Metadata)
	cloned.Scratch = cloneMetadata(state.Scratch)
	return &cloned
}

func cloneMessagesValue(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]Message, len(messages))
	for i, msg := range messages {
		cloned[i] = Message{
			Role:       msg.Role,
			Content:    msg.Content,
			Metadata:   cloneMetadata(msg.Metadata),
			ToolCall:   cloneToolCallValue(msg.ToolCall),
			ToolResult: cloneToolResultValue(msg.ToolResult),
			Reasoning:  msg.Reasoning,
			Images:     cloneImagesValue(msg.Images),
		}
	}
	return cloned
}

func cloneToolCallValue(call *ToolCall) *ToolCall {
	if call == nil {
		return nil
	}
	cloned := *call
	cloned.Arguments = cloneMetadata(call.Arguments)
	return &cloned
}

func cloneToolCallsValue(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	cloned := make([]ToolCall, len(calls))
	for i := range calls {
		cloned[i] = *cloneToolCallValue(&calls[i])
	}
	return cloned
}

func cloneToolResultValue(result *ToolResult) *ToolResult {
	if result == nil {
		return nil
	}
	cloned := *result
	cloned.Artifacts = cloneArtifactsValue(result.Artifacts)
	cloned.Metadata = cloneMetadata(result.Metadata)
	cloned.Images = cloneImagesValue(result.Images)
	return &cloned
}

// cloneImagesValue deep-copies image parts, bytes included: a message clone
// must not share the Data slice with the state the caller keeps.
func cloneImagesValue(images []ImagePart) []ImagePart {
	if len(images) == 0 {
		return nil
	}
	cloned := make([]ImagePart, len(images))
	for i, image := range images {
		cloned[i] = image
		if len(image.Data) > 0 {
			cloned[i].Data = append([]byte(nil), image.Data...)
		}
	}
	return cloned
}

func cloneArtifactsValue(artifacts []Artifact) []Artifact {
	if len(artifacts) == 0 {
		return nil
	}
	cloned := make([]Artifact, len(artifacts))
	for i, artifact := range artifacts {
		cloned[i] = Artifact{
			Kind:     artifact.Kind,
			URI:      artifact.URI,
			Content:  artifact.Content,
			Metadata: cloneMetadata(artifact.Metadata),
		}
	}
	return cloned
}

func cloneGenerateRequestValue(req *GenerateRequest) *GenerateRequest {
	if req == nil {
		return nil
	}
	cloned := *req
	cloned.Messages = cloneMessagesValue(req.Messages)
	if len(req.Tools) > 0 {
		cloned.Tools = make([]ToolSchema, len(req.Tools))
		for i, tool := range req.Tools {
			cloned.Tools[i] = ToolSchema{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: cloneMetadata(tool.InputSchema),
			}
		}
	}
	cloned.Metadata = cloneMetadata(req.Metadata)
	return &cloned
}

func cloneGenerateResultValue(result *GenerateResult) *GenerateResult {
	if result == nil {
		return nil
	}
	cloned := *result
	if len(result.ToolCalls) > 0 {
		cloned.ToolCalls = make([]ToolCall, len(result.ToolCalls))
		for i, call := range result.ToolCalls {
			cloned.ToolCalls[i] = *cloneToolCallValue(&call)
		}
	}
	if len(result.Artifacts) > 0 {
		cloned.Artifacts = cloneArtifactsValue(result.Artifacts)
	}
	cloned.Metadata = cloneMetadata(result.Metadata)
	return &cloned
}

func cloneRunResultValue(result *RunResult) *RunResult {
	if result == nil {
		return nil
	}
	cloned := *result
	if result.Yield != nil {
		yield := *result.Yield
		yield.Metadata = cloneMetadata(result.Yield.Metadata)
		cloned.Yield = &yield
	}
	return &cloned
}

func cloneStreamEventValue(event StreamEvent) StreamEvent {
	return StreamEvent{
		Kind:     event.Kind,
		Delta:    event.Delta,
		Metadata: cloneMetadata(event.Metadata),
	}
}

func mergeMetadata(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := cloneMetadata(base)
	if merged == nil {
		merged = make(map[string]any, len(overlay))
	}
	for k, v := range overlay {
		merged[k] = v
	}
	return merged
}

type defaultStaticPromptBuilder struct{}

func (defaultStaticPromptBuilder) BuildSystemPrompt(context.Context, *RunState) (string, error) {
	return "", nil
}

type defaultContextBuilderImpl struct {
	history History
}

func (b defaultContextBuilderImpl) BuildMessages(ctx context.Context, state *RunState) ([]Message, error) {
	return b.history.BuildContext(ctx, state)
}

type runtimeHookChain struct {
	hooks []Hook
}

func newRuntimeHookChain(hooks ...Hook) runtimeHookChain {
	filtered := make([]Hook, 0, len(hooks))
	for _, hook := range hooks {
		if hook != nil {
			filtered = append(filtered, hook)
		}
	}
	return runtimeHookChain{hooks: filtered}
}

func (c runtimeHookChain) runStage(call func(Hook) error, ctrl *Control) error {
	for _, hook := range c.hooks {
		if err := call(hook); err != nil {
			return err
		}
		if ctrl != nil && (ctrl.ShouldShortCircuit() || ctrl.ShouldYield()) {
			return nil
		}
	}
	return nil
}

func (c runtimeHookChain) runStageWithoutControlStop(call func(Hook) error) error {
	for _, hook := range c.hooks {
		if err := call(hook); err != nil {
			return err
		}
	}
	return nil
}

func (c runtimeHookChain) BeforeRun(ctx context.Context, event *RunEvent, ctrl *Control) error {
	return c.runStage(func(h Hook) error { return h.BeforeRun(ctx, event, ctrl) }, ctrl)
}

func (c runtimeHookChain) BeforePromptBuild(ctx context.Context, event *PromptEvent, ctrl *Control) error {
	return c.runStage(func(h Hook) error { return h.BeforePromptBuild(ctx, event, ctrl) }, ctrl)
}

func (c runtimeHookChain) BeforeContextBuild(ctx context.Context, event *ContextEvent, ctrl *Control) error {
	return c.runStage(func(h Hook) error { return h.BeforeContextBuild(ctx, event, ctrl) }, ctrl)
}

func (c runtimeHookChain) BeforeModel(ctx context.Context, event *ModelEvent, ctrl *Control) error {
	return c.runStage(func(h Hook) error { return h.BeforeModel(ctx, event, ctrl) }, ctrl)
}

func (c runtimeHookChain) AfterModel(ctx context.Context, event *ModelEvent, ctrl *Control) error {
	return c.runStage(func(h Hook) error { return h.AfterModel(ctx, event, ctrl) }, ctrl)
}

func (c runtimeHookChain) BeforeTool(ctx context.Context, event *ToolEvent, ctrl *Control) error {
	return c.runStage(func(h Hook) error { return h.BeforeTool(ctx, event, ctrl) }, ctrl)
}

func (c runtimeHookChain) AfterTool(ctx context.Context, event *ToolEvent, ctrl *Control) error {
	return c.runStageWithoutControlStop(func(h Hook) error { return h.AfterTool(ctx, event, ctrl) })
}

func (c runtimeHookChain) AfterRun(ctx context.Context, event *RunEvent, ctrl *Control) error {
	return c.runStage(func(h Hook) error { return h.AfterRun(ctx, event, ctrl) }, ctrl)
}

func (c runtimeHookChain) OnError(ctx context.Context, event *ErrorEvent, ctrl *Control) error {
	return c.runStage(func(h Hook) error { return h.OnError(ctx, event, ctrl) }, ctrl)
}

func (c runtimeHookChain) OnErrorObserved(ctx context.Context, event *ErrorEvent, ctrl *Control) error {
	return c.runStageWithoutControlStop(func(h Hook) error { return h.OnError(ctx, event, ctrl) })
}
