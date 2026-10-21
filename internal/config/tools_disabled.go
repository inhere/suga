package axconfig

import (
	"fmt"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// DisabledToolNames returns tools.disabled: the tool names the runtime must not
// offer to the model, in configured order (empty = nothing disabled). A name is
// removed from the tool registry, so it is absent from the tool list, from
// tool_search and from the manifest instead of failing on every call. Entries
// are trimmed and de-duplicated; a value that is not a list of names is an error.
func DisabledToolNames() ([]string, error) {
	raw := gkconfig.Get("tools.disabled")
	if raw == nil {
		return nil, nil
	}
	var values []string
	switch v := raw.(type) {
	case []string:
		values = v
	case []any:
		for _, item := range v {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("tools.disabled must be a list of tool names")
			}
			values = append(values, name)
		}
	case string: // a single name is accepted like a one-element list
		values = []string{v}
	default:
		return nil, fmt.Errorf("tools.disabled must be a list of tool names")
	}
	seen := make(map[string]bool, len(values))
	names := make([]string, 0, len(values))
	for _, name := range values {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}
