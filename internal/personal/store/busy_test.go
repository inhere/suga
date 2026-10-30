package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestManySessionsOneProcessNeverBusy: several handles (one per session) of
// one process write events, entries and session meta at the same time while
// others read; no call may come back with SQLITE_BUSY (suga-g866).
func TestManySessionsOneProcessNeverBusy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	const sessions, rounds = 8, 12
	stores := make([]*Store, sessions)
	for i := range stores {
		s, err := Open(path)
		assert.Require(t, assert.NoErr(t, err))
		defer s.Close()
		stores[i] = s
		assert.Require(t, assert.NoErr(t, s.CreateSession(ctx, Session{ID: fmt.Sprintf("s%d", i), Cwd: "/w"})))
	}
	big := strings.Repeat("x", 4096)
	var wg sync.WaitGroup
	errs := make(chan error, sessions*rounds*8)
	for i, s := range stores {
		id := fmt.Sprintf("s%d", i)
		for w := 0; w < 3; w++ {
			wg.Add(1)
			go func(s *Store, w int) {
				defer wg.Done()
				for r := 0; r < rounds; r++ {
					if _, err := s.AppendEvent(ctx, Event{SessionID: id, Kind: "tool", Name: "x"}); err != nil {
						errs <- err
					}
					if _, err := s.AppendEntries(ctx, id, NewEntry{Type: EntryUser, JSON: []byte(`{"Role":"user","Content":"` + big + `"}`)}); err != nil {
						errs <- err
					}
					if err := s.PatchSessionMeta(ctx, id, map[string]any{"k": r}); err != nil {
						errs <- err
					}
					if _, err := s.ListEntries(ctx, id, EntryFilter{}); err != nil {
						errs <- err
					}
				}
			}(s, w)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("busy or failed: %v", err)
	}
}

// TestConcurrentFirstOpenNeverBusy: handles opened at the same moment on a new
// file neither lose the WAL switch to SQLITE_BUSY nor apply a migration twice.
func TestConcurrentFirstOpenNeverBusy(t *testing.T) {
	for round := 0; round < 20; round++ {
		path := filepath.Join(t.TempDir(), "sessions.db")
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := Open(path)
				if err != nil {
					t.Errorf("round %d: %v", round, err)
					return
				}
				_ = s.Close()
			}()
		}
		wg.Wait()
	}
}

// TestOpenSharesOneHandlePerFile: stores of one file share a pool, and it stays
// open until the last of them closes.
func TestOpenSharesOneHandlePerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	a, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	b, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, a.db == b.db)
	assert.NoErr(t, a.Close())
	assert.NoErr(t, a.Close()) // closing twice drops no second reference
	assert.NoErr(t, b.db.Ping())
	assert.NoErr(t, b.Close())
	c, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	defer c.Close()
	assert.True(t, c.db != a.db)
	assert.NoErr(t, c.db.Ping())
}
