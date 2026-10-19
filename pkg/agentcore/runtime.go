package agentcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// Runtime executes agent runs using the public agentcore contracts.
type Runtime interface {
	Run(ctx context.Context, input RunInput) (*RunResult, error)
}

// Config configures the default runtime implementation.
type Config struct {
	Provider            Provider
	Storage             Storage
	History             History
	Tools               ToolRegistry
	PromptBuilder       PromptBuilder
	ContextBuilder      ContextBuilder
	ResumePolicy        ResumePolicy
	ToolExecutionPolicy ToolExecutionPolicy
	ErrorPolicy         ErrorPolicy
	Hooks               []Hook
	MaxSteps            int
	MaxToolCallsPerStep int
	// MaxOutputTokens is the output token limit of every request this runtime
	// makes (GenerateRequest.MaxTokens); 0 leaves the limit to the provider,
	// which applies its own default then (e.g. Claude's 32000). The
	// application resolves it from its configuration.
	MaxOutputTokens int
	// MaxStepsWrapUp, when set, makes reaching MaxSteps ask the model for one
	// more answer instead of failing at once: the text is appended to that
	// request only (not persisted) as a user message. A model that keeps
	// requesting tools anyway still ends the run with an answer (see
	// finishIgnoredWrapUp), never with the max-steps error.
	MaxStepsWrapUp string
	// PrefetchTools, when set, gets every reply with more than one tool call
	// before the calls run one by one. It may start the side-effect-free ones
	// concurrently, as long as each call still runs through the registry in
	// order (see tools.Registry.Prefetch).
	PrefetchTools func(ctx context.Context, calls []ToolCall)
}

// DefaultRuntime provides the minimal public runtime orchestration.
type DefaultRuntime struct {
	provider            Provider
	storage             Storage
	history             History
	tools               ToolRegistry
	promptBuilder       PromptBuilder
	contextBuilder      ContextBuilder
	resumePolicy        ResumePolicy
	toolPolicy          ToolExecutionPolicy
	errorPolicy         ErrorPolicy
	hooks               runtimeHookChain
	maxSteps            int
	maxToolCallsPerStep int
	maxOutputTokens     int
	maxStepsWrapUp      string
	prefetchTools       func(ctx context.Context, calls []ToolCall)
}

// New constructs a runnable runtime, filling in built-in defaults when possible.
func New(cfg Config) *DefaultRuntime {
	return &DefaultRuntime{
		provider:            cfg.Provider,
		storage:             defaultStorage(cfg.Storage),
		history:             defaultHistory(cfg.History),
		tools:               defaultTools(cfg.Tools),
		promptBuilder:       defaultPromptBuilder(cfg.PromptBuilder),
		contextBuilder:      cfg.ContextBuilder,
		resumePolicy:        defaultResumePolicy(cfg.ResumePolicy),
		toolPolicy:          defaultToolExecutionPolicy(cfg.ToolExecutionPolicy, cfg.MaxToolCallsPerStep),
		errorPolicy:         defaultErrorPolicy(cfg.ErrorPolicy),
		hooks:               newRuntimeHookChain(cfg.Hooks...),
		maxSteps:            cfg.MaxSteps,
		maxToolCallsPerStep: cfg.MaxToolCallsPerStep,
		maxOutputTokens:     cfg.MaxOutputTokens,
		maxStepsWrapUp:      cfg.MaxStepsWrapUp,
		prefetchTools:       cfg.PrefetchTools,
	}
}

// Run executes the minimal final-answer runtime path.
func (r *DefaultRuntime) Run(ctx context.Context, input RunInput) (*RunResult, error) {
	if r == nil || r.provider == nil {
		return nil, ErrProviderRequired
	}

	state, appendInstruction, err := r.prepareState(ctx, input)
	if err != nil {
		return nil, err
	}

	ctrl := NewControl()
	runEvent := &RunEvent{
		RunID:     state.RunID,
		AgentID:   state.AgentID,
		Input:     cloneRunInput(input),
		Result:    nil,
		Metadata:  cloneMetadata(state.Metadata),
		Timestamp: time.Now(),
	}

	if err := r.history.Hydrate(ctx, cloneRunStateValue(state)); err != nil {
		return r.returnWithHandledError(ctx, input, state, "memory_hydrate", wrapHistoryError(err), ctrl)
	}

	if err := r.runBeforeRun(ctx, runEvent, ctrl); err != nil {
		return r.returnWithHandledError(ctx, input, state, "before_run", err, ctrl)
	}
	if result, err, handled := r.resolveControl(ctx, state, ctrl); handled {
		return r.finishRun(ctx, input, state, result, err)
	}

	newMessages := make([]Message, 0, 2)
	preInputState := cloneRunStateValue(state)
	if appendInstruction {
		userMessage := Message{Role: "user", Content: input.Instruction, Images: cloneImagesValue(input.Images)}
		state.Messages = append(state.Messages, userMessage)
		newMessages = append(newMessages, userMessage)
	}

	if err := r.saveState(ctx, state, newMessages); err != nil {
		return r.returnWithHandledError(ctx, input, stateForSaveStateFailure(preInputState, state, err), "persist_input", err, ctrl)
	}

	maxSteps := r.effectiveMaxSteps(input)
	// wrappingUp: this step's request asks for a final answer (wrapUp says
	// why and wrapUpText how): at the max steps limit, or when a hook asked
	// through the run's control (Control.RequestWrapUp).
	wrappingUp := false
	wrapUp, wrapUpText := "", ""

	for {
		if maxSteps > 0 && state.Step >= maxSteps {
			if r.maxStepsWrapUp == "" || wrappingUp {
				return r.returnWithHandledError(ctx, input, state, "max_steps", fmt.Errorf("max steps exceeded: %d", maxSteps), NewControl())
			}
			wrappingUp, wrapUp, wrapUpText = true, wrapUpReasonMaxSteps, r.maxStepsWrapUp
		}
		if !wrappingUp && ctrl.WrapUpReason != "" {
			wrappingUp, wrapUp, wrapUpText = true, ctrl.WrapUpReason, ctrl.WrapUpInstruction
		}

		systemPrompt, err := r.buildPrompt(ctx, state, ctrl)
		if err != nil {
			return r.returnWithHandledError(ctx, input, state, "prompt", err, ctrl)
		}
		if result, err, handled := r.resolveControl(ctx, state, ctrl); handled {
			return r.finishRun(ctx, input, state, result, err)
		}

		// Tell the context builder whether this step asks for a final answer
		// (design D5): a host must not insert new user messages into a wrap-up
		// step. The field is transient, set anew for every step.
		state.WrappingUp = ""
		if wrappingUp {
			state.WrappingUp = wrapUp
		}

		messages, err := r.buildContext(ctx, state, ctrl)
		if err != nil {
			return r.returnWithHandledError(ctx, input, state, "context", err, ctrl)
		}
		if result, err, handled := r.resolveControl(ctx, state, ctrl); handled {
			return r.finishRun(ctx, input, state, result, err)
		}

		tools, err := r.listTools(ctx)
		if err != nil {
			return r.returnWithHandledError(ctx, input, state, "tools", err, ctrl)
		}

		if wrappingUp {
			// Tools stay declared (some APIs reject tool history without them);
			// the instruction asks for a final answer from what is known.
			messages = append(messages, Message{Role: "user", Content: wrapUpText})
		}
		req := &GenerateRequest{
			System:    systemPrompt,
			Messages:  messages,
			Tools:     tools,
			MaxTokens: r.maxOutputTokens,
			Metadata:  cloneMetadata(state.Metadata),
		}

		res, err := r.callModel(ctx, state, req, ctrl, input.Stream, hasSuccessfulToolCall(state))
		if err != nil {
			return r.returnWithHandledError(ctx, input, state, "model", err, ctrl)
		}
		if res == nil {
			if result, err, handled := r.resolveControl(ctx, state, ctrl); handled {
				return r.finishRun(ctx, input, state, result, err)
			}
			return r.returnWithHandledError(ctx, input, state, "model", fmt.Errorf("model execution returned no result"), ctrl)
		}

		state.Step++
		// Providers report usage per model call; a run may use several calls for tools.
		state.Usage = state.Usage.Add(res.Usage)
		if result, err, handled := r.resolveControl(ctx, state, ctrl); handled {
			return r.finishRun(ctx, input, state, result, err)
		}

		if wrappingUp && res.FinishReason == FinishToolCalls {
			// The model ignored the wrap-up instruction: the turn still ends with
			// an answer instead of failing and losing the work done so far.
			return r.finishIgnoredWrapUp(ctx, input, state, req, res, wrapUp, maxSteps, ctrl)
		}

		switch res.FinishReason {
		case FinishToolCalls:
			if result, err, handled := r.executeToolCalls(ctx, state, tools, res.ToolCalls, leadOutput{text: res.Content, reasoning: res.Reasoning, meta: res.Metadata}); handled {
				return r.finishRun(ctx, input, state, result, err)
			}
			continue
		case FinishFinal:
			result, err := r.persistFinalAnswer(ctx, input, state, res)
			if err != nil {
				return nil, err
			}
			if wrappingUp && result != nil {
				result.WrapUp = wrapUp
			}
			if result != nil && res.Truncated {
				// The answer itself was cut off at the output limit (a cut-off
				// tool call instead is answered with an error and retried): report
				// it, with the limit that applied, so the caller can name it.
				result.Truncated = true
				result.OutputLimit = res.OutputLimit
				if result.OutputLimit <= 0 {
					result.OutputLimit = r.maxOutputTokens
				}
			}
			return r.finishRun(ctx, input, state, result, nil)
		default:
			return r.returnWithHandledError(ctx, input, state, "model", ErrInvalidFinishReason, ctrl)
		}
	}
}

func (r *DefaultRuntime) prepareState(ctx context.Context, input RunInput) (*RunState, bool, error) {
	runID := strings.TrimSpace(input.RunID)
	if runID == "" {
		runID = newRunID()
	}

	if strings.TrimSpace(input.AgentID) == "" {
		input.AgentID = "agentcore"
	}

	if strings.TrimSpace(input.RunID) == "" {
		return newRunState(runID, input), true, nil
	}

	state, err := LoadRunAuthority(ctx, r.storage, runID)
	if err != nil {
		if isStorageMiss(err) {
			return newRunState(runID, input), true, nil
		}
		return nil, false, err
	}
	if state == nil {
		return newRunState(runID, input), true, nil
	}

	if err := r.resumePolicy.AllowResume(ctx, input, state); err != nil {
		return nil, false, err
	}

	appendInstruction := r.resumePolicy.ShouldAppendInstruction(input, state)
	if state.AgentID == "" {
		state.AgentID = input.AgentID
	}
	if len(input.Metadata) > 0 {
		state.Metadata = r.resumePolicy.MergeMetadata(state.Metadata, input.Metadata)
	}

	return state, appendInstruction, nil
}

func (r *DefaultRuntime) runBeforeRun(ctx context.Context, event *RunEvent, ctrl *Control) error {
	if err := r.hooks.BeforeRun(ctx, event, ctrl); err != nil {
		return wrapHookError(err)
	}
	return nil
}

func (r *DefaultRuntime) runAfterRun(ctx context.Context, event *RunEvent, ctrl *Control) error {
	if err := r.hooks.AfterRun(ctx, event, ctrl); err != nil {
		return wrapHookError(err)
	}
	return nil
}

func (r *DefaultRuntime) buildPrompt(ctx context.Context, state *RunState, ctrl *Control) (string, error) {
	event := &PromptEvent{
		RunID:     state.RunID,
		AgentID:   state.AgentID,
		State:     state,
		System:    "",
		Metadata:  cloneMetadata(state.Metadata),
		Timestamp: time.Now(),
	}
	if err := r.hooks.BeforePromptBuild(ctx, event, ctrl); err != nil {
		return "", wrapHookError(err)
	}
	if ctrl != nil && (ctrl.ShouldYield() || ctrl.ShouldShortCircuit()) {
		return event.System, nil
	}
	systemPrompt, err := r.promptBuilder.BuildSystemPrompt(ctx, state)
	if err != nil {
		return "", err
	}
	event.System = systemPrompt
	return event.System, nil
}

func (r *DefaultRuntime) buildContext(ctx context.Context, state *RunState, ctrl *Control) ([]Message, error) {
	event := &ContextEvent{
		RunID:     state.RunID,
		AgentID:   state.AgentID,
		State:     state,
		Messages:  nil,
		Metadata:  cloneMetadata(state.Metadata),
		Timestamp: time.Now(),
	}
	if err := r.hooks.BeforeContextBuild(ctx, event, ctrl); err != nil {
		return nil, wrapHookError(err)
	}
	if ctrl != nil && (ctrl.ShouldYield() || ctrl.ShouldShortCircuit()) {
		return event.Messages, nil
	}
	builder := r.contextBuilder
	usingDefaultHistoryContext := false
	if builder == nil {
		builder = defaultContextBuilder(r.history)
		usingDefaultHistoryContext = true
	}
	messages, err := builder.BuildMessages(ctx, state)
	if err != nil {
		if usingDefaultHistoryContext {
			return nil, wrapHistoryError(err)
		}
		return nil, err
	}
	event.Messages = cloneMessagesValue(messages)
	return event.Messages, nil
}

func (r *DefaultRuntime) listTools(ctx context.Context) ([]ToolSchema, error) {
	tools, err := r.tools.ListTools(ctx)
	if err != nil {
		var toolErr *ToolError
		if errors.As(err, &toolErr) {
			return nil, err
		}
		return nil, &ToolError{Err: err}
	}
	return tools, nil
}

// emptyResultRetries is how many times callModel re-sends a request whose
// response was empty before failing the run.
const emptyResultRetries = 2

func (r *DefaultRuntime) callModel(ctx context.Context, state *RunState, req *GenerateRequest, ctrl *Control, stream StreamFunc, allowEmptyFinal bool) (*GenerateResult, error) {
	event := &ModelEvent{
		RunID:     state.RunID,
		AgentID:   state.AgentID,
		Request:   cloneGenerateRequestValue(req),
		Metadata:  cloneMetadata(state.Metadata),
		Timestamp: time.Now(),
	}
	if err := r.hooks.BeforeModel(ctx, event, ctrl); err != nil {
		return nil, wrapHookError(err)
	}
	if ctrl != nil && (ctrl.ShouldYield() || ctrl.ShouldShortCircuit()) {
		return nil, nil
	}

	// A response with neither text nor tool calls (seen with reasoning models
	// that spend the whole reply on reasoning) is re-sampled a few times before
	// the run fails: the same request usually yields a usable reply.
	var result *GenerateResult
	for attempt := 0; ; attempt++ {
		var err error
		attemptCtx := ctx
		if allowEmptyFinal && attempt >= emptyResultRetries {
			// Only the last resample is eligible; the observer can mark it only
			// when the provider actually returned an empty response.
			attemptCtx = agentcoreapi.WithEmptyFinalAttempt(ctx, true)
		}
		result, err = r.invokeProvider(attemptCtx, event.Request, stream)
		if err != nil {
			return nil, wrapProviderError(err)
		}
		err = result.Validate()
		if err == nil {
			break
		}
		if !errors.Is(err, ErrEmptyGenerateResult) || ctx.Err() != nil {
			return nil, wrapProviderError(err)
		}
		if attempt >= emptyResultRetries {
			if allowEmptyFinal && result != nil {
				if result.Metadata == nil {
					result.Metadata = map[string]any{}
				}
				result.Metadata[agentcoreapi.EmptyFinalMetaKey] = true
				result.FinishReason = FinishFinal
				break
			}
			return nil, wrapProviderError(err)
		}
	}

	event.Result = cloneGenerateResultValue(result)
	if err := r.hooks.AfterModel(ctx, event, ctrl); err != nil {
		return nil, wrapHookError(err)
	}
	result = event.Result
	return result, nil
}

func (r *DefaultRuntime) invokeProvider(ctx context.Context, req *GenerateRequest, stream StreamFunc) (*GenerateResult, error) {
	if stream != nil {
		if provider, ok := r.provider.(StreamingProvider); ok {
			return provider.Stream(ctx, req, func(ctx context.Context, event StreamEvent) error {
				return stream(ctx, cloneStreamEventValue(event))
			})
		}
	}

	return r.provider.Generate(ctx, req)
}

// hasSuccessfulToolCall uses the current run's persisted transcript rather than
// a separate counter: failed or refused tool results must not unlock this fallback.
func hasSuccessfulToolCall(state *RunState) bool {
	if state == nil {
		return false
	}
	for _, message := range state.Messages {
		if message.Role == "tool" && message.ToolResult != nil && !message.ToolResult.IsError {
			return true
		}
	}
	return false
}

func (r *DefaultRuntime) saveState(ctx context.Context, state *RunState, messages []Message) error {
	if err := r.storage.SaveRun(ctx, state); err != nil {
		return wrapStorageError(err)
	}
	if artifactStore, ok := r.storage.(ArtifactStorage); ok {
		if err := artifactStore.SaveArtifacts(ctx, state.RunID, state.Artifacts); err != nil {
			return wrapStorageError(err)
		}
	}
	if len(messages) == 0 {
		return nil
	}
	if err := r.storage.AppendMessages(ctx, state.RunID, messages...); err != nil {
		return wrapStorageError(err)
	}
	if err := r.history.ObserveMessages(ctx, cloneRunStateValue(state), cloneMessagesValue(messages)...); err != nil {
		return wrapHistoryError(err)
	}
	return nil
}

func (r *DefaultRuntime) effectiveMaxSteps(input RunInput) int {
	if input.MaxSteps > 0 {
		return input.MaxSteps
	}
	return r.maxSteps
}

// leadOutput is the model output accompanying a batch of tool calls; text,
// reasoning and the result's metadata (e.g. a provider's signed thinking
// blocks) are stored on the first assistant tool-call message only, batch
// (api.ToolBatchMetaKey, set when the reply holds several calls) on each.
type leadOutput struct {
	text, reasoning, batch string
	meta                   map[string]any
}

func (r *DefaultRuntime) executeToolCalls(ctx context.Context, state *RunState, schemas []ToolSchema, calls []ToolCall, lead leadOutput) (*RunResult, error, bool) {
	prepareToolCalls(schemas, calls)
	if err := r.toolPolicy.ValidateToolCalls(ctx, state, cloneToolCallsValue(calls)); err != nil {
		return r.handleErrorResult(ctx, state, "max_tool_calls", err, NewControl())
	}
	if r.prefetchTools != nil && len(calls) > 1 {
		runnable := make([]ToolCall, 0, len(calls))
		for _, call := range calls {
			if call.ArgumentsError == "" {
				runnable = append(runnable, call)
			}
		}
		r.prefetchTools(ctx, cloneToolCallsValue(runnable))
	}

	if len(calls) > 1 {
		lead.batch = calls[0].CallID
		if lead.batch == "" {
			lead.batch = fmt.Sprintf("step-%d", state.Step)
		}
	}
	for i, call := range calls {
		// The model's text and reasoning accompanying the tool calls are kept on
		// the first assistant tool-call message only; parallel calls carry no duplicate.
		callLead := leadOutput{batch: lead.batch}
		if i == 0 {
			callLead = lead
		}
		result, err, handled := r.executeSingleTool(ctx, state, call, callLead)
		if handled {
			return result, err, true
		}
	}
	return nil, nil, false
}

func (r *DefaultRuntime) executeSingleTool(ctx context.Context, state *RunState, call ToolCall, lead leadOutput) (*RunResult, error, bool) {
	if call.ArgumentsError != "" {
		// The provider could not decode the arguments: nothing runs (no hooks,
		// no approval), the model gets the decode error and can resend the call.
		result := &ToolResult{CallID: call.CallID, Name: call.Name, IsError: true, Content: fmt.Sprintf(
			"error: this %s call was not run: %s", call.Name, call.ArgumentsError)}
		if result, err, handled := r.persistToolTranscript(ctx, state, call, result, lead); handled {
			return result, err, true
		}
		return nil, nil, false
	}
	ctrl := NewControl()
	event := &ToolEvent{
		RunID:     state.RunID,
		AgentID:   state.AgentID,
		Call:      cloneToolCallValue(&call),
		Metadata:  cloneMetadata(state.Metadata),
		Timestamp: time.Now(),
	}
	if err := r.hooks.BeforeTool(ctx, event, ctrl); err != nil {
		return r.handleErrorResult(ctx, state, "before_tool", wrapHookError(err), ctrl)
	}
	if result, err, handled := r.resolveControl(ctx, state, ctrl); handled {
		return result, err, true
	}

	activeCall := call
	if event.Call != nil {
		activeCall = *cloneToolCallValue(event.Call)
	}

	result, err := r.tools.ExecuteTool(ctx, activeCall)
	if err != nil {
		decision := r.toolPolicy.HandleToolError(ctx, state, activeCall, err)
		if decision.Fail != nil {
			return r.handleErrorResult(ctx, state, "tool", decision.Fail, NewControl())
		}
		result = normalizeToolResult(activeCall, decision.Result)
		// A policy that turns the failure into a result must still flag it as an error.
		result.IsError = true
	} else {
		result = normalizeToolResult(activeCall, result)
	}

	event.Call = cloneToolCallValue(&activeCall)
	event.Result = cloneToolResultValue(result)
	if err := r.hooks.AfterTool(ctx, event, ctrl); err != nil {
		finalCall := activeCall
		if event.Call != nil {
			finalCall = *cloneToolCallValue(event.Call)
		}
		finalResult := normalizeToolResult(finalCall, event.Result)
		if result, persistErr, handled := r.persistToolTranscript(ctx, state, finalCall, finalResult, lead); handled {
			return result, persistErr, true
		}
		return r.handleErrorResult(ctx, state, "after_tool", wrapHookError(err), NewControl())
	}
	finalCall := activeCall
	if event.Call != nil {
		finalCall = *cloneToolCallValue(event.Call)
	}
	finalResult := normalizeToolResult(finalCall, event.Result)
	if ctrl.ShouldYield() || ctrl.ShouldShortCircuit() {
		if result, persistErr, handled := r.persistToolTranscript(ctx, state, finalCall, finalResult, lead); handled {
			return result, persistErr, true
		}
		return r.handleErrorResult(ctx, state, "after_tool", wrapHookError(errors.New("after_tool control is not supported")), NewControl())
	}

	if result, err, handled := r.persistToolTranscript(ctx, state, finalCall, finalResult, lead); handled {
		return result, err, true
	}
	return nil, nil, false
}

func (r *DefaultRuntime) persistToolTranscript(ctx context.Context, state *RunState, call ToolCall, result *ToolResult, lead leadOutput) (*RunResult, error, bool) {
	assistantMessage := Message{
		Role:      "assistant",
		Content:   lead.text,
		ToolCall:  cloneToolCallValue(&call),
		Reasoning: lead.reasoning,
	}
	if len(lead.meta) > 0 || lead.batch != "" {
		assistantMessage.Metadata = cloneMetadata(lead.meta)
		if assistantMessage.Metadata == nil {
			assistantMessage.Metadata = map[string]any{}
		}
		if lead.batch != "" {
			assistantMessage.Metadata[ToolBatchMetaKey] = lead.batch
		}
	}
	toolMessage := Message{
		Role:       "tool",
		Content:    result.Content,
		ToolCall:   cloneToolCallValue(&call),
		ToolResult: cloneToolResultValue(result),
		// Images mirrors the result so a reader of either field sees them; the
		// stored message keeps one copy (ToolResult is written once).
		Images: cloneImagesValue(result.Images),
	}
	state.Messages = append(state.Messages, assistantMessage, toolMessage)
	if len(result.Artifacts) > 0 {
		state.Artifacts = append(state.Artifacts, result.Artifacts...)
	}

	if err := r.saveState(ctx, state, []Message{assistantMessage, toolMessage}); err != nil {
		return nil, r.handleNonRecoverableError(ctx, state, "persist_tool_output", err), true
	}
	return nil, nil, false
}

func (r *DefaultRuntime) handleErrorResult(ctx context.Context, state *RunState, stage string, err error, ctrl *Control) (*RunResult, error, bool) {
	handledErr := r.handleError(ctx, state, stage, err, ctrl)
	var resolved runtimeResolvedControlError
	if errors.As(handledErr, &resolved) {
		return resolved.result, nil, true
	}
	return nil, handledErr, true
}

func (r *DefaultRuntime) handleNonRecoverableError(ctx context.Context, state *RunState, stage string, err error) error {
	if err == nil {
		return nil
	}

	wrapped := classifyRuntimeError(err)
	if persistErr := r.persistFailureState(ctx, state, wrapped); persistErr != nil {
		wrapped = persistErr
	}
	event := &ErrorEvent{
		RunID:     state.RunID,
		AgentID:   state.AgentID,
		Stage:     stage,
		Err:       wrapped,
		Metadata:  cloneMetadata(state.Metadata),
		Timestamp: time.Now(),
	}
	_ = r.hooks.OnErrorObserved(ctx, event, NewControl())
	return wrapped
}

func (r *DefaultRuntime) resolveControl(ctx context.Context, state *RunState, ctrl *Control) (*RunResult, error, bool) {
	if ctrl == nil {
		return nil, nil, false
	}
	if ctrl.ShouldYield() {
		result, err := r.persistYield(ctx, state, ctrl)
		return result, err, true
	}
	if ctrl.ShouldShortCircuit() {
		result, err := r.persistShortCircuit(ctx, state, ctrl)
		return result, err, true
	}
	return nil, nil, false
}

func (r *DefaultRuntime) handleError(ctx context.Context, state *RunState, stage string, err error, ctrl *Control) error {
	if err == nil {
		return nil
	}

	wrapped := classifyRuntimeError(err)
	if persistErr := r.persistFailureState(ctx, state, wrapped); persistErr != nil {
		wrapped = persistErr
	}
	event := &ErrorEvent{
		RunID:     state.RunID,
		AgentID:   state.AgentID,
		Stage:     stage,
		Err:       wrapped,
		Metadata:  cloneMetadata(state.Metadata),
		Timestamp: time.Now(),
	}
	if decision := r.errorPolicy.HandleRecoverable(ctx, state, event); decision.Result != nil || decision.Fail != nil {
		if decision.Fail != nil {
			wrapped = classifyRuntimeError(decision.Fail)
			if persistErr := r.persistFailureState(ctx, state, wrapped); persistErr != nil {
				wrapped = persistErr
			}
			return wrapped
		}

		replacement := cloneRunResultValue(decision.Result)
		if replacement == nil {
			replacement = &RunResult{}
		}
		policyCtrl := NewControl()
		policyCtrl.ShortCircuit(replacement)
		if result, resolveErr := r.persistShortCircuit(ctx, state, policyCtrl); resolveErr != nil {
			return resolveErr
		} else if result != nil {
			return runtimeResolvedControlError{result: result}
		}
	}
	if hookErr := r.hooks.OnError(ctx, event, ctrl); hookErr != nil {
		return wrapHookError(hookErr)
	}
	if result, resolveErr, handled := r.resolveControl(ctx, state, ctrl); handled {
		if resolveErr != nil {
			return resolveErr
		}
		return runtimeResolvedControlError{result: result}
	}
	return wrapped
}

func (r *DefaultRuntime) persistYield(ctx context.Context, state *RunState, ctrl *Control) (*RunResult, error) {
	state.Status = RunYielded
	state.LastError = ""

	if err := r.saveState(ctx, state, nil); err != nil {
		return nil, err
	}

	return &RunResult{
		RunID:  state.RunID,
		Status: RunYielded,
		Output: "",
		Steps:  state.Step,
		Usage:  state.Usage,
		Yield: &Yield{
			Reason:   ctrl.YieldReason,
			ResumeOK: true,
			Metadata: cloneMetadata(ctrl.YieldMetadata),
		},
	}, nil
}

func (r *DefaultRuntime) persistShortCircuit(ctx context.Context, state *RunState, ctrl *Control) (*RunResult, error) {
	replacement, ok := ctrl.Replacement.(*RunResult)
	if !ok || replacement == nil {
		return nil, r.handleError(ctx, state, "short_circuit", fmt.Errorf("short circuit replacement must be *RunResult, got %T", ctrl.Replacement), ctrl)
	}

	result := *replacement
	if result.RunID == "" {
		result.RunID = state.RunID
	}
	if result.Status == "" {
		result.Status = RunCompleted
	}
	if result.Steps == 0 {
		result.Steps = state.Step
	}
	state.Usage = result.Usage
	state.Step = result.Steps

	var newMessages []Message
	switch result.Status {
	case RunYielded:
		state.Status = RunYielded
		state.LastError = ""
		if result.Yield == nil {
			result.Yield = &Yield{ResumeOK: true}
		}
	case RunFailed:
		state.Status = RunFailed
		state.LastError = result.Output
	case RunCompleted:
		state.Status = RunCompleted
		state.LastError = ""
		preMessageState := cloneRunStateValue(state)
		if result.Output != "" {
			assistantMessage := Message{Role: "assistant", Content: result.Output}
			state.Messages = append(state.Messages, assistantMessage)
			newMessages = []Message{assistantMessage}
		}
		if err := r.saveState(ctx, state, newMessages); err != nil {
			handledErr := r.handleError(ctx, stateForSaveStateFailure(preMessageState, state, err), "short_circuit", err, NewControl())
			var resolved runtimeResolvedControlError
			if errors.As(handledErr, &resolved) {
				return resolved.result, nil
			}
			return nil, handledErr
		}
	default:
		state.Status = result.Status
		state.LastError = ""
	}

	if result.Status != RunCompleted {
		if err := r.saveState(ctx, state, newMessages); err != nil {
			return nil, err
		}
	}
	return &result, nil
}

func (r *DefaultRuntime) persistFailureState(ctx context.Context, state *RunState, err error) error {
	if state == nil || r == nil || r.storage == nil {
		return err
	}
	state.Status = RunFailed
	state.LastError = err.Error()
	if saveErr := r.storage.SaveRun(ctx, state); saveErr != nil {
		if errors.As(err, new(*StorageError)) {
			return err
		}
		return wrapStorageError(saveErr)
	}
	return err
}
