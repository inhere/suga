package chat

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

func TestChatModelCommandUsesNextTurnAndPersists(t *testing.T) {
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := personal.New(personal.Config{Repository: repo, Provider: mock.NewProvider(mock.Final("after-switch")),
		ProviderName: "fake", Model: "m1", ContextWindow: 128000, MaxOutputTokens: 100})
	assert.Require(t, assert.NoErr(t, err))
	var stdout, stderr bytes.Buffer
	called := false
	err = Run(context.Background(), runner, Options{
		Thinking: "low",
		ModelOptions: []ModelOption{{Provider: "fake", Model: "m1", Thinking: "low", ContextWindow: 128000, MaxOutputTokens: 100},
			{Provider: "fake", Model: "m2", Thinking: "high", ContextWindow: 64000, MaxOutputTokens: 42}},
		ModelSwitch: func(ctx context.Context, sessionID, ref string) (ModelOption, error) {
			called = true
			assert.Eq(t, "fake/m2", ref)
			err := runner.SwitchModel(ctx, sessionID, mock.NewProvider(mock.Final("switched")), "fake", "m2", 64000, 42)
			assert.NoErr(t, err)
			return ModelOption{Provider: "fake", Model: "m2", Thinking: "high", ContextWindow: 64000, MaxOutputTokens: 42}, err
		},
	}, strings.NewReader(":model\n:model fake/m2\nhello\n:quit\n"), &stdout, &stderr, nil)
	assert.NoErr(t, err)
	assert.True(t, called)
	out := stdout.String()
	assert.StrContains(t, out, "当前模型：fake/m1")
	assert.StrContains(t, out, "* fake/m1")
	assert.StrContains(t, out, "  fake/m2")
	assert.StrContains(t, out, "已切换模型：fake/m2（下一回合生效）")
	assert.StrContains(t, out, "switched")
	assert.StrContains(t, out, "fake/m2")

	sessions, err := repo.ListSessions(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(sessions)))
	assert.Eq(t, "fake", sessions[0].Meta["provider"])
	assert.Eq(t, "m2", sessions[0].Meta["model"])
	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: sessions[0].ID, Kinds: []string{store.EventSession}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(events)))
	assert.Eq(t, "model_switch", events[0].Name)
	assert.Eq(t, "fake/m1", events[0].Attrs["from"])
	assert.Eq(t, "fake/m2", events[0].Attrs["to"])
}

func TestBuildChatModelIncludesEffectiveLimits(t *testing.T) {
	text := BuildChatModel(testTranslator, ModelOption{Provider: "p", Model: "m", Thinking: "high", ContextWindow: 64000, MaxOutputTokens: 42},
		[]ModelOption{{Provider: "p", Model: "m"}, {Provider: "p", Model: "other"}})
	assert.StrContains(t, text, "思考级别：high")
	assert.StrContains(t, text, "上下文窗口：64000")
	assert.StrContains(t, text, "输出上限 max_output_tokens：42")
	assert.StrContains(t, text, "* p/m")
	assert.StrContains(t, text, "  p/other")
}
