package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Snapshot writes a transactionally consistent SQLite snapshot to destination.
// VACUUM INTO is deliberately explicit: it never copies a live WAL or SHM file.
func (s *Store) Snapshot(ctx context.Context, destination string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("snapshot: store is closed")
	}
	quoted := "'" + strings.ReplaceAll(destination, "'", "''") + "'"
	_, err := s.db.ExecContext(ctx, "VACUUM INTO "+quoted)
	if err != nil {
		return fmt.Errorf("vacuum into snapshot: %w", err)
	}
	return nil
}

// CheckIntegrity verifies a staged SQLite database before it can replace the
// user's live store.
func CheckIntegrity(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("sqlite integrity check: %s", result)
	}
	return nil
}
