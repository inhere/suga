package axconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// Spill policy defaults and the accepted values (design: Spill 配额、压缩与透明
// 读取). tools.max_output_bytes and tools.max_spill_bytes keep their meaning;
// tools.spill.* governs the files below $SUGA_HOME/sessions/**/outputs.
const (
	// DefaultSpillMaxBytes is the logical byte budget of all spill files
	// (tools.spill.max_bytes); 0 means no quota, manual gc only.
	DefaultSpillMaxBytes int64 = 1 << 30
	// DefaultSpillMaxAge is the age at which a spill file is reported as
	// removable (tools.spill.max_age); nothing is deleted automatically.
	DefaultSpillMaxAge = 90 * 24 * time.Hour
	// DefaultSpillCompressAfter is how old an idle spill file must be before
	// `gc --compress-spill` compresses it (tools.spill.compress_after).
	DefaultSpillCompressAfter = 7 * 24 * time.Hour
	// DefaultSpillKeepDays is tools.spill.keep_days; 0 keeps the max_age rule only.
	DefaultSpillKeepDays = 0
	// SpillCompressionGzip is the supported compression (tools.spill.compression).
	SpillCompressionGzip = "gzip"
	// SpillCompressionOff turns the compression pass off.
	SpillCompressionOff = "off"
	// SpillOnQuotaRefuse is the supported quota policy (tools.spill.on_quota): a
	// new output over the quota is refused and the output layer keeps head and
	// tail, instead of dropping older outputs silently.
	SpillOnQuotaRefuse = "refuse"
)

// SpillSettings is the spill policy read from tools.spill. The independent
// `gc --compress-spill` pass consumes it; the session writer only needs
// MaxBytes (SpillQuotaBytes).
type SpillSettings struct {
	// MaxBytes is the logical (uncompressed) byte budget of all spill files.
	MaxBytes int64
	// MaxAge is the age at which a spill file is a gc candidate.
	MaxAge time.Duration
	// CompressAfter is the age at which an idle .txt may be gzipped.
	CompressAfter time.Duration
	// Compression is gzip or off.
	Compression string
	// OnQuota is the policy for a new output over MaxBytes (only refuse).
	OnQuota string
	// KeepDays is an additional age rule; 0 means max_age alone applies.
	KeepDays int
}

// DefaultSpillSettings returns the shipped spill policy.
func DefaultSpillSettings() SpillSettings {
	return SpillSettings{
		MaxBytes:      DefaultSpillMaxBytes,
		MaxAge:        DefaultSpillMaxAge,
		CompressAfter: DefaultSpillCompressAfter,
		Compression:   SpillCompressionGzip,
		OnQuota:       SpillOnQuotaRefuse,
		KeepDays:      DefaultSpillKeepDays,
	}
}

// ReadSpillSettings reads and validates tools.spill. An unknown key or an
// unusable value is an error, so a typo cannot silently drop the quota or the
// compression pass's age gate.
func ReadSpillSettings() (SpillSettings, error) {
	settings := DefaultSpillSettings()
	raw := gkconfig.Get("tools.spill")
	if raw == nil {
		return settings, nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return settings, fmt.Errorf("tools.spill must be an object")
	}
	for key := range fields {
		switch key {
		case "max_bytes", "max_age", "compress_after", "compression", "on_quota", "keep_days":
		default:
			return settings, fmt.Errorf("tools.spill.%s is not a recognized setting", key)
		}
	}
	if value, exists := fields["max_bytes"]; exists {
		parsed, ok := snapshotInteger(value)
		if !ok || parsed < 0 {
			return settings, fmt.Errorf("tools.spill.max_bytes must be a whole number >= 0 (0 disables the quota)")
		}
		settings.MaxBytes = parsed
	}
	for key, dst := range map[string]*time.Duration{
		"max_age":        &settings.MaxAge,
		"compress_after": &settings.CompressAfter,
	} {
		value, exists := fields[key]
		if !exists {
			continue
		}
		parsed, err := parseSpillDuration(value)
		if err != nil {
			return settings, fmt.Errorf("tools.spill.%s: %w", key, err)
		}
		*dst = parsed
	}
	for key, dst := range map[string]*string{
		"compression": &settings.Compression,
		"on_quota":    &settings.OnQuota,
	} {
		value, exists := fields[key]
		if !exists {
			continue
		}
		parsed, ok := value.(string)
		if !ok {
			return settings, fmt.Errorf("tools.spill.%s must be a string", key)
		}
		*dst = strings.ToLower(strings.TrimSpace(parsed))
	}
	switch settings.Compression {
	case SpillCompressionGzip, SpillCompressionOff:
	default:
		return settings, fmt.Errorf("tools.spill.compression must be %s or %s, got %q",
			SpillCompressionGzip, SpillCompressionOff, settings.Compression)
	}
	if settings.OnQuota != SpillOnQuotaRefuse {
		return settings, fmt.Errorf("tools.spill.on_quota supports only %s (a new output over the quota is refused, "+
			"older outputs are never dropped by the write path), got %q", SpillOnQuotaRefuse, settings.OnQuota)
	}
	if value, exists := fields["keep_days"]; exists {
		parsed, ok := snapshotInteger(value)
		if !ok || parsed < 0 {
			return settings, fmt.Errorf("tools.spill.keep_days must be a whole number >= 0")
		}
		settings.KeepDays = int(parsed)
	}
	return settings, nil
}

// SpillQuotaBytes is tools.spill.max_bytes as the session writer uses it. A
// value the reader rejects falls back to the default: an unreadable limit must
// not stop a turn (the settings registry reports the error instead).
func SpillQuotaBytes() int64 {
	settings, err := ReadSpillSettings()
	if err != nil {
		return DefaultSpillMaxBytes
	}
	return settings.MaxBytes
}

// parseSpillDuration accepts the age forms the spill settings document, e.g.
// "7d", "90d", "36h" or a time.Duration string like "30m".
func parseSpillDuration(raw any) (time.Duration, error) {
	text, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf("must be a duration like \"7d\" or \"36h\"")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("must not be empty")
	}
	if days, ok := strings.CutSuffix(text, "d"); ok {
		value, err := parsePositiveNumber(days)
		if err != nil {
			return 0, fmt.Errorf("want a duration like \"7d\" or \"36h\", got %q", text)
		}
		if d, ok := api.DurationFromSeconds(value*24*60*60, 0); ok {
			return d, nil
		}
		return 0, fmt.Errorf("duration exceeds maximum")
	}
	parsed, err := time.ParseDuration(text)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("want a positive duration like \"7d\" or \"36h\", got %q", text)
	}
	return parsed, nil
}

// parsePositiveNumber parses the number in front of a unit such as the "7" of
// "7d"; zero and negatives are refused with the day form in mind.
func parsePositiveNumber(text string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("want a positive number, got %q", text)
	}
	return value, nil
}
