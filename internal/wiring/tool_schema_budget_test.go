package wiring

import (
	"context"
	"encoding/json"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// toolSchemaBudget caps every native tool schema the turn sends. The cap is
// one schema as marshalled for an OpenAI-compatible provider:
// {"type":"function","function":{...}}, compact JSON (what actually counts
// against the fixed prompt prefix). web_search (registered only with
// serpapi_key) and view_image (a Hidden alias of file_read) are not in the
// budget; task registers only with a tasks manager, so its cap is checked
// when it is present.
//
// Caps are the Go-marshal counts measured after the 2026-10-23 tool-description
// trim (docs/research/2026-10-23-fixed-prefix-tools-report.md, section 三),
// each rounded up to the next 50; the report's guard is against creeping
// re-growth: a wording change must not quietly rebuild the paragraphs the
// trim deleted. Raise a cap only with the report's counting script redone,
// saying what added the characters.
// 2026-10-23: file_find/file_list/file_symbols/file_grep name the skipped
// directories in no_ignore again (node_modules, vendor, dist) - with only
// "ignored paths" the model listed vendor/ through shell_exec instead (naming
// run, shell_exec 9.3 -> 17.3 calls per run).
// 2026-10-29: file_grep says exclude takes a file wildcard too: a `*_test.go`
// written there failed the whole search and cost a model round trip each time
// (24 of 101 file_grep calls in one session, issue my-tools-2s5k).
var toolSchemaBudget = map[string]int{
	"memory":          2600,
	"session_search":  1950,
	"shell_exec":      1750,
	"file_read":       1700,
	"file_refs":       1700,
	"file_grep":       1700,
	"agent":           1600,
	"file_edit":       1350,
	"file_symbols":    1350,
	"session_message": 1250,
	"task":            1150,
	"recall":          1150,
	"web_fetch":       900,
	"file_find":       880,
	"skill":           800,
	"file_remove":     750,
	"file_list":       800,
	"update_plan":     700,
	"file_write":      600,
	"suga_manifest":   500,
}

// toolSchemaBudgetTotal is ~2 % above the wire sum the trim landed at (the
// same口径 in the capture: 24,206; before the trim: 28,219).
const toolSchemaBudgetTotal = 24800

// TestToolSchemaBudget pins the per-tool and total schema caps of the native
// catalog (docs/research/2026-10-23-fixed-prefix-tools-report.md section 三).
func TestToolSchemaBudget(t *testing.T) {
	resetConfig(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
	assert.NoError(t, gkconfig.Set("model", "openai/gpt-4o"))

	rt, err := OpenCLI(context.Background(), Options{AllowWrite: true})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = rt.Close() })
	schemas, err := rt.Registry.ListTools(context.Background())
	assert.Require(t, assert.NoErr(t, err))

	total := 0
	seen := map[string]bool{}
	for _, s := range schemas {
		capSize, known := toolSchemaBudget[s.Name]
		assert.Require(t, assert.True(t, known), "schema %s has no toolSchemaBudget cap; add it with a measurement", s.Name)
		wire, mErr := json.Marshal(map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        s.Name,
				"description": s.Description,
				"parameters":  s.InputSchema,
			},
		})
		assert.Require(t, assert.NoErr(t, mErr), s.Name)
		total += len(wire)
		assert.True(t, len(wire) <= capSize, "%s schema is %d chars, budget %d", s.Name, len(wire), capSize)
		seen[s.Name] = true
	}
	// Every always-registered tool must stay budgeted: a new one without a cap
	// is a failure above, one removed from the registry is a failure here.
	for name := range toolSchemaBudget {
		if name == "task" { // only with a tasks manager (background + watch)
			continue
		}
		assert.True(t, seen[name], "tool %s did not register; update the budget", name)
	}
	assert.True(t, total <= toolSchemaBudgetTotal, "tool schemas total %d chars, budget %d", total, toolSchemaBudgetTotal)
}
