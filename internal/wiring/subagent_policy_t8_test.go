package wiring

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

func TestSubAgentPolicySnapshotAllowsReadsAndDeniesAsk(t *testing.T) {
	workspace := t.TempDir()
	policy := approvalPolicy{
		Mode:      approvalrules.ModeRun,
		Workspace: workspace,
		Defaults:  defaultApprovalDefaults(approvalrules.ModeRun, false, tools.ApprovalModeDefault),
		Rules: []approvalrules.Rule{
			{Source: approvalrules.SourceUser, Index: 1, Tool: "file_read", Action: approvalrules.Deny},
			{Source: approvalrules.SourceUser, Index: 2, Tool: "file_list", Action: approvalrules.Ask},
			{Source: approvalrules.SourceUser, Index: 3, Tool: "file_find", Action: approvalrules.Allow},
		},
	}
	agents := &subAgents{workspace: workspace, policySnapshot: func() approvalPolicy { return policy }}
	childPolicy := agents.childPolicy()
	// Mutating the parent after child creation must not change the child's rule.
	policy.Rules[0].Action = approvalrules.Allow

	registry := tools.NewRegistry()
	for _, name := range []string{"file_read", "file_list", "file_find"} {
		registry.Register(&nativeStubTool{name: name})
	}
	approval := tools.NewApproval(registry, newPolicyApprover(childPolicy))

	tests := []struct {
		name   string
		tool   string
		denied bool
	}{
		{name: "explicit deny", tool: "file_read", denied: true},
		{name: "ask fails closed without a child terminal", tool: "file_list", denied: true},
		{name: "explicit allow", tool: "file_find", denied: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := approval.ExecuteTool(context.Background(), agentcore.ToolCall{
				CallID: "child-call", Name: tc.tool, Arguments: map[string]any{},
			})
			assert.Eq(t, tc.denied, errors.Is(err, tools.ErrDenied))
		})
	}
}

func TestSubAgentToolSetHasNoWriteTools(t *testing.T) {
	for kind, names := range subAgentTools {
		t.Run(kind, func(t *testing.T) {
			for _, writeTool := range []string{"file_write", "file_edit", "file_remove", "shell_exec"} {
				assert.False(t, slices.Contains(names, writeTool))
			}
		})
	}
}

func TestRunDefaultPolicyProtectsControlPlanePaths(t *testing.T) {
	workspace, sugaHome := t.TempDir(), t.TempDir()
	ruleFile := filepath.Join(workspace, ".suga", "approval-rules.yaml")
	policy := approvalPolicy{
		Mode:      approvalrules.ModeRun,
		Workspace: workspace,
		SugaHome:  sugaHome,
		RuleFiles: []string{ruleFile},
		Defaults:  defaultApprovalDefaults(approvalrules.ModeRun, false, tools.ApprovalModeDefault),
	}

	tests := []struct {
		name string
		path string
		want approvalrules.Action
	}{
		{name: "workspace git", path: filepath.Join(workspace, ".git", "config"), want: approvalrules.Deny},
		{name: "workspace suga", path: filepath.Join(workspace, ".suga", "output-rules.yaml"), want: approvalrules.Deny},
		{name: "suga home", path: filepath.Join(sugaHome, "sessions.db"), want: approvalrules.Deny},
		{name: "user rule file", path: filepath.Join(sugaHome, "approval-rules.yaml"), want: approvalrules.Deny},
		{name: "project rule file", path: ruleFile, want: approvalrules.Deny},
		{name: "ordinary workspace file", path: filepath.Join(workspace, "docs", "note.md"), want: approvalrules.Allow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action, hit, protected := policy.Decide(approvalrules.Target{
				Tool: "file_write", Path: tc.path, Root: workspace, Mode: approvalrules.ModeRun,
			}, tools.Meta{})
			assert.Eq(t, tc.want, action)
			assert.Nil(t, hit)
			// The default-action clamp reports the protection flag too.
			assert.Eq(t, tc.want == approvalrules.Deny, protected)
		})
	}
}
