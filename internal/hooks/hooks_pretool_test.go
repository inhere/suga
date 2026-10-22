package hooks

import (
	"context"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestPreToolUseGate covers the PreToolUse event of the client: the tool filter,
// a deny answer with rewritten arguments, and a timeout that comes back as an
// error (the caller refuses the call on it).
func TestPreToolUseGate(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	calls := useFakeHook(t)
	assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", 2000))

	t.Run("the filter selects the tools and the name sent", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("hooks.pre_tool_use.tools", []string{"shell_*", "file_edit"}))
		assert.NoErr(t, gkconfig.Set("hooks.pre_tool_use.name", "edit"))
		client := mustNew(t)
		gate, ok := client.(ToolGate)
		assert.Require(t, assert.Eq(t, true, ok))
		assert.Eq(t, ToolGate(gate), GateOf(client))
		name, send := gate.PreToolName("shell_exec")
		assert.Eq(t, true, send)
		// name replaces the tool of every matched event, like PostToolUse does.
		assert.Eq(t, "edit", name)
		name, send = gate.PreToolName("file_edit")
		assert.Eq(t, "edit", name)
		assert.Eq(t, true, send)
		_, send = gate.PreToolName("file_read")
		assert.Eq(t, false, send)
	})

	t.Run("a deny answer carries the reason and the rewritten input", func(t *testing.T) {
		t.Setenv("SUGA_FAKE_HOOK_STDOUT", `{"hookSpecificOutput":{"permissionDecision":"deny",`+
			`"permissionDecisionReason":"go.mod is protected","updatedInput":{"path":"docs/ok.txt"}}}`)
		assert.NoErr(t, gkconfig.Set("hooks.pre_tool_use.tools", []string{"file_write"}))
		gate := GateOf(mustNew(t))
		response, err := gate.PreTool(context.Background(), Event{Name: "PreToolUse", SessionID: "s1",
			ToolName: "file_write", ToolInput: map[string]any{"path": "go.mod"}})
		assert.NoErr(t, err)
		assert.Eq(t, "deny", response.Decision)
		assert.Eq(t, "go.mod is protected", response.Reason)
		assert.Eq(t, "docs/ok.txt", response.UpdatedInput["path"])
		got := calls()
		assert.Require(t, assert.Eq(t, 1, len(got)))
		assert.Eq(t, "PreToolUse", got[0].Event)
	})

	t.Run("an allow answer has no objection", func(t *testing.T) {
		t.Setenv("SUGA_FAKE_HOOK_STDOUT", `{"hookSpecificOutput":{"permissionDecision":"allow"}}`)
		gate := GateOf(mustNew(t))
		response, err := gate.PreTool(context.Background(), Event{Name: "PreToolUse", SessionID: "s1", ToolName: "file_write"})
		assert.NoErr(t, err)
		assert.Eq(t, "", response.Decision)
		assert.Nil(t, response.UpdatedInput)
	})

	t.Run("a timeout is an error, never an empty allow", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("hooks.timeout_ms", 50))
		t.Setenv("SUGA_FAKE_HOOK_SLEEP_MS", "4000")
		gate := GateOf(mustNew(t))
		_, err := gate.PreTool(context.Background(), Event{Name: "PreToolUse", SessionID: "s1", ToolName: "file_write"})
		assert.Err(t, err)
		assert.Eq(t, true, strings.Contains(err.Error(), "timed out"))
	})
}
