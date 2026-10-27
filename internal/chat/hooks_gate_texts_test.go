package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// TestToolGateModelTexts pins the model-facing refusals of the PreToolUse hook
// (i18n phase 1b): the historical Chinese text without an injected catalog,
// English with one, and the untouched call shape.
func TestToolGateModelTexts(t *testing.T) {
	call := agentcore.ToolCall{CallID: "c1", Name: "file_write", Arguments: map[string]any{"path": "docs/a.txt"}}
	run := func(t *testing.T, tr i18n.Translator, gate *gateTestClient) error {
		t.Helper()
		registry := tools.NewRegistry()
		registry.Register(&gateTestTool{})
		approval := tools.NewApproval(registry, tools.ApproverFunc(func(context.Context, agentcore.ToolCall, agentcore.Tool) error { return nil }))
		approval.SetRewriter(newToolGate(gate, &gateTestRecorder{}, tr))
		_, err := approval.ExecuteTool(context.Background(), call)
		assert.Require(t, assert.Err(t, err))
		return err
	}
	deny := func() *gateTestClient {
		return &gateTestClient{tool: "file_write", response: sugahooks.Response{Decision: "deny"}}
	}

	t.Run("no injected catalog keeps the historical text", func(t *testing.T) {
		assert.Eq(t, "调用前 hook 拒绝执行（file_write）", run(t, nil, deny()).Error())
	})
	t.Run("language zh keeps the historical text", func(t *testing.T) {
		zh, err := i18n.New(i18n.LanguageZH)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "调用前 hook 拒绝执行（file_write）", run(t, zh, deny()).Error())
	})
	t.Run("language en translates the refusal", func(t *testing.T) {
		en, err := i18n.New(i18n.LanguageEN)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "the PreToolUse hook denied the execution (file_write)", run(t, en, deny()).Error())
	})
	t.Run("a hook failure says so, and the tool name stays", func(t *testing.T) {
		en, err := i18n.New(i18n.LanguageEN)
		assert.Require(t, assert.NoErr(t, err))
		failing := &gateTestClient{tool: "file_write", err: errors.New("hook command timed out after 3s")}
		msg := run(t, en, failing).Error()
		assert.StrContains(t, msg, "the PreToolUse hook could not confirm, so file_write was not executed")
		assert.StrContains(t, msg, "hook command timed out")
		assert.False(t, strings.Contains(msg, "调用前 hook"))
	})
}
