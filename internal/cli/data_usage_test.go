package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// TestDataUsageCommand covers `suga data usage` through the CLI: the JSON report,
// the text report and its --top bound. The ledger is seeded in an isolated
// SUGA_HOME with one row of every kind the report counts.
func TestDataUsageCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_HOOKS", "off")
	ctx := context.Background()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "u1", Title: "a session title that is far too long for one row of the table"}))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "u2", Title: "the second session"}))
	assistant := `{"Role":"assistant","Content":"an answer","Reasoning":"a thought",` +
		`"Metadata":{"suga.responses_reasoning":"why it answered"},` +
		`"ToolCall":{"CallID":"c1","Name":"file_write","Arguments":{"path":"/w/a.txt"}}}`
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	tool := `{"Role":"tool","ToolResult":{"CallID":"c1","Name":"file_write","Content":"` + string(big) + `"}}`
	_, err = repo.AppendEntries(ctx, "u1",
		store.NewEntry{Type: store.EntryUser, JSON: json.RawMessage(`{"Role":"user","Content":"searchable input"}`)},
		store.NewEntry{Type: store.EntryAssistant, JSON: json.RawMessage(assistant)},
		store.NewEntry{Type: store.EntryTool, JSON: json.RawMessage(tool)},
	)
	assert.Require(t, assert.NoErr(t, err))
	for _, event := range []store.Event{
		{SessionID: "u1", Kind: store.EventTool, Name: "file_write", StartedAt: time.Now()},
		{SessionID: "u1", Kind: store.EventTool, Name: "file_write", StartedAt: time.Now()},
		{SessionID: "u2", Kind: store.EventModel, Name: "openai", StartedAt: time.Now(),
			Attrs: map[string]any{"model": "m"}},
	} {
		_, err := repo.AppendEvent(ctx, event)
		assert.Require(t, assert.NoErr(t, err))
	}
	assert.NoErr(t, repo.Close())
	// The session files the report measures: a spilled output and an image.
	sessionsDir := filepath.Join(home, "sessions")
	assert.NoErr(t, os.MkdirAll(filepath.Join(sessionsDir, "u1", "outputs"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(sessionsDir, "u1", "outputs", "t1.txt"), []byte("spilled output"), 0o600))
	assert.NoErr(t, os.MkdirAll(filepath.Join(sessionsDir, "u2", "images"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(sessionsDir, "u2", "images", "aa.png"), []byte("png"), 0o600))
	before, err := os.Stat(dbPath)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("json report", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "usage", "--json")
		assert.Require(t, assert.NoErr(t, err))
		var rep store.UsageReport
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(out), &rep)))
		assert.Eq(t, int64(3), rep.Entries.Rows)
		assert.Eq(t, int64(1), rep.Entries.Tool.Stub.Rows, "the 4KB result moved to the session file")
		assert.Eq(t, int64(3), rep.Events.Rows)
		assert.Eq(t, int64(2), rep.Sessions.InStore)
		assert.Eq(t, int64(1), rep.Sessions.Outputs.Files)
		assert.Eq(t, int64(1), rep.Sessions.Images.Files)
		assert.Eq(t, "u1", rep.Sessions.Top[0].ID)
		assert.Eq(t, int64(len("spilled output")), rep.Sessions.Outputs.Bytes)
	})

	t.Run("text report", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "usage")
		assert.Require(t, assert.NoErr(t, err))
		for _, want := range []string{"库文件", "entries", "events", "全文索引", "会话目录", "合计", "可处理"} {
			assert.StrContains(t, out, want)
		}
		assert.StrContains(t, out, "suga data vacuum")
		assert.StrContains(t, out, "…") // the long title is cut to one row
	})

	t.Run("--top bounds the session list", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "usage", "--json", "--top", "1")
		assert.Require(t, assert.NoErr(t, err))
		var rep store.UsageReport
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(out), &rep)))
		assert.Eq(t, 1, len(rep.Sessions.Top))
	})

	t.Run("no positional arguments", func(t *testing.T) {
		_, err := runCLI(t, ctx, "data", "usage", "extra")
		assert.ErrMsgContains(t, err, "usage")
	})

	t.Run("the report leaves the database as it found it", func(t *testing.T) {
		after, err := os.Stat(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, before.Size(), after.Size())
		assert.Eq(t, before.ModTime(), after.ModTime())
	})
}
