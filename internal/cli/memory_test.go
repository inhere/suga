package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/adapters/gofermem"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/memory/files"
)

// tempRepo returns a temp directory that looks like a repository root (it has
// a .git entry) so the project memory uses RepoLayout without touching any real
// checkout.
func tempRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	assert.Require(t, assert.NoErr(t, os.Mkdir(filepath.Join(root, ".git"), 0o700)))
	return root
}

func TestMemorySummariesPreviewAndApply(t *testing.T) {
	root := tempRepo(t)
	sc := memory.ProjectScope("demo")
	store, err := files.New(map[memory.Scope]files.Layout{sc: files.RepoLayout{Root: root}})
	assert.Require(t, assert.NoErr(t, err))

	outdated := memory.Op{Action: memory.ActionAdd, Title: "Release day", Kind: memory.KindProject,
		Body: "Fridays only. No exceptions."}
	if _, err := store.Write(context.Background(), sc, outdated); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	indexPath := filepath.Join(root, "MEMORY.md")
	indexBefore, err := os.ReadFile(indexPath)
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, string(indexBefore), "— project\n")

	t.Run("preview writes nothing", func(t *testing.T) {
		var out bytes.Buffer
		opts := summariesOptions{missing: true, fill: true}
		assert.NoErr(t, runMemorySummaries(context.Background(), &out, store, []memory.Scope{sc}, opts))
		assert.StrContains(t, out.String(), "- release-day (project): Release day")
		assert.StrContains(t, out.String(), "fill: Fridays only.")
		assert.StrContains(t, out.String(), "dry run")
		now, err := os.ReadFile(indexPath)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, string(indexBefore), string(now))
	})

	t.Run("apply writes the index line only", func(t *testing.T) {
		var out bytes.Buffer
		opts := summariesOptions{missing: true, fill: true, apply: true}
		assert.NoErr(t, runMemorySummaries(context.Background(), &out, store, []memory.Scope{sc}, opts))
		assert.StrContains(t, out.String(), "wrote 1 summary")
		index, err := os.ReadFile(indexPath)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, string(index), "— project: Fridays only.")
		// The entry file itself is untouched.
		entry, err := os.ReadFile(filepath.Join(root, ".suga", "memory", "release-day.md"))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, strings.HasSuffix(string(entry), "Fridays only. No exceptions.\n"))
	})

	t.Run("nothing is missing afterwards", func(t *testing.T) {
		var out bytes.Buffer
		opts := summariesOptions{missing: true}
		assert.NoErr(t, runMemorySummaries(context.Background(), &out, store, []memory.Scope{sc}, opts))
		assert.StrContains(t, out.String(), "no entry is missing a summary")
	})

	t.Run("listing all entries shows the stored summary", func(t *testing.T) {
		var out bytes.Buffer
		assert.NoErr(t, runMemorySummaries(context.Background(), &out, store, []memory.Scope{sc}, summariesOptions{}))
		assert.StrContains(t, out.String(), "- release-day (project): Release day")
		assert.StrContains(t, out.String(), "summary: Fridays only.")
	})
}

func TestMemorySummariesScopeFilter(t *testing.T) {
	root := tempRepo(t)
	project := memory.ProjectScope("demo")
	user := memory.UserScope()
	projectStore, err := files.New(map[memory.Scope]files.Layout{project: files.RepoLayout{Root: root}})
	assert.Require(t, assert.NoErr(t, err))
	userStore, err := files.New(map[memory.Scope]files.Layout{user: files.DirLayout{Dir: filepath.Join(root, "user")}})
	assert.Require(t, assert.NoErr(t, err))
	for _, st := range []*files.Store{projectStore, userStore} {
		sc := project
		if st == userStore {
			sc = user
		}
		_, err := st.Write(context.Background(), sc, memory.Op{Action: memory.ActionAdd, Title: "Tabs", Kind: memory.KindFeedback,
			Body: "Use tabs."})
		assert.Require(t, assert.NoErr(t, err))
	}

	// A router over both scopes, like the obeying runtime builds.
	router := memory.NewRouter()
	assert.NoErr(t, router.Register(memory.ScopeProject, projectStore))
	assert.NoErr(t, router.Register(memory.ScopeUser, userStore))
	var out bytes.Buffer
	assert.NoErr(t, runMemorySummaries(context.Background(), &out, router, []memory.Scope{user}, summariesOptions{missing: true}))
	assert.StrContains(t, out.String(), "user memory")
	assert.False(t, strings.Contains(out.String(), "project:"))
}

// stubMigrator records the MigrateLegacyTitles calls.
type stubMigrator struct {
	moves     []gofermem.TitleMove
	apply     bool
	err       error
	sc        memory.Scope
	callCount int
}

func (m *stubMigrator) MigrateLegacyTitles(ctx context.Context, sc memory.Scope, apply bool) ([]gofermem.TitleMove, error) {
	m.sc, m.apply, m.callCount = sc, apply, m.callCount+1
	return m.moves, m.err
}

func TestRunMemoryGoferTitles(t *testing.T) {
	sc := memory.ProjectScope("demo")
	mig := &stubMigrator{moves: []gofermem.TitleMove{{ID: "legacy", Kind: memory.KindProject, Title: "Old summary",
		NewSummary: "First sentence."}}}

	t.Run("preview lists the moves", func(t *testing.T) {
		var out bytes.Buffer
		assert.NoErr(t, runMemoryGoferTitles(context.Background(), &out, mig, sc, false))
		assert.StrContains(t, out.String(), "gofer memory: 1 entry with the title in the summary slot")
		assert.StrContains(t, out.String(), `- legacy (project): title Old summary`)
		assert.StrContains(t, out.String(), "summary: First sentence.")
		assert.StrContains(t, out.String(), "dry run: nothing was written")
		assert.False(t, mig.apply)
	})
	t.Run("apply writes", func(t *testing.T) {
		var out bytes.Buffer
		assert.NoErr(t, runMemoryGoferTitles(context.Background(), &out, mig, sc, true))
		assert.StrContains(t, out.String(), "migrated 1 entry")
		assert.True(t, mig.apply)
		assert.Eq(t, sc, mig.sc)
	})
	t.Run("empty result", func(t *testing.T) {
		var out bytes.Buffer
		assert.NoErr(t, runMemoryGoferTitles(context.Background(), &out, &stubMigrator{}, sc, false))
		assert.StrContains(t, out.String(), "no gofer entry keeps its title in the summary slot")
	})
	t.Run("cleared summary", func(t *testing.T) {
		var out bytes.Buffer
		m := &stubMigrator{moves: []gofermem.TitleMove{{ID: "x", Title: "T"}}}
		assert.NoErr(t, runMemoryGoferTitles(context.Background(), &out, m, sc, false))
		assert.StrContains(t, out.String(), "summary: cleared (the body has no first sentence)")
	})
	t.Run("backend errors pass through", func(t *testing.T) {
		var out bytes.Buffer
		want := errors.New("gofer is locked")
		assert.ErrIs(t, runMemoryGoferTitles(context.Background(), &out, &stubMigrator{err: want}, sc, false), want)
	})
}

// TestTitleMigratorCapability pins which backends satisfy the gofer-titles
// migration: the gofer store does, the files store does not.
func TestTitleMigratorCapability(t *testing.T) {
	root := tempRepo(t)
	sc := memory.ProjectScope("demo")
	fileStore, err := files.New(map[memory.Scope]files.Layout{sc: files.RepoLayout{Root: root}})
	assert.Require(t, assert.NoErr(t, err))
	gofStore, err := gofermem.New(gofermem.Config{Dir: t.TempDir()})
	assert.Require(t, assert.NoErr(t, err))

	var stores = map[string]memory.Store{"files": fileStore, "gofer": gofStore}
	for name, st := range stores {
		t.Run(name, func(t *testing.T) {
			_, ok := st.(titleMigrator)
			assert.Eq(t, name == "gofer", ok)
		})
	}
}
