package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

func newSession(t *testing.T, s *Store, id string) {
	t.Helper()
	assert.Require(t, assert.NoErr(t, s.CreateSession(context.Background(), Session{ID: id, Title: "t-" + id})))
}

func TestSessionCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	t.Run("create get update", func(t *testing.T) {
		assert.NoErr(t, s.CreateSession(ctx, Session{ID: "a", Title: "A", Cwd: "/x", Meta: map[string]any{"k": "v"}}))
		got, err := s.GetSession(ctx, "a")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "A", got.Title)
		assert.Eq(t, SessionOpen, got.Status)
		assert.Eq(t, "v", got.Meta["k"])
		got.Title, got.Status, got.LastTurnID = "B", SessionRunning, "turn1"
		got.Meta["n"] = 2.0
		assert.NoErr(t, s.UpdateSession(ctx, got))
		again, _ := s.GetSession(ctx, "a")
		assert.Eq(t, "B", again.Title)
		assert.Eq(t, "turn1", again.LastTurnID)
		assert.Eq(t, 2.0, again.Meta["n"])
	})
	t.Run("not found", func(t *testing.T) {
		_, err := s.GetSession(ctx, "nope")
		assert.Eq(t, ErrSessionNotFound, err)
		assert.Eq(t, ErrSessionNotFound, s.UpdateSession(ctx, Session{ID: "nope"}))
		assert.Eq(t, ErrSessionNotFound, s.PatchSessionMeta(ctx, "nope", map[string]any{"a": 1}))
		_, err = s.AppendEntries(ctx, "nope", NewEntry{Type: EntryUser})
		assert.Eq(t, ErrSessionNotFound, err)
	})
	t.Run("patch meta merges and deletes", func(t *testing.T) {
		assert.NoErr(t, s.PatchSessionMeta(ctx, "a", map[string]any{"k": nil, "z": "1"}))
		got, _ := s.GetSession(ctx, "a")
		_, has := got.Meta["k"]
		assert.Eq(t, false, has)
		assert.Eq(t, "1", got.Meta["z"])
		assert.Eq(t, 2.0, got.Meta["n"])
	})
	t.Run("patch meta if absent writes once", func(t *testing.T) {
		applied, err := s.PatchSessionMetaIfAbsent(ctx, "a", "once", map[string]any{"once": "first", "by": "x"})
		assert.NoErr(t, err)
		assert.True(t, applied)
		before, _ := s.GetSession(ctx, "a")
		applied, err = s.PatchSessionMetaIfAbsent(ctx, "a", "once", map[string]any{"once": "second", "by": "y"})
		assert.NoErr(t, err)
		assert.False(t, applied)
		got, _ := s.GetSession(ctx, "a")
		assert.Eq(t, "first", got.Meta["once"])
		assert.Eq(t, "x", got.Meta["by"])
		assert.Eq(t, before.UpdatedAt, got.UpdatedAt) // skipped patch is rolled back
		_, err = s.PatchSessionMetaIfAbsent(ctx, "nope", "once", map[string]any{"once": 1})
		assert.Eq(t, ErrSessionNotFound, err)
		_, err = s.PatchSessionMetaIfAbsent(ctx, "a", "", map[string]any{"once": 1})
		assert.ErrMsg(t, err, "guard key is required")
	})
	t.Run("delete cascades entries", func(t *testing.T) {
		newSession(t, s, "del")
		assert.NoErr(t, s.PatchSessionMeta(ctx, "del", map[string]any{MetaPinned: true}))
		_, err := s.AppendEntries(ctx, "del", NewEntry{Type: EntryUser, JSON: json.RawMessage(`{}`)})
		assert.NoErr(t, err)
		assert.NoErr(t, s.DeleteSession(ctx, "del"))
		_, err = s.GetSession(ctx, "del")
		assert.Eq(t, ErrSessionNotFound, err)
		var n int
		assert.NoErr(t, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE session_id='del'`).Scan(&n))
		assert.Eq(t, 0, n)
		assert.Eq(t, ErrSessionNotFound, s.DeleteSession(ctx, "del"))
	})
	t.Run("list latest first", func(t *testing.T) {
		now := time.Now()
		assert.NoErr(t, s.CreateSession(ctx, Session{ID: "old", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}))
		list, err := s.ListSessions(ctx)
		assert.NoErr(t, err)
		assert.Eq(t, "old", list[len(list)-1].ID)
	})
}

func TestEntries(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	newSession(t, s, "s1")
	newSession(t, s, "s2")
	raw := func(v string) json.RawMessage { return json.RawMessage(fmt.Sprintf(`{"v":%q}`, v)) }

	got, err := s.AppendEntries(ctx, "s1",
		NewEntry{TurnID: "t1", Type: EntryUser, JSON: raw("u1")},
		NewEntry{TurnID: "t1", Type: EntryAssistant, JSON: raw("a1")},
		NewEntry{TurnID: "t2", Type: EntryUser, JSON: raw("u2")},
		NewEntry{Type: EntryCompaction, JSON: raw("c1")},
	)
	assert.Require(t, assert.NoErr(t, err))
	for i, e := range got {
		assert.Eq(t, int64(i+1), e.Seq)
	}
	more, err := s.AppendEntries(ctx, "s1", NewEntry{TurnID: "t2", Type: EntryAssistant, JSON: raw("a2")})
	assert.NoErr(t, err)
	assert.Eq(t, int64(5), more[0].Seq)
	other, _ := s.AppendEntries(ctx, "s2", NewEntry{Type: EntryUser})
	assert.Eq(t, int64(1), other[0].Seq) // seq is per session

	t.Run("filters", func(t *testing.T) {
		all, _ := s.ListEntries(ctx, "s1", EntryFilter{})
		assert.Eq(t, 5, len(all))
		since, _ := s.ListEntries(ctx, "s1", EntryFilter{SinceSeq: 3})
		assert.Eq(t, 2, len(since))
		assert.Eq(t, int64(4), since[0].Seq)
		turn, _ := s.ListEntries(ctx, "s1", EntryFilter{TurnID: "t2"})
		assert.Eq(t, 2, len(turn))
		typed, _ := s.ListEntries(ctx, "s1", EntryFilter{Types: []string{EntryUser, EntryAssistant}, TurnID: "t1"})
		assert.Eq(t, 2, len(typed))
		lim, _ := s.ListEntries(ctx, "s1", EntryFilter{Limit: 2})
		assert.Eq(t, 2, len(lim))
		assert.Eq(t, `{"v":"u1"}`, string(lim[0].JSON))
	})
	t.Run("latest", func(t *testing.T) {
		e, err := s.LatestEntry(ctx, "s1", EntryUser)
		assert.NoErr(t, err)
		assert.Eq(t, int64(3), e.Seq)
		none, err := s.LatestEntry(ctx, "s1", EntryPlan)
		assert.NoErr(t, err)
		assert.Eq(t, true, none == nil)
	})
	t.Run("concurrent appends keep seq unique and contiguous", func(t *testing.T) {
		s2, err := Open(s.Path())
		assert.Require(t, assert.NoErr(t, err))
		defer s2.Close()
		var wg sync.WaitGroup
		errs := make(chan error, 40)
		for _, st := range []*Store{s, s2} {
			wg.Add(1)
			go func(st *Store) {
				defer wg.Done()
				for i := 0; i < 20; i++ {
					if _, err := st.AppendEntries(ctx, "s2", NewEntry{Type: EntryUser}, NewEntry{Type: EntryAssistant}); err != nil {
						errs <- err
					}
				}
			}(st)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		all, _ := s.ListEntries(ctx, "s2", EntryFilter{})
		assert.Eq(t, 81, len(all))
		for i, e := range all {
			assert.Eq(t, int64(i+1), e.Seq)
		}
	})
}

func TestTurns(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	newSession(t, s, "s1")
	assert.Eq(t, ErrSessionNotFound, s.CreateTurn(ctx, Turn{ID: "x", SessionID: "missing"}))
	assert.NoErr(t, s.CreateTurn(ctx, Turn{ID: "t1", SessionID: "s1"}))
	got, err := s.GetTurn(ctx, "t1")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, TurnRunning, got.Status)
	assert.Eq(t, 0, len(got.StateJSON))

	assert.NoErr(t, s.UpsertTurnState(ctx, "t1", []byte(`{"RunID":"t1"}`)))
	end := time.Now()
	assert.NoErr(t, s.UpdateTurn(ctx, Turn{ID: "t1", Status: TurnCompleted, StopReason: "end_turn", EndedAt: end}))
	got, _ = s.GetTurn(ctx, "t1")
	assert.Eq(t, TurnCompleted, got.Status)
	assert.Eq(t, "end_turn", got.StopReason)
	assert.Eq(t, `{"RunID":"t1"}`, string(got.StateJSON)) // UpdateTurn keeps the state
	assert.Eq(t, false, got.EndedAt.IsZero())

	assert.Eq(t, ErrTurnNotFound, s.UpsertTurnState(ctx, "nope", []byte("{}")))
	assert.Eq(t, ErrTurnNotFound, s.UpdateTurn(ctx, Turn{ID: "nope"}))
	_, err = s.GetTurn(ctx, "nope")
	assert.Eq(t, ErrTurnNotFound, err)

	assert.NoErr(t, s.CreateTurn(ctx, Turn{ID: "t2", SessionID: "s1"}))
	done, _ := s.ListTurns(ctx, "s1", TurnCompleted)
	assert.Eq(t, 1, len(done))
	all, _ := s.ListTurns(ctx, "s1")
	assert.Eq(t, 2, len(all))
}

func TestEventsAndUsage(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	base := time.Now()
	add := func(sid, turn, kind string, in, out int64, off time.Duration) {
		_, err := s.AppendEvent(ctx, Event{SessionID: sid, TurnID: turn, Kind: kind, StartedAt: base.Add(off),
			InputTokens: in, OutputTokens: out, CacheReadTokens: 1, CacheWriteTokens: 2, Attrs: map[string]any{"a": 1}})
		assert.NoErr(t, err)
	}
	add("s1", "t1", EventModel, 10, 5, 0)
	add("s1", "t1", EventModel, 20, 7, time.Second)
	add("s1", "t1", EventTurn, 30, 12, 2*time.Second) // aggregate on the turn event must not be summed
	add("s1", "t1", EventTool, 0, 0, 3*time.Second)
	add("s2", "t9", EventModel, 99, 99, 0)

	u, err := s.SumUsage(ctx, "s1")
	assert.NoErr(t, err)
	assert.Eq(t, Usage{Calls: 2, InputTokens: 30, OutputTokens: 12, CacheReadTokens: 2, CacheWriteTokens: 4}, u)
	empty, _ := s.SumUsage(ctx, "none")
	assert.Eq(t, Usage{}, empty)

	t.Run("list", func(t *testing.T) {
		all, err := s.ListEvents(ctx, EventFilter{SessionID: "s1"})
		assert.NoErr(t, err)
		assert.Eq(t, 4, len(all))
		assert.Eq(t, 1.0, all[0].Attrs["a"])
		models, _ := s.ListEvents(ctx, EventFilter{SessionID: "s1", Kinds: []string{EventModel, EventTool}})
		assert.Eq(t, 3, len(models))
		since, _ := s.ListEvents(ctx, EventFilter{SessionID: "s1", Since: base.Add(2 * time.Second)})
		assert.Eq(t, 2, len(since))
		byTurn, _ := s.ListEvents(ctx, EventFilter{TurnID: "t9"})
		assert.Eq(t, 1, len(byTurn))
	})
}

// TestUpdateSessionFieldsKeepsMeta: a meta key patched after the session was
// read survives a state update made from that read (the turn's status
// changes while a background task marks the session tainted).
func TestUpdateSessionFieldsKeepsMeta(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	assert.Require(t, assert.NoErr(t, s.CreateSession(ctx, Session{ID: "s1", Status: SessionOpen, Meta: map[string]any{"loaded_tools": []any{"x"}}})))
	stale, err := s.GetSession(ctx, "s1")
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, s.PatchSessionMeta(ctx, "s1", map[string]any{"tainted_at": "2026-10-13T10:00:00Z"}))

	stale.Status, stale.LastTurnID, stale.Title = SessionRunning, "turn_1", "title"
	assert.Require(t, assert.NoErr(t, s.UpdateSessionFields(ctx, stale)))
	got, err := s.GetSession(ctx, "s1")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, SessionRunning, got.Status)
	assert.Eq(t, "turn_1", got.LastTurnID)
	assert.Eq(t, "title", got.Title)
	assert.Eq(t, "2026-10-13T10:00:00Z", got.Meta["tainted_at"])
	assert.NotNil(t, got.Meta["loaded_tools"])

	assert.ErrIs(t, s.UpdateSessionFields(ctx, Session{ID: "missing"}), ErrSessionNotFound)
}
