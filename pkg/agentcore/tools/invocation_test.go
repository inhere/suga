package tools

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestInvocationID(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		ctx := WithInvocationID(context.Background(), "turn-1/call-1")
		assert.Eq(t, "turn-1/call-1", InvocationID(ctx))
	})
	t.Run("without an id", func(t *testing.T) {
		assert.Eq(t, "", InvocationID(context.Background()))
	})
	t.Run("nested context keeps the id", func(t *testing.T) {
		ctx := WithInvocationID(context.Background(), "turn-1/call-1")
		assert.Eq(t, "turn-1/call-1", InvocationID(context.WithValue(ctx, struct{}{}, 1)))
	})
}
