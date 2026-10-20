package personal

import (
	"context"
	"testing"
	"time"

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

// TestOldFormatIDsStayUsable: a session and a turn written by an older version
// (sess_... / turn_...) are loaded and continued like a new one; nothing in the
// read path looks at the id format.
func TestOldFormatIDsStayUsable(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(
		mock.Final("ok"), mock.Final("again"),
	)})
	assert.Require(t, assert.NoErr(t, err))
	const oldSession = "sess_261020_1iu81hh7i8v0014"
	const oldTurn = "turn_261020_1iu81hh7i8v0015"
	created, err := runner.NewSession(ctx, oldSession, "old")
	assert.Require(t, assert.NoErr(t, err))
	at := time.Now().UTC().Add(-time.Minute)
	assert.NoErr(t, repo.CreateTurn(ctx, store.Turn{
		ID: oldTurn, SessionID: oldSession, Status: store.TurnCompleted, StartedAt: at, EndedAt: at,
	}))

	loaded, err := runner.LoadSession(ctx, oldSession)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, created.ID, loaded.ID)
	res, err := runner.Run(ctx, RunRequest{SessionID: oldSession, Prompt: "next"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, oldSession, res.SessionID)

	// The old turn stays first and the new one follows it.
	turns, err := repo.ListTurns(ctx, oldSession)
	assert.NoErr(t, err)
	assert.Require(t, assert.Len(t, turns, 2))
	assert.Eq(t, oldTurn, turns[0].ID)
	assert.Eq(t, res.TurnID, turns[1].ID)
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
