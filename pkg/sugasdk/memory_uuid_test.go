package sugasdk

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

func TestMemoryUUIDLifecycle(t *testing.T) {
	server := httptest.NewServer(httpNotFoundHandler())
	defer server.Close()
	home := sdkTestHome(t, server.URL)
	ctx := context.Background()
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	defer engine.Close()

	mem := engine.Memory()
	first, err := mem.Add(ctx, "user", MemoryEntry{Title: "UUID identity", Body: "first body", Kind: "user"})
	assert.Require(t, assert.NoErr(t, err))
	assert.NotEmpty(t, first.UUID)
	got, err := mem.Get(ctx, first.UUID)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, first.UUID, got.UUID)

	updated, err := mem.Update(ctx, first.UUID, MemoryEntry{Title: first.Title, Body: "updated body", Kind: first.Kind})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, first.UUID, updated.UUID)
	assert.Eq(t, "updated body", updated.Body)
	assert.NoErr(t, mem.Remove(ctx, first.UUID))
	_, err = mem.Get(ctx, first.UUID)
	assert.True(t, errors.Is(err, memory.ErrNotFound))

	second, err := mem.Add(ctx, "user", MemoryEntry{Title: "UUID identity", Body: "second body", Kind: "user"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, first.ID, second.ID, "file backend reuses the derived id after removal")
	assert.NotEq(t, first.UUID, second.UUID)
	got, err = mem.Get(ctx, second.UUID)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "second body", got.Body)
}

// httpNotFoundHandler prevents the memory-only SDK test from making provider
// calls while still giving sdkTestHome a valid provider URL.
func httpNotFoundHandler() http.Handler {
	return http.NotFoundHandler()
}
