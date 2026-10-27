package sugasdk

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
)

func TestEngineEntryWindowKeepsStableSequence(t *testing.T) {
	ctx := context.Background()
	var requests int32
	server := plainChatServer(t, "unused", &requests, nil)
	engine, session := openTestSession(t, ctx, server.URL)
	repo := openTestStore(t)

	seqs := make([]int64, 0, 5)
	for i := 1; i <= 5; i++ {
		body, err := json.Marshal(map[string]int{"n": i})
		assert.Require(t, assert.NoErr(t, err))
		appended, err := repo.AppendEntries(ctx, session.ID(), store.NewEntry{Type: store.EntryUser, JSON: body})
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, appended, 1)
		seqs = append(seqs, appended[0].Seq)
	}

	entries, err := engine.Entry(ctx, session.ID()+"/"+strconv.FormatInt(seqs[2], 10), 1)
	assert.Require(t, assert.NoErr(t, err))
	assert.Len(t, entries, 3)
	assert.Eq(t, seqs[1], entries[0].Seq)
	assert.Eq(t, seqs[2], entries[1].Seq)
	assert.Eq(t, seqs[3], entries[2].Seq)
	assert.Eq(t, session.ID(), entries[1].SessionID)
	assert.Eq(t, `{"n":3}`, string(entries[1].JSON))

	// Appending after a read continues at the next sequence; no read or cleanup
	// rewrites the stable sequence of the existing records.
	before, err := repo.ListEntries(ctx, session.ID(), store.EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	lastBefore := before[len(before)-1].Seq
	body, err := json.Marshal(map[string]int{"n": 6})
	assert.Require(t, assert.NoErr(t, err))
	appended, err := repo.AppendEntries(ctx, session.ID(), store.NewEntry{Type: store.EntryUser, JSON: body})
	assert.Require(t, assert.NoErr(t, err))
	assert.Len(t, appended, 1)
	assert.Eq(t, lastBefore+1, appended[0].Seq)

	single, err := engine.Entry(ctx, session.ID()+"/"+strconv.FormatInt(appended[0].Seq, 10), 0)
	assert.Require(t, assert.NoErr(t, err))
	assert.Len(t, single, 1)
	assert.Eq(t, appended[0].Seq, single[0].Seq)

	t.Run("invalid references fail closed", func(t *testing.T) {
		for _, ref := range []string{"", "/1", session.ID() + "/0", session.ID() + "/nope", session.ID() + "/99"} {
			_, err := engine.Entry(ctx, ref, 0)
			assert.Err(t, err)
		}
		_, err := engine.Entry(ctx, session.ID()+"/"+strconv.FormatInt(seqs[0], 10), -1)
		assert.Err(t, err)
	})
}
