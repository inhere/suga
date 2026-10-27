package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

func seedObserve(t *testing.T) context.Context {
	t.Helper()
	t.Setenv("SUGA_HOME", shortCLIHome(t))
	t.Setenv("SUGA_HOOKS", "off")
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.NoErr(t, err)
	repo, err := store.Open(dbPath)
	assert.NoErr(t, err)
	defer repo.Close()
	ctx := context.Background()
	now := time.Now()
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "obs-1", Status: store.SessionOpen}))
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "obs-2", Status: store.SessionOpen}))
	model := func(sid string, ago time.Duration, dur, in, cr, cw int64, status string) store.Event {
		return store.Event{SessionID: sid, Kind: store.EventModel, Name: "anthropic", Status: status,
			StartedAt: now.Add(-ago), DurationMs: dur, TTFTMs: dur / 10, InputTokens: in, OutputTokens: 10,
			CacheReadTokens: cr, CacheWriteTokens: cw, Attrs: map[string]any{"model": "m1", "cost_usd": 0.5}}
	}
	for _, e := range []store.Event{
		model("obs-1", time.Hour, 100, 1000, 600, 100, "ok"),
		model("obs-1", 2*time.Hour, 200, 500, 0, 0, "error"),
		model("obs-2", 10*24*time.Hour, 300, 300, 0, 0, "ok"), // old, other session
		{SessionID: "obs-1", Kind: store.EventTool, Name: "bash", Status: "ok", StartedAt: now.Add(-time.Hour), DurationMs: 10},
		{SessionID: "obs-1", Kind: store.EventTool, Name: "bash", Status: "error", StartedAt: now.Add(-time.Hour), DurationMs: 30},
		{SessionID: "obs-1", Kind: store.EventDenial, Name: "bash", Status: "denied", StartedAt: now.Add(-time.Hour)},
		{SessionID: "obs-1", Kind: store.EventCompaction, Name: "c", StartedAt: now.Add(-time.Hour)},
		{SessionID: "obs-1", Kind: store.EventToolSearch, Name: "ts", StartedAt: now.Add(-time.Hour), Attrs: map[string]any{"arguments": "x", "q": "y"}},
	} {
		_, err := repo.AppendEvent(ctx, e)
		assert.NoErr(t, err)
	}
	args := `{"Role":"assistant","ToolCall":{"CallID":"c1","Name":"bash","Arguments":{"cmd":"SECRET-ARG"}}}`
	_, err = repo.AppendEntries(ctx, "obs-1",
		store.NewEntry{Type: store.EntryUser, JSON: json.RawMessage(`{"Role":"user","Content":"hi"}`)},
		store.NewEntry{Type: store.EntryAssistant, JSON: json.RawMessage(args)})
	assert.NoErr(t, err)
	return ctx
}

func runCLI(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs(args)
	return stdout.String(), err
}

func TestStatsCommand(t *testing.T) {
	ctx := seedObserve(t)
	t.Run("json all", func(t *testing.T) {
		out, err := runCLI(t, ctx, "inspect", "stats", "--json")
		assert.NoErr(t, err)
		var rep store.Report
		assert.NoErr(t, json.Unmarshal([]byte(out), &rep))
		assert.Require(t, assert.Eq(t, 1, len(rep.Models)))
		m := rep.Models[0]
		assert.Eq(t, 3, m.Calls)
		assert.Eq(t, 1, m.Errors)
		assert.Eq(t, int64(200), m.P50Ms)
		assert.Eq(t, int64(300), m.P95Ms)
		// input: (1000-700) + 500 + 300 = 1100; hit = 600 / (1100+600+100)
		assert.Eq(t, int64(1100), m.InputTokens)
		assert.Eq(t, int64(600), m.CacheRead)
		assert.Eq(t, int64(100), m.CacheWrite)
		assert.Eq(t, 600.0/1800.0, m.CacheHitRate)
		assert.Eq(t, 1.5, m.CostUSD)
		assert.Require(t, assert.Eq(t, 1, len(rep.Tools)))
		tl := rep.Tools[0]
		assert.Eq(t, 2, tl.Calls)
		assert.Eq(t, 1, tl.Denials)
		assert.Eq(t, 0.5, tl.ErrorRate)
		assert.Eq(t, int64(10), tl.P50Ms)
		assert.Eq(t, int64(30), tl.P95Ms)
		assert.Eq(t, 1, rep.Compactions)
		assert.Eq(t, 1, rep.ToolSearch)
		assert.Eq(t, 1, rep.FailedAttempts)
		assert.Require(t, assert.Eq(t, 1, len(rep.CacheBuckets)))
		assert.Eq(t, 2, rep.CacheBuckets[0].Calls)
	})
	t.Run("trace analysis agrees with the bucket table", func(t *testing.T) {
		// The stats command and the trace analysis classify the same events: an
		// attempt that failed or reported no usage is in neither bucket table, only
		// in the failed_attempts count.
		out, err := runCLI(t, ctx, "inspect", "trace", "obs-1", "--analyze", "--json")
		assert.NoErr(t, err)
		var a personal.TraceAnalysis
		assert.NoErr(t, json.Unmarshal([]byte(out), &a))
		statsJSON, err := runCLI(t, ctx, "inspect", "stats", "--session", "obs-1", "--json")
		assert.NoErr(t, err)
		var rep store.Report
		assert.NoErr(t, json.Unmarshal([]byte(statsJSON), &rep))
		assert.Eq(t, rep.FailedAttempts, a.FailedAttempts)
		assert.Eq(t, 1, a.FailedAttempts)
		assert.Require(t, assert.Eq(t, len(rep.CacheBuckets), len(a.CacheBuckets)))
		assert.Eq(t, 1, a.CacheBuckets[0].Calls)
		statsText, err := runCLI(t, ctx, "inspect", "stats", "--session", "obs-1")
		assert.NoErr(t, err)
		assert.StrContains(t, statsText, "failed_attempts")
		analyzeText, err := runCLI(t, ctx, "inspect", "trace", "obs-1", "--analyze")
		assert.NoErr(t, err)
		assert.StrContains(t, analyzeText, "failed attempts")
	})
	t.Run("since filters old events", func(t *testing.T) {
		out, err := runCLI(t, ctx, "inspect", "stats", "--since", "7d", "--json")
		assert.NoErr(t, err)
		var rep store.Report
		assert.NoErr(t, json.Unmarshal([]byte(out), &rep))
		assert.Eq(t, 2, rep.Models[0].Calls)
	})
	t.Run("session filter and text", func(t *testing.T) {
		out, err := runCLI(t, ctx, "inspect", "stats", "--session", "obs-2")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "anthropic/m1")
		assert.StrContains(t, out, "compactions: 0")
		// That session has no failed attempt: the row stays hidden, so the table of
		// an all-success session reads as before.
		assert.StrNotContains(t, out, "failed_attempts")
		assert.StrContains(t, out, "cold")
	})
	t.Run("bad args", func(t *testing.T) {
		_, err := runCLI(t, ctx, "inspect", "stats", "--since", "abc")
		assert.Err(t, err)
		_, err = runCLI(t, ctx, "inspect", "stats", "--session", "nope")
		assert.Err(t, err)
	})
}

func TestExportCommand(t *testing.T) {
	ctx := seedObserve(t)
	check := func(t *testing.T, out string) []map[string]any {
		var rows []map[string]any
		var last time.Time
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			var row map[string]any
			assert.NoErr(t, json.Unmarshal([]byte(l), &row))
			rows = append(rows, row)
			ts, _ := row["created_at"].(string)
			if ts == "" {
				ts, _ = row["started_at"].(string)
			}
			cur, err := time.Parse(time.RFC3339Nano, ts)
			assert.NoErr(t, err)
			assert.Eq(t, false, cur.Before(last))
			last = cur
		}
		return rows
	}
	t.Run("plain", func(t *testing.T) {
		out, err := runCLI(t, ctx, "inspect", "export", "obs-1")
		assert.NoErr(t, err)
		rows := check(t, out)
		assert.Eq(t, 9, len(rows)) // 2 entries + 7 events of obs-1
		assert.StrContains(t, out, "SECRET-ARG")
	})
	t.Run("redact to file", func(t *testing.T) {
		f := t.TempDir() + "/out.jsonl"
		_, err := runCLI(t, ctx, "inspect", "export", "obs-1", "--redact", "-o", f)
		assert.NoErr(t, err)
		b, err := os.ReadFile(f)
		assert.NoErr(t, err)
		check(t, string(b))
		assert.Eq(t, false, strings.Contains(string(b), "SECRET-ARG"))
		assert.Eq(t, false, strings.Contains(string(b), "Arguments"))
		assert.StrContains(t, string(b), `"CallID":"c1"`)
		assert.Eq(t, false, strings.Contains(string(b), `"arguments"`))
		st, err := os.Stat(f)
		assert.NoErr(t, err)
		// Windows has no Unix permission bits (files report 0666), so only check elsewhere.
		if runtime.GOOS != "windows" {
			assert.Eq(t, os.FileMode(0), st.Mode().Perm()&0o077)
		}
	})
	t.Run("unknown session", func(t *testing.T) {
		_, err := runCLI(t, ctx, "inspect", "export", "nope")
		assert.Err(t, err)
	})
	t.Run("with outputs", func(t *testing.T) {
		sessionsDir, err := appconfig.ResolveSessionsDir()
		assert.Require(t, assert.NoErr(t, err))
		outputs := filepath.Join(sessionsDir, "obs-1", "outputs")
		assert.NoErr(t, os.MkdirAll(outputs, 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(outputs, "0123456789abcdef.txt"), []byte("full output\nline 2"), 0o600))

		plain, err := runCLI(t, ctx, "inspect", "export", "obs-1")
		assert.NoErr(t, err)
		assert.StrNotContains(t, plain, `"kind":"output"`)

		out, err := runCLI(t, ctx, "inspect", "export", "obs-1", "--with-outputs")
		assert.NoErr(t, err)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		assert.Eq(t, 10, len(lines))
		var row map[string]any
		assert.NoErr(t, json.Unmarshal([]byte(lines[len(lines)-1]), &row))
		assert.Eq(t, "output", row["kind"])
		assert.Eq(t, "out:obs-1:0123456789abcdef", row["ref"])
		assert.Eq(t, "full output\nline 2", row["content"])
	})

	t.Run("with a compressed output", func(t *testing.T) {
		sessionsDir, err := appconfig.ResolveSessionsDir()
		assert.Require(t, assert.NoErr(t, err))
		dir := filepath.Join(sessionsDir, "obs-1", "outputs")
		body, err := os.ReadFile(filepath.Join(dir, "0123456789abcdef.txt"))
		assert.Require(t, assert.NoErr(t, err))
		// The maintenance pass leaves the .txt.gz where the .txt was; the ref
		// does not change and the row keeps the decoded content and size.
		f, err := os.Create(filepath.Join(dir, "0123456789abcdef.txt.gz"))
		assert.Require(t, assert.NoErr(t, err))
		zw := gzip.NewWriter(f)
		_, err = zw.Write(body)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.NoErr(t, zw.Close()))
		assert.Require(t, assert.NoErr(t, f.Close()))
		// An interrupted compression leaves <stem>.txt.gz.tmp behind: it is no
		// output, so it must not show up as a ref recall could not read back.
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(dir, "ffffffffffffffff.txt.gz.tmp"), []byte("half"), 0o600)))
		assert.Require(t, assert.NoErr(t, os.Remove(filepath.Join(dir, "0123456789abcdef.txt"))))

		out, err := runCLI(t, ctx, "inspect", "export", "obs-1", "--with-outputs")
		assert.NoErr(t, err)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		assert.Eq(t, 10, len(lines))
		var row map[string]any
		assert.NoErr(t, json.Unmarshal([]byte(lines[len(lines)-1]), &row))
		assert.Eq(t, "out:obs-1:0123456789abcdef", row["ref"])
		assert.Eq(t, "full output\nline 2", row["content"])
		assert.Eq(t, float64(len(body)), row["bytes"])
	})
}

// TestWriteJSONString covers the streaming escaper of export --with-outputs:
// content longer than one chunk is escaped piecewise, so a chunk boundary
// inside a multi-byte rune must not change the decoded text.
func TestWriteJSONString(t *testing.T) {
	t.Run("multi-byte content across chunk boundaries round-trips", func(t *testing.T) {
		content := strings.Repeat("a", 64<<10-1) + strings.Repeat("中文🙂", 30000) + "\ttab\nnewline \"quote\" \\slash"
		var buf bytes.Buffer
		n, err := writeJSONString(&buf, strings.NewReader(content))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(len(content)), n)
		var got string
		assert.Require(t, assert.NoErr(t, json.Unmarshal(buf.Bytes(), &got)))
		assert.Eq(t, content, got)
	})
}
