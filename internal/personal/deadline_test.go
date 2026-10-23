package personal

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// pins a fixed reading, so the wording of the hint (minutes, near-deadline
// state) is deterministic.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// deadlineTestState builds the state a runner would have with
// DeadlineEnv set to an env var that carries deadline.
func deadlineTestState(t *testing.T, envName, deadline string, now func() time.Time) *deadlineState {
	t.Helper()
	if deadline != "" {
		t.Setenv(envName, deadline)
	}
	return newDeadlineState(envName, now)
}

func TestDeadlineHintDisabledWithoutConfig(t *testing.T) {
	t.Run("no config value attaches nothing", func(t *testing.T) {
		state := newDeadlineState("", nil)
		assert.Eq(t, "", state.hint())
	})
	t.Run("configured but env var missing attaches nothing", func(t *testing.T) {
		t.Setenv("SUGA_TEST_DEADLINE", "")
		state := deadlineTestState(t, "SUGA_TEST_DEADLINE", "", nil)
		assert.Eq(t, "", state.hint())
	})
	t.Run("configured but env var not RFC3339 attaches nothing", func(t *testing.T) {
		state := deadlineTestState(t, "SUGA_TEST_DEADLINE", "ten past ten", nil)
		assert.Eq(t, "", state.hint())
	})
}

func TestDeadlineHintWording(t *testing.T) {
	deadline := time.Date(2026, 1, 1, 22, 28, 0, 0, time.FixedZone("CST", 8*3600))
	normal := time.Date(2026, 1, 1, 21, 40, 0, 0, time.FixedZone("CST", 8*3600))
	nearFive := time.Date(2026, 1, 1, 22, 25, 0, 0, time.FixedZone("CST", 8*3600))
	nearShare := time.Date(2026, 1, 1, 22, 20, 0, 0, time.FixedZone("CST", 8*3600))
	overdue := time.Date(2026, 1, 1, 23, 30, 0, 0, time.FixedZone("CST", 8*3600))

	t.Run("normal hint states the remaining minutes", func(t *testing.T) {
		state := &deadlineState{deadline: deadline, now: fixedClock(normal)}
		got := state.hint()
		// A 60 minute job 48 minutes in: the wording the design asks for.
		assert.Eq(t, "[时间] 现在 21:40（+08:00），本任务截止 22:28，剩余约 48 分钟。", got)
	})
	t.Run("under five minutes asks for an immediate wrap-up", func(t *testing.T) {
		state := &deadlineState{deadline: deadline, now: fixedClock(nearFive)}
		got := state.hint()
		assert.StrContains(t, got, "即将到达时限：请尽快提交已完成的工作并汇报未完成项")
		assert.StrContains(t, got, "22:28")
		assert.StrContains(t, got, "现在 22:25（+08:00）")
	})
	t.Run("under ten percent of the observed window asks for the same", func(t *testing.T) {
		state := &deadlineState{deadline: deadline, now: fixedClock(nearShare)}
		// The start reference comes from an earlier request of the same run:
		// 21:00 -> the window is 88 minutes and 10% of it is below 8.8, and the
		// remaining 8 minutes are above the five minute floor, so this shows
		// the share branch on its own.
		state.observeStart(time.Date(2026, 1, 1, 21, 0, 0, 0, time.FixedZone("CST", 8*3600)))
		assert.StrContains(t, state.hint(), "即将到达时限")
	})
	t.Run("past the deadline reports the overrun", func(t *testing.T) {
		state := &deadlineState{deadline: deadline, now: fixedClock(overdue)}
		got := state.hint()
		assert.StrContains(t, got, "已超过本任务截止 22:28")
		assert.StrContains(t, got, "已超时")
	})
	t.Run("the hint never appears when the deadline is unavailable", func(t *testing.T) {
		state := &deadlineState{now: fixedClock(normal)}
		assert.Eq(t, "", state.hint())
	})
}

// The hook is a BeforeModel hook of the real runner path: every model request
// carries the hint as its last message, the stored messages do not, and the
// prefix tracker (which runs before the hint) keeps reporting a stable
// append-only prefix.
func TestDeadlineHintThroughRunner(t *testing.T) {
	t.Run("configured: attached per request, not stored, prefix unchanged", func(t *testing.T) {
		ctx := context.Background()
		repo, err := store.Open(newStorePath(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		provider := &nextProvider{}
		t.Setenv("SUGA_TEST_DEADLINE", "2026-01-01T22:28:00+08:00")
		now := fixedClock(time.Date(2026, 1, 1, 21, 40, 0, 0, time.FixedZone("CST", 8*3600)))
		runner, err := New(Config{Repository: repo, Provider: provider,
			DeadlineEnv: "SUGA_TEST_DEADLINE", Now: now})
		assert.Require(t, assert.NoErr(t, err))
		first, err := runner.Run(ctx, RunRequest{Prompt: "first"})
		assert.Require(t, assert.NoErr(t, err))
		second, err := runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "second"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, provider.requests, 2))
		for _, req := range provider.requests {
			msgs := req.Messages
			assert.Require(t, len(msgs) > 1)
			assert.Eq(t, "user", msgs[len(msgs)-1].Role)
			assert.StrContains(t, msgs[len(msgs)-1].Content, "[时间] 现在 21:40（+08:00），本任务截止 22:28")
		}
		// The hint does not reach the session storage: LoadMessages holds only
		// the real messages.
		for _, turnID := range []string{first.TurnID, second.TurnID} {
			stored, err := repo.AgentStorage().LoadMessages(ctx, turnID)
			assert.Require(t, assert.NoErr(t, err))
			for _, m := range stored {
				assert.False(t, strings.Contains(m.Content, "[时间] 现在"))
			}
		}
		// Stable prefix: the first request of a configured run, minus its
		// trailing hint, must have exactly the bytes the same request carries
		// without the hint. Run the control turn on a second runner without
		// the deadline configured and compare the digests.
		control := &nextProvider{}
		plainRunner, err := New(Config{Repository: repo, Provider: control})
		assert.Require(t, assert.NoErr(t, err))
		_, err = plainRunner.Run(ctx, RunRequest{Prompt: "first"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, control.requests, 1))
		before := stablePrefixDigest(control.requests[0].System, control.requests[0].Messages)
		after := stablePrefixDigest(provider.requests[0].System, provider.requests[0].Messages[:len(provider.requests[0].Messages)-1])
		assert.Eq(t, before, after)
		// The prefix tracker saw both requests as an append: the hint (added
		// after the tracker hook) did not rewrite any earlier message.
		report, ok := runner.rec.prefix.report(first.SessionID)
		assert.Require(t, ok)
		assert.Eq(t, "append", report.Reason)
	})
	t.Run("unconfigured: no hint is attached", func(t *testing.T) {
		ctx := context.Background()
		repo, err := store.Open(newStorePath(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		provider := &nextProvider{}
		runner, err := New(Config{Repository: repo, Provider: provider})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.NoErr(t, err))
		_, err = runner.Run(ctx, RunRequest{Prompt: "plain"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, provider.requests, 1))
		for _, m := range provider.requests[0].Messages {
			assert.False(t, strings.Contains(m.Content, "[时间] 现在"))
		}
	})
	t.Run("configured but env var missing: no hint, no error", func(t *testing.T) {
		ctx := context.Background()
		repo, err := store.Open(newStorePath(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		provider := &nextProvider{}
		runner, err := New(Config{Repository: repo, Provider: provider, DeadlineEnv: "SUGA_TEST_DEADLINE_GONE"})
		assert.Require(t, assert.NoErr(t, err))
		_, err = runner.Run(ctx, RunRequest{Prompt: "still answer"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, provider.requests, 1))
		for _, m := range provider.requests[0].Messages {
			assert.False(t, strings.Contains(m.Content, "[时间] 现在"))
		}
	})
}

// TestDeadlineHookStableWithinTurn: inside one turn the hint keeps the same
// bytes and the same position across model requests, so a provider that binds
// thinking signatures to the request prefix (Anthropic 2026) keeps accepting
// an earlier step's replayed thinking blocks (2026-10-23 claude-haiku-5-5).
func TestDeadlineHookStableWithinTurn(t *testing.T) {
	state := &deadlineState{deadline: time.Date(2026, 1, 1, 22, 28, 0, 0, time.FixedZone("CST", 8*3600)),
		now: fixedClock(time.Date(2026, 1, 1, 21, 40, 0, 0, time.FixedZone("CST", 8*3600)))}
	t.Run("text frozen and repositioned after the turn's prompt on replay", func(t *testing.T) {
		hook := &deadlineHook{state: state, turnAt: -1}
		ctx := context.Background()
		// First request: the plain prompt; the hint is appended.
		first := &agentcore.ModelEvent{Request: &agentcore.GenerateRequest{Messages: []agentcore.Message{
			{Role: "user", Content: "start"}}}}
		assert.Require(t, assert.NoErr(t, hook.BeforeModel(ctx, first, agentcore.NewControl())))
		assert.Eq(t, 2, len(first.Request.Messages))
		assert.StrContains(t, first.Request.Messages[1].Content, "[时间] 现在 21:40（+08:00），本任务截止 22:28")
		// Second request of the same turn: tool calls and results are stored
		// behind the prompt and a minute has passed; the hint must stay the
		// same bytes at the same place, in front of the stored messages.
		second := &agentcore.ModelEvent{Request: &agentcore.GenerateRequest{Messages: []agentcore.Message{
			{Role: "user", Content: "start"},
			{Role: "assistant", ToolCall: &agentcore.ToolCall{CallID: "t1", Name: "file_read"}},
			{Role: "tool", ToolResult: &agentcore.ToolResult{CallID: "t1", Content: "x"}}}}}
		assert.Require(t, assert.NoErr(t, hook.BeforeModel(ctx, second, agentcore.NewControl())))
		assert.Eq(t, first.Request.Messages[1].Content, second.Request.Messages[1].Content)
		assert.Eq(t, "user", second.Request.Messages[1].Role)
		assert.Eq(t, "assistant", second.Request.Messages[2].Role)
		assert.Eq(t, 4, len(second.Request.Messages))
		// A wrap-up step: the runtime appends a request-only instruction at
		// the end. The hint must not move behind it - the earlier steps'
		// thinking was created with the hint right after the prompt.
		third := &agentcore.ModelEvent{Request: &agentcore.GenerateRequest{Messages: []agentcore.Message{
			{Role: "user", Content: "start"},
			{Role: "assistant", ToolCall: &agentcore.ToolCall{CallID: "t1", Name: "file_read"}},
			{Role: "tool", ToolResult: &agentcore.ToolResult{CallID: "t1", Content: "x"}},
			{Role: "user", Content: "请收尾"}}}}
		assert.Require(t, assert.NoErr(t, hook.BeforeModel(ctx, third, agentcore.NewControl())))
		assert.Eq(t, 5, len(third.Request.Messages))
		assert.Eq(t, first.Request.Messages[1].Content, third.Request.Messages[1].Content)
		assert.Eq(t, "请收尾", third.Request.Messages[4].Content)
	})
	t.Run("a disabled hint stays out and freezes nothing", func(t *testing.T) {
		hook := &deadlineHook{state: &deadlineState{}, turnAt: -1}
		event := &agentcore.ModelEvent{Request: &agentcore.GenerateRequest{Messages: []agentcore.Message{
			{Role: "user", Content: "start"}}}}
		assert.Require(t, assert.NoErr(t, hook.BeforeModel(context.Background(), event, agentcore.NewControl())))
		assert.Eq(t, 1, len(event.Request.Messages))
	})
}

// stablePrefixDigest hashes the stable prefix of a request: the system
// prompt and every message, as bytes. The configured run's digest over its
// messages minus the trailing hint must equal the unconfigured run's digest
// over all of its messages - the hint changes nothing before it.
func stablePrefixDigest(system string, msgs []agentcore.Message) [32]byte {
	h := sha256.New()
	h.Write([]byte(system))
	for _, m := range msgs {
		raw, _ := json.Marshal(m)
		h.Write(raw)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
