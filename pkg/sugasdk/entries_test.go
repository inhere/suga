package sugasdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
)

func TestEngineEntriesPaging(t *testing.T) {
	ctx := context.Background()
	var requests int32
	server := plainChatServer(t, "answer", &requests, nil)
	engine, session := openTestSession(t, ctx, server.URL)

	// Real turns write their records through the normal send path.
	for _, text := range []string{"one", "two", "three"} {
		_, err := session.Send(ctx, Prompt{Text: text}, nil)
		assert.Require(t, assert.NoErr(t, err))
	}
	all, err := engine.Entries(ctx, session.ID(), 0, 0)
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, len(all) >= 6)
	for i := 1; i < len(all); i++ {
		assert.True(t, all[i].Seq > all[i-1].Seq)
	}
	assert.Eq(t, session.ID(), all[0].SessionID)

	t.Run("pages without gaps or overlap", func(t *testing.T) {
		var got []Entry
		from := int64(1)
		for {
			page, err := engine.Entries(ctx, session.ID(), from, 2)
			assert.Require(t, assert.NoErr(t, err))
			if len(page) == 0 {
				break
			}
			assert.True(t, len(page) <= 2)
			got = append(got, page...)
			from = page[len(page)-1].Seq + 1
		}
		assert.Eq(t, len(all), len(got))
		for i := range all {
			assert.Eq(t, all[i].Seq, got[i].Seq)
		}
	})

	t.Run("fromSeq is inclusive and may be past the end", func(t *testing.T) {
		page, err := engine.Entries(ctx, session.ID(), all[2].Seq, 1)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, page, 1)
		assert.Eq(t, all[2].Seq, page[0].Seq)

		page, err = engine.Entries(ctx, session.ID(), all[len(all)-1].Seq+100, 5)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, page, 0)
	})

	t.Run("limit zero uses the default and large limits are clamped", func(t *testing.T) {
		repo := openTestStore(t)
		for i := 0; i < MaxEntriesLimit+5; i++ {
			body, _ := json.Marshal(map[string]int{"n": i})
			_, err := repo.AppendEntries(ctx, session.ID(), store.NewEntry{Type: store.EntryUser, JSON: body})
			assert.Require(t, assert.NoErr(t, err))
		}
		page, err := engine.Entries(ctx, session.ID(), 1, 0)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, page, DefaultEntriesLimit)
		page, err = engine.Entries(ctx, session.ID(), 1, 1<<20)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, page, MaxEntriesLimit)
	})

	t.Run("unknown or empty session is an error", func(t *testing.T) {
		_, err := engine.Entries(ctx, "no-such-session", 1, 10)
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "not found")
		_, err = engine.Entries(ctx, " ", 1, 10)
		assert.Err(t, err)
	})
}
