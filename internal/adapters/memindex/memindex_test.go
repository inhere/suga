package memindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/memory/files"
)

func TestStoreSearchSynchronizesFilesAndFallsBack(t *testing.T) {
	ctx := context.Background()
	scope := memory.UserScope()
	root := t.TempDir()
	base, err := files.New(map[memory.Scope]files.Layout{scope: files.DirLayout{Dir: root}},
		files.WithClock(func() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) }))
	assert.Require(t, assert.NoErr(t, err))
	indexed := New(base)

	t.Run("unbound is the same substring search", func(t *testing.T) {
		_, err := base.Write(ctx, scope, memory.Op{Action: memory.ActionAdd, Kind: memory.KindProject,
			Title: "Unbound title", Body: "needle in the body"})
		assert.Require(t, assert.NoErr(t, err))
		want, err := base.Search(ctx, scope, "needle", 10)
		assert.Require(t, assert.NoErr(t, err))
		got, err := indexed.Search(ctx, scope, "needle", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, want, got)
	})

	repo := openRepo(t)
	indexed.Bind(repo)
	var cnID string
	t.Run("write is immediately searchable and ranked", func(t *testing.T) {
		_, err := indexed.Write(ctx, scope, memory.Op{Action: memory.ActionAdd, Kind: memory.KindFeedback,
			Title: "needle title", Body: "short"})
		assert.Require(t, assert.NoErr(t, err))
		_, err = indexed.Write(ctx, scope, memory.Op{Action: memory.ActionAdd, Kind: memory.KindProject,
			Title: "ordinary title", Body: "needle plus a much longer body with unrelated words"})
		assert.Require(t, assert.NoErr(t, err))
		hits, err := indexed.Search(ctx, scope, "needle", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, hits, 3))
		assert.Eq(t, "needle-title", hits[0].ID)
	})
	t.Run("applies_when is searchable through the index", func(t *testing.T) {
		entry, err := indexed.Write(ctx, scope, memory.Op{Action: memory.ActionAdd, Kind: memory.KindFeedback,
			Title: "build flag", Body: "use the flag", AppliesWhen: []string{"toolchain zetagolang 1.26"}})
		assert.Require(t, assert.NoErr(t, err))
		hits, err := indexed.Search(ctx, scope, "zetagolang", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{entry.ID}, hitIDs(hits))
	})
	t.Run("long English and short Chinese terms hit", func(t *testing.T) {
		entry, err := indexed.Write(ctx, scope, memory.Op{Action: memory.ActionAdd, Kind: memory.KindUser,
			Title: "中文记忆", Body: "alpha environment"})
		assert.Require(t, assert.NoErr(t, err))
		cnID = entry.ID
		hits, err := indexed.Search(ctx, scope, "alpha", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{cnID}, hitIDs(hits))
		hits, err = indexed.Search(ctx, scope, "记忆", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{cnID}, hitIDs(hits))
	})
	t.Run("manual edit and deletion are synchronized", func(t *testing.T) {
		path := filepath.Join(root, "unbound-title.md")
		data, err := os.ReadFile(path)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(string(data)+"\nmanual-only-token\n"), 0o600)))
		hits, err := indexed.Search(ctx, scope, "manual-only-token", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{"unbound-title"}, hitIDs(hits))
		assert.Require(t, assert.NoErr(t, os.Remove(path)))
		hits, err = indexed.Search(ctx, scope, "manual-only-token", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, hits)
	})
	t.Run("a deleted index table is rebuilt", func(t *testing.T) {
		_, err := repo.DB().ExecContext(ctx, `DROP TABLE memory_fts`)
		assert.Require(t, assert.NoErr(t, err))
		hits, err := indexed.Search(ctx, scope, "alpha", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{cnID}, hitIDs(hits))
	})
	t.Run("a deleted database is rebuilt from files", func(t *testing.T) {
		path := repo.Path()
		assert.Require(t, assert.NoErr(t, repo.Close()))
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
		fresh, err := store.Open(path)
		assert.Require(t, assert.NoErr(t, err))
		defer fresh.Close()
		indexed.Bind(fresh)
		hits, err := indexed.Search(ctx, scope, "alpha", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []string{cnID}, hitIDs(hits))
	})
}

func openRepo(t *testing.T) *store.Store {
	t.Helper()
	repo, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	return repo
}

func hitIDs(hits []memory.Hit) []string {
	ids := make([]string, len(hits))
	for i, hit := range hits {
		ids[i] = hit.ID
	}
	return ids
}

// TestEntriesForwardsToFiles keeps the optional Enumerator capability visible
// through the index wrapper: the read-only memory lint report enumerates a
// scope once instead of calling Get for every entry.
func TestEntriesForwardsToFiles(t *testing.T) {
	ctx := context.Background()
	scope := memory.UserScope()
	base, err := files.New(map[memory.Scope]files.Layout{scope: files.DirLayout{Dir: t.TempDir()}})
	assert.Require(t, assert.NoErr(t, err))
	indexed := New(base)
	for _, title := range []string{"Use tabs", "Reply in Chinese"} {
		_, werr := indexed.Write(ctx, scope, memory.Op{Action: memory.ActionAdd, Kind: memory.KindFeedback,
			Title: title, Body: "Body of " + title})
		assert.Require(t, assert.NoErr(t, werr))
	}

	entries, err := indexed.Entries(ctx, scope)
	assert.Require(t, assert.NoErr(t, err))
	assert.Len(t, entries, 2)

	t.Run("backend without enumeration is reported", func(t *testing.T) {
		wrapped := New(plainStore{Store: base})
		_, err := wrapped.Entries(ctx, scope)
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "cannot enumerate entries")
	})
}

// plainStore hides the optional memory.Summarizer capability of its base.
type plainStore struct{ memory.Store }

func TestSetSummaryForwardsToFiles(t *testing.T) {
	ctx := context.Background()
	scope := memory.UserScope()
	base, err := files.New(map[memory.Scope]files.Layout{scope: files.DirLayout{Dir: t.TempDir()}})
	assert.Require(t, assert.NoErr(t, err))
	indexed := New(base)
	e, err := indexed.Write(ctx, scope, memory.Op{Action: memory.ActionAdd, Kind: memory.KindFeedback,
		Title: "Use tabs", Body: "Tabs."})
	assert.Require(t, assert.NoErr(t, err))

	assert.NoErr(t, indexed.SetSummary(ctx, scope, e.ID, "项目缩进约定"))
	ix, err := base.Index(ctx, scope)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, ix.Lines, 1))
	assert.Eq(t, "项目缩进约定", ix.Lines[0].Summary)

	t.Run("backend without a summary slot is reported", func(t *testing.T) {
		wrapped := New(plainStore{Store: base})
		err := wrapped.SetSummary(ctx, scope, e.ID, "x")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "cannot store a summary separately")
	})
}
