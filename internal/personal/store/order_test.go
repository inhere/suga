package store

import (
	"context"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// TestListTurnsKeepsCreationOrderWithinOneTimestamp: turns created one after
// another with the same stored started_at come back in creation order (rowid),
// never in the order of their random ids. The ids below sort the opposite way
// to the insert order, so an id tiebreaker would reorder them.
func TestListTurnsKeepsCreationOrderWithinOneTimestamp(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	assert.NoErr(t, s.CreateSession(ctx, Session{ID: "s_1", CreatedAt: at, UpdatedAt: at}))
	created := []string{"t_zzzzzzzzzzzz", "t_000000000000", "t_aaaaaaaaaaaa"}
	for _, id := range created {
		assert.NoErr(t, s.CreateTurn(ctx, Turn{ID: id, SessionID: "s_1", StartedAt: at}))
	}
	turns, err := s.ListTurns(ctx, "s_1")
	assert.NoErr(t, err)
	assert.Eq(t, created, turnIDs(turns))
}

// TestListSessionsKeepsCreationOrderWithinOneTimestamp: sessions sharing
// updated_at are listed newest first by creation order, not by id.
func TestListSessionsKeepsCreationOrderWithinOneTimestamp(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	created := []string{"s_zzzzzzzzzzzz", "s_000000000000"}
	for _, id := range created {
		assert.NoErr(t, s.CreateSession(ctx, Session{ID: id, CreatedAt: at, UpdatedAt: at}))
	}
	sessions, err := s.ListSessions(ctx)
	assert.NoErr(t, err)
	got := make([]string, 0, len(sessions))
	for _, item := range sessions {
		got = append(got, item.ID)
	}
	// Newest (last created) first.
	assert.Eq(t, []string{"s_000000000000", "s_zzzzzzzzzzzz"}, got)
}

func turnIDs(turns []Turn) []string {
	out := make([]string, 0, len(turns))
	for _, t := range turns {
		out = append(out, t.ID)
	}
	return out
}
