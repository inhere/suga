package store

import (
	"context"
	"fmt"
	"os"
)

// VacuumResult reports the database file sizes around a VACUUM. The main file
// only grows while the data sits in the -wal file, so a caller that wants to
// show the reclaimed space compares the totals (BytesBefore/BytesAfter).
type VacuumResult struct {
	BytesBefore    int64
	BytesAfter     int64
	DBBytesBefore  int64
	DBBytesAfter   int64
	WALBytesBefore int64
	WALBytesAfter  int64
}

// Vacuum rewrites the database file so that the space freed by deletions and
// the FTS shrink returns to the disk, then truncates the WAL. It needs roughly
// as much free space as the file is large (SQLite writes a new file and swaps
// it in), so the caller checks that first. The store must not be used from
// another process while it runs: VACUUM rewrites every page.
func (s *Store) Vacuum(ctx context.Context) (VacuumResult, error) {
	var result VacuumResult
	result.BytesBefore = DatabaseFileBytes(s.path)
	result.DBBytesBefore = fileBytes(s.path)
	result.WALBytesBefore = fileBytes(s.path + "-wal")
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return result, fmt.Errorf("vacuum: %w", err)
	}
	// The VACUUM rewrote the main file; the old WAL content is now dead weight,
	// so it is truncated (this is what makes the .db-wal small again, and it
	// replaces the file with an empty one that the next writer grows again if
	// needed).
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return result, fmt.Errorf("truncate wal: %w", err)
	}
	result.BytesAfter = DatabaseFileBytes(s.path)
	result.DBBytesAfter = fileBytes(s.path)
	result.WALBytesAfter = fileBytes(s.path + "-wal")
	return result, nil
}

// DatabaseFileBytes is the size of the database file path plus its -wal and
// -shm side files, the number that matters for the free-space check: a VACUUM
// writes a new file of about the main size while the old one still exists.
func DatabaseFileBytes(path string) int64 {
	total := int64(0)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		total += fileBytes(path + suffix)
	}
	return total
}

// fileBytes is the size of one file (0 when it does not exist).
func fileBytes(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
