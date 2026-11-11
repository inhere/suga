package axconfig

import (
	"fmt"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
)

// Keys of the external-origin judge (design 2026-11-10 B). Both are read-only
// for `suga config set`, so the model cannot switch the judge off.
const (
	keyOriginJudgeEnabled = "tools.origin_judge.enabled"
	keyOriginJudgeTimeout = "tools.origin_judge.timeout"
)

// DefaultOriginJudgeTimeout bounds one judge call.
const DefaultOriginJudgeTimeout = 45 * time.Second

// OriginJudgeSettings configures the external-origin judge.
type OriginJudgeSettings struct {
	Enabled bool
	Timeout time.Duration
}

// ReadOriginJudgeSettings reads tools.origin_judge.enabled (default true) and
// tools.origin_judge.timeout (a Go duration, default 45s). A timeout that is not
// a positive duration is an error, so a bad value fails startup instead of
// leaving the judge without a deadline.
func ReadOriginJudgeSettings() (OriginJudgeSettings, error) {
	out := OriginJudgeSettings{Enabled: gkconfig.Bool(keyOriginJudgeEnabled, true), Timeout: DefaultOriginJudgeTimeout}
	if gkconfig.Get(keyOriginJudgeTimeout) == nil {
		return out, nil
	}
	raw := strings.TrimSpace(gkconfig.String(keyOriginJudgeTimeout))
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return OriginJudgeSettings{}, fmt.Errorf("%s must be a positive duration such as \"20s\", got %q", keyOriginJudgeTimeout, raw)
	}
	out.Timeout = d
	return out, nil
}
