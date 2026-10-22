package tools

import "context"

// invocationKey is the context key of one tool invocation's id.
type invocationKey struct{}

// WithInvocationID returns a context carrying id as the id of the tool call
// that is about to run. The runtime sets it once per call, before the
// BeforeTool hooks, so a hook, the tool itself and the AfterTool hooks all read
// the same value (InvocationID). A host's in-process tool (wiring.Options.
// ExtraTools) reads it the same way and may use it as the idempotency key of
// its own side-effect ledger (see pkg/sugasdk).
func WithInvocationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, invocationKey{}, id)
}

// InvocationID returns the id of the tool invocation running with ctx, or ""
// when ctx carries none (outside a tool call). The id is stable for the whole
// call - hooks before and after the tool, the tool itself - and unique within
// a session: the runtime builds it from the run id and the provider's call id.
func InvocationID(ctx context.Context) string {
	id, _ := ctx.Value(invocationKey{}).(string)
	return id
}
