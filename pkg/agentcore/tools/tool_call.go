package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// ToolCallProxy is the always-registered proxy tool of the segment apply mode:
// it lets the model call a deferred native tool that is already loaded while
// the tool table stays unchanged (design Q9, provider probe 2026-10-23).
//
// The runtime expands a proxy call into the real tool call before execution
// (agentcore.CallExpander), so approval, the PreToolUse hook, taint handling and
// the trace all see the real tool name. Execute keeps the same path as a
// fallback for a caller that does not expand: it runs the real tool through the
// inner layers, never around them.
type ToolCallProxy struct {
	vis *Visibility
}

// CallExpander is a tool layer that expands a proxy tool call (tool_call) into
// the real tool call. It is structural (the runtime asserts it on the outermost
// registry), so every wrapper between the runtime and Visibility has to forward
// it, otherwise a proxied call would reach approval/repo layers as tool_call.
type CallExpander interface {
	ExpandCall(ctx context.Context, call api.ToolCall) (api.ToolCall, bool)
}

// NewToolCallProxy creates tool_call backed by v.
func NewToolCallProxy(v *Visibility) *ToolCallProxy { return &ToolCallProxy{vis: v} }

func (t *ToolCallProxy) Name() string { return ToolCallName }

func (t *ToolCallProxy) Description() string {
	return "Call a deferred tool listed in the session context by its name, within this segment. " +
		"The tool must be loaded first with tool_search. Tools join the tool table directly " +
		"after the next segment."
}

// ToolMeta marks the proxy as read-only: the real tool is approved under its own
// name and meta by the execution layers, so the proxy itself must not ask.
func (*ToolCallProxy) ToolMeta() Meta {
	return Meta{Category: CategorySearch, ReadOnly: true}
}

func (t *ToolCallProxy) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":      map[string]any{"type": "string", "description": "Deferred tool name"},
				"arguments": map[string]any{"type": "object", "description": "The tool's own arguments"},
			},
			"required": []any{"name", "arguments"},
		},
	}
}

// Execute expands the proxy call and runs the real tool through the inner
// layers. An unknown or not-yet-loaded target is an error result listing the
// deferred tools, so the model can correct itself in the next step.
func (t *ToolCallProxy) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	if real, ok := t.vis.ExpandCall(ctx, call); ok {
		res, err := t.vis.ExecuteTool(ctx, real)
		if res != nil && res.Name == "" {
			res.Name = real.Name
		}
		return res, err
	}
	name, _ := call.Arguments["name"].(string)
	return nil, fmt.Errorf("tool_call cannot call %q: it needs a deferred tool name that is loaded already "+
		"(load it with tool_search first, then call it again)\nDeferred tools: %s",
		strings.TrimSpace(name), strings.Join(t.deferredNames(), ", "))
}

// deferredNames lists the deferred tool names for the error text, sorted.
func (t *ToolCallProxy) deferredNames() []string {
	if t.vis == nil {
		return nil
	}
	out := append([]string(nil), t.vis.names...)
	sort.Strings(out)
	return out
}
