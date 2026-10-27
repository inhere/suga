package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/memory/files"
)

func TestMemoryProvenance(t *testing.T) {
	ctx := context.Background()
	user := memory.UserScope()
	proj := memory.ProjectScope("demo")
	fpA := memory.Fingerprint("Ports", "api on 8080")
	fpB := memory.Fingerprint("Ports", "api on 9090")

	t.Run("record upserts and keeps created_at", func(t *testing.T) {
		s := openTemp(t)
		e := memory.Entry{ID: "ports", Prov: memory.Provenance{SessionID: "s1", TurnID: "t1", Source: "tool", Tainted: true}}
		assert.NoErr(t, s.Record(ctx, "files", user, e, fpA))
		rows, err := s.MemoryMetaRows(ctx, "files", user)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, rows, 1))
		first := rows[0]
		assert.Eq(t, "user", first.Scope)
		assert.Eq(t, "s1", first.SessionID)
		assert.Eq(t, "t1", first.TurnID)
		assert.Eq(t, "tool", first.Source)
		assert.True(t, first.Tainted)
		assert.True(t, first.UUID != "")
		assert.Eq(t, fpA, first.Fingerprint)
		assert.True(t, first.DeletedAt.IsZero())

		e.Prov = memory.Provenance{SessionID: "s2", Source: "user"}
		assert.NoErr(t, s.Record(ctx, "files", user, e, fpB))
		rows, err = s.MemoryMetaRows(ctx, "files", user)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, rows, 1))
		assert.Eq(t, "s2", rows[0].SessionID)
		assert.False(t, rows[0].Tainted)
		assert.Eq(t, fpB, rows[0].Fingerprint)
		assert.Eq(t, first.CreatedAt, rows[0].CreatedAt)
	})

	t.Run("tombstone matches by backend scope and fingerprint", func(t *testing.T) {
		s := openTemp(t)
		assert.NoErr(t, s.Record(ctx, "files", proj, memory.Entry{ID: "ports"}, fpA))
		hit, err := s.IsTombstoned(ctx, "files", proj, fpA)
		assert.NoErr(t, err)
		assert.False(t, hit, "a live entry is not a tombstone")

		assert.NoErr(t, s.Tombstone(ctx, "files", proj, "ports", fpA))
		for _, tc := range []struct {
			name    string
			backend string
			scope   memory.Scope
			fp      string
			want    bool
		}{
			{"same", "files", proj, fpA, true},
			{"other fingerprint", "files", proj, fpB, false},
			{"other backend", "gofer", proj, fpA, false},
			{"other scope", "files", memory.ProjectScope("other"), fpA, false},
			{"empty fingerprint", "files", proj, "", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				hit, err := s.IsTombstoned(ctx, tc.backend, tc.scope, tc.fp)
				assert.NoErr(t, err)
				assert.Eq(t, tc.want, hit)
			})
		}
	})

	t.Run("tombstone without fingerprint keeps the recorded one", func(t *testing.T) {
		s := openTemp(t)
		assert.NoErr(t, s.Record(ctx, "files", user, memory.Entry{ID: "ports"}, fpA))
		assert.NoErr(t, s.Tombstone(ctx, "files", user, "ports", ""))
		hit, err := s.IsTombstoned(ctx, "files", user, fpA)
		assert.NoErr(t, err)
		assert.True(t, hit)
	})

	t.Run("tombstone for an unrecorded entry", func(t *testing.T) {
		s := openTemp(t)
		assert.NoErr(t, s.Tombstone(ctx, "gofer", proj, "handmade", fpB))
		hit, err := s.IsTombstoned(ctx, "gofer", proj, fpB)
		assert.NoErr(t, err)
		assert.True(t, hit)
	})

	t.Run("reusing an id keeps the old tombstone", func(t *testing.T) {
		s := openTemp(t)
		firstEntry := memory.Entry{ID: "ports"}
		assert.NoErr(t, s.Record(ctx, "files", user, firstEntry, fpA))
		firstRows, err := s.MemoryMetaRows(ctx, "files", user)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, firstRows, 1))
		firstUUID := firstRows[0].UUID
		assert.NoErr(t, s.Tombstone(ctx, "files", user, "ports", fpA))
		// A different entry later gets the same id.
		assert.NoErr(t, s.Record(ctx, "files", user, memory.Entry{ID: "ports"}, fpB))

		hit, err := s.IsTombstoned(ctx, "files", user, fpA)
		assert.NoErr(t, err)
		assert.True(t, hit, "removed content stays tombstoned")
		hit, err = s.IsTombstoned(ctx, "files", user, fpB)
		assert.NoErr(t, err)
		assert.False(t, hit, "the new live entry is not a tombstone")

		rows, err := s.MemoryMetaRows(ctx, "files", user)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, rows, 2))
		assert.Eq(t, "ports", rows[0].EntryID)
		assert.True(t, rows[0].DeletedAt.IsZero())
		assert.Eq(t, "ports@"+fpA[:16], rows[1].EntryID)
		assert.Eq(t, firstUUID, rows[1].UUID, "the tombstone keeps the removed entry UUID")
		assert.False(t, rows[1].DeletedAt.IsZero())
		assert.NotEq(t, firstUUID, rows[0].UUID, "a reused id gets a new UUID")
	})
}

// TestMemoryUUIDMigrationBackfillsLargeLegacyMetadata verifies that upgrading
// a legacy metadata table fills every UUID in one short transaction.
func TestMemoryUUIDMigrationBackfillsLargeLegacyMetadata(t *testing.T) {
	ctx := context.Background()
	s, _ := openAtCoreSchema(t)
	defer s.Close()
	assert.Require(t, assert.NoErr(t, s.migrateUpTo(ctx, ledgerFTSMigration)))
	assert.Require(t, assert.NoErr(t, execNone(s, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 20001)
		INSERT INTO memory_meta(backend, scope, entry_id, created_at, updated_at)
		SELECT 'files', 'user', 'legacy-' || x, 't', 't' FROM c`)))

	start := time.Now()
	assert.Require(t, assert.NoErr(t, s.migrate(ctx)))
	elapsed := time.Since(start)
	t.Logf("memory UUID migration over 20001 rows: %v", elapsed)
	budget := 5 * time.Second
	if raceEnabled {
		budget = 20 * time.Second
	}
	assert.True(t, elapsed < budget, "memory UUID migration took "+elapsed.String())
	var missing int
	assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM memory_meta WHERE uuid IS NULL OR uuid = ''`).Scan(&missing))
	assert.Eq(t, 0, missing)
}

// TestMemoryProvenanceWithGoverned runs the store as the injected
// ProvenanceStore of a governed files backend: a removed entry cannot be
// written back.
func TestMemoryProvenanceWithGoverned(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	fs, err := files.New(map[memory.Scope]files.Layout{memory.UserScope(): files.DirLayout{Dir: filepath.Join(t.TempDir(), "mem")}})
	assert.Require(t, assert.NoErr(t, err))
	g := memory.NewGoverned(fs, memory.Policy{Provenance: s})

	add := memory.Op{Action: memory.ActionAdd, Title: "Prefers tabs", Body: "use tabs in Go files", Kind: memory.KindUser,
		Prov: memory.Provenance{SessionID: "s1", Source: "tool"}}
	e, err := g.Write(ctx, memory.UserScope(), add)
	assert.Require(t, assert.NoErr(t, err))
	rows, err := s.MemoryMetaRows(ctx, files.Name, memory.UserScope())
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, rows, 1))
	assert.Eq(t, "s1", rows[0].SessionID)

	_, err = g.Write(ctx, memory.UserScope(), memory.Op{Action: memory.ActionRemove, ID: e.ID})
	assert.Require(t, assert.NoErr(t, err))
	_, err = g.Write(ctx, memory.UserScope(), add)
	assert.True(t, errors.Is(err, memory.ErrTombstoned))
}
