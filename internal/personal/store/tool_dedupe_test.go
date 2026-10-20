package store

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// Data governance P2: `suga data dedupe-tool-results` rewrites the historical
// tool entries that store the same body twice (batched, resumable) and restores
// them with --undo.

// dedupeSessions are the sessions the fixture fills.
var dedupeSessions = []string{"s1", "s2", "s3"}

// dedupeFixture appends the same four entries to every session: a user message,
// a double-stored tool result (the candidate), a tool result that differs from
// Content (left alone) and an assistant answer. It returns the store and the
// stored JSON of every entry, for the byte comparison with --undo.
func dedupeFixture(t *testing.T, s *Store) map[string]map[int64]string {
	t.Helper()
	ctx := context.Background()
	before := map[string]map[int64]string{}
	for i, id := range dedupeSessions {
		newSession(t, s, id)
		call := &api.ToolCall{CallID: "c" + id, Name: "shell", Arguments: map[string]any{"n": float64(i)}}
		entries := []NewEntry{
			{Type: EntryUser, JSON: msgJSON(t, api.Message{Role: "user", Content: "list the files"})},
			{Type: EntryTool, JSON: msgJSON(t, api.Message{Role: "tool", Content: "same-" + id, ToolCall: call,
				ToolResult: &api.ToolResult{CallID: "c" + id, Name: "shell", Content: "same-" + id}})},
			{Type: EntryTool, JSON: msgJSON(t, api.Message{Role: "tool", Content: "short-" + id, ToolCall: call,
				ToolResult: &api.ToolResult{CallID: "c" + id, Name: "shell", Content: "longer-" + id}})},
			{Type: EntryAssistant, JSON: msgJSON(t, api.Message{Role: "assistant", Content: "done"})},
		}
		_, err := s.AppendEntries(ctx, id, entries...)
		assert.Require(t, assert.NoErr(t, err))
		before[id] = storedEntries(t, s, id)
	}
	return before
}

// storedEntries reads the raw JSON of a session's entries by seq.
func storedEntries(t *testing.T, s *Store, sessionID string) map[int64]string {
	t.Helper()
	entries, err := s.ListEntries(context.Background(), sessionID, EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	out := make(map[int64]string, len(entries))
	for _, e := range entries {
		out[e.Seq] = string(e.JSON)
	}
	return out
}

func TestToolDedupeReportAndStatus(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	before := dedupeFixture(t, s)

	status, err := s.ToolDedupeStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	// One double-stored tool result per session.
	assert.Eq(t, int64(len(dedupeSessions)), status.PendingRows)
	assert.Eq(t, int64(len(dedupeSessions)*len("same-s1")), status.PendingBytes)
	assert.Eq(t, int64(0), status.MarkedRows)
	assert.False(t, status.Started)

	// --batch 1: every window holds one tool entry, so the pass needs several
	// write transactions.
	var batches int
	report, err := s.DedupeToolResults(ctx, ToolDedupeOptions{Batch: 1, OnBatch: func(ToolDedupeProgress) { batches++ }})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(len(dedupeSessions)), report.Rows)
	assert.True(t, report.Batches > 1)
	assert.Eq(t, report.Batches, batches)
	assert.True(t, report.Complete)

	status, err = s.ToolDedupeStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(0), status.PendingRows)
	assert.Eq(t, int64(len(dedupeSessions)), status.MarkedRows)
	assert.True(t, status.Complete)

	for _, id := range dedupeSessions {
		after := storedEntries(t, s, id)
		for seq, raw := range after {
			if strings.Contains(raw, `"ResultSameAsContent":true`) {
				assert.True(t, !strings.Contains(raw, `"Content":"same-`+id+`","Artifacts"`))
				continue
			}
			// Every other row is byte for byte what it was.
			assert.Eq(t, before[id][seq], raw)
		}
	}

	// A rerun has nothing left to do.
	again, err := s.DedupeToolResults(ctx, ToolDedupeOptions{Batch: 1})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(0), again.Rows)
	assert.True(t, again.Complete)

	// --undo restores exactly the bytes the entries held before the dedupe.
	var undone int
	undo, err := s.UndoToolDedupe(ctx, ToolDedupeOptions{Batch: 2, OnBatch: func(ToolDedupeProgress) { undone++ }})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(len(dedupeSessions)), undo.Rows)
	assert.Eq(t, undo.Batches, undone)
	for _, id := range dedupeSessions {
		assert.Eq(t, before[id], storedEntries(t, s, id))
	}
	status, err = s.ToolDedupeStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(0), status.MarkedRows)
	assert.Eq(t, int64(len(dedupeSessions)), status.PendingRows)
	assert.True(t, status.UndoComplete)

	// --abort drops both checkpoints; nothing else changes.
	assert.NoErr(t, s.ResetToolDedupe(ctx))
	status, err = s.ToolDedupeStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.False(t, status.Started)
	assert.False(t, status.UndoStarted)
	assert.Eq(t, int64(len(dedupeSessions)), status.PendingRows)
}

func TestToolDedupeResumesAfterInterrupt(t *testing.T) {
	s := openTemp(t)
	dedupeFixture(t, s)

	// Cancel inside the first batch's progress callback: the checkpoint of that
	// batch stands and the pass reports the interruption.
	cctx, cancel := context.WithCancel(context.Background())
	report, err := s.DedupeToolResults(cctx, ToolDedupeOptions{Batch: 1, OnBatch: func(ToolDedupeProgress) { cancel() }})
	assert.Require(t, assert.Err(t, err))
	assert.False(t, report.Complete)
	assert.True(t, report.Rows > 0)

	status, err := s.ToolDedupeStatus(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, status.Started)
	assert.False(t, status.Complete)
	assert.True(t, status.PendingRows > 0)

	// The rerun continues at the checkpoint and finishes the rest.
	rest, err := s.DedupeToolResults(context.Background(), ToolDedupeOptions{Batch: 1})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, rest.Complete)
	assert.Eq(t, int64(len(dedupeSessions)), report.Rows+rest.Rows)
	assert.NotEq(t, report.CursorSession, "")

	status, err = s.ToolDedupeStatus(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(0), status.PendingRows)
}

// TestToolDedupeRestartsAfterComplete: a completed pass sits on the last tool
// entry; a rerun must scan from the start again so it sees the double-stored rows
// written since it finished (a resumed cursor would hide them, suga-rykn). The
// --undo job shares the same runToolRewrite loop.
func TestToolDedupeRestartsAfterComplete(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	dedupeFixture(t, s)
	first, err := s.DedupeToolResults(ctx, ToolDedupeOptions{Batch: 2})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, first.Complete)
	assert.Eq(t, int64(len(dedupeSessions)), first.Rows)

	// A new double-stored row appears after the finish, past the cursor.
	call := &api.ToolCall{CallID: "cNew", Name: "shell"}
	_, err = s.AppendEntries(ctx, "s3", NewEntry{Type: EntryTool, JSON: msgJSON(t, api.Message{Role: "tool",
		Content: "same-new", ToolCall: call, ToolResult: &api.ToolResult{CallID: "cNew", Name: "shell", Content: "same-new"}})})
	assert.Require(t, assert.NoErr(t, err))

	again, err := s.DedupeToolResults(ctx, ToolDedupeOptions{Batch: 2})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, again.Complete)
	assert.Eq(t, int64(1), again.Rows, "the rerun scans from the start and rewrites the new row")
	status, err := s.ToolDedupeStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(0), status.PendingRows)
}

// TestToolDedupeKeepsReadPaths stores a real conversation (a tool call and its
// result) through the agent storage, dedupes it, and checks that the messages
// LoadMessages returns and the hits session_search reports are the same before
// and after.
func TestToolDedupeKeepsReadPaths(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	newSession(t, s, "s1")
	assert.NoErr(t, s.CreateTurn(ctx, Turn{ID: "turn_1", SessionID: "s1"}))

	call := &api.ToolCall{CallID: "c1", Name: "shell", Arguments: map[string]any{"cmd": "ls"}}
	tool := api.Message{Role: "tool", Content: "HEADER line\nmiddle\nFOOTER", ToolCall: call,
		ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: "HEADER line\nmiddle\nFOOTER"}}
	st := s.AgentStorage()
	assert.NoErr(t, st.AppendMessages(ctx, "turn_1",
		api.Message{Role: "user", Content: "run ls"},
		api.Message{Role: "assistant", Content: "calling", ToolCall: call},
		tool,
		api.Message{Role: "assistant", Content: "all done"}))

	// The store kept the tool body once, so an old binary would lose it: this is
	// what --undo exists for.
	entries, err := s.ListEntries(ctx, "s1", EntryFilter{TurnID: "turn_1", Types: []string{EntryTool}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 1, len(entries))
	assert.True(t, strings.Contains(string(entries[0].JSON), `"ResultSameAsContent":true`))

	msgsBefore, err := st.LoadMessages(ctx, "turn_1")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 4, len(msgsBefore))
	assert.Eq(t, tool, msgsBefore[2])

	// The historical double-stored form is what the dedupe command rewrites.
	_, err = s.AppendEntries(ctx, "s1", NewEntry{TurnID: "turn_2", Type: EntryTool, JSON: msgJSON(t, tool)})
	assert.Require(t, assert.NoErr(t, err))
	hitsBefore, err := s.SearchEntries(ctx, EntrySearch{Query: "HEADER"})
	assert.Require(t, assert.NoErr(t, err))
	// Tool output is not in the full-text index (only user/AI messages and
	// summaries are), so the query finds none of these tool entries; what
	// matters is that the dedupe leaves the hits unchanged (compared below).
	assert.Eq(t, 0, len(hitsBefore))

	// Undo gives the double-stored form back; neither pass changes what is read.
	_, err = s.DedupeToolResults(ctx, ToolDedupeOptions{Batch: 1})
	assert.Require(t, assert.NoErr(t, err))
	msgsAfter, err := st.LoadMessages(ctx, "turn_1")
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, reflect.DeepEqual(msgsBefore, msgsAfter))
	hitsAfter, err := s.SearchEntries(ctx, EntrySearch{Query: "HEADER"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, hitsBefore, hitsAfter)

	_, err = s.UndoToolDedupe(ctx, ToolDedupeOptions{Batch: 1})
	assert.Require(t, assert.NoErr(t, err))
	restored, err := s.ListEntries(ctx, "s1", EntryFilter{TurnID: "turn_2", Types: []string{EntryTool}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(msgJSON(t, tool)), string(restored[0].JSON))
	msgsRestored, err := st.LoadMessages(ctx, "turn_1")
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, reflect.DeepEqual(msgsBefore, msgsRestored))
}
