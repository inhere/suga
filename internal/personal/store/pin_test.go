package store

import (
	"context"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

func TestPinnedSessionsSurviveGarbageCollection(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-48 * time.Hour)

	newOldSession := func(t *testing.T, s *Store, id string, meta map[string]any) {
		t.Helper()
		assert.Require(t, assert.NoErr(t, s.CreateSession(ctx, Session{
			ID: id, CreatedAt: old, UpdatedAt: old, Meta: meta,
		})))
	}

	t.Run("age gc protects pinned sdk and non-sdk sessions", func(t *testing.T) {
		s := openTemp(t)
		defer s.Close()
		newOldSession(t, s, "pinned-sdk", map[string]any{MetaOrigin: OriginSDK, MetaPinned: true})
		newOldSession(t, s, "pinned-run", map[string]any{MetaOrigin: OriginRun, MetaPinned: true})
		newOldSession(t, s, "unpinned", map[string]any{MetaOrigin: OriginRun})

		report, err := s.CollectGarbage(ctx, now.Add(-24*time.Hour), nil, true)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, report.Sessions, 1)
		assert.Eq(t, "unpinned", report.Sessions[0].ID)
		for _, id := range []string{"pinned-sdk", "pinned-run"} {
			_, err := s.GetSession(ctx, id)
			assert.NoErr(t, err)
		}
	})

	t.Run("retention protects a pinned sdk session", func(t *testing.T) {
		s := openTemp(t)
		defer s.Close()
		newOldSession(t, s, "pinned-sdk", map[string]any{MetaOrigin: OriginSDK, MetaPinned: true})
		newOldSession(t, s, "unpinned", map[string]any{MetaOrigin: OriginRun})

		report, err := s.CollectGarbageByRetention(ctx, now, GCRetentionPolicy{NonInteractive: 24 * time.Hour}, nil, true)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, report.NonInteractive.Sessions, 1)
		assert.Eq(t, "unpinned", report.NonInteractive.Sessions[0].ID)
		_, err = s.GetSession(ctx, "pinned-sdk")
		assert.NoErr(t, err)
	})
}

func TestPinnedSession(t *testing.T) {
	t.Run("only a boolean true pins", func(t *testing.T) {
		assert.True(t, PinnedSession(Session{Meta: map[string]any{MetaPinned: true}}))
		assert.False(t, PinnedSession(Session{Meta: map[string]any{MetaPinned: false}}))
		assert.False(t, PinnedSession(Session{Meta: map[string]any{MetaPinned: "true"}}))
		assert.False(t, PinnedSession(Session{}))
	})
}
