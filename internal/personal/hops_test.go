package personal

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestTurnHops: the depth of a turn travels in ctx, and everything outside a turn
// (or a turn the person started) reads 0.
func TestTurnHops(t *testing.T) {
	assert.Eq(t, 0, TurnHops(context.Background()))
	assert.Eq(t, 0, TurnHops(WithTurnHops(context.Background(), 0)))
	assert.Eq(t, 2, TurnHops(WithTurnHops(context.Background(), 2)))
	// A nested child (a sub-agent's ctx) keeps the depth it inherited.
	parent := WithTurnHops(context.Background(), 3)
	child, cancel := context.WithCancel(parent)
	defer cancel()
	assert.Eq(t, 3, TurnHops(child))
}
