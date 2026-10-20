package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// coreMigration is the schema before the ledger FTS migration.
const coreMigration = "20261008-230000-create-core-store.sql"

// ledgerFTSMigration is the migration that created entries_fts.
const ledgerFTSMigration = "20261010-234359-add-ledger-fts-memory-meta.sql"

func migrationNames(t *testing.T) []string {
	t.Helper()
	files, err := migrationFS.ReadDir("migrations")
	assert.Require(t, assert.NoErr(t, err))
	names := []string{}
	for _, f := range files {
		names = append(names, f.Name())
	}
	sort.Strings(names)
	return names
}

func msgJSON(t *testing.T, m api.Message) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(m)
	assert.Require(t, assert.NoErr(t, err))
	return raw
}

func user(t *testing.T, text string) NewEntry {
	return NewEntry{Type: EntryUser, JSON: msgJSON(t, api.Message{Role: "user", Content: text})}
}

func assistant(t *testing.T, text string) NewEntry {
	return NewEntry{Type: EntryAssistant, JSON: msgJSON(t, api.Message{Role: "assistant", Content: text})}
}

// toolMsg mirrors agentcore's tool transcript message: Content repeats the result.
func toolMsg(t *testing.T, content, result string) NewEntry {
	call := &api.ToolCall{CallID: "c1", Name: "shell"}
	return NewEntry{Type: EntryTool, JSON: msgJSON(t, api.Message{Role: "tool", Content: content, ToolCall: call,
		ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: result}})}
}

// varietyEntries covers every branch of the role/text expressions.
func varietyEntries(t *testing.T) []NewEntry {
	return []NewEntry{
		user(t, "我们来讨论记忆管理的设计"),
		user(t, "  padded text\n\t"),
		{Type: EntryAssistant, JSON: msgJSON(t, api.Message{Role: "assistant", ToolCall: &api.ToolCall{CallID: "c1", Name: "shell"}})},
		assistant(t, "这里有缓存失效问题"),
		toolMsg(t, "same output", "same output"),
		toolMsg(t, "", "only the result"),
		toolMsg(t, "lead", "different result"),
		{Type: EntryCompaction, JSON: json.RawMessage(`{"summary":"压缩摘要：讨论了检索方案","first_kept_seq":3}`)},
		{Type: EntryPlan, JSON: json.RawMessage(`{"Items":[{"Content":"plan step text"}]}`)},
		// A handoff record indexes its summary, like a compaction one.
		{Type: EntryHandoff, JSON: json.RawMessage(`{"source":"s_old","summary":"交接摘要：handoff 前的进展"}`)},
		{Type: EntryContextUpdate, JSON: json.RawMessage(`{"Content":"context text"}`)},
		{Type: EntryUser, JSON: json.RawMessage(`not json`)},
		{Type: EntryUser, JSON: json.RawMessage(`{}`)},
	}
}

func createSession(t *testing.T, s *Store, id, cwd string) {
	t.Helper()
	now := time.Now()
	assert.Require(t, assert.NoErr(t, s.CreateSession(context.Background(), Session{ID: id, Cwd: cwd, CreatedAt: now, UpdatedAt: now})))
}

func appendAll(t *testing.T, s *Store, sid string, in ...NewEntry) []Entry {
	t.Helper()
	out, err := s.AppendEntries(context.Background(), sid, in...)
	assert.Require(t, assert.NoErr(t, err))
	return out
}

type ftsRow struct {
	SessionID, Role, Text string
	Seq                   int64
}

func queryFTSRows(t *testing.T, db *sql.DB, q string, args ...any) []ftsRow {
	t.Helper()
	rows, err := db.Query(q, args...)
	assert.Require(t, assert.NoErr(t, err))
	defer rows.Close()
	out := []ftsRow{}
	for rows.Next() {
		var r ftsRow
		assert.Require(t, assert.NoErr(t, rows.Scan(&r.SessionID, &r.Seq, &r.Role, &r.Text)))
		out = append(out, r)
	}
	assert.Require(t, assert.NoErr(t, rows.Err()))
	return out
}

func indexedRows(t *testing.T, s *Store, sid string) []ftsRow {
	return queryFTSRows(t, s.db, `SELECT session_id, seq, role, text FROM entries_fts WHERE session_id = ? ORDER BY seq`, sid)
}

func search(t *testing.T, s *Store, q EntrySearch) []EntryHit {
	t.Helper()
	hits, err := s.SearchEntries(context.Background(), q)
	assert.Require(t, assert.NoErr(t, err))
	return hits
}

func hitKeys(hits []EntryHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = fmt.Sprintf("%s/%d", h.SessionID, h.Seq)
	}
	sort.Strings(out)
	return out
}

func TestFTSIndexOnAppend(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	appendAll(t, s, "s1", varietyEntries(t)...)

	// seq is the position in varietyEntries + 1; skipped rows are absent. The
	// tool results (5-7) are not indexed any more: recall reads them.
	assert.Eq(t, []ftsRow{
		{"s1", EntryUser, "我们来讨论记忆管理的设计", 1},
		{"s1", EntryUser, "padded text", 2},
		{"s1", EntryAssistant, "这里有缓存失效问题", 4},
		{"s1", EntryCompaction, "压缩摘要：讨论了检索方案", 8},
		{"s1", EntryHandoff, "交接摘要：handoff 前的进展", 10},
	}, indexedRows(t, s, "s1"))
}

func TestFTSSearch(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "a", "/work/proj")
	createSession(t, s, "b", "/work/other")
	appendAll(t, s, "a", varietyEntries(t)...)
	appendAll(t, s, "a",
		assistant(t, "alpha beta"),             // 14
		assistant(t, "alpha gamma"),            // 15
		user(t, "call NEAR(a, b) and x*y"),     // 16
		user(t, "rate 100% done; snake_case"),  // 17
		user(t, "rate 1000 done; snakeXcase"),  // 18
		user(t, `quote "x" OR -minus col:val`), // 19
	)
	appendAll(t, s, "b", user(t, "另一个工作区也谈记忆管理"), user(t, "缓存命中"))

	t.Run("chinese term of 3+ runes uses MATCH", func(t *testing.T) {
		hits := search(t, s, EntrySearch{Query: "记忆管理"})
		assert.Eq(t, []string{"a/1", "b/1"}, hitKeys(hits))
		for _, h := range hits {
			assert.StrContains(t, h.Snippet, "记忆管理")
			assert.Eq(t, EntryUser, h.Role)
			assert.False(t, h.CreatedAt.IsZero())
		}
		// Compaction summaries are searchable too.
		hits = search(t, s, EntrySearch{Query: "检索方案"})
		assert.Eq(t, []string{"a/8"}, hitKeys(hits))
		assert.Eq(t, EntryCompaction, hits[0].Role)
	})

	t.Run("2-rune term falls back to LIKE", func(t *testing.T) {
		hits := search(t, s, EntrySearch{Query: "缓存"})
		assert.Eq(t, []string{"a/4", "b/2"}, hitKeys(hits))
		for _, h := range hits {
			assert.StrContains(t, h.Snippet, "缓存")
		}
	})

	t.Run("FTS syntax characters are literal", func(t *testing.T) {
		for _, q := range []string{`"`, `*`, `OR`, `NEAR(`, `-`, `a"b"c`, `foo OR bar`, `NEAR(x y)`, `col:val`,
			`"unterminated`, `(`, `)`, `^abc`, `%`, `_`, `\`, `AND NOT`, `{a b}`} {
			_, err := s.SearchEntries(context.Background(), EntrySearch{Query: q})
			assert.NoErr(t, err, q)
		}
		assert.Eq(t, []string{"a/16"}, hitKeys(search(t, s, EntrySearch{Query: "NEAR(a,"})))
		assert.Eq(t, []string{"a/16"}, hitKeys(search(t, s, EntrySearch{Query: "x*y"})))
		assert.Eq(t, []string{"a/19"}, hitKeys(search(t, s, EntrySearch{Query: `"x" -minus col:val`})))
		// LIKE wildcards are escaped: "0%" and "e_" match literally.
		assert.Eq(t, []string{"a/17"}, hitKeys(search(t, s, EntrySearch{Query: "0%"})))
		assert.Eq(t, []string{"a/17"}, hitKeys(search(t, s, EntrySearch{Query: "e_"})))
	})

	t.Run("multiple terms are AND", func(t *testing.T) {
		assert.Eq(t, []string{"a/14"}, hitKeys(search(t, s, EntrySearch{Query: "alpha beta"})))
		assert.Eq(t, []string{"a/14", "a/15"}, hitKeys(search(t, s, EntrySearch{Query: "alpha"})))
		// One short term sends the whole query to LIKE, still AND.
		assert.Eq(t, []string{"a/15"}, hitKeys(search(t, s, EntrySearch{Query: "alpha ga"})))
		assert.Eq(t, []string{"a/4"}, hitKeys(search(t, s, EntrySearch{Query: "缓存 失效"})))
	})

	t.Run("session and cwd filters", func(t *testing.T) {
		assert.Eq(t, []string{"b/1"}, hitKeys(search(t, s, EntrySearch{Query: "记忆管理", SessionIDs: []string{"b"}})))
		assert.Eq(t, []string{"a/1", "b/1"}, hitKeys(search(t, s, EntrySearch{Query: "记忆管理", SessionIDs: []string{"a", "b"}})))
		assert.Eq(t, []string{"a/1"}, hitKeys(search(t, s, EntrySearch{Query: "记忆管理", CWDPrefix: "/work/pro"})))
		assert.Eq(t, []string{"b/2"}, hitKeys(search(t, s, EntrySearch{Query: "缓存", CWDPrefix: "/work/other"})))
		assert.Empty(t, search(t, s, EntrySearch{Query: "记忆管理", CWDPrefix: "/WORK"}))
		assert.Empty(t, search(t, s, EntrySearch{Query: "记忆管理", SessionIDs: []string{"nope"}}))
	})

	t.Run("LIKE snippet keeps 60 runes around the hit", func(t *testing.T) {
		createSession(t, s, "long", "/x")
		long := strings.Repeat("前", 100) + "缓存" + strings.Repeat("后", 100)
		appendAll(t, s, "long", user(t, long))
		hits := search(t, s, EntrySearch{Query: "缓存", SessionIDs: []string{"long"}})
		assert.Require(t, assert.Len(t, hits, 1))
		assert.Eq(t, "…"+strings.Repeat("前", 60)+"缓存"+strings.Repeat("后", 60)+"…", hits[0].Snippet)
	})

	t.Run("limit defaults to 20 and is capped at 100", func(t *testing.T) {
		createSession(t, s, "many", "/m")
		in := make([]NewEntry, 105)
		for i := range in {
			in[i] = user(t, fmt.Sprintf("needle number %d", i))
		}
		appendAll(t, s, "many", in...)
		assert.Len(t, search(t, s, EntrySearch{Query: "needle"}), 20)
		assert.Len(t, search(t, s, EntrySearch{Query: "needle", Limit: 7}), 7)
		assert.Len(t, search(t, s, EntrySearch{Query: "needle", Limit: 500}), 100)
	})

	t.Run("empty query is an error", func(t *testing.T) {
		_, err := s.SearchEntries(context.Background(), EntrySearch{Query: "  \t"})
		assert.Err(t, err)
	})
}

// seedTwoSessions interleaves entries of a and b so their implicit rowids mix;
// returns the seq of the keyword entry in b.
func seedTwoSessions(t *testing.T, s *Store) int64 {
	createSession(t, s, "a", "/w")
	createSession(t, s, "b", "/w")
	for i := 0; i < 5; i++ {
		appendAll(t, s, "a", user(t, fmt.Sprintf("filler a %d", i)))
		appendAll(t, s, "b", user(t, fmt.Sprintf("filler b %d", i)))
	}
	appendAll(t, s, "a", assistant(t, "共享关键词 in a"))
	got := appendAll(t, s, "b", assistant(t, "共享关键词 in b"))
	return got[0].Seq
}

// assertOnlyB checks that the keyword now hits only b, at the right entry.
func assertOnlyB(t *testing.T, s *Store, seqB int64) {
	hits := search(t, s, EntrySearch{Query: "共享关键词"})
	assert.Require(t, assert.Len(t, hits, 1))
	assert.Eq(t, "b", hits[0].SessionID)
	assert.Eq(t, seqB, hits[0].Seq)
	entries, err := s.ListEntries(context.Background(), "b", EntryFilter{SinceSeq: seqB - 1, Limit: 1})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, string(entries[0].JSON), "共享关键词 in b")
	assert.Empty(t, indexedRows(t, s, "a"))
	assert.Len(t, indexedRows(t, s, "b"), 6)
}

func TestFTSDeleteSessionAndGC(t *testing.T) {
	ctx := context.Background()
	t.Run("DeleteSession", func(t *testing.T) {
		s := openTemp(t)
		seqB := seedTwoSessions(t, s)
		assert.Len(t, search(t, s, EntrySearch{Query: "共享关键词"}), 2)
		assert.NoErr(t, s.DeleteSession(ctx, "a"))
		assertOnlyB(t, s, seqB)
	})

	t.Run("CollectGarbage with VACUUM", func(t *testing.T) {
		s := openTemp(t)
		seqB := seedTwoSessions(t, s)
		old := time.Now().Add(-48 * time.Hour)
		_, err := s.db.Exec(`UPDATE sessions SET updated_at = ? WHERE id = 'a'`, formatTime(old))
		assert.Require(t, assert.NoErr(t, err))
		report, err := s.CollectGarbage(ctx, time.Now().Add(-24*time.Hour), nil, true)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, report.Applied)
		assert.Len(t, report.Sessions, 1)
		assertOnlyB(t, s, seqB)
	})
}

// openAtCoreSchema opens a database migrated only up to the core store, i.e.
// a ledger written before the FTS migration existed.
func openAtCoreSchema(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", buildDSN(path))
	assert.Require(t, assert.NoErr(t, err))
	s := &Store{db: db, path: path}
	assert.Require(t, assert.NoErr(t, s.migrateUpTo(context.Background(), coreMigration)))
	var n int
	assert.NoErr(t, db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE name = 'entries_fts'`).Scan(&n))
	assert.Require(t, assert.Eq(t, 0, n))
	return s, path
}

// writeHistory inserts entries with plain SQL, as the pre-FTS store did.
func writeHistory(t *testing.T, s *Store, sid string, in []NewEntry) {
	t.Helper()
	now := formatTime(time.Now())
	_, err := s.db.Exec(`INSERT INTO sessions(id, cwd, created_at, updated_at) VALUES (?, '/w', ?, ?)`, sid, now, now)
	assert.Require(t, assert.NoErr(t, err))
	for i, e := range in {
		_, err := s.db.Exec(`INSERT INTO entries(session_id, seq, turn_id, type, json, created_at) VALUES (?, ?, '', ?, ?, ?)`,
			sid, i+1, e.Type, string(e.JSON), now)
		assert.Require(t, assert.NoErr(t, err))
	}
}

func TestFTSMigrationBackfillsHistoryOnce(t *testing.T) {
	ctx := context.Background()
	s, path := openAtCoreSchema(t)
	writeHistory(t, s, "old", varietyEntries(t))
	assert.Require(t, assert.NoErr(t, s.migrate(ctx)))

	hits := search(t, s, EntrySearch{Query: "记忆管理"})
	assert.Eq(t, []string{"old/1"}, hitKeys(hits))
	assert.Eq(t, []string{"old/4"}, hitKeys(search(t, s, EntrySearch{Query: "缓存"})))
	assert.Len(t, indexedRows(t, s, "old"), 8)

	// New writes after the migration are indexed by AppendEntries.
	got := appendAll(t, s, "old", user(t, "迁移之后的记忆管理"))
	assert.Eq(t, []string{"old/1", fmt.Sprintf("old/%d", got[0].Seq)}, hitKeys(search(t, s, EntrySearch{Query: "记忆管理"})))
	assert.NoErr(t, s.Close())

	// Re-open: the migration is recorded once and does not backfill again.
	s2, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer s2.Close()
	var n int
	assert.NoErr(t, s2.db.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&n))
	assert.Eq(t, len(migrationNames(t)), n)
	for _, name := range migrationNames(t) {
		assert.NoErr(t, s2.db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name).Scan(&n))
		assert.Eq(t, 1, n, name)
	}
	assert.Len(t, indexedRows(t, s2, "old"), 9)
}

// ftsBackfillMigrations returns the UP sections of every migration that
// backfills entries_fts, oldest first.
func ftsBackfillMigrations(t *testing.T) []string {
	t.Helper()
	out := []string{}
	for _, name := range migrationNames(t) {
		b, err := migrationFS.ReadFile("migrations/" + name)
		assert.Require(t, assert.NoErr(t, err))
		up, err := parseUp(name, string(b))
		assert.Require(t, assert.NoErr(t, err))
		if strings.Contains(up, "INSERT INTO entries_fts") {
			out = append(out, up)
		}
	}
	return out
}

// TestFTSHandoffMigrationBackfill: a ledger written before handoff summaries
// were indexed gets them backfilled once, without duplicating the rows the
// earlier migration had already indexed.
func TestFTSHandoffMigrationBackfill(t *testing.T) {
	ctx := context.Background()
	handoff := NewEntry{Type: EntryHandoff, JSON: json.RawMessage(`{"source":"s_old","summary":"交接摘要：上次做到 handoff 检索"}`)}

	t.Run("a pre-upgrade ledger gets its handoff rows indexed", func(t *testing.T) {
		s, _ := openAtCoreSchema(t)
		defer s.Close()
		// At the schema before this migration: the conversation types were
		// indexed by the older migration, and this handoff record was written by
		// the older binary (writeHistory bypasses AppendEntries).
		assert.Require(t, assert.NoErr(t, s.migrateUpTo(ctx, ledgerFTSMigration)))
		writeHistory(t, s, "old", append(varietyEntries(t), handoff))
		assert.Empty(t, indexedRows(t, s, "old"))

		assert.NoErr(t, s.migrate(ctx))
		// The backfill takes only handoff rows: an older binary indexed the
		// conversation types itself (AppendEntries), and a per-row existence
		// check against entries_fts was a full scan per row on a large ledger.
		rows := indexedRows(t, s, "old")
		assert.Require(t, assert.Len(t, rows, 2))
		assert.Eq(t, ftsRow{"old", EntryHandoff, "交接摘要：上次做到 handoff 检索", 14}, rows[1])

		hits := search(t, s, EntrySearch{Query: "上次做到"})
		assert.Eq(t, []string{"old/14"}, hitKeys(hits))
		assert.Eq(t, EntryHandoff, hits[0].Role)
		assert.StrContains(t, hits[0].Snippet, "上次做到")
		assert.Eq(t, []string{"old/10", "old/14"}, hitKeys(search(t, s, EntrySearch{Query: "摘要", Kinds: []string{EntryHandoff}})))
	})

	t.Run("an already indexed ledger only gains the handoff rows", func(t *testing.T) {
		s, path := openAtCoreSchema(t)
		writeHistory(t, s, "old", append(varietyEntries(t), handoff))
		// Migrate to the schema of the previous release: the conversation types
		// are indexed, the handoff records still are not.
		assert.Require(t, assert.NoErr(t, s.migrateUpTo(ctx, ledgerFTSMigration)))
		assert.Len(t, indexedRows(t, s, "old"), 7)
		assert.NoErr(t, s.Close())

		s2, err := Open(path)
		assert.Require(t, assert.NoErr(t, err))
		defer s2.Close()
		rows := indexedRows(t, s2, "old")
		assert.Require(t, assert.Len(t, rows, 9)) // the seven stay single, two handoffs join
		assert.Eq(t, EntryHandoff, rows[7].Role)
		assert.Eq(t, ftsRow{"old", EntryHandoff, "交接摘要：上次做到 handoff 检索", 14}, rows[8])
	})
}

func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(regexp.MustCompile(`\s*([(),|])\s*`).ReplaceAllString(s, " $1 ")), " ")
}

// TestFTSExpressionsMatchMigration guards the index contract (design 0.3): the
// types a new entry is indexed with and the rows the maintenance command
// deletes come from the same constants, while no migration backfills
// entries_fts any more. A new startup backfill is the full-table work that hung
// v0.31.0, and the two historical backfills stay as they were applied: they
// indexed tool results in full, and the delete pass of the removed
// `suga data shrink-fts` is what cleaned those rows up.
func TestFTSExpressionsMatchMigration(t *testing.T) {
	t.Run("only the two historical backfills touch entries_fts", func(t *testing.T) {
		backfills := ftsBackfillMigrations(t)
		// 20261010 (conversation types) and 20261017 (handoff summaries). A new
		// one must fail here and be justified as "not a startup full-table job".
		assert.Eq(t, 2, len(backfills))
		for _, mig := range backfills {
			assert.False(t, strings.Contains(normalizeSQL(mig), normalizeSQL(`role = 'tool'`)),
				"deleting the tool rows is the maintenance command's job, not a migration's")
		}
	})

	t.Run("write path skips tool results", func(t *testing.T) {
		// Tool results are read with recall: they never enter the index.
		assert.Eq(t, `'user', 'assistant', 'compaction', 'handoff'`, ftsIndexedTypes)
		assert.NotContains(t, ftsSourceSelect(""), "'tool'")
	})

	t.Run("backfill and AppendEntries index the same rows", func(t *testing.T) {
		ctx := context.Background()
		s, _ := openAtCoreSchema(t)
		defer s.Close()
		in := varietyEntries(t)
		writeHistory(t, s, "hist", in)
		assert.Require(t, assert.NoErr(t, s.migrate(ctx)))
		backfilled := indexedRows(t, s, "hist")

		// The constants select exactly what the migration wrote, minus the tool
		// rows: the historical backfill indexed them, the live write path does not.
		viaConst := queryFTSRows(t, s.db, ftsSourceSelect(` AND session_id = ?`)+` ORDER BY seq`, "hist")
		withoutTool := make([]ftsRow, 0, len(backfilled))
		for _, row := range backfilled {
			if row.Role != EntryTool {
				withoutTool = append(withoutTool, row)
			}
		}
		assert.True(t, len(withoutTool) < len(backfilled), "the migration indexed tool results")
		assert.Eq(t, withoutTool, viaConst)

		// And AppendEntries writes the same role/text/seq for the same bodies.
		createSession(t, s, "live", "/w")
		appendAll(t, s, "live", in...)
		live := indexedRows(t, s, "live")
		assert.Require(t, assert.Len(t, live, len(viaConst)))
		for i := range live {
			live[i].SessionID = "hist"
		}
		assert.Eq(t, viaConst, live)
	})
}

func t1NamedTool(t *testing.T, name, content string) NewEntry {
	t.Helper()
	call := &api.ToolCall{CallID: "t1-call", Name: name}
	msg := api.Message{
		Role:       "tool",
		Content:    content,
		ToolCall:   call,
		ToolResult: &api.ToolResult{CallID: "t1-call", Name: name, Content: content},
	}
	raw, err := json.Marshal(msg)
	assert.Require(t, assert.NoErr(t, err))
	return NewEntry{Type: EntryTool, JSON: raw}
}

func TestFTSShortAndLongTermsStayAND(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	appendAll(t, s, "s1",
		user(t, "token only"),
		user(t, "中文 token"),
		user(t, "中"),
		user(t, "数据库索引"),
	)

	t.Run("mixed query keeps the short term filter", func(t *testing.T) {
		hits := search(t, s, EntrySearch{Query: "中文 token"})
		assert.Eq(t, []string{"s1/2"}, hitKeys(hits))
	})
	t.Run("one rune term uses LIKE semantics", func(t *testing.T) {
		hits := search(t, s, EntrySearch{Query: "中"})
		assert.Eq(t, []string{"s1/2", "s1/3"}, hitKeys(hits))
	})
	t.Run("three rune term remains searchable", func(t *testing.T) {
		hits := search(t, s, EntrySearch{Query: "数据库"})
		assert.Eq(t, []string{"s1/4"}, hitKeys(hits))
	})
}

func TestFTSMixedQueryUsesMatchOrdering(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	appendAll(t, s, "s1", user(t, "中 needle needle needle"), user(t, "中 needle"))

	// Match ranking puts the more selective long-term hit first; all-LIKE
	// fallback would use created_at DESC and return the newer second row first.
	hits := search(t, s, EntrySearch{Query: "中 needle"})
	assert.Require(t, assert.Len(t, hits, 2))
	assert.Eq(t, int64(1), hits[0].Seq)
	assert.Eq(t, int64(2), hits[1].Seq)
}

func TestFTSKindAndTimeFilters(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	entries := appendAll(t, s, "s1",
		user(t, "kindneedle"),
		assistant(t, "kindneedle"),
		NewEntry{Type: EntryCompaction, JSON: json.RawMessage(`{"summary":"kindneedle"}`)},
		NewEntry{Type: EntryHandoff, JSON: json.RawMessage(`{"source":"s_old","summary":"kindneedle"}`)},
		NewEntry{Type: EntryPlan, JSON: json.RawMessage(`{"Items":[{"Content":"kindneedle"}]}`)},
	)
	assert.Len(t, entries, 5)

	t.Run("each indexed kind and multiple kinds", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			kinds []string
			want  []string
		}{
			{"user", []string{EntryUser}, []string{"s1/1"}},
			{"assistant", []string{EntryAssistant}, []string{"s1/2"}},
			{"compaction", []string{EntryCompaction}, []string{"s1/3"}},
			{"handoff", []string{EntryHandoff}, []string{"s1/4"}},
			{"multiple", []string{EntryUser, EntryAssistant}, []string{"s1/1", "s1/2"}},
			{"the two summary kinds", []string{EntryCompaction, EntryHandoff}, []string{"s1/3", "s1/4"}},
			{"tool has no row in a new index", []string{EntryTool}, []string{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				assert.Eq(t, tc.want, hitKeys(search(t, s, EntrySearch{Query: "kindneedle", Kinds: tc.kinds})))
			})
		}
	})
	t.Run("no kinds returns every indexed role", func(t *testing.T) {
		assert.Eq(t, []string{"s1/1", "s1/2", "s1/3", "s1/4"}, hitKeys(search(t, s, EntrySearch{Query: "kindneedle"})))
	})
	t.Run("invalid kind is rejected", func(t *testing.T) {
		_, err := s.SearchEntries(context.Background(), EntrySearch{Query: "kindneedle", Kinds: []string{"unknown"}})
		assert.Err(t, err)
		_, err = s.SearchEntries(context.Background(), EntrySearch{Query: "kindneedle", Kinds: []string{""}})
		assert.Err(t, err)
	})

	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for i := range entries {
		at := base.AddDate(0, 0, i)
		_, err := s.DB().ExecContext(context.Background(), `UPDATE entries SET created_at = ? WHERE session_id = ? AND seq = ?`, formatTime(at), "s1", entries[i].Seq)
		assert.Require(t, assert.NoErr(t, err))
	}
	t.Run("since is inclusive and until is exclusive", func(t *testing.T) {
		query := EntrySearch{Query: "kindneedle", Since: base.AddDate(0, 0, 1), Until: base.AddDate(0, 0, 3)}
		assert.Eq(t, []string{"s1/2", "s1/3"}, hitKeys(search(t, s, query)))
		assert.Eq(t, []string{"s1/4"}, hitKeys(search(t, s, EntrySearch{Query: "kindneedle", Since: base.AddDate(0, 0, 3)})))
	})
}

// TestFTSSearchExcludesRetrievalToolTranscripts: search results must not
// recursively contain the retrieval tools' own transcripts, while other tool
// results stay searchable. Only a ledger written before tool results left the
// index holds tool rows at all, so the fixture is a legacy one.
func TestFTSSearchExcludesRetrievalToolTranscripts(t *testing.T) {
	s := openLegacyLedger(t, legacySession{"s1", []NewEntry{
		t1NamedTool(t, "shell", "searchpollution"),
		t1NamedTool(t, "session_search", "searchpollution"),
		t1NamedTool(t, "recall", "searchpollution"),
	}})

	hits := search(t, s, EntrySearch{Query: "searchpollution"})
	assert.Eq(t, []string{"s1/1"}, hitKeys(hits))
	assert.Eq(t, EntryTool, hits[0].Role)
}

// TestFTSHandoffMigrationStaysFastOnALargeLedger: the handoff backfill must not
// scan entries_fts per entry (v0.31.0 did, and never finished on a ~1 GB
// ledger, blocking every start).
func TestFTSHandoffMigrationStaysFastOnALargeLedger(t *testing.T) {
	ctx := context.Background()
	s, _ := openAtCoreSchema(t)
	defer s.Close()
	// A large ledger of the previous release: the conversation rows indexed by
	// the earlier migration, the handoff row not.
	var batch []NewEntry
	for i := 0; i < 20000; i++ {
		batch = append(batch, NewEntry{Type: EntryUser, JSON: json.RawMessage(fmt.Sprintf(`{"Role":"user","Content":"message %d about the parser"}`, i))})
	}
	batch = append(batch, NewEntry{Type: EntryHandoff, JSON: json.RawMessage(`{"source":"x","summary":"交接：大库回填"}`)})
	writeHistory(t, s, "big", batch)
	assert.Require(t, assert.NoErr(t, s.migrateUpTo(ctx, ledgerFTSMigration)))
	start := time.Now()
	assert.Require(t, assert.NoErr(t, s.migrate(ctx)))
	assert.True(t, time.Since(start) < 5*time.Second, "backfill took "+time.Since(start).String())
	assert.Eq(t, []string{"big/20001"}, hitKeys(search(t, s, EntrySearch{Query: "大库回填"})))
}

// TestFTSHasSearchableEntries covers the scope check a search that matched nothing
// asks: it tells "this scope holds no history at all" (where another wording cannot
// help) from "the words did not match" (suag-hpu7).
func TestFTSHasSearchableEntries(t *testing.T) {
	ctx := context.Background()
	has := func(t *testing.T, s *Store, q EntrySearch) bool {
		t.Helper()
		ok, err := s.HasSearchableEntries(ctx, q)
		assert.Require(t, assert.NoErr(t, err))
		return ok
	}
	empty := openTemp(t)
	assert.False(t, has(t, empty, EntrySearch{}), "a store with no session has no history")

	s := openTemp(t)
	// Platform-built paths: workspaceScopeCondition cleans what the caller passes,
	// so a literal "/work/proj" would not match a stored cwd on Windows.
	proj := filepath.Join(string(filepath.Separator), "work", "proj")
	other := filepath.Join(string(filepath.Separator), "work", "other")
	createSession(t, s, "a", proj)
	createSession(t, s, "b", other)
	createSession(t, s, "c", proj) // no entry of its own
	appendAll(t, s, "a", user(t, "the needle phrase"))
	appendAll(t, s, "c", NewEntry{Type: EntryPlan, JSON: json.RawMessage(`{"Items":[]}`)})

	t.Run("a session with an entry has history", func(t *testing.T) {
		assert.True(t, has(t, s, EntrySearch{}))
		assert.True(t, has(t, s, EntrySearch{SessionIDs: []string{"a"}}))
	})
	t.Run("structured state is not searchable history", func(t *testing.T) {
		assert.False(t, has(t, s, EntrySearch{CurrentSessionID: "c"}))
	})
	t.Run("the workspace scope counts a sibling session", func(t *testing.T) {
		assert.True(t, has(t, s, EntrySearch{CurrentSessionID: "c", WorkspaceDir: proj}))
		assert.False(t, has(t, s, EntrySearch{CurrentSessionID: "c", WorkspaceDir: filepath.Join(string(filepath.Separator), "work", "elsewhere")}))
	})
	t.Run("agent sessions are left out unless asked for", func(t *testing.T) {
		createSession(t, s, "sub", other)
		assert.Require(t, assert.NoErr(t, s.PatchSessionMeta(ctx, "sub", map[string]any{MetaKind: KindAgent, MetaParentSession: "b"})))
		appendAll(t, s, "sub", user(t, "only the sub-agent wrote this"))
		assert.False(t, has(t, s, EntrySearch{SessionIDs: []string{"sub"}}))
		assert.True(t, has(t, s, EntrySearch{SessionIDs: []string{"sub"}, IncludeAgents: true}))
	})
	t.Run("kinds narrow the check", func(t *testing.T) {
		assert.True(t, has(t, s, EntrySearch{Kinds: []string{EntryUser}}))
		assert.False(t, has(t, s, EntrySearch{Kinds: []string{EntryHandoff}}))
	})
}
