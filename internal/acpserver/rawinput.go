package acpserver

import (
	"github.com/inhere/suga/pkg/agentcore/redact"
)

// maxRawInputString bounds each string value of rawInput (in runes); longer
// values are cut and marked, so a large file_write body cannot bloat a
// notification. Keys and structure are never dropped.
const maxRawInputString = 2000

// toolRawInput is the ACP rawInput of a tool call: the call arguments with
// secrets masked by the same redact.Secrets the trace uses. Redaction is done
// per value (not on the marshalled JSON) so a masked match can never swallow a
// quote and break the structure. nil for a call without arguments.
func toolRawInput(args map[string]any) any {
	if len(args) == 0 {
		// an untyped nil: a typed nil map in the interface would be sent as null
		return nil
	}
	return redactMap(args)
}

func redactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, ok := v.(string); ok && isSecretKey(k) {
			// the key itself names a credential: mask the whole value
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch x := v.(type) {
	case string:
		return truncateRawString(redact.Secrets(x))
	case map[string]any:
		return redactMap(x)
	case []any:
		list := make([]any, len(x))
		for i, item := range x {
			list[i] = redactValue(item)
		}
		return list
	case []string:
		list := make([]any, len(x))
		for i, item := range x {
			list[i] = truncateRawString(redact.Secrets(item))
		}
		return list
	default:
		return v
	}
}

// isSecretKey reuses the redact patterns: a key counts as secret when
// "<key>=x" gets its value masked.
func isSecretKey(key string) bool {
	probe := key + "=x"
	return redact.Secrets(probe) != probe
}

func truncateRawString(s string) string {
	r := []rune(s)
	if len(r) <= maxRawInputString {
		return s
	}
	return string(r[:maxRawInputString]) + "…[truncated]"
}
