package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Write transactions and SQLITE_BUSY (issue suga-q3jy).
//
// Several handles write the same file (a chat runtime, the task-end recorder,
// other suga processes). The DSN sets _txlock=immediate, so every transaction
// takes the write lock at BEGIN instead of at its first statement: a writer that
// then does read-then-write cannot fail on a lock upgrade half way through (the
// "first statement is a write" workaround of AppendEntries/PatchSessionMeta was
// that upgrade).
//
// busy_timeout (15s) covers the ordinary wait for the lock. When it expires
// anyway, beginWriteTx retries the BEGIN with exponential backoff a bounded
// number of times. Only the begin is retried: a transaction that already ran
// statements is rolled back and never replayed, so a partially executed write
// cannot be applied twice.
const (
	txBeginAttempts  = 5
	txBeginBaseDelay = 20 * time.Millisecond
	txBeginMaxDelay  = 400 * time.Millisecond
	// txBeginBudget caps the time spent on retries after the first attempt (the
	// busy wait of the first attempt is bounded by busy_timeout).
	txBeginBudget = 2 * time.Second
)

// sqlerr is a driver error carrying a SQLite result code (modernc's *sqlite.Error
// implements Code()).
type sqlerr interface{ Code() int }

// isBusy reports whether err is SQLITE_BUSY (5) or SQLITE_LOCKED (6): the two
// "another connection holds the lock, try again" results.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	var se sqlerr
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 5, 6:
			return true
		}
		return false
	}
	// Fallback for an error already wrapped into a plain error by a caller.
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// beginWriteTx begins the store's write transaction (BEGIN IMMEDIATE) and
// retries only the begin while the database stays busy. The caller owns the
// returned transaction and must Commit or Rollback it.
func (s *Store) beginWriteTx(ctx context.Context) (*sql.Tx, error) {
	deadline := time.Now().Add(txBeginBudget)
	var lastErr error
	for attempt := 0; attempt < txBeginAttempts; attempt++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err == nil {
			return tx, nil
		}
		if !isBusy(err) {
			return nil, err
		}
		lastErr = err
		delay := txBeginBaseDelay << attempt
		if delay > txBeginMaxDelay {
			delay = txBeginMaxDelay
		}
		if time.Now().Add(delay).After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}
