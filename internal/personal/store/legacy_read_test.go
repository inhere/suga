package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// TestLegacyToolRowsReadBack: the two tool-row shapes a ledger written before
// v0.41.0 holds must read back unchanged, because the migration commands that
// produced them are gone (v0.35 and older stored the result twice; the removed
// dedupe pass rewrote it with the ResultSameAsContent marker). ListEntries is
// what export and the context assembly read, and both decode the same bytes.
func TestLegacyToolRowsReadBack(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	// The v0.35 shape: Content and ToolResult.Content hold the same output.
	doubleJSON := `{"Role":"tool","Content":"out","ToolResult":{"CallID":"c1","Name":"sh","Content":"out"}}`
	// The deduped shape: one copy plus the marker the decoder turns back into the
	// result.
	dedupedJSON := `{"Role":"tool","Content":"out2","ToolResult":{"CallID":"c2","Name":"sh"},"ResultSameAsContent":true}`
	appendAll(t, s, "s1",
		NewEntry{Type: EntryTool, JSON: []byte(doubleJSON)},
		NewEntry{Type: EntryTool, JSON: []byte(dedupedJSON)})

	entries, err := s.ListEntries(ctx, "s1", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, entries, 2))
	assert.Eq(t, doubleJSON, string(entries[0].JSON))
	assert.Eq(t, dedupedJSON, string(entries[1].JSON))

	for seq, want := range map[int64]string{1: "out", 2: "out2"} {
		var m api.Message
		assert.Require(t, assert.NoErr(t, json.Unmarshal(entries[seq-1].JSON, &m)))
		assert.Eq(t, want, m.Content, "entry %d", seq)
		assert.Eq(t, want, m.ToolResult.Content, "the decode restores the result of entry %d", seq)
	}
}
