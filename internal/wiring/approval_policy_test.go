package wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

func TestCompileLegacyMCPRules(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"mcp_read", "srv/mcp_*"}))
	assert.NoErr(t, gkconfig.Set("tools.read_only_allow", []string{"srv/mcp_read"}))

	run, err := compileLegacyMCPRules(false)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, run, 2))
	assert.Eq(t, "*/mcp_read", run[0].Tool)
	assert.Eq(t, "srv/mcp_*", run[1].Tool)
	for _, rule := range run {
		assert.Eq(t, approvalrules.SourceLegacy, rule.Source)
		assert.Eq(t, approvalrules.Allow, rule.Action)
		assert.Eq(t, []approvalrules.Mode{approvalrules.ModeChat, approvalrules.ModeRun}, rule.Modes)
	}

	readOnly, err := compileLegacyMCPRules(true)
	assert.Require(t, assert.NoError(t, err))
	assert.Require(t, assert.Len(t, readOnly, 1))
	assert.Eq(t, "srv/mcp_read", readOnly[0].Tool)
}

func TestApprovalPolicyDefaultsAndLegacyIsolation(t *testing.T) {
	loaded := ApprovalRules{Settings: appconfig.DefaultApprovalRulesSettings()}
	nativeTarget := approvalrules.Target{Tool: "shell_exec"}
	mcpTarget := approvalrules.Target{Tool: "query", MCP: true, MCPServer: "srv"}
	readOnlyMeta := tools.Meta{ReadOnly: true}
	agentStateMeta := tools.Meta{AgentState: true}

	tests := []struct {
		name     string
		mode     approvalrules.Mode
		chatAuto bool
		target   approvalrules.Target
		meta     tools.Meta
		want     approvalrules.Action
	}{
		{name: "chat asks", mode: approvalrules.ModeChat, target: nativeTarget, want: approvalrules.Ask},
		{name: "chat auto allows", mode: approvalrules.ModeChat, chatAuto: true, target: nativeTarget, want: approvalrules.Allow},
		{name: "run allows native", mode: approvalrules.ModeRun, target: nativeTarget, want: approvalrules.Allow},
		{name: "ACP asks native", mode: approvalrules.ModeACP, target: nativeTarget, want: approvalrules.Ask},
		{name: "run denies MCP", mode: approvalrules.ModeRun, target: mcpTarget, want: approvalrules.Deny},
		{name: "ACP asks MCP", mode: approvalrules.ModeACP, target: mcpTarget, want: approvalrules.Ask},
		{name: "read-only tool allows", mode: approvalrules.ModeRun, target: nativeTarget, meta: readOnlyMeta, want: approvalrules.Allow},
		{name: "agent state allows", mode: approvalrules.ModeRun, target: nativeTarget, meta: agentStateMeta, want: approvalrules.Allow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := compileApprovalPolicy(tc.mode, tc.chatAuto, false, loaded, nil, tools.ApprovalModeDefault)
			action, hit, _ := policy.Decide(tc.target, tc.meta)
			assert.Eq(t, tc.want, action)
			assert.Nil(t, hit)
		})
	}

	legacy := []approvalrules.Rule{{Source: approvalrules.SourceLegacy, Index: 1, Tool: "srv/query", Action: approvalrules.Allow}}
	cli := compileApprovalPolicy(approvalrules.ModeRun, false, false, loaded, legacy, tools.ApprovalModeDefault)
	action, hit, _ := cli.Decide(mcpTarget, tools.Meta{})
	assert.Eq(t, approvalrules.Allow, action)
	assert.Eq(t, approvalrules.SourceLegacy, hit.Source)

	acp := compileApprovalPolicy(approvalrules.ModeACP, false, false, loaded, nil, tools.ApprovalModeDefault)
	action, hit, _ = acp.Decide(mcpTarget, tools.Meta{})
	assert.Eq(t, approvalrules.Ask, action)
	assert.Nil(t, hit)
}

func TestApprovalPolicyExplicitDenyOverridesLegacyAndReadOnlyClamp(t *testing.T) {
	loaded := ApprovalRules{
		Settings: appconfig.DefaultApprovalRulesSettings(),
		User: ApprovalRuleSource{Rules: []approvalrules.Rule{{
			Source: approvalrules.SourceUser, Index: 1, Tool: "srv/query", Action: approvalrules.Deny,
		}}},
	}
	legacy := []approvalrules.Rule{{Source: approvalrules.SourceLegacy, Index: 1, Tool: "srv/query", Action: approvalrules.Allow}}
	policy := compileApprovalPolicy(approvalrules.ModeRun, false, false, loaded, legacy, tools.ApprovalModeDefault)
	action, hit, _ := policy.Decide(approvalrules.Target{Tool: "query", MCP: true, MCPServer: "srv"}, tools.Meta{})
	assert.Eq(t, approvalrules.Deny, action)
	assert.Eq(t, approvalrules.SourceUser, hit.Source)

	loaded.User.Rules[0].Action = approvalrules.Allow
	readOnly := compileApprovalPolicy(approvalrules.ModeRun, false, true, loaded, legacy, tools.ApprovalModeDefault)
	action, hit, _ = readOnly.Decide(approvalrules.Target{Tool: "query", MCP: true, MCPServer: "srv"}, tools.Meta{})
	assert.Eq(t, approvalrules.Deny, action)
	assert.Eq(t, approvalrules.SourceUser, hit.Source)
}

func TestCompileCLIApprovalPolicyLoadsRulesAndKeepsACPSeparate(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("SUGA_HOME", home)
	assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"srv/query"}))
	assert.NoErr(t, gkconfig.Set("tools.approval_rules", map[string]any{"enabled": true}))
	userFile := filepath.Join(home, appconfig.ApprovalRulesFileName)
	assert.NoErr(t, os.WriteFile(userFile, []byte("rules:\n  - tool: srv/query\n    action: deny\n"), 0o600))

	cli, err := compileCLIApprovalPolicy(workspace, approvalrules.ModeRun, false, false, tools.ApprovalModeDefault)
	assert.Require(t, assert.NoError(t, err))
	action, hit, _ := cli.Decide(approvalrules.Target{Tool: "query", MCP: true, MCPServer: "srv"}, tools.Meta{})
	assert.Eq(t, approvalrules.Deny, action)
	assert.Eq(t, approvalrules.SourceUser, hit.Source)

	acp, err := compileACPApprovalPolicy(workspace, false, tools.ApprovalModeDefault)
	assert.Require(t, assert.NoError(t, err))
	action, hit, _ = acp.Decide(approvalrules.Target{Tool: "query", MCP: true, MCPServer: "srv"}, tools.Meta{})
	assert.Eq(t, approvalrules.Deny, action)
	assert.Eq(t, approvalrules.SourceUser, hit.Source)

	// The ACP result above comes only from the explicit deny; removing the file
	// demonstrates that the legacy allowlist itself is not read by ACP.
	assert.NoErr(t, os.Remove(userFile))
	acp, err = compileACPApprovalPolicy(workspace, false, tools.ApprovalModeDefault)
	assert.Require(t, assert.NoError(t, err))
	action, hit, _ = acp.Decide(approvalrules.Target{Tool: "query", MCP: true, MCPServer: "srv"}, tools.Meta{})
	assert.Eq(t, approvalrules.Ask, action)
	assert.Nil(t, hit)

	// Keep this test on the real gate path as well: a legacy allowlist entry
	// authorizes an MCP call without changing native tool registration.
	legacyPolicy := compileApprovalPolicy(approvalrules.ModeRun, false, false,
		ApprovalRules{Settings: appconfig.DefaultApprovalRulesSettings()},
		[]approvalrules.Rule{{Source: approvalrules.SourceLegacy, Index: 1, Tool: "srv/query", Action: approvalrules.Allow}}, tools.ApprovalModeDefault)
	gate := newCLIMCPGateWithPolicy(legacyPolicy)
	assert.NoErr(t, gate.Decide(context.Background(), agentcore.ToolCall{Name: "query"}, newServerStubTool("srv", "query")))
}

func TestCLIMCPGateAskFailsClosedWithoutTerminal(t *testing.T) {
	policy := compileApprovalPolicy(approvalrules.ModeRun, false, false, ApprovalRules{
		Settings: appconfig.DefaultApprovalRulesSettings(),
		User: ApprovalRuleSource{Rules: []approvalrules.Rule{{
			Source: approvalrules.SourceUser, Index: 1, Tool: "srv/query", Action: approvalrules.Ask,
		}}},
	}, []approvalrules.Rule{{Source: approvalrules.SourceLegacy, Index: 1, Tool: "srv/query", Action: approvalrules.Allow}}, tools.ApprovalModeDefault)
	gate := newCLIMCPGateWithPolicy(policy)
	err := gate.Decide(context.Background(), agentcore.ToolCall{Name: "query"}, newServerStubTool("srv", "query"))
	assert.ErrIs(t, err, tools.ErrDenied)
}

// approvalPolicy.Decide reports protected for both clamp sites: a broad allow
// rule matching a protected path, and the default action (no rule matched).
func TestApprovalPolicyDecideReportsProtected(t *testing.T) {
	workspace, sugaHome := t.TempDir(), t.TempDir()
	broadAllow := []approvalrules.Rule{{Tool: "file_write", Path: []string{"**"}, Root: workspace, Action: approvalrules.Allow}}
	newPolicy := func(rules []approvalrules.Rule, native approvalrules.Action) approvalPolicy {
		return approvalPolicy{
			Mode: approvalrules.ModeChat, Workspace: workspace, SugaHome: sugaHome, Rules: rules,
			Defaults: approvalDefaults{Native: native},
		}
	}
	target := func(path string) approvalrules.Target {
		// targetFor fills Workspace/SugaHome in real calls; the rule-path clamp reads them from the target.
		return approvalrules.Target{Tool: "file_write", Root: workspace, Path: path, Workspace: workspace, SugaHome: sugaHome}
	}
	t.Run("allow rule on protected path", func(t *testing.T) {
		action, hit, protected := newPolicy(broadAllow, approvalrules.Ask).Decide(target(".git/hooks/x"), tools.Meta{})
		assert.Eq(t, approvalrules.Ask, action)
		assert.NotNil(t, hit)
		assert.True(t, protected)
	})
	t.Run("allow rule on ordinary path", func(t *testing.T) {
		action, _, protected := newPolicy(broadAllow, approvalrules.Ask).Decide(target("docs/a.txt"), tools.Meta{})
		assert.Eq(t, approvalrules.Allow, action)
		assert.False(t, protected)
	})
	t.Run("default allow on protected path", func(t *testing.T) {
		action, hit, protected := newPolicy(nil, approvalrules.Allow).Decide(target(".git/hooks/x"), tools.Meta{})
		assert.Eq(t, approvalrules.Ask, action)
		assert.Nil(t, hit)
		assert.True(t, protected)
	})
	t.Run("default allow on ordinary path", func(t *testing.T) {
		action, _, protected := newPolicy(nil, approvalrules.Allow).Decide(target("docs/a.txt"), tools.Meta{})
		assert.Eq(t, approvalrules.Allow, action)
		assert.False(t, protected)
	})
	t.Run("ordinary ask is not protected", func(t *testing.T) {
		action, _, protected := newPolicy(nil, approvalrules.Ask).Decide(target(".git/hooks/x"), tools.Meta{})
		assert.Eq(t, approvalrules.Ask, action)
		assert.False(t, protected)
	})
}
