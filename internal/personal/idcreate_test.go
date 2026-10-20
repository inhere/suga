package personal

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// TestRunRetriesTakenTurnID checks that a turn whose generated id is already
// taken is inserted under a fresh id, and that the run result and the turn's
// events name the row that was actually stored.
func TestRunRetriesTakenTurnID(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(
		mock.Final("one"), mock.Final("two"), mock.Final("three"),
	)})
	assert.Require(t, assert.NoErr(t, err))
	first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
	assert.Require(t, assert.NoErr(t, err))

	restore := newTurnID
	t.Cleanup(func() { newTurnID = restore })

	t.Run("a taken id is replaced", func(t *testing.T) {
		calls := 0
		newTurnID = func() string {
			calls++
			if calls <= 2 {
				return first.TurnID
			}
			return restore()
		}
		second, err := runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "two"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 3, calls)
		assert.Eq(t, true, second.TurnID != first.TurnID)
		_, err = repo.GetTurn(ctx, second.TurnID)
		assert.NoErr(t, err)
		// The turn event names the stored turn, not the discarded candidate.
		events, err := repo.ListEvents(ctx, store.EventFilter{
			SessionID: first.SessionID, TurnID: second.TurnID, Kinds: []string{store.EventTurn},
		})
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(events))
	})

	t.Run("three taken ids fail the turn", func(t *testing.T) {
		calls := 0
		newTurnID = func() string {
			calls++
			return first.TurnID
		}
		_, err := runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "three"})
		assert.Require(t, assert.NotNil(t, err))
		assert.Eq(t, 3, calls)
		assert.StrContains(t, err.Error(), "create turn")
	})
}

// TestNewSessionRetriesTakenSessionID checks the same retry for created
// sessions, and that an id the caller supplied is never retried.
func TestNewSessionRetriesTakenSessionID(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider()})
	assert.Require(t, assert.NoErr(t, err))
	existing, err := runner.NewSession(ctx, "s_taken000000", "existing")
	assert.Require(t, assert.NoErr(t, err))

	restore := newSessionID
	t.Cleanup(func() { newSessionID = restore })

	t.Run("a taken generated id is replaced", func(t *testing.T) {
		calls := 0
		newSessionID = func() string {
			calls++
			if calls == 1 {
				return existing.ID
			}
			return restore()
		}
		model, err := runner.NewSession(ctx, "", "retry")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, calls)
		assert.Eq(t, true, model.ID != existing.ID)
		_, err = repo.GetSession(ctx, model.ID)
		assert.NoErr(t, err)
	})

	t.Run("three taken ids fail the create", func(t *testing.T) {
		calls := 0
		newSessionID = func() string {
			calls++
			return existing.ID
		}
		_, err := runner.NewSession(ctx, "", "collide")
		assert.Require(t, assert.NotNil(t, err))
		assert.Eq(t, 3, calls)
		assert.StrContains(t, err.Error(), "create personal session")
	})

	t.Run("a supplied id is not retried", func(t *testing.T) {
		calls := 0
		newSessionID = func() string {
			calls++
			return restore()
		}
		_, err := runner.NewSession(ctx, existing.ID, "again")
		assert.Require(t, assert.NotNil(t, err))
		assert.Eq(t, 0, calls)
	})
}
