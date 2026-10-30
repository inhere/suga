package output

import (
	"fmt"
	"math"

	"github.com/inhere/suga/internal/personal"
)

// ContextUsage returns the last request's context size, model window and integer
// percentage. The result is absent when either input is unknown; callers should
// omit context information in that case.
func ContextUsage(lastInputTokens, contextWindow int) (used, window, share int, ok bool) {
	if contextWindow <= 0 || lastInputTokens <= 0 {
		return 0, 0, 0, false
	}
	return lastInputTokens, contextWindow, lastInputTokens * 100 / contextWindow, true
}

// ContextShare renders the last model request's context usage as "ctx 55%".
// It is empty when the model window or the request size is unknown. The ratio is
// intentionally not clamped, so an oversized request remains visible as >100%.
func ContextShare(lastInputTokens, contextWindow int) string {
	_, _, share, ok := ContextUsage(lastInputTokens, contextWindow)
	if !ok {
		return ""
	}
	return fmt.Sprintf("ctx %d%%", share)
}

// WithContext adds the optional context usage object to a stream-json line.
// used is the latest model request's input token count, including cache tokens;
// window is the effective model context window and share is its integer percent.
// occupancy is the turn's own estimate of that request (the figure the context
// trace event carries): when it is present, its window wins and est_tokens plus
// the 3-decimal ratio are added, so a caller can compute how full the context is
// without the estimate being a second, differently rounded figure.
func WithContext(line map[string]any, result personal.RunResult, contextWindow int, occupancy *personal.ContextOccupancy) map[string]any {
	if occupancy != nil && occupancy.Window > 0 {
		contextWindow = occupancy.Window
	}
	used, _, share, ok := ContextUsage(result.LastInputTokens, contextWindow)
	if !ok && occupancy == nil {
		return line
	}
	view := map[string]any{"window": contextWindow}
	if ok {
		view["used"], view["share"] = used, share
	}
	if occupancy != nil {
		view["est_tokens"] = occupancy.EstTokens
		if contextWindow > 0 {
			view["ratio"] = ContextRatio(occupancy.EstTokens, contextWindow)
		}
	}
	line["context"] = view
	return line
}

// addContextOccupancy adds the turn's context occupancy to a stream-json line
// (turn_end): est_tokens, window and the 3-decimal ratio. A nil occupancy (the
// turn assembled no request) leaves the line as it is.
func addContextOccupancy(line map[string]any, occupancy *personal.ContextOccupancy) {
	if occupancy == nil || occupancy.Window <= 0 {
		return
	}
	line["context"] = map[string]any{
		"est_tokens": occupancy.EstTokens,
		"window":     occupancy.Window,
		"ratio":      ContextRatio(occupancy.EstTokens, occupancy.Window),
	}
}

// ContextRatio is est/window rounded to 3 decimals, the share of the context a
// request occupies (0 when the window is unknown).
func ContextRatio(est, window int) float64 {
	if window <= 0 {
		return 0
	}
	return math.Round(float64(est)/float64(window)*1000) / 1000
}
