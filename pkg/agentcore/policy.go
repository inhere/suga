package agentcore

import (
	"context"
	"fmt"
	"strings"
)

// DefaultErrorPolicy preserves the built-in v0 recoverable error semantics.
type DefaultErrorPolicy struct{}

func (DefaultErrorPolicy) HandleRecoverable(_ context.Context, _ *RunState, _ *ErrorEvent) ErrorDecision {
	return ErrorDecision{}
}

// DefaultResumePolicy preserves the built-in v0 resume semantics.
type DefaultResumePolicy struct{}

func (DefaultResumePolicy) AllowResume(_ context.Context, _ RunInput, state *RunState) error {
	if state == nil {
		return nil
	}
	if state.Status == RunCompleted || state.Status == RunFailed {
		return fmt.Errorf("%w: status=%s", ErrRunNotResumable, state.Status)
	}
	return nil
}

func (DefaultResumePolicy) ShouldAppendInstruction(input RunInput, _ *RunState) bool {
	switch input.ResumeMode {
	case ResumeFromState:
		return false
	case ResumeWithInput:
		return true
	case ResumeAuto, "":
		return strings.TrimSpace(input.Instruction) != "" || len(input.Images) > 0
	default:
		return strings.TrimSpace(input.Instruction) != "" || len(input.Images) > 0
	}
}

func (DefaultResumePolicy) MergeMetadata(stateMetadata, inputMetadata map[string]any) map[string]any {
	return mergeMetadata(stateMetadata, inputMetadata)
}

// DefaultToolExecutionPolicy preserves the built-in v0 tool execution semantics.
type DefaultToolExecutionPolicy struct {
	MaxCallsPerStep int
}

func (p DefaultToolExecutionPolicy) ValidateToolCalls(_ context.Context, _ *RunState, calls []ToolCall) error {
	if p.MaxCallsPerStep > 0 && len(calls) > p.MaxCallsPerStep {
		return fmt.Errorf("max tool calls per step exceeded: %d > %d", len(calls), p.MaxCallsPerStep)
	}
	return nil
}

func (DefaultToolExecutionPolicy) HandleToolError(_ context.Context, _ *RunState, call ToolCall, err error) ToolErrorDecision {
	return ToolErrorDecision{
		Result: toolErrorAsResult(call, err),
	}
}
