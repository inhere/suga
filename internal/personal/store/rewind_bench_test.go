package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// seedBulk inserts n assistant rows (seq 1..n) for a session in one statement.
func seedBulk(tb testing.TB, s *Store, sid string, n int) {
	tb.Helper()
	_, err := s.db.Exec(`INSERT INTO sessions(id, title, cwd, status, last_turn_id, created_at, updated_at, meta_json)
		VALUES (?, '', '/w', 'open', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '{}')`, sid)
	if err != nil {
		tb.Fatal(err)
	}
	_, err = s.db.Exec(`WITH RECURSIVE seq(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i < ?)
		INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
		SELECT ?, i, '', 'assistant', '{"Role":"assistant","Content":"x"}', '2026-01-01T00:00:00Z' FROM seq`, n, sid)
	if err != nil {
		tb.Fatal(err)
	}
}

// BenchmarkRewindRecords compares the two lookups of a session's rewind records
// on a ledger with several big sessions and a handful of rewind rows: the
// primary-key range with a type filter reads every row of the session, the type
// index reads only the rewind rows of the whole ledger.
func BenchmarkRewindRecords(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "db", "sessions.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	for i := 0; i < 5; i++ {
		seedBulk(b, s, fmt.Sprintf("big%d", i), 20000)
	}
	for _, id := range []string{"big0", "big1"} {
		if _, err := s.db.Exec(`INSERT INTO entries(session_id, seq, turn_id, type, json, created_at) VALUES (?, 20001, '', 'rewind', '{"cut_seq":5,"target_turn":"t","scope":"both"}', '2026-01-01T00:00:00Z')`, id); err != nil {
			b.Fatal(err)
		}
	}
	for name, q := range map[string]string{
		"pk-range":   `SELECT seq, json FROM entries WHERE type = ? AND session_id = ? ORDER BY seq ASC`,
		"type-index": rewindRecordsSQL,
	} {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				rows, err := s.db.QueryContext(context.Background(), q, EntryRewind, "big0")
				if err != nil {
					b.Fatal(err)
				}
				n := 0
				for rows.Next() {
					n++
				}
				_ = rows.Close()
				if n != 1 {
					b.Fatalf("rows = %d", n)
				}
			}
		})
	}
}

// BenchmarkRecentEntriesSkipping: a 20k-entry session whose last 10k entries are
// masked must answer the newest visible entries within 2x of the unmasked read
// (plan T1 fail-closed threshold).
func BenchmarkRecentEntriesSkipping(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "db", "sessions.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	seedBulk(b, s, "big", 20000)
	ctx := context.Background()
	types := []string{EntryAssistant}
	tail := NewSeqRanges(SeqRange{Lo: 10000, Hi: 20001})
	run := func(b *testing.B, skip SeqRanges, wantTop int64) {
		for i := 0; i < b.N; i++ {
			got, err := s.RecentEntriesSkipping(ctx, "big", types, skip, 20)
			if err != nil || len(got) != 20 || got[0].Seq != wantTop {
				b.Fatalf("got %d rows err %v", len(got), err)
			}
		}
	}
	b.Run("no-mask", func(b *testing.B) { run(b, nil, 20000) })
	b.Run("tail-10k-masked", func(b *testing.B) { run(b, tail, 10000) })
}
