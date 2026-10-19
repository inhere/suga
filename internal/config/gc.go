package axconfig

import (
	"fmt"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
)

// Garbage-collection retention (data governance P3, design 0.3 会话来源).
// `suga data gc --retention` removes sessions by class instead of by one age: a
// session an interactive chat created is kept for the interactive period, one a
// non-interactive entry point created (run, ACP, sub-agent) for the shorter
// one. A period of 0 keeps its class forever.
const (
	DefaultGCRetentionInteractive    = 180 * 24 * time.Hour
	DefaultGCRetentionNonInteractive = 30 * 24 * time.Hour
)

// GCRetentionSettings is the validated gc.retention policy.
type GCRetentionSettings struct {
	Interactive    time.Duration
	NonInteractive time.Duration
}

// DefaultGCRetentionSettings returns the documented defaults (180d / 30d).
func DefaultGCRetentionSettings() GCRetentionSettings {
	return GCRetentionSettings{
		Interactive:    DefaultGCRetentionInteractive,
		NonInteractive: DefaultGCRetentionNonInteractive,
	}
}

// ReadGCRetentionSettings reads gc.retention.interactive and
// gc.retention.noninteractive. Unknown fields are rejected, so a misspelled key
// cannot silently keep months of sessions. A value is a duration ("180d",
// "4320h") or bare seconds; a negative value is an error.
func ReadGCRetentionSettings() (GCRetentionSettings, error) {
	settings := DefaultGCRetentionSettings()
	raw := gkconfig.Get("gc.retention")
	if raw == nil {
		return settings, nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return settings, fmt.Errorf("gc.retention must be an object")
	}
	for key := range fields {
		switch key {
		case "interactive", "noninteractive":
		default:
			return settings, fmt.Errorf("gc.retention.%s is not a recognized setting", key)
		}
	}
	for key, dst := range map[string]*time.Duration{
		"interactive":    &settings.Interactive,
		"noninteractive": &settings.NonInteractive,
	} {
		value, exists := fields[key]
		if !exists {
			continue
		}
		parsed, err := gcRetentionDuration(value)
		if err != nil {
			return settings, fmt.Errorf("gc.retention.%s: %w", key, err)
		}
		*dst = parsed
	}
	return settings, nil
}

// gcRetentionDuration accepts what parseIdleDuration accepts (a duration such
// as "180d" or "4320h", or bare seconds) with the retention key named in the
// error.
func gcRetentionDuration(raw any) (time.Duration, error) {
	switch value := raw.(type) {
	case string:
		return parseIdleDuration(strings.TrimSpace(value))
	case int, int8, int16, int32, int64, float32, float64:
		return parseIdleDuration(fmt.Sprint(value))
	default:
		return 0, fmt.Errorf(`want a duration like "180d" or "30d", got %T`, raw)
	}
}
