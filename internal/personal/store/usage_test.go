package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// usageFixture is a ledger with one row of every kind the usage report counts:
// conversation entries, a tool result inline and one moved to its session file,
// events, FTS rows and session files.
type usageFixture struct {
	dbPath, sessionsDir string
	assistantJSON       []byte
	metaJSON            []byte
	argsJSON            []byte
	eventBytes          int64
}

func newUsageFixture(t *testing.T) usageFixture {
	t.Helper()
	root := t.TempDir()
	fx := usageFixture{dbPath: filepath.Join(root, "db", "sessions.db"), sessionsDir: filepath.Join(root, "sessions")}
	s, err := Open(fx.dbPath, WithSessionsDir(fx.sessionsDir))
	assert.Require(t, assert.NoErr(t, err))
	ctx := context.Background()
	createSession(t, s, "s1", "/w")
	createSession(t, s, "s2", "/w")

	meta := map[string]any{
		"suga.responses_reasoning": "the stored reasoning of the provider",
		"keep":                     "short",
	}
	args := map[string]any{"path": "/w/a.txt", "content": "written by the model"}
	assistant := api.Message{
		Role: "assistant", Content: "an answer", Reasoning: "a thought", Metadata: meta,
		ToolCall: &api.ToolCall{CallID: "c1", Name: "file_write", Arguments: args},
	}
	fx.assistantJSON = msgJSON(t, assistant)
	fx.metaJSON, err = json.Marshal(meta)
	assert.Require(t, assert.NoErr(t, err))
	fx.argsJSON, err = json.Marshal(args)
	assert.Require(t, assert.NoErr(t, err))

	small := api.Message{Role: "tool", Content: "small",
		ToolResult: &api.ToolResult{CallID: "c1", Name: "file_write", Content: "small"}}
	big := api.Message{Role: "tool", Content: bigToolContent(),
		ToolResult: &api.ToolResult{CallID: "c2", Name: "shell", Content: bigToolContent()}}
	appendAll(t, s, "s1",
		NewEntry{TurnID: "t1", Type: EntryUser, JSON: json.RawMessage(`{"Role":"user","Content":"find the searchable words"}`)},
		NewEntry{TurnID: "t1", Type: EntryAssistant, JSON: fx.assistantJSON},
		NewEntry{TurnID: "t1", Type: EntryTool, JSON: msgJSON(t, small)},
		NewEntry{TurnID: "t1", Type: EntryTool, JSON: msgJSON(t, big)},
		NewEntry{TurnID: "t1", Type: EntryPlan, JSON: json.RawMessage(`{"steps":[1]}`)},
	)
	appendAll(t, s, "s2", NewEntry{TurnID: "t2", Type: EntryUser, JSON: json.RawMessage(`{"Role":"user","Content":"another searchable message"}`)})

	events := []Event{
		{Kind: EventTool, Name: "file_write", StartedAt: time.Now(), Attrs: map[string]any{"raw_bytes": float64(10)}},
		{Kind: EventTool, Name: "shell", StartedAt: time.Now(), Attrs: map[string]any{"raw_bytes": float64(20)}},
		{Kind: EventModel, Name: "openai", StartedAt: time.Now(), Attrs: map[string]any{"model": "gpt", "cost_usd": 0.5}},
	}
	for _, event := range events {
		event.SessionID = "s1"
		_, err := s.AppendEvent(ctx, event)
		assert.Require(t, assert.NoErr(t, err))
		fx.eventBytes += int64(len(mustJSON(t, event.Attrs)) + len(event.Error))
	}
	assert.NoErr(t, s.Close())

	// Session files: one tool body file (written by the move above), spill
	// outputs plain and compressed, an image and a snapshot.
	writeSessionFile(t, filepath.Join(fx.sessionsDir, "s1", "outputs", "turn-1.txt"), "spill")
	writeSessionFile(t, filepath.Join(fx.sessionsDir, "s1", "outputs", "turn-2.txt.gz"), "gz!")
	writeSessionFile(t, filepath.Join(fx.sessionsDir, "s1", "images", "aa.png"), "png")
	writeSessionFile(t, filepath.Join(fx.sessionsDir, "s2", "snapshots", "before"), "snap")
	writeSessionFile(t, filepath.Join(fx.sessionsDir, "s2", "notes.txt"), "note")
	return fx
}

// bigToolContent is a tool result above the inline threshold, so the store moves
// it into the session file and leaves a stub in the ledger.
func bigToolContent() string {
	body := make([]byte, 4096)
	for i := range body {
		body[i] = 'x'
	}
	return string(body)
}

func writeSessionFile(t *testing.T, path, content string) {
	t.Helper()
	assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o700)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(content), 0o600)))
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	assert.Require(t, assert.NoErr(t, err))
	return raw
}

// openUsageFixture opens the fixture read-only, which is how the command reads a
// ledger a running suga may be writing.
func openUsageFixture(t *testing.T, fx usageFixture) *Store {
	t.Helper()
	ro, err := OpenReadOnly(fx.dbPath, WithSessionsDir(fx.sessionsDir))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = ro.Close() })
	return ro
}

func TestUsageReport(t *testing.T) {
	ctx := context.Background()
	fx := newUsageFixture(t)
	ro := openUsageFixture(t, fx)

	t.Run("file and free pages", func(t *testing.T) {
		rep, err := ro.Usage(ctx, UsageOptions{Top: 10})
		assert.Require(t, assert.NoErr(t, err))
		info, err := os.Stat(fx.dbPath)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, info.Size(), rep.File.Bytes)
		assert.True(t, rep.File.PageSize > 0)
		assert.Eq(t, rep.File.Pages*rep.File.PageSize, rep.File.Bytes, "page count covers the file")
		assert.Eq(t, rep.File.FreePages*rep.File.PageSize, rep.File.FreeBytes)
		assert.Eq(t, fx.dbPath, rep.File.Path)
	})

	t.Run("entries by type and the assistant split", func(t *testing.T) {
		rep, err := ro.Usage(ctx, UsageOptions{Top: 10})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(6), rep.Entries.Rows)
		byName := map[string]NamedStat{}
		for _, stat := range rep.Entries.Types {
			byName[stat.Name] = stat
		}
		assert.Eq(t, int64(2), byName[EntryUser].Rows)
		assert.Eq(t, int64(1), byName[EntryAssistant].Rows)
		assert.Eq(t, int64(2), byName[EntryTool].Rows)
		assert.Eq(t, int64(len(fx.assistantJSON)), byName[EntryAssistant].Bytes)

		assert.Eq(t, int64(1), rep.Entries.Assistant.Rows)
		fields := map[string]int64{}
		for _, stat := range rep.Entries.Assistant.Fields {
			fields[stat.Name] = stat.Bytes
		}
		assert.Eq(t, int64(len("an answer")), fields["Content"])
		assert.Eq(t, int64(len("a thought")), fields["Reasoning"])
		assert.Eq(t, int64(len(fx.metaJSON)), fields["Metadata"])
		assert.Eq(t, int64(len(fx.argsJSON)), fields["ToolCall.Arguments"])

		assert.Eq(t, "suga.responses_reasoning", rep.Entries.Assistant.MetadataKeys[0].Name)
		assert.Eq(t, int64(len("the stored reasoning of the provider")), rep.Entries.Assistant.MetadataKeys[0].Bytes)
		assert.Eq(t, 2, len(rep.Entries.Assistant.MetadataKeys))

		assert.Eq(t, 1, len(rep.Entries.Assistant.Tools))
		assert.Eq(t, "file_write", rep.Entries.Assistant.Tools[0].Name)
		assert.Eq(t, int64(1), rep.Entries.Assistant.Tools[0].Rows)
		assert.Eq(t, int64(len(fx.argsJSON)), rep.Entries.Assistant.Tools[0].Bytes)
	})

	t.Run("tool results: one stub, one inline", func(t *testing.T) {
		rep, err := ro.Usage(ctx, UsageOptions{Top: 10})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(1), rep.Entries.Tool.Stub.Rows)
		assert.Eq(t, int64(1), rep.Entries.Tool.Inline.Rows)
		assert.Contains(t, rep.Entries.Suggestion, "suga data bodies --move")
	})

	t.Run("events by kind", func(t *testing.T) {
		rep, err := ro.Usage(ctx, UsageOptions{Top: 10})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(3), rep.Events.Rows)
		assert.Eq(t, fx.eventBytes, rep.Events.Bytes)
		assert.Eq(t, EventTool, rep.Events.Kinds[0].Name)
		assert.Eq(t, int64(2), rep.Events.Kinds[0].Rows)
	})

	t.Run("fts rows and shadow tables", func(t *testing.T) {
		rep, err := ro.Usage(ctx, UsageOptions{Top: 10})
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, rep.FTS.Rows >= 3, "the conversation entries are indexed")
		roles := map[string]int64{}
		for _, role := range rep.FTS.Roles {
			roles[role.Name] = role.Rows
		}
		assert.Eq(t, int64(2), roles[EntryUser])
		assert.Eq(t, int64(1), roles[EntryAssistant])
		assert.Eq(t, int64(0), roles[EntryTool], "tool results are not indexed")
		assert.True(t, rep.FTS.DataBytes > 0, "the inverted index has blocks")
		assert.Eq(t, rep.FTS.DataBytes+rep.FTS.ContentBytes, rep.FTS.Bytes)
	})

	t.Run("sessions directory", func(t *testing.T) {
		rep, err := ro.Usage(ctx, UsageOptions{Top: 10})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(2), rep.Sessions.InStore)
		assert.Eq(t, int64(2), rep.Sessions.Dirs)
		assert.Eq(t, int64(1), rep.Sessions.Tools.Files)
		assert.True(t, rep.Sessions.Tools.Bytes > int64(len(bigToolContent())))
		assert.Eq(t, int64(2), rep.Sessions.Outputs.Files)
		assert.Eq(t, int64(1), rep.Sessions.OutputsGz.Files)
		assert.Eq(t, int64(len("spill")+len("gz!")), rep.Sessions.Outputs.Bytes)
		assert.Eq(t, int64(1), rep.Sessions.Images.Files)
		assert.Eq(t, int64(1), rep.Sessions.Snapshots.Files)
		assert.Eq(t, int64(1), rep.Sessions.Other.Files)
		assert.Eq(t, "s1", rep.Sessions.Top[0].ID)
		assert.True(t, rep.Sessions.Top[0].DirBytes > 0)
		assert.Eq(t, rep.TotalBytes, rep.File.Bytes+rep.Sessions.Tools.Bytes+rep.Sessions.Outputs.Bytes+
			rep.Sessions.Images.Bytes+rep.Sessions.Snapshots.Bytes+rep.Sessions.Other.Bytes)
	})

	t.Run("top bounds every list", func(t *testing.T) {
		rep, err := ro.Usage(ctx, UsageOptions{Top: 1})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(rep.Sessions.Top))
		assert.Eq(t, 1, len(rep.Entries.Assistant.MetadataKeys))
		assert.Eq(t, 1, len(rep.Entries.Assistant.Tools))
		// Without --top the report uses its own default.
		def, err := ro.Usage(ctx, UsageOptions{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, usageDefaultTop, def.Top)
	})
}

// TestUsageIsReadOnly: the report leaves the database file as it found it - same
// size, same mtime, no WAL beside it.
func TestUsageIsReadOnly(t *testing.T) {
	ctx := context.Background()
	fx := newUsageFixture(t)
	before, err := os.Stat(fx.dbPath)
	assert.Require(t, assert.NoErr(t, err))
	ro := openUsageFixture(t, fx)
	_, err = ro.Usage(ctx, UsageOptions{Top: 5})
	assert.Require(t, assert.NoErr(t, err))
	after, err := os.Stat(fx.dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, before.Size(), after.Size())
	assert.Eq(t, before.ModTime(), after.ModTime())
	// SQLite creates an empty -wal beside a WAL database even for a reader, but a
	// reader writes no page into it: the file stays at zero bytes.
	assert.Eq(t, int64(0), walState(t, fx.dbPath), "a read-only report writes no WAL")
}

// walState is the size of the WAL beside a database, -1 when there is none.
func walState(t *testing.T, dbPath string) int64 {
	t.Helper()
	info, err := os.Stat(dbPath + "-wal")
	if os.IsNotExist(err) {
		return -1
	}
	assert.Require(t, assert.NoErr(t, err))
	return info.Size()
}

// TestUsageFTSToolRowsSuggestShrink: a ledger whose index still holds historical
// tool rows gets the shrink-fts command (new writes no longer index them).
func TestUsageFTSToolRowsSuggestShrink(t *testing.T) {
	ctx := context.Background()
	fx := newUsageFixture(t)
	rw, err := Open(fx.dbPath, WithSessionsDir(fx.sessionsDir))
	assert.Require(t, assert.NoErr(t, err))
	_, err = rw.DB().ExecContext(ctx, `INSERT INTO entries_fts(session_id, seq, role, text) VALUES ('s1', 99, 'tool', 'old tool output')`)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, rw.Close())

	ro := openUsageFixture(t, fx)
	rep, err := ro.Usage(ctx, UsageOptions{Top: 5})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(1), rolesOf(rep.FTS.Roles)[EntryTool])
	assert.Contains(t, rep.FTS.Suggestion, "suga data shrink-fts")
}

// rolesOf turns the FTS role list into a lookup for the assertions.
func rolesOf(roles []NamedStat) map[string]int64 {
	out := make(map[string]int64, len(roles))
	for _, role := range roles {
		out[role.Name] = role.Rows
	}
	return out
}

// TestUsageFreePagesSuggestVacuum: pages a deletion freed stay in the file, and
// the report names the command that returns them.
func TestUsageFreePagesSuggestVacuum(t *testing.T) {
	ctx := context.Background()
	fx := newUsageFixture(t)
	rw, err := Open(fx.dbPath, WithSessionsDir(fx.sessionsDir))
	assert.Require(t, assert.NoErr(t, err))
	_, err = rw.DB().ExecContext(ctx, `DELETE FROM entries WHERE session_id = 's1'`)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, rw.Close())

	ro := openUsageFixture(t, fx)
	rep, err := ro.Usage(ctx, UsageOptions{Top: 5})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, rep.File.FreePages > 0, "the deleted rows left free pages")
	assert.Eq(t, rep.File.FreePages*rep.File.PageSize, rep.File.FreeBytes)
	assert.Contains(t, rep.File.Suggestion, "suga data vacuum")
}

// TestUsageOlderStore: a store written before the body_ref column and the FTS
// index existed still reports (no dbstat-style surprises, no error): every tool
// row counts as inline and the index section stays empty.
func TestUsageOlderStore(t *testing.T) {
	ctx := context.Background()
	s, path := openAtCoreSchema(t)
	writeHistory(t, s, "old", []NewEntry{
		{Type: EntryUser, JSON: []byte(`{"Role":"user","Content":"hello"}`)},
		{Type: EntryTool, JSON: []byte(`{"Role":"tool","ToolResult":{"CallID":"c1","Name":"bash","Content":"ok"}}`)},
	})
	assert.NoErr(t, s.Close())

	ro, err := OpenReadOnly(path)
	assert.Require(t, assert.NoErr(t, err))
	defer ro.Close()
	rep, err := ro.Usage(ctx, UsageOptions{Top: 5})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(2), rep.Entries.Rows)
	assert.Eq(t, int64(1), rep.Entries.Tool.Inline.Rows)
	assert.Eq(t, int64(0), rep.Entries.Tool.Stub.Rows)
	assert.Eq(t, int64(0), rep.FTS.Rows, "this store has no entries_fts")
	assert.Eq(t, int64(0), rep.Sessions.Dirs, "the derived sessions root does not exist")
}

// TestUsageTablesFromDBStat covers the dbstat section: either this SQLite build
// has the virtual table and reports page usage, or the report says it does not.
func TestUsageTablesFromDBStat(t *testing.T) {
	ctx := context.Background()
	fx := newUsageFixture(t)
	ro := openUsageFixture(t, fx)
	rep, err := ro.Usage(ctx, UsageOptions{Top: 30})
	assert.Require(t, assert.NoErr(t, err))
	if !rep.DBStat {
		assert.Eq(t, 0, len(rep.Tables))
		t.Log("this SQLite build has no dbstat virtual table; byte statistics stand alone")
		return
	}
	names := map[string]int64{}
	for _, table := range rep.Tables {
		names[table.Name] = table.Bytes
	}
	assert.True(t, names["entries"] > 0, "the entries table holds pages")
	assert.True(t, names["sessions"] > 0)
}
