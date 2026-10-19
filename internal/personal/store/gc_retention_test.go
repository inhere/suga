package store

import (
	"context"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// TestCollectGarbageByRetention: `suga data gc --retention` removes sessions by
// origin class (design 0.3, 会话来源). A chat session, a session a chat
// continued and a session from before meta.origin existed are interactive; run,
// ACP and sub-agent sessions are not. A missing origin must never be guessed
// the other way round: a historical session is only removed once the longer
// period has passed.
func TestCollectGarbageByRetention(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	day := 24 * time.Hour
	policy := GCRetentionPolicy{Interactive: 180 * day, NonInteractive: 30 * day}

	setup := func(t *testing.T) *Store {
		t.Helper()
		s := openTemp(t)
		mk := func(id string, age time.Duration, meta map[string]any) {
			at := now.Add(-age)
			assert.Require(t, assert.NoErr(t, s.CreateSession(ctx, Session{ID: id, CreatedAt: at, UpdatedAt: at, Meta: meta})))
			appendAll(t, s, id, user(t, "hello "+id))
			// AppendEntries bumps updated_at to now; age the session again.
			_, err := s.DB().ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(at), id)
			assert.Require(t, assert.NoErr(t, err))
		}
		mk("chat-old", 200*day, map[string]any{MetaOrigin: OriginChat})
		mk("historical", 200*day, nil) // no origin: the interactive class
		mk("run-old", 40*day, map[string]any{MetaOrigin: OriginRun})
		mk("acp-old-used", 40*day, map[string]any{MetaOrigin: OriginACP, MetaUsedInteractively: true})
		mk("run-fresh", 10*day, map[string]any{MetaOrigin: OriginRun})
		mk("chat-fresh", 10*day, map[string]any{MetaOrigin: OriginChat})
		mk("agent-child", 40*day, map[string]any{
			MetaKind: KindAgent, MetaParentSession: "chat-old", MetaOrigin: OriginAgent,
		})
		return s
	}

	t.Run("dry run classifies and writes nothing", func(t *testing.T) {
		s := setup(t)
		defer s.Close()
		report, err := s.CollectGarbageByRetention(ctx, now, policy, nil, false)
		assert.NoErr(t, err)
		assert.Eq(t, false, report.Applied)
		assert.Eq(t, 3, len(report.Interactive.Sessions))    // chat-old, historical, agent-child
		assert.Eq(t, 1, len(report.NonInteractive.Sessions)) // run-old
		assert.Eq(t, "run-old", report.NonInteractive.Sessions[0].ID)
		assert.Eq(t, int64(3), report.Interactive.Entries)
		assert.Eq(t, int64(1), report.NonInteractive.Entries)
		assert.True(t, report.Interactive.EstimatedBytes > 0)
		assert.True(t, report.NonInteractive.EstimatedBytes > 0)
		assert.Eq(t, 180*day, report.Interactive.Retention)
		assert.Eq(t, 30*day, report.NonInteractive.Retention)
		assert.Eq(t, 4, len(report.Sessions()))
		assert.Eq(t, int64(7), gcCount(t, s, `SELECT COUNT(*) FROM sessions`), "a dry run writes nothing")
	})

	t.Run("apply removes the expired classes and their old orphan events", func(t *testing.T) {
		s := setup(t)
		defer s.Close()
		for _, event := range []Event{
			{SessionID: "vanished", Kind: EventModel, StartedAt: now.Add(-40 * day)}, // orphan, older than 30d: removed
			{SessionID: "vanished", Kind: EventModel, StartedAt: now.Add(-5 * day)},  // orphan, recent: kept
			{SessionID: "run-old", Kind: EventModel, StartedAt: now.Add(-40 * day)},  // goes with its session
		} {
			_, err := s.AppendEvent(ctx, event)
			assert.Require(t, assert.NoErr(t, err))
		}
		report, err := s.CollectGarbageByRetention(ctx, now, policy, nil, true)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, report.Applied)
		for _, id := range []string{"chat-old", "historical", "run-old", "agent-child"} {
			_, err := s.GetSession(ctx, id)
			assert.ErrIs(t, err, ErrSessionNotFound)
		}
		for _, id := range []string{"run-fresh", "chat-fresh", "acp-old-used"} {
			_, err := s.GetSession(ctx, id)
			assert.NoErr(t, err)
		}
		assert.Eq(t, int64(3), gcCount(t, s, `SELECT COUNT(*) FROM sessions`))
		assert.Eq(t, int64(1), gcCount(t, s, `SELECT COUNT(*) FROM events`), "only the recent orphan event stays")
		assert.Eq(t, int64(0), gcCount(t, s, `SELECT COUNT(*) FROM entries_fts WHERE session_id NOT IN (SELECT id FROM sessions)`))
	})

	t.Run("a held session is kept and a class without a period is off", func(t *testing.T) {
		s := setup(t)
		defer s.Close()
		held := map[string]bool{"run-old": true}
		report, err := s.CollectGarbageByRetention(ctx, now, policy, held, false)
		assert.NoErr(t, err)
		assert.Empty(t, report.NonInteractive.Sessions)
		assert.Eq(t, 3, len(report.Interactive.Sessions))

		// A period of zero turns its class off, whatever the session's age.
		off := GCRetentionPolicy{Interactive: 0, NonInteractive: policy.NonInteractive}
		report, err = s.CollectGarbageByRetention(ctx, now, off, nil, false)
		assert.NoErr(t, err)
		assert.Empty(t, report.Interactive.Sessions)
		assert.Eq(t, 1, len(report.NonInteractive.Sessions))
	})
}

// TestSessionOriginHelpers: meta.origin is the entry point, and a session from
// before the key existed or one a chat continued counts as interactive.
func TestSessionOriginHelpers(t *testing.T) {
	t.Run("origin and used_interactively", func(t *testing.T) {
		chat := Session{Meta: map[string]any{MetaOrigin: OriginChat}}
		assert.Eq(t, OriginChat, SessionOrigin(chat))
		assert.True(t, InteractiveSession(chat))

		run := Session{Meta: map[string]any{MetaOrigin: OriginRun}}
		assert.False(t, InteractiveSession(run))
		assert.False(t, SessionUsedInteractively(run))

		used := Session{Meta: map[string]any{MetaOrigin: OriginRun, MetaUsedInteractively: true}}
		assert.True(t, SessionUsedInteractively(used))
		assert.True(t, InteractiveSession(used))
	})

	t.Run("a historical session is interactive", func(t *testing.T) {
		assert.Eq(t, "", SessionOrigin(Session{}))
		assert.True(t, InteractiveSession(Session{}), "a session from before meta.origin is kept longer")
	})
}
