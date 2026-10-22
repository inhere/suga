package personal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// messagesJSON renders a request's messages, for a byte-for-byte comparison.
func messagesJSON(t *testing.T, msgs []agentcore.Message) string {
	t.Helper()
	raw, err := json.Marshal(msgs)
	assert.Require(t, assert.NoErr(t, err))
	return string(raw)
}

// TestEphemeralContext: the per-request context of one model call (the output
// contract) rides at the end of the projection, is not stored, and disappears
// with its request.
func TestEphemeralContext(t *testing.T) {
	ctx := context.Background()

	t.Run("empty leaves the projection byte for byte as it was", func(t *testing.T) {
		plain := okProvider()
		runner := compactRunner(t, openObserveRepo(t), plain, nil, nil)
		_, err := runner.Run(ctx, RunRequest{Prompt: "one"})
		assert.Require(t, assert.NoErr(t, err))

		explicit := okProvider()
		runner2 := compactRunner(t, openObserveRepo(t), explicit, nil, nil)
		_, err = runner2.Run(ctx, RunRequest{Prompt: "one", EphemeralContext: ""})
		assert.Require(t, assert.NoErr(t, err))

		assert.Eq(t, messagesJSON(t, lastRequest(plain).Messages), messagesJSON(t, lastRequest(explicit).Messages))
	})

	t.Run("non-empty is the last message and is not stored", func(t *testing.T) {
		const marker = "EPHEMERAL-CONTRACT-BLOCK"
		prov := okProvider()
		repo := openObserveRepo(t)
		runner := compactRunner(t, repo, prov, nil, nil)

		first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
		assert.Require(t, assert.NoErr(t, err))
		second, err := runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "two", EphemeralContext: marker})
		assert.Require(t, assert.NoErr(t, err))

		msgs := lastRequest(prov).Messages
		assert.True(t, len(msgs) > 2)
		assert.Eq(t, "user", msgs[len(msgs)-1].Role)
		assert.Eq(t, marker, msgs[len(msgs)-1].Content)
		// It comes after the kept history, not before this turn's messages.
		assert.NotContains(t, messagesJSON(t, msgs[:len(msgs)-1]), marker)

		entries, err := repo.ListEntries(ctx, first.SessionID, store.EntryFilter{})
		assert.Require(t, assert.NoErr(t, err))
		assert.NotEmpty(t, entries)
		for _, entry := range entries {
			assert.NotContains(t, string(entry.JSON), marker)
		}

		// The next turn of the same session does not carry it.
		_, err = runner.Run(ctx, RunRequest{SessionID: second.SessionID, Prompt: "three"})
		assert.Require(t, assert.NoErr(t, err))
		assert.NotContains(t, messagesJSON(t, lastRequest(prov).Messages), marker)
	})
}
