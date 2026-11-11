package wiring

import (
	"context"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
)

func TestChatApprovalRulesDisabledKeepsToolNameFallback(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, gkconfig.Set("tools.approval_rules", map[string]any{"enabled": false, "persist_ask": true}))
	gate, err := newCLIMCPGate(false, tools.ApprovalModeDefault)
	assert.Require(t, assert.NoErr(t, err))
	asker := &scriptedAsker{answers: []string{"a"}}
	approver := newChatApprover(chatApproverOptions{gate: gate, asker: asker, workspace: t.TempDir()})
	tool := &nativeStubTool{name: "shell_exec"}
	assert.NoErr(t, approver.Decide(context.Background(), agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": "echo one"}}, tool))
	assert.NoErr(t, approver.Decide(context.Background(), agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{"command": "echo two"}}, tool))
	assert.Len(t, asker.questions, 1)
}
