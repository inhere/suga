package sugasdk

// SessionOptions.TaintTools: a host tool named there taints its session like
// memory.taint_tools does, for that session only.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// taintFeature is the >= 12 byte text the first host tool returns and the
// Execute tool later receives verbatim.
const taintFeature = "feature-text-from-the-outside-9731"

// hostTool is a minimal host tool with configurable meta.
type hostTool struct {
	name    string
	meta    tools.Meta
	content string
	runs    *int32
}

func (h hostTool) Name() string        { return h.name }
func (h hostTool) Description() string { return "taint test tool " + h.name }
func (h hostTool) Schema() api.ToolSchema {
	return api.ToolSchema{Name: h.name, Description: h.Description(), InputSchema: map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	}}
}
func (h hostTool) ToolMeta() tools.Meta { return h.meta }
func (h hostTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	atomic.AddInt32(h.runs, 1)
	return &api.ToolResult{CallID: call.CallID, Name: h.name, Content: h.content}, nil
}

// scriptedProvider answers the n-th model request with the n-th scripted tool
// call and the request after the script with a final text.
func scriptedProvider(t *testing.T, script []struct {
	name string
	args map[string]any
}) *httptest.Server {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		const usage = `"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}`
		n := int(atomic.AddInt32(&calls, 1)) - 1
		if n >= len(script) {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],`+usage+`}`)
			return
		}
		args, err := json.Marshal(script[n].args)
		assert.NoErr(t, err)
		quoted, err := json.Marshal(string(args))
		assert.NoErr(t, err)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"id":"c`+string(rune('1'+n))+`","function":{"name":"`+
			script[n].name+`","arguments":`+string(quoted)+`}}]},"finish_reason":"tool_calls"}],`+usage+`}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// runTaintScenario runs the three scripted calls (taint tool, memory write,
// Execute tool with the feature text) and returns how many times MemoryConfirm
// was asked and how many times the Execute tool really ran.
func runTaintScenario(t *testing.T, taintNames []string, external bool) (confirms, execRuns int32) {
	t.Helper()
	ctx := context.Background()
	server := scriptedProvider(t, []struct {
		name string
		args map[string]any
	}{
		{"host_read", map[string]any{"text": "go"}},
		{"memory", map[string]any{"action": "add", "scope": "project", "kind": "user", "title": "Taint probe",
			"summary": "probe entry", "body": "plain body without the feature"}},
		{"host_exec", map[string]any{"text": taintFeature}},
	})
	home := sdkTestHome(t, server.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })

	var readRuns int32
	session, err := engine.OpenSession(ctx, "", SessionOptions{
		Workspace:        t.TempDir(),
		ProjectMemoryKey: "taint-probe",
		ExtraTools: []api.Tool{
			hostTool{name: "host_read", content: taintFeature, runs: &readRuns,
				meta: tools.Meta{Category: tools.CategoryRead, ReadOnly: true, External: external}},
			hostTool{name: "host_exec", content: "executed", runs: &execRuns,
				meta: tools.Meta{Category: tools.CategoryExecute}},
		},
		TaintTools: taintNames,
		Approver:   allowAll{},
		MemoryConfirm: func(context.Context, MemoryConfirmRequest) bool {
			atomic.AddInt32(&confirms, 1)
			return true
		},
	})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.Send(ctx, Prompt{Text: "run the probe"}, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "COMPLETED", result.State)
	assert.Eq(t, int32(1), atomic.LoadInt32(&readRuns))
	return atomic.LoadInt32(&confirms), atomic.LoadInt32(&execRuns)
}

func TestSessionTaintTools(t *testing.T) {
	t.Run("a registered tool taints the session", func(t *testing.T) {
		// Steps 1-3: the memory write asks MemoryConfirm, and a taint-only tool
		// is not an external origin, so the Execute call is not refused.
		confirms, execRuns := runTaintScenario(t, []string{"host_read", "host_read", " "}, false)
		assert.Eq(t, int32(1), confirms)
		assert.Eq(t, int32(1), execRuns)
	})
	t.Run("an External tool is refused by the origin guard", func(t *testing.T) {
		// Step 4: control group; the session is tainted either way.
		confirms, execRuns := runTaintScenario(t, nil, true)
		assert.Eq(t, int32(1), confirms)
		assert.Eq(t, int32(0), execRuns)
	})
	t.Run("without registration nothing is tainted", func(t *testing.T) {
		// Step 5.
		confirms, execRuns := runTaintScenario(t, nil, false)
		assert.Eq(t, int32(0), confirms)
		assert.Eq(t, int32(1), execRuns)
	})
}
