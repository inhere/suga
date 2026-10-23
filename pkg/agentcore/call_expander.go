package agentcore

import (
	"context"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// CallExpander is implemented by a tool registry that expands a proxy tool call
// (tool_call, the segment apply mode of deferred tools) into the real tool call
// before execution. The runtime expands at its outermost point, so the runtime
// hooks, the approval layer, the PreToolUse hook, taint handling and the trace
// all see the real tool name: a proxied call behaves exactly like a direct one.
type CallExpander interface {
	ExpandCall(ctx context.Context, call api.ToolCall) (api.ToolCall, bool)
}
