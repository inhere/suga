package personal

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// TestSessionOriginRecordedAtCreation: the entry point names itself in
// meta.origin, resuming a session leaves the origin alone, and a chat that
// continues a run or ACP session marks it used interactively - which is what
// `suga data gc --retention` classifies by (design 0.3, 会话来源).
func TestSessionOriginRecordedAtCreation(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(), Model: "m", ProviderName: "p"})
	assert.Require(t, assert.NoErr(t, err))

	t.Run("every entry point records its origin and resume keeps it", func(t *testing.T) {
		for _, tc := range []struct{ origin, want string }{
			{OriginChat, store.OriginChat},
			{OriginRun, store.OriginRun},
			{OriginACP, store.OriginACP},
		} {
			model, err := runner.NewSessionFor(ctx, "", "t-"+tc.origin, tc.origin)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, tc.want, store.SessionOrigin(model))
			stored, err := repo.GetSession(ctx, model.ID)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, tc.want, store.SessionOrigin(stored))

			again, err := runner.LoadSession(ctx, model.ID)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, tc.want, store.SessionOrigin(again))
		}
	})

	t.Run("an empty origin stores nothing", func(t *testing.T) {
		model, err := runner.NewSession(ctx, "", "plain")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "", store.SessionOrigin(model))
		assert.True(t, store.InteractiveSession(model), "a session without an origin counts as interactive")
	})

	t.Run("only a chat marks a non-interactive session used interactively", func(t *testing.T) {
		run, err := runner.NewSessionFor(ctx, "", "batch", OriginRun)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, store.InteractiveSession(run))

		assert.NoErr(t, runner.MarkSessionUsedInteractively(ctx, run.ID))
		stored, err := repo.GetSession(ctx, run.ID)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, store.SessionUsedInteractively(stored))
		assert.True(t, store.InteractiveSession(stored))

		// Idempotent; and a chat, a sub-agent and a historical session need no
		// marker.
		assert.NoErr(t, runner.MarkSessionUsedInteractively(ctx, run.ID))
		chat, err := runner.NewSessionFor(ctx, "", "chat", OriginChat)
		assert.Require(t, assert.NoErr(t, err))
		assert.NoErr(t, runner.MarkSessionUsedInteractively(ctx, chat.ID))
		stored, err = repo.GetSession(ctx, chat.ID)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, store.SessionUsedInteractively(stored))
	})
}
