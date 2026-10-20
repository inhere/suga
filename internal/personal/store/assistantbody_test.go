package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// Assistant body files (design 2026-10-20, 0.3): an assistant entry above the
// threshold moves into sessions/<id>/tools.jsonl as well, and its stub keeps the
// full Content (the full-text search indexes it) while Reasoning, Metadata and the
// tool call arguments stay in the record.

// bigAssistantMessage is an assistant message whose JSON is well above the
// default 1,024 byte threshold: the reasoning, the encrypted reasoning of a
// Responses provider and a file-writing tool call argument.
func bigAssistantMessage(callID string) api.Message {
	return api.Message{
		Role:      "assistant",
		Content:   "把实现写进新文件",
		Reasoning: strings.Repeat("先看一下现有的实现，再动手改。", 40),
		Metadata:  map[string]any{"suga.responses_reasoning": strings.Repeat("enc", 200)},
		ToolCall: &api.ToolCall{CallID: callID, Name: "file_write",
			Arguments: map[string]any{"path": "a.go", "content": strings.Repeat("y", 2000)}},
	}
}

// ftsRowOf reads the full-text row of one entry as the search sees it.
func ftsRowOf(t *testing.T, s *Store, sessionID string, seq int64) (role, text string) {
	t.Helper()
	err := s.db.QueryRow(`SELECT role, text FROM entries_fts WHERE session_id = ? AND seq = ?`, sessionID, seq).Scan(&role, &text)
	assert.Require(t, assert.NoErr(t, err))
	return role, text
}

// ftsTextOfRow is the text the full-text expression extracts from the stored row,
// which is what a backfill or a rebuild of the index writes.
func ftsTextOfRow(t *testing.T, s *Store, sessionID string, seq int64) string {
	t.Helper()
	var text string
	q := `SELECT text FROM (` + ftsSourceSelect(` AND session_id = ? AND seq = ?`) + `)`
	assert.Require(t, assert.NoErr(t, s.db.QueryRow(q, sessionID, seq).Scan(&text)))
	return text
}

func TestAssistantBodyMovesOutAndReadsBack(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	big := msgJSON(t, bigAssistantMessage("c1"))
	appended := appendAll(t, s, "s1", NewEntry{Type: EntryAssistant, JSON: big})

	// The row holds a stub plus the location of the record; the file holds the body.
	row, ref := storedRow(t, s, "s1", appended[0].Seq)
	assert.True(t, ref != "", "body_ref must be set")
	assert.True(t, len(row) < len(big)/2, "the stub must be much smaller than the body: %d vs %d", len(row), len(big))
	path := filepath.Join(s.sessionsDir, "s1", LegacyBodyFileName)
	data, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, string(data), `"seq":1,`)
	assert.StrContains(t, string(data), string(big))

	// The stub keeps exactly the fields a reader of the row still needs.
	var stub api.Message
	assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(row), &stub)))
	assert.Eq(t, "assistant", stub.Role)
	assert.Eq(t, "把实现写进新文件", stub.Content, "the stub keeps the whole Content")
	assert.Require(t, assert.True(t, stub.ToolCall != nil))
	assert.Eq(t, "c1", stub.ToolCall.CallID)
	assert.Eq(t, "file_write", stub.ToolCall.Name)
	assert.Eq(t, 0, len(stub.ToolCall.Arguments), "the stub carries no call arguments")
	assert.Eq(t, "", stub.Reasoning)
	assert.Eq(t, 0, len(stub.Metadata))
	assert.False(t, strings.Contains(row, `"Reasoning"`))
	assert.False(t, strings.Contains(row, `"Metadata"`))
	assert.False(t, strings.Contains(row, `"Arguments"`))

	t.Run("ListEntries returns the original bytes", func(t *testing.T) {
		entries, err := s.ListEntries(ctx, "s1", EntryFilter{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(entries))
		assert.Eq(t, string(big), string(entries[0].JSON))
		assert.False(t, entries[0].Stub(), "a hydrated entry is not a stub")
	})

	t.Run("LatestEntry returns the original bytes", func(t *testing.T) {
		latest, err := s.LatestEntry(ctx, "s1", EntryAssistant)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, string(big), string(latest.JSON))
	})

	t.Run("the call id is still readable from the row", func(t *testing.T) {
		refs, err := s.ListEntryRefs(ctx, "s1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(refs))
		assert.Eq(t, "c1", refs[0].CallID)
	})
}

// TestAssistantBodyKeepsTheFullTextRow: the full-text row is written from the
// entry JSON before the row is swapped to its stub, and the stub keeps the whole
// Content, so the indexed text is exactly what an inline write produces.
func TestAssistantBodyKeepsTheFullTextRow(t *testing.T) {
	moved := openTemp(t)
	inline, err := Open(filepath.Join(t.TempDir(), "db", "sessions.db"), WithToolBodyInlineBytes(0))
	assert.Require(t, assert.NoErr(t, err))
	defer inline.Close()

	big := msgJSON(t, bigAssistantMessage("c1"))
	createSession(t, moved, "s1", "/w")
	createSession(t, inline, "s1", "/w")
	appendAll(t, moved, "s1", NewEntry{Type: EntryAssistant, JSON: big})
	appendAll(t, inline, "s1", NewEntry{Type: EntryAssistant, JSON: big})

	roleMoved, textMoved := ftsRowOf(t, moved, "s1", 1)
	roleInline, textInline := ftsRowOf(t, inline, "s1", 1)
	assert.Eq(t, roleInline, roleMoved)
	assert.Eq(t, textInline, textMoved)
	assert.Eq(t, "assistant", roleMoved)
	assert.StrContains(t, textMoved, "把实现写进新文件")
	// A backfill or rebuild reads the stored row: a stub has to yield the same text
	// as the row it replaced, or a reindex would clip every moved entry.
	assert.Eq(t, ftsTextOfRow(t, inline, "s1", 1), ftsTextOfRow(t, moved, "s1", 1))
	assert.StrContains(t, ftsTextOfRow(t, moved, "s1", 1), "把实现写进新文件")
	// Only Content is indexed: the reasoning never was part of the row, so a stub
	// that drops it cannot change the row.
	assert.False(t, strings.Contains(textMoved, "先看一下现有的实现"))
}

// TestAssistantBodyMovesOnlyWhenTheStubSaves: the stub has to keep the Content, so
// an entry whose size is mostly Content stays inline (design 0.3, revision 8).
func TestAssistantBodyMovesOnlyWhenTheStubSaves(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	// ~8 KB of Content and nothing the stub would drop: the savings stay below the
	// threshold.
	bigContent := msgJSON(t, api.Message{Role: "assistant", Content: strings.Repeat("答", 2000)})
	// Mostly reasoning: the stub drops nearly all of it.
	bigReasoning := msgJSON(t, api.Message{Role: "assistant", Content: "好",
		Reasoning: strings.Repeat("想一想", 500)})
	appended := appendAll(t, s, "s1",
		NewEntry{Type: EntryAssistant, JSON: bigContent},
		NewEntry{Type: EntryAssistant, JSON: bigReasoning})

	row, ref := storedRow(t, s, "s1", appended[0].Seq)
	assert.Eq(t, "", ref, "a big Content alone does not move the entry")
	assert.Eq(t, string(bigContent), row)
	_, ref = storedRow(t, s, "s1", appended[1].Seq)
	assert.True(t, ref != "", "an entry the stub shrinks below the threshold moves")

	entries, err := s.ListEntries(ctx, "s1", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(bigContent), string(entries[0].JSON))
	assert.Eq(t, string(bigReasoning), string(entries[1].JSON))
}

// TestListEntriesNoHydrateLeavesTheStubs: a reader that only needs Content or
// metadata (EntryFilter.NoHydrate) never opens the session file, and
// HydrateEntries reads back exactly the entries it is handed.
func TestListEntriesNoHydrateLeavesTheStubs(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	big := msgJSON(t, bigAssistantMessage("c1"))
	appended := appendAll(t, s, "s1", user(t, "写个文件"),
		NewEntry{Type: EntryAssistant, JSON: big},
		NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c1"))})
	assert.Eq(t, 3, len(appended))
	path := filepath.Join(s.sessionsDir, "s1", LegacyBodyFileName)
	// Removing the file proves which reads open it: NoHydrate must not.
	assert.Require(t, assert.NoErr(t, os.Remove(path)))

	entries, err := s.ListEntries(ctx, "s1", EntryFilter{
		Types: []string{EntryUser, EntryAssistant, EntryTool}, NoHydrate: true,
	})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 3, len(entries))
	assert.False(t, entries[0].Stub(), "the user entry never moved out")
	assert.True(t, entries[1].Stub() && entries[2].Stub())

	// The assistant stub keeps its Content, so a reader of that field is served
	// without the file; the tool stub keeps the call id and name.
	var stub api.Message
	assert.Require(t, assert.NoErr(t, json.Unmarshal(entries[1].JSON, &stub)))
	assert.Eq(t, "把实现写进新文件", stub.Content)
	assert.Eq(t, "c1", stub.ToolCall.CallID)
	assert.Eq(t, "file_write", stub.ToolCall.Name)
	var toolStub api.Message
	assert.Require(t, assert.NoErr(t, json.Unmarshal(entries[2].JSON, &toolStub)))
	assert.Eq(t, "c1", toolStub.ToolResult.CallID)

	// Hydrating an inline entry needs no file; hydrating a stub whose record is gone
	// is an error, never a stub sent on as the body.
	assert.NoErr(t, s.HydrateEntries(ctx, "s1", entries[:1]))
	assert.False(t, entries[0].Stub())
	err = s.HydrateEntries(ctx, "s1", entries[1:2])
	assert.True(t, errors.Is(err, ErrToolBodyMissing), "want ErrToolBodyMissing, got %v", err)
	assert.True(t, entries[1].Stub(), "a failed hydration leaves the stub as it was")
	_, err = s.ListEntries(ctx, "s1", EntryFilter{})
	assert.True(t, errors.Is(err, ErrToolBodyMissing), "want ErrToolBodyMissing, got %v", err)
}

// TestAssistantBodyWriteFailureRollsBack: moving an assistant body out happens
// inside the append transaction like a tool body, so a failed file write leaves no
// row behind.
func TestAssistantBodyWriteFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blocked := filepath.Join(dir, "sessions")
	assert.Require(t, assert.NoErr(t, os.WriteFile(blocked, []byte("x"), 0o600)))
	s, err := Open(filepath.Join(dir, "db", "sessions.db"), WithSessionsDir(blocked))
	assert.Require(t, assert.NoErr(t, err))
	defer s.Close()
	createSession(t, s, "s1", "/w")

	_, err = s.AppendEntries(ctx, "s1", NewEntry{Type: EntryAssistant, JSON: msgJSON(t, bigAssistantMessage("c1"))})
	assert.True(t, err != nil, "the append must fail when the body cannot be written")
	var rows int
	assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE session_id = 's1'`).Scan(&rows))
	assert.Eq(t, 0, rows, "the rolled back transaction keeps no row")
}

// TestAssistantStubKeepsFullContent: an assistant stub keeps the whole Content
// (a preview would clip the indexed text), so reading it back needs no rewrite;
// the removed --slim pass never touched it and nothing narrows it now.
func TestAssistantStubKeepsFullContent(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	msg := bigAssistantMessage("c1")
	appended := appendAll(t, s, "s1", NewEntry{Type: EntryAssistant, JSON: msgJSON(t, msg)})
	before, refBefore := storedRow(t, s, "s1", appended[0].Seq)
	assert.True(t, refBefore != "")
	assert.StrContains(t, before, msg.Content, "the stub keeps the whole content")
	assert.Eq(t, string(msgJSON(t, msg)), storedEntries(t, s, "s1")[appended[0].Seq], "reading the row returns the original message")
}
