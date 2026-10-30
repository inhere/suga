// Package store is the single-file SQLite core store for personal sessions:
// sessions, a linear entry log, turns (agentcore run state) and events.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const (
	migrateUpMarker   = "-- Migrate:UP"
	migrateDownMarker = "-- Migrate:DOWN"
)

// timeLayout is the fixed-width UTC format used for every timestamp column, so
// lexical ordering equals chronological ordering.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(timeLayout, s)
	return t
}

// Store wraps the shared *sql.DB of the core store.
type Store struct {
	db   *sql.DB
	path string

	// release gives the handle back when the store closes: a shared read-write
	// handle drops a reference (see sharedDB), nil closes db itself.
	release   func() error
	closeOnce sync.Once
	closeErr  error

	// sessionsDir is the root of the per-session files (sessions/<id>/
	// tools.jsonl). It is <db dir>/../sessions by default, so every caller that
	// only knows the database path (restore staging, inspect trace) resolves the
	// same place.
	sessionsDir string
	// toolBodyInlineBytes is the threshold above which a new tool entry body
	// moves to the session file (store.body_inline_bytes); 0 disables it.
	toolBodyInlineBytes int64

	// searchMu protects the process-local cursor snapshots. They deliberately
	// live beside the store rather than in SQLite, so cursors never become
	// persistent state or part of a search query.
	searchMu        sync.Mutex
	searchSnapshots map[string]*searchSnapshot
	searchSnapshotN uint64

	// shrinkMu guards shrinkPending, the memoized answer to "does this ledger
	// still hold tool rows the shrink command has not rewritten".
	shrinkMu      sync.Mutex
	shrinkPending *bool
}

// fileURI is the file: URI of a store path, well-formed on Windows too. It is
// shared by the read-write and read-only DSN builders.
func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // "C:/x" -> "/C:/x" keeps the file: URI well-formed on Windows
	}
	u := url.URL{Path: p}
	return "file://" + u.EscapedPath()
}

// buildDSN makes every pooled connection run the pragmas on open (modernc
// executes each _pragma value as "PRAGMA ..." for every new connection).
//
// Several handles write the same file (a chat runtime, the task-end recorder,
// other suga processes), so a writer may wait for another's lock:
//   - busy_timeout 15s: SQLite's busy wait is a sleep-and-retry, not a queue,
//     so a writer can lose the lock to a busy one several times in a row;
//   - synchronous NORMAL (the setting SQLite recommends with WAL): a commit
//     no longer fsyncs the WAL, only a checkpoint does. On Windows under
//     load an fsync took 15-50ms, enough for 100 commits of two writers to
//     run past a 5s busy_timeout (suag-5o5g). A power loss may drop the last
//     commits, never corrupt the database.
//   - _txlock immediate: every transaction of this handle takes the write lock
//     at BEGIN, so a read-then-write cannot fail later on a lock upgrade. A
//     begin that stays busy is retried by beginWriteTx (tx.go).
func buildDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(15000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_txlock", "immediate")
	return fileURI(path) + "?" + q.Encode()
}

// buildReadOnlyDSN is buildDSN for a pure reader: mode=ro makes SQLite fail on
// a missing file instead of creating it, and the pragmas a reader may not set
// (journal_mode, synchronous) are left out. busy_timeout stays, so a reader
// waits for a writer instead of failing at once (SQLITE_BUSY, suag-5o5g).
func buildReadOnlyDSN(path string) string {
	q := url.Values{}
	q.Add("mode", "ro")
	q.Add("_pragma", "busy_timeout(15000)")
	q.Add("_pragma", "foreign_keys(1)")
	return fileURI(path) + "?" + q.Encode()
}

// Option adjusts what a Store knows besides its database file.
type Option func(*Store)

// WithSessionsDir sets the sessions root that holds the tool body files
// (default: <db dir>/../sessions).
func WithSessionsDir(dir string) Option {
	return func(s *Store) {
		if dir != "" {
			s.sessionsDir = dir
		}
	}
}

// WithToolBodyInlineBytes sets the tool body threshold (default 1024, 0 keeps
// every body inline).
func WithToolBodyInlineBytes(n int64) Option {
	return func(s *Store) { s.toolBodyInlineBytes = n }
}

// Open opens (creating if needed) the store at path and applies pending
// migrations. Repeated opens are idempotent.
func Open(path string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Every Open of one file in this process shares one *sql.DB: a pool per
	// session would race each other (and their own first-open migration) for the
	// SQLite write lock (suga-g866). A migration re-checks the ledger under the
	// write lock, so concurrent first opens (even of other processes) apply each
	// migration once.
	db, release, err := acquireDB(abs)
	if err != nil {
		return nil, err
	}
	s := newStore(db, abs)
	s.release = release
	for _, opt := range opts {
		opt(s)
	}
	if err := s.migrate(context.Background()); err != nil {
		_ = s.Close()
		return nil, err
	}
	// The persistent switch of the tool body files and their checkpoint live in
	// a small table this open creates (never a migration: opening a ledger must
	// not start full-table work) and a reader does not create.
	if err := s.ensureStoreMeta(context.Background()); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// sharedHandle is the process-wide read-write handle of one database file.
type sharedHandle struct {
	db   *sql.DB
	refs int
}

var (
	sharedMu      sync.Mutex
	sharedHandles = map[string]*sharedHandle{}
)

// sharedKey is the registry key of a database path (Windows paths compare
// case-insensitively).
func sharedKey(abs string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(abs)
	}
	return abs
}

// pingAttempts bounds the retries of the first connection: switching a fresh
// file to WAL needs a lock SQLite does not wait for through busy_timeout when
// another process opens the same new file at the same moment.
const pingAttempts = 8

// acquireDB returns the shared read-write handle of abs (opening it on first
// use) and the release func that drops this caller's reference; the handle
// closes with its last reference.
func acquireDB(abs string) (*sql.DB, func() error, error) {
	key := sharedKey(abs)
	sharedMu.Lock()
	defer sharedMu.Unlock()
	h := sharedHandles[key]
	if h == nil {
		db, err := sql.Open("sqlite", buildDSN(abs))
		if err != nil {
			return nil, nil, fmt.Errorf("open store: %w", err)
		}
		for attempt := 0; ; attempt++ {
			err = db.Ping()
			if err == nil {
				break
			}
			if !isBusy(err) || attempt >= pingAttempts {
				_ = db.Close()
				return nil, nil, fmt.Errorf("open store: %w", err)
			}
			time.Sleep(txBeginBaseDelay << min(attempt, 4))
		}
		h = &sharedHandle{db: db}
		sharedHandles[key] = h
	}
	h.refs++
	release := func() error {
		sharedMu.Lock()
		defer sharedMu.Unlock()
		h.refs--
		if h.refs > 0 {
			return nil
		}
		if sharedHandles[key] == h {
			delete(sharedHandles, key)
		}
		return h.db.Close()
	}
	return h.db, release, nil
}

// newStore is the store of a path with the defaults every open shares.
func newStore(db *sql.DB, abs string) *Store {
	return &Store{
		db: db, path: abs,
		sessionsDir:         deriveSessionsDir(abs),
		toolBodyInlineBytes: DefaultToolBodyInlineBytes,
	}
}

// deriveSessionsDir is the default sessions root of a database path: a store at
// $SUGA_HOME/db/sessions.db owns $SUGA_HOME/sessions.
func deriveSessionsDir(dbPath string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(dbPath)), "sessions")
}

// OpenReadOnly opens an existing store for reading only: it never creates the
// file, never migrates and never writes. A missing file or a database without
// the migration ledger (an empty or foreign file) is reported as an error, so a
// read-only page cannot touch a session store it merely is asked to show.
func OpenReadOnly(path string, opts ...Option) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("open store read-only: %w", err)
	}
	db, err := sql.Open("sqlite", buildReadOnlyDSN(abs))
	if err != nil {
		return nil, fmt.Errorf("open store read-only: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open store read-only: %w", err)
	}
	var ledger int
	if err := db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&ledger); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open store read-only: %w", err)
	}
	if ledger == 0 {
		_ = db.Close()
		return nil, fmt.Errorf("store %s is not initialized: run a suga session against it first", abs)
	}
	s := newStore(db, abs)
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Close releases the database handle.
// A shared handle closes with its last store; closing twice is harmless.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		if s.release != nil {
			s.closeErr = s.release()
			return
		}
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

// DB exposes the handle for read-only queries by sibling packages.
func (s *Store) DB() *sql.DB { return s.db }

// Path returns the absolute database file path.
func (s *Store) Path() string { return s.path }

// migrate applies embedded UP sections in file-name order, recording each in
// schema_migrations so that a second Open skips them.
func (s *Store) migrate(ctx context.Context) error { return s.migrateUpTo(ctx, "") }

// migrateUpTo is migrate stopping after the file named last ("" = all); tests
// use it to build a database at an older schema version.
func (s *Store) migrateUpTo(ctx context.Context, last string) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	files, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".sql") {
			names = append(names, f.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if last != "" && name > last {
			break
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		raw, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		up, err := parseUp(name, string(raw))
		if err != nil {
			return err
		}
		// DDL and the ledger row commit together so a crash cannot half-apply.
		tx, err := s.beginWriteTx(ctx)
		if err != nil {
			return err
		}
		// Another process may have applied it since the check above; the write
		// lock is held now, so this re-check is final.
		var applied int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name).Scan(&applied); err != nil {
			_ = tx.Rollback()
			return err
		}
		if applied > 0 {
			_ = tx.Rollback()
			continue
		}
		if _, err := tx.ExecContext(ctx, up); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(name, applied_at) VALUES (?, ?)`, name, formatTime(time.Now())); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if last != "" {
		return nil
	}
	return s.BackfillMemoryUUIDs(ctx)
}

// parseUp extracts the UP section of a miglite-format file.
func parseUp(name, content string) (string, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, migrateUpMarker) {
		return "", fmt.Errorf("missing migration up marker in %s", name)
	}
	up := strings.TrimPrefix(content, migrateUpMarker)
	if i := strings.Index(up, migrateDownMarker); i >= 0 {
		up = up[:i]
	}
	up = strings.TrimSpace(up)
	if up == "" {
		return "", fmt.Errorf("empty migration up section in %s", name)
	}
	return up, nil
}
