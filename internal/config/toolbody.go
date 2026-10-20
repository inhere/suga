package axconfig

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// ToolBodySettings is the tool body file policy (design 2026-10-19,
// tool-output-files): store.tool_body_inline_bytes is the size above which a
// tool result moves to sessions/<id>/tools.jsonl, 0 keeps every body in the
// ledger.
type ToolBodySettings struct {
	// InlineBytes is the threshold; the shipped default is 1,024 bytes.
	InlineBytes int64
}

// DefaultToolBodySettings returns the shipped policy.
func DefaultToolBodySettings() ToolBodySettings {
	return ToolBodySettings{InlineBytes: 1024}
}

// DeprecatedConfigRemoval is the release the deprecated configuration key alias
// (store.tool_body_inline_bytes) is dropped in. The command line notices quote
// the same version, so keep both in sync.
const DeprecatedConfigRemoval = "v0.41.0"

// deprecatedConfigKeys maps a key that is still read under an old name to the
// name that replaces it.
var deprecatedConfigKeys = map[string]string{
	"store.tool_body_inline_bytes": "store.body_inline_bytes",
}

// DeprecatedKeyNotices returns one notice per deprecated key the loaded
// configuration still sets. Callers print them to stderr once after Load, so
// stdout (and the stream-json contract) stays clean.
func DeprecatedKeyNotices() []string {
	notices := make([]string, 0, len(deprecatedConfigKeys))
	for old, current := range deprecatedConfigKeys {
		if gkconfig.Get(old) == nil {
			continue
		}
		notices = append(notices, fmt.Sprintf("提示：配置键 %s 已弃用，将在 %s 移除，改用 %s",
			old, DeprecatedConfigRemoval, current))
	}
	sort.Strings(notices) // map order is random; the notice list stays stable
	return notices
}

// deprecatedConfigKeyUsed returns the old name the value of key comes from, so
// the command that shows a value can say so next to its source ("" when the
// value does not come from a deprecated key).
func deprecatedConfigKeyUsed(key string) string {
	for old, current := range deprecatedConfigKeys {
		if current == key && gkconfig.Get(old) != nil {
			return old
		}
	}
	return ""
}

// ReadToolBodySettings reads the new key first; the old key remains a compatible alias.
func ReadToolBodySettings() (ToolBodySettings, error) {
	settings := DefaultToolBodySettings()
	key := "store.body_inline_bytes"
	raw := gkconfig.Get(key)
	if raw == nil {
		key = "store.tool_body_inline_bytes"
		raw = gkconfig.Get(key)
	}
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
