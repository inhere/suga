package output

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
)

func TestContextShare(t *testing.T) {
	for _, tc := range []struct {
		name              string
		lastInput, window int
		want              string
	}{
		{name: "rounds down to zero", lastInput: 9, window: 1000, want: "ctx 0%"},
		{name: "reports an oversized request", lastInput: 1050, window: 1000, want: "ctx 105%"},
		{name: "unknown window is omitted", lastInput: 550, window: 0, want: ""},
		{name: "no request is omitted", lastInput: 0, window: 1000, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, ContextShare(tc.lastInput, tc.window))
		})
	}
}

func TestWithContext(t *testing.T) {
	t.Run("adds used window and share", func(t *testing.T) {
		line := ResultLineWithContext("s", personal.RunResult{LastInputTokens: 550}, 1000, nil)
		assert.Eq(t, map[string]any{"used": 550, "window": 1000, "share": 55}, line["context"])
	})
	t.Run("adds the estimate and its ratio", func(t *testing.T) {
		occ := &personal.ContextOccupancy{EstTokens: 12345, Window: 200000}
		line := ResultLineWithContext("s", personal.RunResult{LastInputTokens: 12000}, 0, occ)
		assert.Eq(t, map[string]any{
			"used": 12000, "window": 200000, "share": 6,
			"est_tokens": 12345, "ratio": 0.062,
		}, line["context"])
	})
	t.Run("reports a full context", func(t *testing.T) {
		occ := &personal.ContextOccupancy{EstTokens: 500, Window: 1000}
		line := ResultLineWithContext("s", personal.RunResult{}, 0, occ)
		assert.Eq(t, map[string]any{"window": 1000, "est_tokens": 500, "ratio": 0.5}, line["context"])
	})
	t.Run("omits unknown context", func(t *testing.T) {
		line := WithContext(map[string]any{}, personal.RunResult{LastInputTokens: 550}, 0, nil)
		_, ok := line["context"]
		assert.False(t, ok)
	})
	t.Run("keeps the window of the occupancy", func(t *testing.T) {
		// The trace event's window is the compact window; it wins over the model
		// setting so ratio and window never disagree.
		occ := &personal.ContextOccupancy{EstTokens: 100, Window: 8000}
		line := WithContext(map[string]any{}, personal.RunResult{LastInputTokens: 100}, 0, occ)
		assert.Eq(t, map[string]any{"used": 100, "window": 8000, "share": 1, "est_tokens": 100, "ratio": 0.013}, line["context"])
	})
}
