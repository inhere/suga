package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// TestToolBodyRefMigrationStaysFastOnALargeLedger: adding entries.body_ref is one
// ALTER (O(1)) whatever the ledger size. A migration that reads the rows would run
// on every open of a ~1 GB store, which is what made v0.31.0 hang.
func TestToolBodyRefMigrationStaysFastOnALargeLedger(t *testing.T) {
	ctx := context.Background()
	s, _ := openAtCoreSchema(t)
	defer s.Close()
	assert.Require(t, assert.NoErr(t, s.migrateUpTo(ctx, queryIndexMigration)))
	assert.Require(t, assert.NoErr(t, execNone(s, `INSERT INTO sessions(id, cwd, created_at, updated_at) VALUES ('big', '/w', 't', 't')`)))
	assert.Require(t, assert.NoErr(t, execNone(s, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 20000)
		INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
		SELECT 'big', x, '', 'tool', '{"Role":"tool","Content":"' || hex(zeroblob(200)) || '"}', 't' FROM c`)))

	start := time.Now()
	assert.Require(t, assert.NoErr(t, s.migrate(ctx)))
	elapsed := time.Since(start)
	t.Logf("body_ref migration over 20k entries: %v", elapsed)
	assert.True(t, elapsed < 2*time.Second, "body_ref migration took "+elapsed.String())

	var refs int
	assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE body_ref IS NULL`).Scan(&refs))
	assert.Eq(t, 20000, refs, "every existing row keeps a NULL body_ref")
}

// Tool body files (design 2026-10-19, 0.3): a tool entry above the threshold
// moves into sessions/<id>/tools.jsonl, reads hand back the same bytes.

// bigToolMessage is a tool message whose JSON is well above the default 1,024
// byte threshold.
func bigToolMessage(callID string) api.Message {
	body := strings.Repeat("line of tool output\n", 200)
	return api.Message{
		Role:     "tool",
		Content:  body,
		ToolCall: &api.ToolCall{CallID: callID, Name: "shell", Arguments: map[string]any{"cmd": "ls"}},
		ToolResult: &api.ToolResult{
			CallID: callID, Name: "shell", Content: body,
		},
	}
}

// storedRow reads one entries row as the database holds it.
func storedRow(t *testing.T, s *Store, sessionID string, seq int64) (body string, bodyRef string) {
	t.Helper()
	err := s.db.QueryRow(`SELECT json, COALESCE(body_ref, '') FROM entries WHERE session_id = ? AND seq = ?`,
		sessionID, seq).Scan(&body, &bodyRef)
	assert.Require(t, assert.NoErr(t, err))
	return body, bodyRef
}

func TestToolBodyMovesAboveThresholdAndReadsBack(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	big := msgJSON(t, bigToolMessage("c1"))
	appended := appendAll(t, s, "s1", user(t, "run ls"), NewEntry{Type: EntryTool, JSON: big})

	// The row holds a stub plus the location, and the file holds the body.
	row, ref := storedRow(t, s, "s1", appended[1].Seq)
	assert.True(t, ref != "", "body_ref must be set")
	assert.True(t, len(row) < len(big), "stored stub must be smaller than the body")
	path := filepath.Join(s.sessionsDir, "s1", ToolBodyFileName)
	data, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, string(data), `"seq":2,`)
	assert.StrContains(t, string(data), string(big))

	t.Run("ListEntries returns the original bytes", func(t *testing.T) {
		entries, err := s.ListEntries(ctx, "s1", EntryFilter{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, len(entries))
		assert.Eq(t, string(big), string(entries[1].JSON))
		// The user entry never left the ledger.
		_, userRef := storedRow(t, s, "s1", appended[0].Seq)
		assert.Eq(t, "", userRef)
	})

	t.Run("LatestEntry returns the original bytes", func(t *testing.T) {
		latest, err := s.LatestEntry(ctx, "s1", EntryTool)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, string(big), string(latest.JSON))
	})

	t.Run("a filter without tool touches no file", func(t *testing.T) {
		// Removing the file proves the read path did not open it.
		assert.Require(t, assert.NoErr(t, os.Remove(path)))
		entries, err := s.ListEntries(ctx, "s1", EntryFilter{Types: []string{EntryUser}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(entries))
	})
}

func TestToolBodySmallEntriesStayInline(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	small := msgJSON(t, toolMessage()) // below the threshold
	appended := appendAll(t, s, "s1", NewEntry{Type: EntryTool, JSON: small})
	row, ref := storedRow(t, s, "s1", appended[0].Seq)
	assert.Eq(t, "", ref)
	assert.Eq(t, string(small), row)
	_, err := os.Stat(filepath.Join(s.sessionsDir, "s1", ToolBodyFileName))
	assert.True(t, errors.Is(err, os.ErrNotExist), "no file is created for inline bodies")
	entries, err := s.ListEntries(ctx, "s1", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(small), string(entries[0].JSON))
}

func TestToolBodyThresholdZeroDisablesMoving(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db", "sessions.db")
	s, err := Open(path, WithToolBodyInlineBytes(0))
	assert.Require(t, assert.NoErr(t, err))
	defer s.Close()
	createSession(t, s, "s1", "/w")
	big := msgJSON(t, bigToolMessage("c1"))
	appended := appendAll(t, s, "s1", NewEntry{Type: EntryTool, JSON: big})
	row, ref := storedRow(t, s, "s1", appended[0].Seq)
	assert.Eq(t, "", ref)
	assert.Eq(t, string(big), row)
}

func TestToolBodyMissingAndTamperedRecordsAreErrors(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T) (*Store, string) {
		s := openTemp(t)
		createSession(t, s, "s1", "/w")
		appendAll(t, s, "s1", NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c1"))})
		return s, filepath.Join(s.sessionsDir, "s1", ToolBodyFileName)
	}

	t.Run("missing file", func(t *testing.T) {
		s, path := open(t)
		assert.Require(t, assert.NoErr(t, os.Remove(path)))
		_, err := s.ListEntries(ctx, "s1", EntryFilter{})
		assert.True(t, errors.Is(err, ErrToolBodyMissing), "want ErrToolBodyMissing, got %v", err)
		assert.StrContains(t, err.Error(), "has no tools.jsonl")
	})

	t.Run("tampered record", func(t *testing.T) {
		s, path := open(t)
		data, err := os.ReadFile(path)
		assert.Require(t, assert.NoErr(t, err))
		// Flip one byte of the body: the sha256 of the record no longer matches.
		at := strings.Index(string(data), `"json":`) + len(`"json":`) + 2
		data[at] ^= 0x01
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, data, 0o600)))
		_, err = s.ListEntries(ctx, "s1", EntryFilter{})
		assert.True(t, errors.Is(err, ErrToolBodyMissing), "want ErrToolBodyMissing, got %v", err)
		assert.StrContains(t, err.Error(), "sha256")
	})

	t.Run("record of another seq", func(t *testing.T) {
		s, _ := open(t)
		// Point seq 1 at a record written for seq 7: the header check rejects it.
		writer, err := s.openToolBodyWriter("s1")
		assert.Require(t, assert.NoErr(t, err))
		ref, err := writer.WriteRecord(7, []byte(`{"Role":"tool"}`))
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.NoErr(t, writer.Close(true)))
		_, err = s.db.Exec(`UPDATE entries SET body_ref = ? WHERE session_id = 's1' AND seq = 1`, ref.String())
		assert.Require(t, assert.NoErr(t, err))
		_, err = s.ListEntries(ctx, "s1", EntryFilter{})
		assert.True(t, errors.Is(err, ErrToolBodyMissing), "want ErrToolBodyMissing, got %v", err)
		assert.StrContains(t, err.Error(), "record holds seq 7")
	})
}

func TestToolBodyWriteFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// A sessions root that is a regular file: the session directory cannot be
	// created, so the file write fails inside the transaction.
	blocked := filepath.Join(dir, "sessions")
	assert.Require(t, assert.NoErr(t, os.WriteFile(blocked, []byte("x"), 0o600)))
	s, err := Open(filepath.Join(dir, "db", "sessions.db"), WithSessionsDir(blocked))
	assert.Require(t, assert.NoErr(t, err))
	defer s.Close()
	createSession(t, s, "s1", "/w")

	_, err = s.AppendEntries(ctx, "s1", NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c1"))})
	assert.True(t, err != nil, "the append must fail when the body cannot be written")
	var rows int
	assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE session_id = 's1'`).Scan(&rows))
	assert.Eq(t, 0, rows, "the rolled back transaction keeps no row")
}

// TestToolBodyRecordIsTruncatedWhenTheTransactionFails: the record is written
// into the file before the row is swapped to its stub, so a failure after that
// write must leave no record behind (the transaction rolls back and the writer
// truncates the file to the length it found).
func TestToolBodyRecordIsTruncatedWhenTheTransactionFails(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	defer s.Close()
	createSession(t, s, "s1", "/w")
	// The stub swap is the step after the record write: make it fail.
	_, err := s.DB().Exec(`CREATE TRIGGER fail_stub BEFORE UPDATE ON entries
		WHEN NEW.body_ref IS NOT NULL BEGIN SELECT RAISE(ABORT, 'boom'); END`)
	assert.Require(t, assert.NoErr(t, err))

	_, err = s.AppendEntries(ctx, "s1", NewEntry{Type: EntryTool, JSON: msgJSON(t, bigToolMessage("c1"))})
	assert.True(t, err != nil, "the stub swap fails")
	var rows int
	assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE session_id = 's1'`).Scan(&rows))
	assert.Eq(t, 0, rows, "the rolled back transaction keeps no row")
	data, readErr := os.ReadFile(filepath.Join(s.sessionsDir, "s1", ToolBodyFileName))
	if !errors.Is(readErr, os.ErrNotExist) {
		assert.Require(t, assert.NoErr(t, readErr))
		assert.Eq(t, 0, len(data), "the record was truncated again")
	}
}

func TestToolBodyConcurrentAppendsFromTwoStores(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db", "sessions.db")
	a, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer a.Close()
	b, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer b.Close()
	createSession(t, a, "s1", "/w")

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			for n := 0; n < 5; n++ {
				msg := bigToolMessage("c" + string(rune('a'+i)) + string(rune('0'+n)))
				if _, err := s.AppendEntries(context.Background(), "s1", NewEntry{Type: EntryTool, JSON: msgJSON(t, msg)}); err != nil {
					errs <- err
				}
			}
		}(i, s)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("append failed: %v", err)
	}
	entries, err := a.ListEntries(context.Background(), "s1", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 10, len(entries))
	for _, e := range entries {
		var m api.Message
		assert.Require(t, assert.NoErr(t, json.Unmarshal(e.JSON, &m)))
		assert.True(t, m.ToolResult != nil && len(m.ToolResult.Content) > 1000, "body of seq %d was not restored", e.Seq)
	}
}
