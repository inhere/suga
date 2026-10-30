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

// TestMemoryReAddSameTitleThenUpdate reproduces suga-r1kg: remove an entry, add
// one with the same title (same derived id), then update it; the read-back must
// show the new text.
func TestMemoryReAddSameTitleThenUpdate(t *testing.T) {
	server := httptest.NewServer(httpNotFoundHandler())
	defer server.Close()
	home := sdkTestHome(t, server.URL)
	ctx := context.Background()
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	defer engine.Close()

	mem := engine.Memory()
	first, err := mem.Add(ctx, "user", MemoryEntry{Title: "Readd title", Body: "first body", Kind: "user"})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, mem.Remove(ctx, first.UUID))
	second, err := mem.Add(ctx, "user", MemoryEntry{Title: "Readd title", Body: "second body", Kind: "user"})
	assert.Require(t, assert.NoErr(t, err))
	updated, err := mem.Update(ctx, second.UUID, MemoryEntry{Title: second.Title, Body: "edited body", Kind: second.Kind})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "edited body", updated.Body)
	got, err := mem.Get(ctx, second.UUID)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "edited body", got.Body)
	list, err := mem.List(ctx, "user")
	assert.Require(t, assert.NoErr(t, err))
	for _, e := range list {
		if e.ID == second.ID {
			assert.Eq(t, "edited body", e.Body)
		}
	}
}

// TestMemoryReAddThenEditToRemovedTextFailsLoudly pins the other half of
// suga-r1kg: editing a re-added entry back to the text removed earlier is
// refused with ErrTombstoned (never a silent success with stale content).
func TestMemoryReAddThenEditToRemovedTextFailsLoudly(t *testing.T) {
	server := httptest.NewServer(httpNotFoundHandler())
	defer server.Close()
	home := sdkTestHome(t, server.URL)
	ctx := context.Background()
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	defer engine.Close()

	mem := engine.Memory()
	first, err := mem.Add(ctx, "user", MemoryEntry{Title: "Loud title", Body: "removed text", Kind: "user"})
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, mem.Remove(ctx, first.UUID))
	second, err := mem.Add(ctx, "user", MemoryEntry{Title: "Loud title", Body: "kept text", Kind: "user"})
	assert.Require(t, assert.NoErr(t, err))
	_, err = mem.Update(ctx, second.UUID, MemoryEntry{Title: second.Title, Body: "removed text", Kind: second.Kind})
	assert.True(t, errors.Is(err, memory.ErrTombstoned))
	got, err := mem.Get(ctx, second.UUID)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "kept text", got.Body)
}
