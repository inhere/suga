package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/inhere/suga/pkg/agentcore/memory"
)

// The memory_meta side table keeps provenance and tombstones for memory
// entries of every backend (design D4: files are the source of truth, SQLite
// manages provenance). One row per (backend, scope, entry_id); a tombstone is
// a row with deleted_at set and the fingerprint of the removed content.

var _ memory.ProvenanceStore = (*Store)(nil)

// tombstoneKeySep separates the entry id from the fingerprint prefix in the
// entry_id of an archived tombstone row (see Record).
const tombstoneKeySep = "@"

// Record implements memory.ProvenanceStore: it upserts the provenance of an
// added or replaced entry and clears any tombstone of the same id.
//
// Ids are reused (an add may get the id of an entry removed earlier), but the
// primary key holds one row per id. So before the live row replaces a
// tombstone, the tombstone is archived under "<id>@<fingerprint prefix>" in
// the same transaction; IsTombstoned keeps matching it by fingerprint.
func (s *Store) Record(ctx context.Context, backend string, sc memory.Scope, e memory.Entry, fingerprint string) error {
	now := formatTime(time.Now())
	scope := sc.String()
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE OR REPLACE memory_meta
			SET entry_id = entry_id || ? || substr(fingerprint, 1, 16)
			WHERE backend = ? AND scope = ? AND entry_id = ? AND deleted_at IS NOT NULL AND COALESCE(fingerprint, '') <> ''`,
		tombstoneKeySep, backend, scope, e.ID); err != nil {
		return fmt.Errorf("archive tombstone: %w", err)
	}
	tainted := 0
	if e.Prov.Tainted {
		tainted = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_meta
			(backend, scope, entry_id, session_id, turn_id, source, tainted, deleted_at, fingerprint, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)
			ON CONFLICT(backend, scope, entry_id) DO UPDATE SET
				session_id = excluded.session_id, turn_id = excluded.turn_id, source = excluded.source,
				tainted = excluded.tainted, deleted_at = NULL, fingerprint = excluded.fingerprint,
				updated_at = excluded.updated_at`,
		backend, scope, e.ID, e.Prov.SessionID, e.Prov.TurnID, e.Prov.Source, tainted,
		fingerprint, now, now); err != nil {
		return fmt.Errorf("record memory provenance: %w", err)
	}
	return tx.Commit()
}

// Tombstone implements memory.ProvenanceStore: it marks the entry removed and
// stores the fingerprint of the removed content. An empty fingerprint keeps
// the one recorded with the entry. Entries without a provenance row (e.g.
// written by hand) get a new tombstone row.
func (s *Store) Tombstone(ctx context.Context, backend string, sc memory.Scope, id, fingerprint string) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO memory_meta
		(backend, scope, entry_id, deleted_at, fingerprint, created_at, updated_at)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?)
		ON CONFLICT(backend, scope, entry_id) DO UPDATE SET
			deleted_at = excluded.deleted_at,
			fingerprint = COALESCE(excluded.fingerprint, memory_meta.fingerprint),
			updated_at = excluded.updated_at`,
		backend, sc.String(), id, now, fingerprint, now, now)
	if err != nil {
		return fmt.Errorf("tombstone memory entry: %w", err)
	}
	return nil
}

// IsTombstoned implements memory.ProvenanceStore.
func (s *Store) IsTombstoned(ctx context.Context, backend string, sc memory.Scope, fingerprint string) (bool, error) {
	if fingerprint == "" {
		return false, nil
	}
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM memory_meta
		WHERE backend = ? AND scope = ? AND fingerprint = ? AND deleted_at IS NOT NULL LIMIT 1`,
		backend, sc.String(), fingerprint).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check memory tombstones: %w", err)
	}
	return true, nil
}

// MemoryMeta is one memory_meta row (stats, export and tests).
type MemoryMeta struct {
	Backend, Scope, EntryID string
	SessionID, TurnID       string
	Source                  string
	Tainted                 bool
	DeletedAt               time.Time
	Fingerprint             string
	CreatedAt, UpdatedAt    time.Time
}

// MemoryMetaRows lists the rows of one backend and scope ordered by entry id.
func (s *Store) MemoryMetaRows(ctx context.Context, backend string, sc memory.Scope) ([]MemoryMeta, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT backend, scope, entry_id, COALESCE(session_id, ''), COALESCE(turn_id, ''),
		COALESCE(source, ''), COALESCE(tainted, 0), COALESCE(deleted_at, ''), COALESCE(fingerprint, ''),
		COALESCE(created_at, ''), COALESCE(updated_at, '')
		FROM memory_meta WHERE backend = ? AND scope = ? ORDER BY entry_id`, backend, sc.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryMeta
	for rows.Next() {
		var m MemoryMeta
		var tainted int
		var deleted, created, updated string
		if err := rows.Scan(&m.Backend, &m.Scope, &m.EntryID, &m.SessionID, &m.TurnID, &m.Source, &tainted,
			&deleted, &m.Fingerprint, &created, &updated); err != nil {
			return nil, err
		}
		m.Tainted = tainted != 0
		m.DeletedAt, m.CreatedAt, m.UpdatedAt = parseTime(deleted), parseTime(created), parseTime(updated)
		out = append(out, m)
	}
	return out, rows.Err()
}
