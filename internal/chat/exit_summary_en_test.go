package chat

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// The English side of the chat text migrated last (T-L): the exit summary, the
// budget lines of a turn and the warning of a delivery a model without vision
// takes. Every case drives the caller chat itself uses (chat.Run, the renderer
// the turn feeds, the session socket's delivery endpoint) with an English
// catalog injected, so a text that never reaches the translator fails here.

// TestChatENExitSummary: a chat started with the English catalog leaves the exit
// summary in English, while the resume command stays exactly as it is (it is
// copied and run as it is).
func TestChatENExitSummary(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := shortTestHome(t)
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: mock.NewProvider(mock.Final("ok")),
		ProcessRoot: filepath.Join(home, "run"),
	})
	assert.Require(t, assert.NoErr(t, err))
	ctx := context.Background()
	model, err := runner.NewSession(ctx, "sess-en-exit", "New chat")
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, runner.SaveSessionShortName(ctx, model.ID, "ws-k7"))

	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, runner, Options{SessionID: model.ID, Translator: enTranslator(t)}, reader, &stdout, &stderr, nil)
	}()
	// One turn, so the summary counts it and reports the tokens it used.
	_, err = io.WriteString(writer, "hi\n:quit\n")
	assert.NoErr(t, err)
	assert.NoErr(t, writer.Close())
	assert.NoErr(t, <-done)

	got := stderr.String()
	assert.StrContains(t, got, "turns this chat: 1 · in ")
	assert.StrContains(t, got, " tokens · ran ")
	assert.StrContains(t, got, "session ws-k7 · resume: suga chat --resume "+model.ID)
	assert.NotContains(t, got, "本次")
	assert.NotContains(t, got, "续接：")
	assert.NotContains(t, got, "运行 ")
}

// TestChatENBudgetLines: the budget notice printed while a turn streams and the
// wrap-up reason of the status line are English, through the renderer the turn's
// events feed and the builder its status line calls.
func TestChatENBudgetLines(t *testing.T) {
	fixedClock := func() time.Time { return time.Unix(0, 0) }

	t.Run("the notice and the wrap-up reason of the status line", func(t *testing.T) {
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Model: "m", Translator: enTranslator(t), now: fixedClock})
		r.BeginTurn()
		r.Handle(personal.Event{Kind: "usage", Usage: &personal.UsageUpdate{Budget: &personal.BudgetSnapshot{
			Scope: "session", NoticeRatio: 0.8, Exceeded: true, Reason: "max_tokens",
			Tokens: personal.BudgetMetric{Limit: 100, Used: 90},
		}}})
		r.Status(personal.RunResult{}, time.Second, 0)

		got := out.String()
		assert.StrContains(t, got, "budget notice: session budget already used 90%")
		assert.StrContains(t, got, "this turn wrapped up because the token budget ran out (100/90)")
		assert.NotContains(t, got, "预算提醒")
		assert.NotContains(t, got, "预算用尽收尾")
	})

	t.Run("the cost and the generic wrap-up reason", func(t *testing.T) {
		statusLine := func(snapshot *personal.BudgetSnapshot) string {
			lines := BuildTurnStatus(TurnStatus{Model: "m", Budget: snapshot, Translator: enTranslator(t)})
			assert.Require(t, len(lines) > 0)
			return lines[len(lines)-1].Text
		}
		assert.StrContains(t, statusLine(&personal.BudgetSnapshot{Scope: "turn", Exceeded: true, Reason: "max_cost_usd",
			CostUSD: personal.BudgetMetric{Limit: 1.25, Used: 1.5}}),
			"this turn wrapped up because the cost budget ran out ($1.2500/$1.5000)")
		assert.StrContains(t, statusLine(&personal.BudgetSnapshot{Scope: "turn", Exceeded: true, Reason: "unknown"}),
			"this turn wrapped up because the budget ran out")
	})
}

// TestChatENDeliveryNoVisionWarning: the warning a sender of `suga send --image`
// gets from an English chat whose model cannot see is English, through the
// delivery endpoint of the session socket.
func TestChatENDeliveryNoVisionWarning(t *testing.T) {
	sessionsDir := t.TempDir()
	h := newSendImageHarness(t, newToolGateProv(), sessionsDir, false)
	h.state.translator = enTranslator(t)
	h.state.modelInfo = ModelOption{Provider: "p", Model: "m"}
	ref := deliveryImageFile(t, sessionsDir, h.sessID, []byte("picture bytes"))

	delivery, _, err := h.state.deliverMessage(personalprocess.ModeFollowUp, "look at this", "cli", "", 0,
		[]personalprocess.ImageRef{ref})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "model p/m cannot see images; the 1 attached image(s) were replaced with text notes",
		delivery.Warning)
	assert.NotContains(t, delivery.Warning, "不支持看图")
}
