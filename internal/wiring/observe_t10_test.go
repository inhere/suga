package wiring

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

type t10ObservedTool struct{}

func (t10ObservedTool) Name() string        { return "act" }
func (t10ObservedTool) Description() string { return "test tool" }
func (t10ObservedTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: "act", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}},
	}}
}
func (t10ObservedTool) Kind() tools.Kind { return tools.KindNative }
func (t10ObservedTool) Execute(context.Context, api.ToolCall) (*agentcore.ToolResult, error) {
	return &agentcore.ToolResult{Content: "acted"}, nil
}

func TestApprovalRuleAttrsUseWiredPolicyAndObservePath(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	loaded := ApprovalRules{
		Workspace: t.TempDir(), Settings: appconfig.DefaultApprovalRulesSettings(),
		User: ApprovalRuleSource{Rules: []approvalrules.Rule{{Source: approvalrules.SourceUser, Index: 3, Tool: "act", Action: approvalrules.Allow}}},
	}
	policy := compileApprovalPolicy(approvalrules.ModeRun, false, false, loaded, nil, tools.ApprovalModeDefault)
	registry := tools.NewRegistry()
	registry.Register(t10ObservedTool{})
	runner, err := personal.New(personal.Config{
		Repository: repo,
		Provider:   mock.NewProvider(mock.ToolCall("call-1", "act", map[string]any{}), mock.Final("done")),
		Tools:      tools.NewApproval(registry, newPolicyApprover(policy)),
	})
	assert.Require(t, assert.NoErr(t, err))
	result, err := runner.Run(context.Background(), personal.RunRequest{Prompt: "run"})
	assert.Require(t, assert.NoErr(t, err))
	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: result.SessionID, Kinds: []string{store.EventTool}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, events, 1))
	assert.Eq(t, "rule", events[0].Attrs["approval"])
	assert.Eq(t, "user#3 act action=allow", events[0].Attrs["approval_rule"])
	assert.NotContains(t, events[0].Attrs, "approval_scope")
}

func TestDeniedApprovalRuleAttrsUseWiredPolicy(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	loaded := ApprovalRules{
		Workspace: t.TempDir(), Settings: appconfig.DefaultApprovalRulesSettings(),
		User: ApprovalRuleSource{Rules: []approvalrules.Rule{{Source: approvalrules.SourceProject, Index: 2, Tool: "act", Action: approvalrules.Deny}}},
	}
	policy := compileApprovalPolicy(approvalrules.ModeRun, false, false, loaded, nil, tools.ApprovalModeDefault)
	registry := tools.NewRegistry()
	registry.Register(t10ObservedTool{})
	runner, err := personal.New(personal.Config{
		Repository: repo,
		Provider:   mock.NewProvider(mock.ToolCall("call-2", "act", map[string]any{}), mock.Final("done")),
		Tools:      tools.NewApproval(registry, newPolicyApprover(policy)),
	})
	assert.Require(t, assert.NoErr(t, err))
	result, err := runner.Run(context.Background(), personal.RunRequest{Prompt: "run"})
	assert.Require(t, assert.NoErr(t, err))
	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: result.SessionID, Kinds: []string{store.EventDenial}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, events, 1))
	assert.Eq(t, "project#2 act action=deny", events[0].Attrs["approval_rule"])
}
