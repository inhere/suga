package axconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/inhere/suga/pkg/agentcore"
)

// Chat idle consolidation defaults (design D6/Q1).
const (
	DefaultIdleAfter    = 15 * time.Minute
	DefaultIdleMinRatio = 0.65
)

// IdleSettings configures the chat idle consolidation.
type IdleSettings struct {
	// After is the quiet time after a turn ends before consolidating
	// (context.idle_after); 0 disables it.
	After time.Duration
	// MinRatio is the share of the segment threshold the last request's
	// context must reach (context.idle_min_ratio).
	MinRatio float64
}

// ReadIdleSettings reads context.idle_after (a Go duration such as "3s" or
// "15m", or a plain number of seconds; 0 turns it off) and
// context.idle_min_ratio (0..1). Unset keys take the defaults; an invalid
// value is an error.
func ReadIdleSettings() (IdleSettings, error) {
	out := IdleSettings{After: DefaultIdleAfter, MinRatio: DefaultIdleMinRatio}
	if gkconfig.Get("context.idle_after") != nil {
		raw := strings.TrimSpace(gkconfig.String("context.idle_after"))
		d, err := parseIdleDuration(raw)
		if err != nil {
			return IdleSettings{}, fmt.Errorf("context.idle_after: %w", err)
		}
		out.After = d
	}
	if gkconfig.Get("context.idle_min_ratio") != nil {
		raw := strings.TrimSpace(gkconfig.String("context.idle_min_ratio"))
		ratio, err := strconv.ParseFloat(raw, 64)
		if err != nil || ratio < 0 || ratio > 1 {
			return IdleSettings{}, fmt.Errorf("context.idle_min_ratio must be a number between 0 and 1, got %q", raw)
		}
		out.MinRatio = ratio
	}
	return out, nil
}

// parseIdleDuration accepts a duration ("2s", "15m"), a day count ("30d") or bare seconds ("0", "90").
func parseIdleDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty value")
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		if secs < 0 {
			return 0, fmt.Errorf("must not be negative, got %q", raw)
		}
		if secs == 0 {
			return 0, nil
		}
		if d, ok := agentcore.DurationFromSeconds(secs, 0); ok {
			return d, nil
		}
		return 0, fmt.Errorf("invalid seconds %q", raw)
	}
	if days, err := strconv.ParseFloat(strings.TrimSuffix(raw, "d"), 64); err == nil && strings.HasSuffix(raw, "d") {
		if days < 0 {
			return 0, fmt.Errorf("must not be negative, got %q", raw)
		}
		// A day count such as "30d": retention periods are documented in days.
		if d, ok := agentcore.DurationFromSeconds(days*24*60*60, 0); ok {
			return d, nil
		}
		return 0, fmt.Errorf("invalid days %q", raw)
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("want a duration like \"30s\", \"15m\" or \"30d\", got %q", raw)
	}
	if d < 0 {
		return 0, fmt.Errorf("must not be negative, got %q", raw)
	}
	return d, nil
}
