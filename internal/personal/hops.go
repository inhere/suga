package personal

import "context"

// The automatic-reply depth of a turn (design session_message D3): how many
// session messages a turn is away from the person. A turn the person started
// (a typed line, a `suga send` on the command line, a hook) has 0; a turn a
// session message with hops h started has h, and every message its tools send
// carries h+1, so two sessions cannot answer each other without end.

// withTurnHops records the depth of the turn ctx belongs to.
func withTurnHops(ctx context.Context, hops int) context.Context {
	return context.WithValue(ctx, turnHopsKey{}, hops)
}

// TurnHops is the depth of the turn ctx belongs to (0 outside a turn, and for
// every turn the person started).
func TurnHops(ctx context.Context) int {
	hops, _ := ctx.Value(turnHopsKey{}).(int)
	return hops
}

type turnHopsKey struct{}
