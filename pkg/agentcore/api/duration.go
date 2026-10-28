package api

import (
	"math"
	"time"
)

// DurationFromSeconds converts a positive floating-point second count without
// first overflowing time.Duration. max is the largest permitted duration; a
// non-positive max means the largest representable duration.
func DurationFromSeconds(seconds float64, max time.Duration) (time.Duration, bool) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, false
	}
	if max <= 0 {
		max = time.Duration(math.MaxInt64)
	}
	if seconds >= max.Seconds() {
		return max, true
	}
	return time.Duration(seconds * float64(time.Second)), true
}
