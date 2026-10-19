package agentcore

import (
	"context"

	"github.com/inhere/suga/pkg/agentcore/api"
	internalutils "github.com/inhere/suga/pkg/agentcore/internal/utils"
)

type ContextBuilder = api.ContextBuilder
type PromptBuilder = api.PromptBuilder
type ResumePolicy = api.ResumePolicy
type ToolExecutionPolicy = api.ToolExecutionPolicy
type ToolErrorDecision = api.ToolErrorDecision
type ErrorPolicy = api.ErrorPolicy
type ErrorDecision = api.ErrorDecision

type RunInput = api.RunInput
type RunStatus = api.RunStatus

const (
	RunIdle      = api.RunIdle
	RunRunning   = api.RunRunning
	RunYielded   = api.RunYielded
	RunCompleted = api.RunCompleted
	RunFailed    = api.RunFailed
)

type ResumeMode = api.ResumeMode

const (
	ResumeAuto      = api.ResumeAuto
	ResumeFromState = api.ResumeFromState
	ResumeWithInput = api.ResumeWithInput
)

type Message = api.Message

// ImagePart is one image part of a message (see api.ImagePart): the bytes live
// in memory only and the store keeps the reference.
type ImagePart = api.ImagePart

// ToolBatchMetaKey marks the tool calls of one reply, see api.ToolBatchMetaKey.
const ToolBatchMetaKey = api.ToolBatchMetaKey

type Artifact = api.Artifact
type Usage = api.Usage
type Yield = api.Yield
type RunState = api.RunState
type RunResult = api.RunResult

// ---------------- Tools ----------------

type ToolCall = api.ToolCall
type ToolResult = api.ToolResult
type ToolSchema = api.ToolSchema
type Tool = api.Tool
type ToolRegistry = api.ToolRegistry

// ---------------- Provider ----------------

type Provider = api.Provider
type StreamEventKind = api.StreamEventKind

const (
	StreamEventTextDelta   = api.StreamEventTextDelta
	StreamEventMessageStop = api.StreamEventMessageStop
	StreamEventRetry       = api.StreamEventRetry
)

type StreamEvent = api.StreamEvent
type StreamFunc = api.StreamFunc
type StreamingProvider = api.StreamingProvider
type ToolChooser = api.ToolChooser
type GenerateRequest = api.GenerateRequest
type GenerateResult = api.GenerateResult
type FinishReason = api.FinishReason

// ToolChoiceNone forbids tool calls (GenerateRequest.ToolChoice), see
// api.ToolChoiceNone.
const ToolChoiceNone = api.ToolChoiceNone

// Usage metadata keys are re-exported for callers using the root agentcore API.
const (
	UsageEstimatedMetaKey        = api.UsageEstimatedMetaKey
	EstimatedInputTokensMetaKey  = api.EstimatedInputTokensMetaKey
	EstimatedOutputTokensMetaKey = api.EstimatedOutputTokensMetaKey
	EmptyFinalMetaKey            = api.EmptyFinalMetaKey
)

const (
	FinishFinal     = api.FinishFinal
	FinishToolCalls = api.FinishToolCalls
)

// ---------------- History ----------------

type History = api.History

type noopHistory struct{}

func (noopHistory) Hydrate(context.Context, *RunState) error { return nil }

func (noopHistory) ObserveMessages(context.Context, *RunState, ...Message) error { return nil }

func (noopHistory) BuildContext(_ context.Context, state *RunState) ([]Message, error) {
	if state == nil || len(state.Messages) == 0 {
		return nil, nil
	}
	return cloneMessagesValue(state.Messages), nil
}

// ---------------- Storage ----------------

type Storage = api.Storage
type ArtifactStorage = api.ArtifactStorage

// ---------------- Hook ----------------

type Hook = api.Hook
type NopHook = api.NopHook
type Control = api.Control
type RunEvent = api.RunEvent
type PromptEvent = api.PromptEvent
type ContextEvent = api.ContextEvent
type ModelEvent = api.ModelEvent
type ToolEvent = api.ToolEvent
type ErrorEvent = api.ErrorEvent

func NewControl() *Control {
	return api.NewControl()
}

func cloneMetadata(src map[string]any) map[string]any {
	return internalutils.CloneMetadata(src)
}
