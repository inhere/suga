package sugasdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
)

func TestEngineTraceEvents(t *testing.T) {
	ctx := context.Background()
	var requests int32
	server := plainChatServer(t, "answer", &requests, nil)
	engine, session := openTestSession(t, ctx, server.URL)

	_, err := session.Send(ctx, Prompt{Text: "hello"}, nil)
	assert.Require(t, assert.NoErr(t, err))
	// A synthetic event with known fields checks the field mapping.
	repo := openTestStore(t)
	_, err = repo.AppendEvent(ctx, store.Event{SessionID: session.ID(), TurnID: "t-x", Kind: "tool", Name: "demo",
		Status: "ok", DurationMs: 7, InputTokens: 3, OutputTokens: 4, Attrs: map[string]any{"k": "v"}})
	assert.Require(t, assert.NoErr(t, err))

	all, err := engine.TraceEvents(ctx, session.ID(), 0, 0)
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, len(all) >= 2)
	for i := 1; i < len(all); i++ {
		assert.True(t, all[i].ID > all[i-1].ID)
	}
	last := all[len(all)-1]
	assert.Eq(t, "tool", last.Kind)
	assert.Eq(t, "demo", last.Name)
	assert.Eq(t, int64(7), last.DurationMs)
	assert.Eq(t, int64(3), last.InputTokens)
	assert.Eq(t, "t-x", last.TurnID)
	var attrs map[string]string
	assert.NoErr(t, json.Unmarshal(last.Attrs, &attrs))
	assert.Eq(t, "v", attrs["k"])

	t.Run("pages by event id", func(t *testing.T) {
		var got []TraceEvent
		var after int64
		for {
			page, err := engine.TraceEvents(ctx, session.ID(), after, 1)
			assert.Require(t, assert.NoErr(t, err))
			if len(page) == 0 {
				break
			}
			assert.Len(t, page, 1)
			got = append(got, page...)
			after = page[0].ID
		}
		assert.Eq(t, len(all), len(got))
	})

	t.Run("cursor past the end is empty; bad session errors", func(t *testing.T) {
		page, err := engine.TraceEvents(ctx, session.ID(), last.ID+1000, 5)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, page, 0)
		_, err = engine.TraceEvents(ctx, "no-such-session", 0, 5)
		assert.Err(t, err)
		_, err = engine.TraceEvents(ctx, "", 0, 5)
		assert.Err(t, err)
	})
}
