package axconfig

import (
	"fmt"
	"strings"

	goccyyaml "github.com/goccy/go-yaml"
)

// parseYAMLStringList parses the YAML flow-sequence form users copy from
// config.yaml, for example `[a/b, "c d"]`. It is only used when the value starts
// with `[`, so the plain comma form keeps working; both forms end up as the same
// block-style YAML list in the file.
//
// Every item must be a non-empty string: `[1, 2]` or `[[a]]` is reported instead
// of being written as literal text, because the old comma parsing silently
// turned `[everything/echo]` into a bracket glob.
func parseYAMLStringList(raw string) ([]string, error) {
	var items []any
	if err := goccyyaml.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("want a YAML list like [a, b]: %v", err)
	}
	out := make([]string, 0, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("list item %d (%v) is not a string; quote it as \"...\"", index, item)
		}
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("list item %d is empty", index)
		}
		out = append(out, text)
	}
	return out, nil
}
