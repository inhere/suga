package agentcore

import (
	"context"
	"fmt"
	"os"
)

type testStorage struct{}

func (testStorage) LoadRun(context.Context, string) (*RunState, error)      { return nil, nil }
func (testStorage) LoadMessages(context.Context, string) ([]Message, error) { return nil, nil }
func (testStorage) SaveRun(context.Context, *RunState) error                { return nil }
func (testStorage) AppendMessages(context.Context, string, ...Message) error {
	return nil
}

type testPromptBuilder struct{}

func (testPromptBuilder) BuildSystemPrompt(context.Context, *RunState) (string, error) {
	return "", nil
}

type testContextBuilder struct{}

func (testContextBuilder) BuildMessages(context.Context, *RunState) ([]Message, error) {
	return nil, nil
}

type runtimeTestPromptBuilder struct {
	prompt  string
	err     error
	calls   *int
	onBuild func(*RunState) error
}

func (b runtimeTestPromptBuilder) BuildSystemPrompt(_ context.Context, state *RunState) (string, error) {
	if b.calls != nil {
		*b.calls = *b.calls + 1
	}
	if b.onBuild != nil {
		if err := b.onBuild(state); err != nil {
			return "", err
		}
	}
	if b.err != nil {
		return "", b.err
	}
	return b.prompt, nil
}

type runtimeTestContextBuilder struct {
	err     error
	calls   *int
	onBuild func(*RunState) error
}

func (b runtimeTestContextBuilder) BuildMessages(_ context.Context, state *RunState) ([]Message, error) {
	if b.calls != nil {
		*b.calls = *b.calls + 1
	}
	if b.onBuild != nil {
		if err := b.onBuild(state); err != nil {
			return nil, err
		}
	}
	if b.err != nil {
		return nil, b.err
	}
	if state == nil || len(state.Messages) == 0 {
		return nil, nil
	}
	msgs := make([]Message, len(state.Messages))
	copy(msgs, state.Messages)
	return msgs, nil
}

type runtimeTestProvider struct {
	results []*GenerateResult
	errs    []error
	calls   []*GenerateRequest
}

func (p *runtimeTestProvider) Name() string {
	return "runtime-test"
}

func (p *runtimeTestProvider) Generate(_ context.Context, req *GenerateRequest) (*GenerateResult, error) {
	p.calls = append(p.calls, cloneGenerateRequest(req))

	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return nil, err
	}
	if len(p.results) == 0 {
		return nil, fmt.Errorf("no scripted provider result")
	}

	result := cloneGenerateResult(p.results[0])
	p.results = p.results[1:]
	return result, nil
}

type runtimeTestStreamingProvider struct {
	runtimeTestProvider
	streamEvents []StreamEvent
	streamCalls  int
}

func (p *runtimeTestStreamingProvider) Stream(ctx context.Context, req *GenerateRequest, emit StreamFunc) (*GenerateResult, error) {
	p.streamCalls++
	p.calls = append(p.calls, cloneGenerateRequest(req))

	for _, event := range p.streamEvents {
		if emit == nil {
			continue
		}
		if err := emit(ctx, event); err != nil {
			return nil, err
		}
	}

	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return nil, err
	}
	if len(p.results) == 0 {
		return nil, fmt.Errorf("no scripted provider result")
	}

	result := cloneGenerateResult(p.results[0])
	p.results = p.results[1:]
	return result, nil
}

type runtimeTestTool struct {
	name        string
	description string
	result      *ToolResult
	err         error
	handler     func(ToolCall) (*ToolResult, error)
	calls       []ToolCall
}

func (t *runtimeTestTool) Name() string {
	return t.name
}

func (t *runtimeTestTool) Description() string {
	if t.description != "" {
		return t.description
	}
	return "runtime test tool"
}

func (t *runtimeTestTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        t.name,
		Description: t.Description(),
		InputSchema: map[string]any{"type": "object"},
	}
}

type runtimeTestToolWithSchema struct {
	runtimeTestTool
	inputSchema map[string]any
}

func (t *runtimeTestToolWithSchema) Schema() ToolSchema {
	return ToolSchema{
		Name:        t.name,
		Description: t.Description(),
		InputSchema: cloneMetadata(t.inputSchema),
	}
}

func (t *runtimeTestTool) Execute(_ context.Context, call ToolCall) (*ToolResult, error) {
	t.calls = append(t.calls, call)
	if t.handler != nil {
		return t.handler(call)
	}
	if t.err != nil {
		return nil, t.err
	}
	if t.result == nil {
		return &ToolResult{CallID: call.CallID, Name: call.Name}, nil
	}
	result := cloneToolResult(t.result)
	if result.CallID == "" {
		result.CallID = call.CallID
	}
	if result.Name == "" {
		result.Name = call.Name
	}
	return result, nil
}

type runtimeTestStorage struct {
	runs              map[string]*RunState
	messages          map[string][]Message
	artifacts         map[string][]Artifact
	loadRunErr        error
	loadMsgsErr       error
	saveRunErr        error
	appendErr         error
	artifactLoadErr   error
	artifactSaveErr   error
	appendErrOnCall   int
	appendCalls       int
	artifactLoadCalls int
	artifactSaveCalls int
	appendHook        func(string, []Message)
}

func newRuntimeTestStorage() *runtimeTestStorage {
	return &runtimeTestStorage{
		runs:      make(map[string]*RunState),
		messages:  make(map[string][]Message),
		artifacts: make(map[string][]Artifact),
	}
}

func (s *runtimeTestStorage) LoadRun(_ context.Context, runID string) (*RunState, error) {
	if s.loadRunErr != nil {
		return nil, s.loadRunErr
	}
	return cloneRunState(s.runs[runID]), nil
}

func (s *runtimeTestStorage) LoadMessages(_ context.Context, runID string) ([]Message, error) {
	if s.loadMsgsErr != nil {
		return nil, s.loadMsgsErr
	}
	return cloneMessages(s.messages[runID]), nil
}

func (s *runtimeTestStorage) SaveRun(_ context.Context, state *RunState) error {
	if s.saveRunErr != nil {
		return s.saveRunErr
	}
	if state == nil {
		return nil
	}
	s.runs[state.RunID] = cloneRunState(state)
	return nil
}

func (s *runtimeTestStorage) AppendMessages(_ context.Context, runID string, msgs ...Message) error {
	s.appendCalls++
	if s.appendErrOnCall > 0 && s.appendCalls == s.appendErrOnCall {
		return s.appendErr
	}
	if s.appendErr != nil && s.appendErrOnCall == 0 {
		return s.appendErr
	}
	if s.appendHook != nil {
		s.appendHook(runID, cloneMessages(msgs))
	}
	s.messages[runID] = append(s.messages[runID], cloneMessages(msgs)...)
	return nil
}

func (s *runtimeTestStorage) SaveArtifacts(_ context.Context, runID string, artifacts []Artifact) error {
	s.artifactSaveCalls++
	if s.artifactSaveErr != nil {
		return s.artifactSaveErr
	}
	s.artifacts[runID] = append([]Artifact(nil), artifacts...)
	return nil
}

func (s *runtimeTestStorage) LoadArtifacts(_ context.Context, runID string) ([]Artifact, error) {
	s.artifactLoadCalls++
	if s.artifactLoadErr != nil {
		return nil, s.artifactLoadErr
	}
	if artifacts, ok := s.artifacts[runID]; ok {
		return append([]Artifact(nil), artifacts...), nil
	}
	return nil, os.ErrNotExist
}

type runtimeTestHook struct {
	NopHook
	beforeRun       func(*Control)
	beforePrompt    func(*Control)
	beforeContext   func(*Control)
	beforeModel     func(*Control)
	afterModel      func(*Control)
	beforeTool      func(*Control)
	beforeToolEvent func(*ToolEvent, *Control)
	afterTool       func(*Control)
	afterToolEvent  func(*ToolEvent, *Control)
	afterToolErr    error
	afterRun        func(*Control)
	afterRunEvent   func(*RunEvent, *Control)
	afterRunErr     error
	onError         func(*Control)
	onErrorEvent    func(*ErrorEvent, *Control)
	onErrorReturn   func(*ErrorEvent, *Control) error
	beforeModelErr  error
	beforeRunCalls  int
}

func (h *runtimeTestHook) BeforeRun(_ context.Context, _ *RunEvent, ctrl *Control) error {
	h.beforeRunCalls++
	if h.beforeRun != nil {
		h.beforeRun(ctrl)
	}
	return nil
}

func (h *runtimeTestHook) BeforePromptBuild(_ context.Context, _ *PromptEvent, ctrl *Control) error {
	if h.beforePrompt != nil {
		h.beforePrompt(ctrl)
	}
	return nil
}

func (h *runtimeTestHook) BeforeContextBuild(_ context.Context, _ *ContextEvent, ctrl *Control) error {
	if h.beforeContext != nil {
		h.beforeContext(ctrl)
	}
	return nil
}

func (h *runtimeTestHook) BeforeModel(_ context.Context, _ *ModelEvent, ctrl *Control) error {
	if h.beforeModel != nil {
		h.beforeModel(ctrl)
	}
	return h.beforeModelErr
}

func (h *runtimeTestHook) AfterModel(_ context.Context, _ *ModelEvent, ctrl *Control) error {
	if h.afterModel != nil {
		h.afterModel(ctrl)
	}
	return nil
}

func (h *runtimeTestHook) BeforeTool(_ context.Context, event *ToolEvent, ctrl *Control) error {
	if h.beforeTool != nil {
		h.beforeTool(ctrl)
	}
	if h.beforeToolEvent != nil {
		h.beforeToolEvent(event, ctrl)
	}
	return nil
}

func (h *runtimeTestHook) AfterTool(_ context.Context, event *ToolEvent, ctrl *Control) error {
	if h.afterTool != nil {
		h.afterTool(ctrl)
	}
	if h.afterToolEvent != nil {
		h.afterToolEvent(event, ctrl)
	}
	return h.afterToolErr
}

func (h *runtimeTestHook) AfterRun(_ context.Context, event *RunEvent, ctrl *Control) error {
	if h.afterRun != nil {
		h.afterRun(ctrl)
	}
	if h.afterRunEvent != nil {
		h.afterRunEvent(event, ctrl)
	}
	return h.afterRunErr
}

func (h *runtimeTestHook) OnError(_ context.Context, event *ErrorEvent, ctrl *Control) error {
	if h.onError != nil {
		h.onError(ctrl)
	}
	if h.onErrorEvent != nil {
		h.onErrorEvent(event, ctrl)
	}
	if h.onErrorReturn != nil {
		return h.onErrorReturn(event, ctrl)
	}
	return nil
}

func afterRunErrorHook(err error) *runtimeTestHook {
	return &runtimeTestHook{afterRunErr: err}
}

func cloneGenerateRequest(req *GenerateRequest) *GenerateRequest {
	if req == nil {
		return nil
	}
	cloned := &GenerateRequest{
		Model:       req.Model,
		System:      req.System,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Metadata:    cloneMetadata(req.Metadata),
		ToolChoice:  req.ToolChoice,
	}
	if len(req.Messages) > 0 {
		cloned.Messages = cloneMessages(req.Messages)
	}
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
	return cloned
}

func cloneGenerateResult(result *GenerateResult) *GenerateResult {
	if result == nil {
		return nil
	}
	cloned := &GenerateResult{
		Content:      result.Content,
		FinishReason: result.FinishReason,
		Usage:        result.Usage,
		Artifacts:    append([]Artifact(nil), result.Artifacts...),
		Metadata:     cloneMetadata(result.Metadata),
		Reasoning:    result.Reasoning,
		Truncated:    result.Truncated,
		OutputLimit:  result.OutputLimit,
	}
	if len(result.ToolCalls) > 0 {
		cloned.ToolCalls = make([]ToolCall, len(result.ToolCalls))
		for i, call := range result.ToolCalls {
			cloned.ToolCalls[i] = *cloneToolCallValue(&call)
		}
	}
	return cloned
}

func cloneRunState(state *RunState) *RunState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.Messages = cloneMessages(state.Messages)
	cloned.Artifacts = append([]Artifact(nil), state.Artifacts...)
	cloned.Metadata = cloneMetadata(state.Metadata)
	cloned.Scratch = cloneMetadata(state.Scratch)
	return &cloned
}

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]Message, len(messages))
	for i, msg := range messages {
		cloned[i] = Message{
			Role:       msg.Role,
			Content:    msg.Content,
			Metadata:   cloneMetadata(msg.Metadata),
			ToolCall:   cloneToolCall(msg.ToolCall),
			ToolResult: cloneToolResult(msg.ToolResult),
			Images:     cloneImagesValue(msg.Images),
		}
	}
	return cloned
}

func cloneToolCall(call *ToolCall) *ToolCall {
	if call == nil {
		return nil
	}
	cloned := *call
	cloned.Arguments = cloneMetadata(call.Arguments)
	return &cloned
}

func cloneToolResult(result *ToolResult) *ToolResult {
	if result == nil {
		return nil
	}
	cloned := *result
	cloned.Artifacts = append([]Artifact(nil), result.Artifacts...)
	cloned.Metadata = cloneMetadata(result.Metadata)
	cloned.Images = cloneImagesValue(result.Images)
	return &cloned
}
