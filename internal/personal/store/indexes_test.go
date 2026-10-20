package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// queryIndexMigration adds idx_entries_type and idx_events_turn; the handoff
// FTS migration is the schema right before it.
const (
	queryIndexMigration = "20261017-120000-add-query-indexes.sql"
	handoffFTSMigration = "20261017-101500-index-handoff-fts.sql"
)

// explainPlan returns the EXPLAIN QUERY PLAN details of q as one string.
func explainPlan(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+q, args...)
	assert.Require(t, assert.NoErr(t, err))
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		assert.Require(t, assert.NoErr(t, rows.Scan(&id, &parent, &notUsed, &detail)))
		out = append(out, detail)
	}
	assert.Require(t, assert.NoErr(t, rows.Err()))
	return strings.Join(out, " | ")
}

// TestQueryIndexesServeTheirQueries: the two indexes of queryIndexMigration
// must exist and appear in the plans of the queries they were added for. Each
// case drops the index again and re-plans, so the assertion cannot pass on a
// query that never looks at the index.
func TestQueryIndexesServeTheirQueries(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "a", "/w")
	appendAll(t, s, "a", user(t, "hello"))

	t.Run("entries(type) serves the searchable-ledger check", func(t *testing.T) {
		// HasSearchableEntries' scan over ftsSourceSelect("").
		q := `SELECT 1 FROM (` + ftsSourceSelect("") + `) AS f WHERE 1 LIMIT 1`
		plan := explainPlan(t, s, q)
		assert.StrContains(t, plan, "idx_entries_type")
		assert.Require(t, assert.NoErr(t, execNone(s, `DROP INDEX idx_entries_type`)))
		assert.NotContains(t, explainPlan(t, s, q), "idx_entries_type")
	})

	t.Run("events(turn_id) serves the turn filter of trace", func(t *testing.T) {
		// ListEvents with TurnID set.
		q := `SELECT id FROM events WHERE session_id = ? AND turn_id = ? ORDER BY started_at ASC, id ASC`
		plan := explainPlan(t, s, q, "a", "t1")
		assert.StrContains(t, plan, "idx_events_turn")
		assert.Require(t, assert.NoErr(t, execNone(s, `DROP INDEX idx_events_turn`)))
		assert.NotContains(t, explainPlan(t, s, q, "a", "t1"), "idx_events_turn")
	})

	t.Run("no index on events(kind)", func(t *testing.T) {
		// Not added on purpose: the measured plans made SumUsage and the global
		// report slower (see the migration comment).
		var n int
		assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_events_kind%'`).Scan(&n))
		assert.Eq(t, 0, n)
	})

	// Re-opening applies nothing new: the indexes and the ledger row are in
	// place, and the dropped indexes are not recreated (the migration is
	// recorded once), so the store must still open.
	assert.NoErr(t, s.Close())
	s2, err := Open(s.Path())
	assert.Require(t, assert.NoErr(t, err))
	defer s2.Close()
}

func execNone(s *Store, q string) error {
	_, err := s.db.Exec(q)
	return err
}

// TestQueryIndexMigrationStaysFastOnALargeLedger: the indexes are built on an
// existing ledger at Open, once. The build must be a single scan per index and
// finish in seconds on a large store (a ~1 GB ledger is the real-world size;
// v0.31.0 shipped a migration that never finished on it).
func TestQueryIndexMigrationStaysFastOnALargeLedger(t *testing.T) {
	ctx := context.Background()
	s, _ := openAtCoreSchema(t)
	defer s.Close()
	// A ledger of the previous release: no query indexes yet.
	assert.Require(t, assert.NoErr(t, s.migrateUpTo(ctx, handoffFTSMigration)))
	assert.Require(t, assert.NoErr(t, execNone(s, `INSERT INTO sessions(id, cwd, created_at, updated_at) VALUES ('big', '/w', 't', 't')`)))
	assert.Require(t, assert.NoErr(t, execNone(s, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 60000)
		INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
		SELECT 'big', x, '', CASE WHEN x % 4 = 0 THEN 'plan' ELSE 'user' END, '{"Role":"user","Content":"hi"}', 't' FROM c`)))
	assert.Require(t, assert.NoErr(t, execNone(s, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 200000)
		INSERT INTO events(session_id, turn_id, kind, name, status, started_at)
		SELECT 'big', 't'||(x % 5000), CASE WHEN x % 2 = 0 THEN 'model' ELSE 'tool' END, 'p', '', 't' FROM c`)))

	start := time.Now()
	assert.Require(t, assert.NoErr(t, s.migrate(ctx)))
	elapsed := time.Since(start)
	t.Logf("index migration over 60k entries / 200k events: %v", elapsed)
	// The race detector slows SQLite several times over (CI ran 6.2s with -race,
	// coverage and a loaded runner). The budget there stays well under what the
	// v0.31.0 migration needed, which never finished on a large ledger.
	budget := 5 * time.Second
	if raceEnabled {
		budget = 20 * time.Second
	}
	assert.True(t, elapsed < budget, "index migration took "+elapsed.String())

	for _, name := range []string{"idx_entries_type", "idx_events_turn"} {
		var n int
		assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n))
		assert.Eq(t, 1, n, name)
	}
	var applied int
	assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, queryIndexMigration).Scan(&applied))
	assert.Eq(t, 1, applied)
}
