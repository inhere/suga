package wiring

import (
	"fmt"
	"sort"
	"strings"

	axconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// applyDisabledTools removes tools.disabled from reg and validates the names
// against the native catalog and the registered tools. A name the runtime
// cannot know is a startup error (a typo must not silently leave the tool on);
// MCP tool names (server/tool) are always accepted because they only exist
// after their server started.
func applyDisabledTools(reg *tools.Registry) error {
	names, err := axconfig.DisabledToolNames()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	known := make(map[string]bool, len(names))
	for _, spec := range native.Catalog() {
		known[spec.Name] = true
	}
	registered := reg.List()
	for _, name := range registered {
		known[name] = true
	}
	var unknown []string
	for _, name := range names {
		if !known[name] && !strings.Contains(name, "/") {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(registered)
		return fmt.Errorf("tools.disabled: unknown tool %s; available: %s",
			strings.Join(unknown, ", "), strings.Join(registered, ", "))
	}
	removeDisabledTools(reg)
	return nil
}

// removeDisabledTools drops tools.disabled from reg without validating: the
// runtime registers some tools after the preferred-tools snapshot, so the
// policy is applied once before that snapshot (validating) and once after the
// last registration (this one, which also covers MCP tools).
func removeDisabledTools(reg *tools.Registry) {
	names, err := axconfig.DisabledToolNames()
	if err != nil { // applyDisabledTools reported it at startup
		return
	}
	for _, name := range names {
		reg.Remove(name)
	}
}
