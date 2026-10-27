package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/inhere/suga/pkg/agentcore/memory"
)

// The memory_meta side table keeps provenance, stable UUIDs and tombstones for
// memory entries of every backend. Files remain the content source of truth;
// SQLite owns the cross-backend identity and governance metadata.

var _ memory.ProvenanceStore = (*Store)(nil)

// tombstoneKeySep separates the entry id from the fingerprint prefix in the
// entry_id of an archived tombstone row (see Record).
const tombstoneKeySep = "@"

// newMemoryUUID returns a RFC 4122 version 4 UUID without adding a dependency
// to the storage package. UUID generation is only used for durable identities;
// callers propagate the random source error instead of writing an empty UUID.
func newMemoryUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate memory uuid: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(raw[:])
	return hexValue[:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:], nil
}

// Record implements memory.ProvenanceStore: it upserts the provenance of an
// added or replaced entry and clears any tombstone of the same id.
//
// Ids are reused (an add may get the id of an entry removed earlier), but the
// primary key holds one row per id. So before the live row replaces a tombstone,
// the tombstone is archived under "<id>@<fingerprint prefix>" in the same
// transaction; its UUID remains available for audit and migration checks.
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
	uuid, err := newMemoryUUID()
	if err != nil {
		return err
	}
	tainted := 0
	if e.Prov.Tainted {
		tainted = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_meta
			(backend, scope, entry_id, uuid, session_id, turn_id, source, tainted, deleted_at, fingerprint, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)
			ON CONFLICT(backend, scope, entry_id) DO UPDATE SET
				session_id = excluded.session_id, turn_id = excluded.turn_id, source = excluded.source,
				tainted = excluded.tainted, deleted_at = NULL, fingerprint = excluded.fingerprint,
				uuid = COALESCE(NULLIF(memory_meta.uuid, ''), excluded.uuid), updated_at = excluded.updated_at`,
		backend, scope, e.ID, uuid, e.Prov.SessionID, e.Prov.TurnID, e.Prov.Source, tainted,
		fingerprint, now, now); err != nil {
		return fmt.Errorf("record memory provenance: %w", err)
	}
	return tx.Commit()
}

// Tombstone implements memory.ProvenanceStore: it marks the entry removed and
// stores the fingerprint of the removed content. An empty fingerprint keeps
// the one recorded with the entry. Entries without a provenance row get a UUID
// now, so a later ID reuse cannot erase this tombstone's identity.
func (s *Store) Tombstone(ctx context.Context, backend string, sc memory.Scope, id, fingerprint string) error {
	now := formatTime(time.Now())
	uuid, err := newMemoryUUID()
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO memory_meta
			(backend, scope, entry_id, uuid, deleted_at, fingerprint, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)
			ON CONFLICT(backend, scope, entry_id) DO UPDATE SET
				deleted_at = excluded.deleted_at,
				fingerprint = COALESCE(excluded.fingerprint, memory_meta.fingerprint),
				uuid = COALESCE(NULLIF(memory_meta.uuid, ''), excluded.uuid),
				updated_at = excluded.updated_at`,
		backend, sc.String(), id, uuid, now, fingerprint, now, now)
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
	UUID                    string
	SessionID, TurnID       string
	Source                  string
	Tainted                 bool
	DeletedAt               time.Time
	Fingerprint             string
	CreatedAt, UpdatedAt    time.Time
}

// MemoryMetaRows lists the rows of one backend and scope ordered by entry id.
func (s *Store) MemoryMetaRows(ctx context.Context, backend string, sc memory.Scope) ([]MemoryMeta, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT backend, scope, entry_id, COALESCE(uuid, ''), COALESCE(session_id, ''), COALESCE(turn_id, ''),
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
		if err := rows.Scan(&m.Backend, &m.Scope, &m.EntryID, &m.UUID, &m.SessionID, &m.TurnID, &m.Source, &tainted,
			&deleted, &m.Fingerprint, &created, &updated); err != nil {
			return nil, err
		}
		m.Tainted = tainted != 0
		m.DeletedAt, m.CreatedAt, m.UpdatedAt = parseTime(deleted), parseTime(created), parseTime(updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// MemoryRef locates a live memory entry by its stable UUID.
type MemoryRef struct {
	Backend, Scope, EntryID, UUID string
}

// MemoryRefByUUID resolves only live rows; tombstones remain auditable through
// MemoryMetaRows but cannot be used to mutate a deleted memory entry.
func (s *Store) MemoryRefByUUID(ctx context.Context, uuid string) (MemoryRef, error) {
	var ref MemoryRef
	err := s.db.QueryRowContext(ctx, `SELECT backend, scope, entry_id, uuid FROM memory_meta
		WHERE uuid = ? AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1`, uuid).
		Scan(&ref.Backend, &ref.Scope, &ref.EntryID, &ref.UUID)
	if err == sql.ErrNoRows {
		return MemoryRef{}, fmt.Errorf("%w: memory uuid %q", memory.ErrNotFound, uuid)
	}
	if err != nil {
		return MemoryRef{}, fmt.Errorf("find memory uuid: %w", err)
	}
	return ref, nil
}

// EnsureMemoryUUID creates the metadata for an old live entry that predates
// UUIDs. If an ID currently points at a tombstone, that row is archived first.
func (s *Store) EnsureMemoryUUID(ctx context.Context, backend string, sc memory.Scope, id string) (string, error) {
	now := formatTime(time.Now())
	var uuid, deleted, fingerprint string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(uuid, ''), COALESCE(deleted_at, ''), COALESCE(fingerprint, '')
		FROM memory_meta WHERE backend = ? AND scope = ? AND entry_id = ?`, backend, sc.String(), id).
		Scan(&uuid, &deleted, &fingerprint)
	if err == nil && deleted == "" && uuid != "" {
		return uuid, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("find memory uuid: %w", err)
	}

	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err == nil && deleted == "" && uuid == "" {
		uuid, err = newMemoryUUID()
		if err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE memory_meta SET uuid = ?, updated_at = ? WHERE backend = ? AND scope = ? AND entry_id = ?`,
			uuid, now, backend, sc.String(), id); err != nil {
			return "", fmt.Errorf("record memory uuid: %w", err)
		}
		return uuid, tx.Commit()
	}
	if deleted != "" {
		archive := id + tombstoneKeySep + fingerprint
		if fingerprint == "" {
			archive = id + tombstoneKeySep + "legacy"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE memory_meta SET entry_id = ? WHERE backend = ? AND scope = ? AND entry_id = ?`,
			archive, backend, sc.String(), id); err != nil {
			return "", fmt.Errorf("archive memory tombstone: %w", err)
		}
	}
	uuid, err = newMemoryUUID()
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_meta
			(backend, scope, entry_id, uuid, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		backend, sc.String(), id, uuid, now, now); err != nil {
		return "", fmt.Errorf("record memory uuid: %w", err)
	}
	return uuid, tx.Commit()
}

// BackfillMemoryUUIDs fills UUIDs on rows from databases created before the
// UUID migration. It runs in one transaction so opening a large store is safe.
func (s *Store) BackfillMemoryUUIDs(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT backend, scope, entry_id FROM memory_meta WHERE uuid IS NULL OR uuid = ''`)
	if err != nil {
		return err
	}
	type row struct{ backend, scope, id string }
	var missing []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.backend, &r.scope, &r.id); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range missing {
		uuid, err := newMemoryUUID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE memory_meta SET uuid = ? WHERE backend = ? AND scope = ? AND entry_id = ? AND (uuid IS NULL OR uuid = '')`,
			uuid, r.backend, r.scope, r.id); err != nil {
			return fmt.Errorf("backfill memory uuid: %w", err)
		}
	}
	return tx.Commit()
}
