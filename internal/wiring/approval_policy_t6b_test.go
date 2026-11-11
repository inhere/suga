package wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

func TestPolicyApproverTranslatesRunActions(t *testing.T) {
	workspace := t.TempDir()
	assert.NoErr(t, os.Mkdir(filepath.Join(workspace, "docs"), 0o755))
	policy := approvalPolicy{
		Mode: approvalrules.ModeRun, Workspace: workspace,
		Defaults: defaultApprovalDefaults(approvalrules.ModeRun, false, tools.ApprovalModeDefault),
		Rules: []approvalrules.Rule{
			{Source: approvalrules.SourceUser, Index: 1, Tool: "file_read", Action: approvalrules.Deny},
			{Source: approvalrules.SourceUser, Index: 2, Tool: "file_write", Path: []string{"docs/**"}, Root: workspace, Action: approvalrules.Allow},
			{Source: approvalrules.SourceUser, Index: 3, Tool: "file_edit", Action: approvalrules.Ask},
		},
	}
	registry := tools.NewRegistry()
	for _, name := range []string{"file_read", "file_write", "file_edit"} {
		registry.Register(&nativeStubTool{name: name})
	}
	approval := tools.NewApproval(registry, newPolicyApprover(policy))

	tests := []struct {
		name   string
		call   agentcore.ToolCall
		want   string
		denied bool
	}{
		{name: "explicit deny", call: agentcore.ToolCall{Name: "file_read"}, want: "user#1 file_read action=deny", denied: true},
		{name: "explicit allow", call: agentcore.ToolCall{Name: "file_write", Arguments: map[string]any{"path": "docs/a.txt"}}, want: "user#2 file_write path=\"docs/**\" action=allow"},
		{name: "ask fails closed", call: agentcore.ToolCall{Name: "file_edit", Arguments: map[string]any{"path": "docs/a.txt"}}, want: "user#3 file_edit action=ask", denied: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, report := tools.WithApprovalReport(context.Background())
			_, err := approval.ExecuteTool(ctx, tc.call)
			assert.Eq(t, tc.denied, err != nil)
			assert.Eq(t, tc.want, report.Rule)
			assert.Empty(t, report.Scope)
		})
	}
}

func TestChatPolicyRulePrecedesShortcuts(t *testing.T) {
	workspace := t.TempDir()
	policy := approvalPolicy{
		Mode: approvalrules.ModeRun, Workspace: workspace,
		Defaults: defaultApprovalDefaults(approvalrules.ModeRun, false, tools.ApprovalModeDefault),
		Rules: []approvalrules.Rule{
			{Source: approvalrules.SourceUser, Index: 1, Tool: "file_read", Action: approvalrules.Deny},
			{Source: approvalrules.SourceUser, Index: 2, Tool: "file_write", Action: approvalrules.Ask},
		},
	}
	gate := &cliMCPGate{policy: policy}
	asker := &scriptedAsker{answers: []string{"y"}}
	approver := newChatApprover(chatApproverOptions{gate: gate, auto: true, asker: asker, workspace: workspace})
	registry := tools.NewRegistry()
	read, write := &nativeStubTool{name: "file_read"}, &nativeStubTool{name: "file_write"}
	registry.Register(read)
	registry.Register(write)
	approval := tools.NewApproval(registry, approver)

	ctx, report := tools.WithApprovalReport(context.Background())
	_, err := approval.ExecuteTool(ctx, agentcore.ToolCall{Name: "file_read"})
	assert.ErrIs(t, err, tools.ErrDenied)
	assert.Eq(t, "user#1 file_read action=deny", report.Rule)
	assert.Len(t, asker.questions, 0)

	ctx, report = tools.WithApprovalReport(context.Background())
	_, err = approval.ExecuteTool(ctx, agentcore.ToolCall{Name: "file_write"})
	assert.NoErr(t, err)
	assert.Eq(t, "user#2 file_write action=ask", report.Rule)
	assert.Eq(t, tools.ApprovedByUser, report.Source)
	assert.Len(t, asker.questions, 1)
}
