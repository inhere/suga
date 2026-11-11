package api

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestTurnPromptRoundTrip(t *testing.T) {
	t.Run("unset is empty", func(t *testing.T) {
		assert.Eq(t, "", TurnPromptFrom(context.Background()))
	})
	t.Run("set and read back", func(t *testing.T) {
		ctx := WithTurnPrompt(context.Background(), "fix the build")
		assert.Eq(t, "fix the build", TurnPromptFrom(ctx))
	})
	t.Run("child overrides parent", func(t *testing.T) {
		ctx := WithTurnPrompt(WithTurnPrompt(context.Background(), "a"), "b")
		assert.Eq(t, "b", TurnPromptFrom(ctx))
	})
}
