package axconfig

import (
	"fmt"
	"math"
	"sort"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// HookEntry is one entry of hooks.commands: an external hook program with an
// optional per-entry timeout. TimeoutMS is 0 when the entry does not set
// timeout_ms; internal/hooks then falls back to the hooks.timeout_ms default.
type HookEntry struct {
	Command   []string
	TimeoutMS int
}

// ReadHookEntries reads and validates the complete hooks.commands value. The
// value is a YAML list of mappings, each with a command (program plus
// arguments) and an optional timeout_ms; a malformed entry fails startup and
// `suga config set` instead of silently dropping a hook. Entries are returned in
// configuration order because the runtime dispatches them in that order.
func ReadHookEntries() ([]HookEntry, error) {
	raw := gkconfig.Get("hooks.commands")
	if raw == nil {
		return nil, nil
	}
	// The YAML loader yields []any; a value written through gkconfig.Set keeps
	// the typed slice, so both containers are accepted (same as mcp.servers).
	var items []any
	switch value := raw.(type) {
	case []any:
		items = value
	case []map[string]any:
		items = make([]any, 0, len(value))
		for _, item := range value {
			items = append(items, item)
		}
	default:
		return nil, fmt.Errorf("hooks.commands must be a list of entries")
	}
	if len(items) == 0 {
		return nil, nil
	}

	entries := make([]HookEntry, 0, len(items))
	for i, item := range items {
		entry, err := normalizeHookEntry(i, item)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// normalizeHookEntry validates one list item. index is the YAML position (from
// 0) and is part of every error message so the user can fix the right entry.
func normalizeHookEntry(index int, raw any) (HookEntry, error) {
	mapping, ok := raw.(map[string]any)
	if !ok {
		return HookEntry{}, fmt.Errorf("hooks.commands[%d]: must be a mapping", index)
	}
	if field := firstUnknownHookEntryField(mapping); field != "" {
		return HookEntry{}, fmt.Errorf("hooks.commands[%d]: unknown field %q (only command and timeout_ms are supported)", index, field)
	}
	command, err := hookEntryCommand(index, mapping["command"])
	if err != nil {
		return HookEntry{}, err
	}
	timeoutMS, err := hookEntryTimeoutMS(index, mapping["timeout_ms"])
	if err != nil {
		return HookEntry{}, err
	}
	return HookEntry{Command: command, TimeoutMS: timeoutMS}, nil
}

// firstUnknownHookEntryField returns the alphabetically first key that is not
// one of the two supported entry fields, so the reported error is stable.
func firstUnknownHookEntryField(mapping map[string]any) string {
	unknown := make([]string, 0, len(mapping))
	for key := range mapping {
		if key == "command" || key == "timeout_ms" {
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	return unknown[0]
}

func hookEntryCommand(index int, raw any) ([]string, error) {
	failed := fmt.Errorf("hooks.commands[%d].command: must be a non-empty list of strings with a program name", index)
	var command []string
	switch value := raw.(type) {
	case []string:
		command = append(command, value...)
	case []any:
		command = make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, failed
			}
			command = append(command, text)
		}
	default:
		return nil, failed
	}
	// A list whose program name is only whitespace would start nothing; the
	// runtime expects command[0] to name the program.
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, failed
	}
	return command, nil
}

func hookEntryTimeoutMS(index int, raw any) (int, error) {
	if raw == nil {
		return 0, nil
	}
	failed := fmt.Errorf("hooks.commands[%d].timeout_ms: must be a positive integer", index)
	var value int64
	switch number := raw.(type) {
	case int:
		value = int64(number)
	case int64:
		value = number
	case uint64:
		if number > math.MaxInt {
			return 0, failed
		}
		value = int64(number)
	case float64:
		// YAML may decode a whole number as float64 when the document uses a
		// float notation; only whole values are accepted.
		if number != math.Trunc(number) || math.IsNaN(number) || math.IsInf(number, 0) ||
			number > math.MaxInt || number < math.MinInt {
			return 0, failed
		}
		value = int64(number)
	default:
		return 0, failed
	}
	if value <= 0 || value > math.MaxInt {
		return 0, failed
	}
	return int(value), nil
}
