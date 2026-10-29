package api

import "context"

// sessionIDKey is the context key of a session id; unexported so only
// WithSessionID can set the value.
type sessionIDKey struct{}

// turnIDKey is the context key of a turn id; unexported so only WithTurnID can
// set the value.
type turnIDKey struct{}

// WithSessionID marks ctx with the agent session the call belongs to. A
// session id is a runtime concept (who is talking), not a provider one, so it
// lives in the contract package and the layers on top read it from here:
//
//   - llm: the value of llm.SessionIDPlaceholder ("{{session_id}}") in
//     llm.headers and the per-conversation cache key of a request.
//   - tasks: the default session of a call when tasks.Config.Session is nil.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}

// SessionIDFrom returns the session id set by WithSessionID, or "".
func SessionIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(sessionIDKey{}).(string)
	return id
}

// WithTurnID marks ctx with the agent turn the call belongs to. llm.headers
// expands it as "{{turn_id}}" (a gateway tracing one turn of a long session).
func WithTurnID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, turnIDKey{}, id)
}

// TurnIDFrom returns the turn id set by WithTurnID, or "".
func TurnIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(turnIDKey{}).(string)
	return id
}
