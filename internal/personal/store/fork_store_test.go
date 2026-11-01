package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// forkInput builds a payload of a user turn, a big tool result and an answer,
// numbered 1..3, with fixed created_at values.
func forkInput(t *testing.T, id string) ForkInput {
	t.Helper()
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	tool := NewEntry{Type: EntryTool, TurnID: "ft1", JSON: msgJSON(t, bigToolMessage("c1")), Seq: 2, CreatedAt: at.Add(time.Second)}
	u := user(t, "forked needle question")
	u.TurnID, u.Seq, u.CreatedAt = "ft1", 1, at
	a := assistant(t, "forked answer")
	a.TurnID, a.Seq, a.CreatedAt = "ft1", 3, at.Add(2*time.Second)
	return ForkInput{
		Session: Session{ID: id, Title: "t (fork)", Cwd: "/w", Meta: map[string]any{"forked_from": "src"}},
		Turns:   []Turn{{ID: "ft1", Status: TurnCompleted, StartedAt: at, EndedAt: at.Add(3 * time.Second)}},
		Entries: []NewEntry{u, tool, a},
	}
}

func countRows(t *testing.T, s *Store, q string, args ...any) int {
	t.Helper()
	var n int
	assert.Require(t, assert.NoErr(t, s.db.QueryRow(q, args...).Scan(&n)))
	return n
}

func TestCreateForkedSession(t *testing.T) {
	ctx := context.Background()
	t.Run("writes session, turns, entries, FTS and the body file", func(t *testing.T) {
		s := openTemp(t)
		in := forkInput(t, "fork1")
		assert.Require(t, assert.NoErr(t, s.CreateForkedSession(ctx, in)))

		sess, err := s.GetSession(ctx, "fork1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "t (fork)", sess.Title)
		assert.Eq(t, "src", sess.Meta["forked_from"])
		turns, err := s.ListTurns(ctx, "fork1")
		assert.NoErr(t, err)
		assert.Len(t, turns, 1)
		assert.Eq(t, TurnCompleted, turns[0].Status)

		got, err := s.ListEntries(ctx, "fork1", EntryFilter{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []int64{1, 2, 3}, seqsOf(got))
		// created_at is kept as given, and the big body reads back whole.
		assert.True(t, got[0].CreatedAt.Equal(in.Entries[0].CreatedAt))
		assert.True(t, got[2].CreatedAt.Equal(in.Entries[2].CreatedAt))
		assert.Eq(t, string(in.Entries[1].JSON), string(got[1].JSON))
		_, ref := storedRow(t, s, "fork1", 2)
		assert.NotEmpty(t, ref, "the big tool body moved to the session file")

		// The fork's entries are in the full-text index.
		hits, err := s.SearchEntries(ctx, EntrySearch{Query: "forked needle", SessionIDs: []string{"fork1"}})
		assert.NoErr(t, err)
		assert.Len(t, hits, 1)
		assert.Eq(t, int64(1), hits[0].Seq)

		// Appending afterwards continues at seq 4.
		more := appendAll(t, s, "fork1", user(t, "next"))
		assert.Eq(t, int64(4), more[0].Seq)
	})
	t.Run("seq must be 1..n", func(t *testing.T) {
		s := openTemp(t)
		in := forkInput(t, "fork2")
		in.Entries[1].Seq = 5
		assert.Err(t, s.CreateForkedSession(ctx, in))
		_, err := s.GetSession(ctx, "fork2")
		assert.True(t, errors.Is(err, ErrSessionNotFound))
	})
	t.Run("a failure in the middle leaves no rows and no files", func(t *testing.T) {
		s := openTemp(t)
		in := forkInput(t, "fork3")
		// The big tool entry (seq 2) is already written to the body file when the
		// typeless entry fails.
		bad := NewEntry{Seq: 4, CreatedAt: time.Now()}
		in.Entries = append(in.Entries, bad)
		assert.Err(t, s.CreateForkedSession(ctx, in))

		_, err := s.GetSession(ctx, "fork3")
		assert.True(t, errors.Is(err, ErrSessionNotFound))
		assert.Eq(t, 0, countRows(t, s, `SELECT COUNT(1) FROM entries WHERE session_id = 'fork3'`))
		assert.Eq(t, 0, countRows(t, s, `SELECT COUNT(1) FROM turns WHERE session_id = 'fork3'`))
		assert.Eq(t, 0, countRows(t, s, `SELECT COUNT(1) FROM entries_fts WHERE session_id = 'fork3'`))
		path, ok := s.toolBodyPath("fork3")
		assert.True(t, ok)
		_, statErr := os.Stat(path)
		assert.True(t, os.IsNotExist(statErr), "body file left behind")
		_, statErr = os.Stat(filepath.Dir(path))
		assert.True(t, os.IsNotExist(statErr), "session directory left behind")
	})
	t.Run("a turn failure rolls back the session row", func(t *testing.T) {
		s := openTemp(t)
		in := forkInput(t, "fork4")
		in.Turns = append(in.Turns, Turn{ID: "ft1", Status: TurnCompleted}) // duplicate id
		assert.Err(t, s.CreateForkedSession(ctx, in))
		assert.Eq(t, 0, countRows(t, s, `SELECT COUNT(1) FROM sessions WHERE id = 'fork4'`))
	})
	t.Run("an existing session id fails and keeps the existing body file", func(t *testing.T) {
		s := openTemp(t)
		createSession(t, s, "taken", "/w")
		appendAll(t, s, "taken", NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c9"))})
		path, _ := s.toolBodyPath("taken")
		before, err := os.ReadFile(path)
		assert.Require(t, assert.NoErr(t, err))

		assert.Err(t, s.CreateForkedSession(ctx, forkInput(t, "taken")))
		after, err := os.ReadFile(path)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, string(before), string(after))
		got, err := s.ListEntries(ctx, "taken", EntryFilter{})
		assert.NoErr(t, err)
		assert.Len(t, got, 1)
	})
}
