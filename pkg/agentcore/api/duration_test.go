package api

import (
	"math"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

func TestDurationFromSeconds(t *testing.T) {
	max := 10 * time.Second
	for _, tc := range []struct {
		name  string
		value float64
		want  time.Duration
		ok    bool
	}{
		{"normal", 2.5, 2500 * time.Millisecond, true},
		{"clamped", 1.2e10, max, true},
		{"zero", 0, 0, false},
		{"negative", -1, 0, false},
		{"nan", math.NaN(), 0, false},
		{"infinity", math.Inf(1), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DurationFromSeconds(tc.value, max)
			assert.Eq(t, tc.ok, ok)
			assert.Eq(t, tc.want, got)
		})
	}
}
