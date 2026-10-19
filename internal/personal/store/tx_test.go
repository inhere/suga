package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// holdWriteLock takes the write lock of the store through its own connection
// and holds it for hold. It signals through locked once the lock is taken.
func holdWriteLock(t *testing.T, path string, hold time.Duration, locked chan<- struct{}) {
	t.Helper()
	db, err := sql.Open("sqlite", buildDSN(path))
	assert.Require(t, assert.NoErr(t, err))
	defer db.Close()
	conn, err := db.Conn(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Errorf("hold write lock: %v", err)
		return
	}
	if locked != nil {
		locked <- struct{}{}
	}
	time.Sleep(hold)
	_, _ = conn.ExecContext(context.Background(), `COMMIT`)
}

// withoutBusyWait points the store's only pooled connection at busy_timeout 0,
// so a BEGIN that meets a held write lock fails at once instead of waiting: the
// application-level retry of beginWriteTx is what has to bridge the wait.
func withoutBusyWait(t *testing.T, s *Store) {
	t.Helper()
	s.db.SetMaxOpenConns(1)
	_, err := s.db.Exec(`PRAGMA busy_timeout = 0`)
	assert.Require(t, assert.NoErr(t, err))
	var busy int
	assert.NoErr(t, s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy))
	assert.Eq(t, 0, busy)
}

// TestBeginTakesTheWriteLockUpFront: the DSN's _txlock=immediate must make
// BeginTx itself take the write lock, so a transaction cannot start as a reader
// and hit SQLITE_BUSY later at its first write.
func TestBeginTakesTheWriteLockUpFront(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	writer, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer writer.Close()
	assert.Require(t, assert.NoErr(t, writer.CreateSession(context.Background(), Session{ID: "s1", Cwd: "/w"})))

	reader, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer reader.Close()
	withoutBusyWait(t, reader)

	locked := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		holdWriteLock(t, path, 150*time.Millisecond, locked)
	}()
	<-locked
	// The write lock is held by the other handle: a deferred BEGIN would succeed
	// here (and fail later), an immediate one is busy at once.
	tx, err := reader.db.BeginTx(context.Background(), nil)
	if err == nil {
		_ = tx.Rollback()
		t.Fatal("BEGIN while another handle holds the write lock must report the lock")
	}
	assert.True(t, isBusy(err), "unexpected error: "+err.Error())
	<-done
}

// TestWriteTransactionRetriesWhileAnotherProcessHoldsTheLock: a write that meets
// a held write lock must survive the wait through the bounded retry of the
// transaction start, not fail with "database is locked".
func TestWriteTransactionRetriesWhileAnotherProcessHoldsTheLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	writer, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer writer.Close()
	assert.Require(t, assert.NoErr(t, writer.CreateSession(ctx, Session{ID: "s1", Cwd: "/w"})))

	waiter, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer waiter.Close()
	withoutBusyWait(t, waiter)

	locked := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		holdWriteLock(t, path, 150*time.Millisecond, locked)
	}()
	<-locked
	_, err = waiter.AppendEntries(ctx, "s1", NewEntry{Type: EntryUser, JSON: []byte(`{"Role":"user","Content":"retry"}`)})
	<-done
	assert.Require(t, assert.NoErr(t, err))
	got, err := waiter.ListEntries(ctx, "s1", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Len(t, got, 1)
}

// TestConcurrentAppendEntriesFromTwoStores: two handles appending to one session
// with real write transactions (seq assignment, entries_fts row) neither fail
// with a lock error nor assign the same seq.
func TestConcurrentAppendEntriesFromTwoStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	a, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer a.Close()
	b, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer b.Close()
	assert.Require(t, assert.NoErr(t, a.CreateSession(ctx, Session{ID: "s1", Cwd: "/w"})))

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := s.AppendEntries(ctx, "s1", NewEntry{Type: EntryUser, JSON: []byte(`{"Role":"user","Content":"x"}`)}); err != nil {
					errs <- err
				}
				if err := s.PatchSessionMeta(ctx, "s1", map[string]any{"k": i}); err != nil {
					errs <- err
				}
			}
		}(s)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("write failed: %v", err)
	}
	entries, err := a.ListEntries(ctx, "s1", EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Len(t, entries, 40)
	for i, e := range entries {
		assert.Eq(t, int64(i+1), e.Seq)
	}
}
