package wiring

import (
	"context"
	"errors"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// newGatedRegistry puts the CLI approver in front of registry, as the runtime does.
func newGatedRegistry(registry *tools.Registry, readOnly bool) (*tools.Approval, error) {
	gate, err := newCLIMCPGate(readOnly, tools.ApprovalModeDefault)
	if err != nil {
		return nil, err
	}
	return tools.NewApproval(registry, gate), nil
}

// denied reports whether err is the gate's refusal for name.
func deniedCall(err error, name string) bool {
	return err != nil && strings.Contains(err.Error(), "需要许可："+name) && errors.Is(err, tools.ErrDenied)
}

func TestCLIMCPGateDefaultsAndOverrides(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	registry := tools.NewRegistry()
	for _, name := range []string{"gofer_issue_list", "gofer_work_get", "gofer_work_report", "gofer_issue_close", "mcp_write"} {
		registry.Register(newServerStubTool("gofer", name))
	}
	t.Run("no auto_allow denies every MCP tool", func(t *testing.T) {
		gate, err := newGatedRegistry(registry, false)
		assert.NoErr(t, err)
		for _, name := range []string{"gofer_issue_list", "gofer_work_get", "gofer_work_report", "gofer_issue_close", "mcp_write"} {
			_, err := gate.ExecuteTool(context.Background(), agentcore.ToolCall{Name: name})
			assert.True(t, deniedCall(err, name))
		}
	})
	t.Run("auto_allow opens only listed tools", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"mcp_write", "gofer/gofer_*_list"}))
		gate, err := newGatedRegistry(registry, false)
		assert.NoErr(t, err)
		for _, name := range []string{"mcp_write", "gofer_issue_list"} {
			_, err = gate.ExecuteTool(context.Background(), agentcore.ToolCall{Name: name})
			assert.NoErr(t, err)
		}
		_, err = gate.ExecuteTool(context.Background(), agentcore.ToolCall{Name: "gofer_work_report"})
		assert.True(t, deniedCall(err, "gofer_work_report"))
	})
	t.Run("read_only_allow does not apply outside read-only", func(t *testing.T) {
		gkconfig.Reset()
		assert.NoErr(t, gkconfig.Set("tools.read_only_allow", []string{"gofer/gofer_*_list"}))
		gate, err := newGatedRegistry(registry, false)
		assert.NoErr(t, err)
		_, err = gate.ExecuteTool(context.Background(), agentcore.ToolCall{Name: "gofer_issue_list"})
		assert.True(t, deniedCall(err, "gofer_issue_list"))
	})
}

// TestCLIMCPGateReadOnly verifies --read-only uses only tools.read_only_allow.
func TestCLIMCPGateReadOnly(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	registry := tools.NewRegistry()
	for _, name := range []string{"gofer_x_list", "gofer_work_report", "mcp_write", "gofer_issue_close"} {
		registry.Register(newServerStubTool("gofer", name))
	}
	registry.Register(newServerStubTool("thirdparty", "gofer_y_list"))
	call := func(t *testing.T, name string) error {
		gate, err := newGatedRegistry(registry, true)
		assert.Require(t, assert.NoErr(t, err))
		_, err = gate.ExecuteTool(context.Background(), agentcore.ToolCall{Name: name})
		return err
	}

	t.Run("unconfigured denies everything", func(t *testing.T) {
		for _, name := range []string{"gofer_x_list", "gofer_work_report", "mcp_write"} {
			assert.True(t, deniedCall(call(t, name), name))
		}
	})
	t.Run("auto_allow is validated but ignored", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"mcp_write", "gofer_*"}))
		for _, name := range []string{"mcp_write", "gofer_issue_close", "gofer_x_list"} {
			assert.True(t, deniedCall(call(t, name), name))
		}
		assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"[bad"}))
		_, err := newGatedRegistry(registry, true)
		assert.Err(t, err)
		assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"mcp_write", "gofer_*"}))
	})
	t.Run("read_only_allow allows and denies", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.read_only_allow", []string{"gofer/gofer_*_list", "gofer/gofer_work_report"}))
		for _, name := range []string{"gofer_x_list", "gofer_work_report"} {
			assert.NoErr(t, call(t, name))
		}
		// Not listed, and listed name from an unlisted server.
		for _, name := range []string{"mcp_write", "gofer_issue_close", "gofer_y_list"} {
			assert.True(t, deniedCall(call(t, name), name))
		}
	})
	t.Run("non read-only keeps user auto_allow", func(t *testing.T) {
		gate, err := newGatedRegistry(registry, false)
		assert.NoErr(t, err)
		_, err = gate.ExecuteTool(context.Background(), agentcore.ToolCall{Name: "mcp_write"})
		assert.NoErr(t, err)
	})
}

func TestCLIMCPGateReadOnlyAllowValidation(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "bare entry", value: []string{"gofer/gofer_*_list", "gofer_work_report"}, want: "must be server-qualified"},
		{name: "empty entry", value: []string{" "}, want: "empty glob"},
		{name: "bad glob", value: []string{"gofer/[x"}, want: "glob"},
		{name: "not a list", value: "gofer/x", want: "must be a list"},
		{name: "non-string item", value: []any{"gofer/x", 3}, want: "must contain glob strings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.NoErr(t, gkconfig.Set("tools.read_only_allow", tc.value))
			// The list is validated in both modes, so a typo surfaces before any read-only job.
			for _, readOnly := range []bool{true, false} {
				_, err := newCLIMCPGate(readOnly, tools.ApprovalModeDefault)
				assert.True(t, err != nil && strings.Contains(err.Error(), "tools.read_only_allow") && strings.Contains(err.Error(), tc.want))
			}
		})
	}
}

// TestCLIMCPGateServerQualified verifies a qualified entry trusts only the named
// server, so a third-party server cannot borrow a tool name.
func TestCLIMCPGateServerQualified(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	registry := tools.NewRegistry()
	registry.Register(newServerStubTool("gofer", "gofer_work_report"))
	registry.Register(newServerStubTool("thirdparty", "gofer_session_list"))
	registry.Register(newServerStubTool("thirdparty", "gofer_issue_get"))

	run := func(t *testing.T, readOnly bool, name string) error {
		gate, err := newGatedRegistry(registry, readOnly)
		assert.NoErr(t, err)
		_, err = gate.ExecuteTool(context.Background(), agentcore.ToolCall{Name: name})
		return err
	}
	t.Run("read_only_allow is server-scoped", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.read_only_allow", []string{"gofer/gofer_work_report", "gofer/gofer_session_list"}))
		assert.NoErr(t, run(t, true, "gofer_work_report"))
		for _, name := range []string{"gofer_session_list", "gofer_issue_get"} {
			assert.True(t, deniedCall(run(t, true, name), name))
		}
	})
	t.Run("auto_allow entries: qualified is server-scoped, bare matches any server", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"thirdparty/gofer_session_list"}))
		assert.NoErr(t, run(t, false, "gofer_session_list"))
		assert.True(t, run(t, false, "gofer_issue_get") != nil)
		assert.True(t, run(t, false, "gofer_work_report") != nil)
		assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"gofer_issue_get"}))
		assert.NoErr(t, run(t, false, "gofer_issue_get"))
	})
	t.Run("invalid server glob rejected", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"[/x"}))
		_, err := newGatedRegistry(registry, false)
		assert.True(t, err != nil)
	})
}
