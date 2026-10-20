package axconfig

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// ToolBodySettings is the tool body file policy (design 2026-10-19,
// tool-output-files): store.body_inline_bytes is the size above which a tool
// result moves to sessions/<id>/bodies.jsonl, 0 keeps every body in the ledger.
type ToolBodySettings struct {
	// InlineBytes is the threshold; the shipped default is 1,024 bytes.
	InlineBytes int64
}

// DefaultToolBodySettings returns the shipped policy.
func DefaultToolBodySettings() ToolBodySettings {
	return ToolBodySettings{InlineBytes: 1024}
}

// DeprecatedConfigRemoval is the release the removed configuration key alias
// (store.tool_body_inline_bytes) stopped being read in.
const DeprecatedConfigRemoval = "v0.41.0"

// removedConfigKeys maps a key that is no longer read to the name that replaces
// it. The value under the old name is ignored -- with a warning, not an error: a
// configuration file that still sets it must not keep suga from starting.
var removedConfigKeys = map[string]string{
	"store.tool_body_inline_bytes": "store.body_inline_bytes",
}

// DeprecatedKeyNotices returns one warning per removed key the loaded
// configuration still sets. Callers print them to stderr once after Load, so
// stdout (and the stream-json contract) stays clean.
func DeprecatedKeyNotices() []string {
	notices := make([]string, 0, len(removedConfigKeys))
	for old, current := range removedConfigKeys {
		if gkconfig.Get(old) == nil {
			continue
		}
		notices = append(notices, fmt.Sprintf("警告：配置键 %s 已在 %s 移除并被忽略，改用 %s",
			old, DeprecatedConfigRemoval, current))
	}
	sort.Strings(notices) // map order is random; the notice list stays stable
	return notices
}

// ReadToolBodySettings reads the threshold; the removed old name is not read any
// more, so a configuration that sets only it keeps the shipped default.
func ReadToolBodySettings() (ToolBodySettings, error) {
	settings := DefaultToolBodySettings()
	key := "store.body_inline_bytes"
	raw := gkconfig.Get(key)
	if raw == nil {
		return settings, nil
	}
	value, err := configInt64(raw)
	if err != nil {
		return settings, fmt.Errorf("%s: %w", key, err)
	}
	if value < 0 {
		return settings, fmt.Errorf("%s must be 0 or more, got %d", key, value)
	}
	settings.InlineBytes = value
	return settings, nil
}

// configInt64 accepts the numbers a YAML config may deliver for an integer key.
func configInt64(raw any) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint64:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	default:
		return 0, fmt.Errorf("want a whole number, got %T", raw)
	}
}
