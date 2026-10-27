package sugasdk

import (
	"context"
	"errors"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
)

func TestEnginePin(t *testing.T) {
	ctx := context.Background()
	server := plainChatServer(t, "unused", nil, nil)
	engine, session := openTestSession(t, ctx, server.URL)
	defer session.Close()
	defer engine.Close()

	t.Run("sets and clears the persistent marker", func(t *testing.T) {
		assert.NoErr(t, engine.Pin(ctx, session.ID(), true))
		repo := openTestStore(t)
		model, err := repo.GetSession(ctx, session.ID())
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, store.PinnedSession(model))

		assert.NoErr(t, engine.Pin(ctx, session.ID(), false))
		model, err = repo.GetSession(ctx, session.ID())
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, store.PinnedSession(model))
	})

	t.Run("rejects invalid ids and missing sessions", func(t *testing.T) {
		assert.ErrMsg(t, engine.Pin(ctx, "   ", true), "sugasdk: session id is empty")
		err := engine.Pin(ctx, "missing", true)
		assert.True(t, errors.Is(err, store.ErrSessionNotFound), err)
	})
}
