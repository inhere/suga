// Package tools provides the tool registry and execution framework.
package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// Kind classifies the runtime type of a tool.
type Kind string

const (
	KindNative Kind = "native"
	KindMCP    Kind = "mcp"
)

// ErrDenied is the sentinel for a tool call refused by a permission layer
// (approval gate, read-only mode ...). The observe layer uses it to tell a
// denial from an ordinary tool failure.
var ErrDenied = errors.New("tool call denied")

// deniedError carries the user-facing message while matching ErrDenied.
type deniedError struct{ msg string }

func (e *deniedError) Error() string { return e.msg }
func (e *deniedError) Unwrap() error { return ErrDenied }

// Denied returns an error that prints as msg and satisfies errors.Is(err, ErrDenied).
func Denied(msg string) error { return &deniedError{msg: msg} }

// Kinded is implemented by tools that report their runtime kind. It is
// optional: a tool without it is native (see KindOf).
type Kinded interface {
	Kind() Kind
}

// KindOf returns the runtime kind of t: its Kind() when it implements Kinded,
// KindNative otherwise (including a nil tool).
func KindOf(t api.Tool) Kind {
	if k, ok := t.(Kinded); ok {
		return k.Kind()
	}
	return KindNative
}

// Lookup is the query capability wrapping registry layers (observe, visibility,
// approval) pass through so permission code can still resolve a tool's Kind and
// MCP server name behind them.
type Lookup interface {
	Get(name string) (api.Tool, error)
}

// Registry holds all registered tools and dispatches calls.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]api.Tool
	// prefetched are the calls of the current reply run ahead, see Prefetch.
	prefetched prefetchState
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]api.Tool)}
}

// Register adds a tool to the registry. Panics on duplicate name.
func (r *Registry) Register(t api.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		panic(fmt.Sprintf("tool %q already registered", t.Name()))
	}
	r.tools[t.Name()] = t
}

// TryRegister adds a tool unless the name is already taken; it never overwrites
// and reports whether the tool was added. Earlier registrations win.
func (r *Registry) TryRegister(t api.Tool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		return false
	}
	r.tools[t.Name()] = t
	return true
}

// MCPServerNamer is implemented by MCP-backed tools so permission checks know
// which configured server a tool came from.
type MCPServerNamer interface {
	MCPServer() string
}

// MustRegister is like Register but does not panic — it overwrites.
func (r *Registry) MustRegister(t api.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Remove drops the tool with the given name; an unregistered name is ignored.
// The host uses it for a policy the model must not even see (a disabled tool is
// missing from Schemas and from ToolSearch, not merely refused on call).
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Get returns the tool with the given name, or an error if not found.
func (r *Registry) Get(name string) (api.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	return t, nil
}

// Schemas returns the ToolSchema list for all registered tools in deterministic order.
func (r *Registry) Schemas() []api.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.tools) == 0 {
		return nil
	}

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	schemas := make([]api.ToolSchema, 0, len(names))
	for _, name := range names {
		schema := r.tools[name].Schema()
		if schema.Name == "" {
			schema.Name = name
		}
		if schema.Description == "" {
			schema.Description = r.tools[name].Description()
		}
		schemas = append(schemas, schema)
	}
	return schemas
}

// ListTools exposes registered schemas through the agentcore tool-registry contract.
func (r *Registry) ListTools(context.Context) ([]api.ToolSchema, error) {
	return r.Schemas(), nil
}

// ExecuteTool dispatches a public tool call to a registered tool.
func (r *Registry) ExecuteTool(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	if p := r.takePrefetched(call); p != nil {
		select {
		case <-p.done:
			return p.res, p.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.execute(ctx, call)
}

func (r *Registry) execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	t, err := r.Get(call.Name)
	if err != nil {
		return nil, err
	}

	result, err := t.Execute(ctx, cloneToolCall(call))
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = &api.ToolResult{}
	}
	if result.CallID == "" {
		result.CallID = call.CallID
	}
	if result.Name == "" {
		result.Name = call.Name
	}
	return result, nil
}

// List returns the names of all registered tools.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

func cloneToolCall(call api.ToolCall) api.ToolCall {
	call.Arguments = cloneMap(call.Arguments)
	return call
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
