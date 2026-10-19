package api

import "context"

// ToolCall is a model-requested invocation of a named tool.
type ToolCall struct {
	CallID    string
	Name      string
	Arguments map[string]any
	// ArgumentsError is set when the provider could not decode the arguments
	// the model sent (Arguments is then empty). The runtime does not run such
	// a call: it answers it with an error result so the model can resend it.
	ArgumentsError string
}

// ToolResult is the public result returned after executing a tool call.
type ToolResult struct {
	CallID    string
	Name      string
	Content   string
	Artifacts []Artifact
	// IsError marks a failed or refused tool call; providers forward it to
	// the model (e.g. Claude tool_result is_error).
	IsError  bool
	Metadata map[string]any
	// Images are the pictures this result returns (e.g. view_image); providers
	// that cannot carry them in a tool result send them in a following user
	// message (see the llm package).
	Images []ImagePart `json:",omitempty"`
}

// ToolSchema describes a callable tool in a provider-neutral way.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Tool exposes a concrete executable tool.
type Tool interface {
	Name() string
	Description() string
	Schema() ToolSchema
	Execute(ctx context.Context, call ToolCall) (*ToolResult, error)
}

// ToolRegistry exposes lookup and execution contracts for tools.
type ToolRegistry interface {
	ListTools(ctx context.Context) ([]ToolSchema, error)
	ExecuteTool(ctx context.Context, call ToolCall) (*ToolResult, error)
}
