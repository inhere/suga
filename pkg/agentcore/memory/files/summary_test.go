package files

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// TestIndexLineWithoutSummaryIsUnchanged proves the old line format still
// parses and renders byte for byte, handwritten lines included.
func TestIndexLineWithoutSummaryIsUnchanged(t *testing.T) {
	old := "# Memory\n" +
		"- [Use tabs](use-tabs.md) — feedback\n" +
		"hand written prose\n" +
		"- [Repo layout](repo-layout.md) — project\n"
	doc := parseIndex(old, "")
	assert.Eq(t, 2, len(doc.entries()))
	assert.Eq(t, "", doc.entries()[0].Summary)
	assert.Eq(t, old, string(doc.render()))

	t.Run("repo layout link", func(t *testing.T) {
		repo := "# Memory\n" +
			"- [Use tabs](.suga/memory/use-tabs.md) — feedback\n" +
			"hand written prose\n" +
			"- [Repo layout](.suga/memory/repo-layout.md) — project\n"
		doc := parseIndex(repo, "")
		assert.Eq(t, 0, len(doc.entries()))
		doc = parseIndex(repo, ".suga/memory")
		assert.Eq(t, 2, len(doc.entries()))
		assert.Eq(t, "repo-layout", doc.entries()[1].ID)
		assert.Eq(t, repo, string(doc.render()))
	})
}

func TestIndexLineSummaryParsing(t *testing.T) {
	data := "# Memory\n" +
		"- [Use tabs](use-tabs.md) — feedback: 项目缩进约定，读写都走 tab\n" +
		"- [Empty](empty.md) — user:\n"
	doc := parseIndex(data, "")
	assert.Require(t, assert.Len(t, doc.entries(), 2))
	assert.Eq(t, "项目缩进约定，读写都走 tab", doc.entries()[0].Summary)
	assert.Eq(t, "", doc.entries()[1].Summary)
	// A line that already exists keeps its bytes (empty summary after a colon)
	// until it is written again; rendering one from scratch drops the colon.
	assert.Eq(t, data, string(doc.render()))
	assert.Eq(t, "- [Empty](empty.md) — user", renderEntryLine(memory.IndexLine{ID: "empty", Title: "Empty", Kind: memory.KindUser}, ""))
}

func TestSummaryWriteReadAndReplace(t *testing.T) {
	dir := t.TempDir()
	store := newStore(t, map[memory.Scope]Layout{user: DirLayout{Dir: filepath.Join(dir, "mem")}})

	op := add("Use tabs", "Tabs everywhere in Go files.", memory.KindFeedback)
	op.Summary = "项目缩进约定"
	e, err := store.Write(ctx, user, op)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "项目缩进约定", e.Summary)

	index := readText(t, filepath.Join(dir, "mem", "MEMORY.md"))
	assert.StrContains(t, index, "- [Use tabs](use-tabs.md) — feedback: 项目缩进约定\n")

	ix, err := store.Index(ctx, user)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, ix.Lines, 1))
	assert.Eq(t, "项目缩进约定", ix.Lines[0].Summary)
	assert.Eq(t, "- use-tabs (feedback): Use tabs — 项目缩进约定", ix.Lines[0].Render())

	got, err := store.Get(ctx, user, "use-tabs")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "项目缩进约定", got.Summary)

	t.Run("replace without a summary keeps it", func(t *testing.T) {
		replaced, err := store.Write(ctx, user, memory.Op{Action: memory.ActionReplace, ID: "use-tabs",
			Title: "Use tabs", Body: "Spaces now."})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "项目缩进约定", replaced.Summary)
		assert.StrContains(t, readText(t, filepath.Join(dir, "mem", "MEMORY.md")), "— feedback: 项目缩进约定")
	})
}

func TestSummaryFoldedAndTruncated(t *testing.T) {
	dir := t.TempDir()
	store := newStore(t, map[memory.Scope]Layout{user: DirLayout{Dir: filepath.Join(dir, "mem")}})
	op := add("Long one", "body", memory.KindProject)
	op.Summary = "first line\nsecond line"
	_, err := store.Write(ctx, user, op)
	assert.Require(t, assert.NoErr(t, err))
	index := readText(t, filepath.Join(dir, "mem", "MEMORY.md"))
	assert.StrContains(t, index, "— project: first line second line\n")

	op = add("Too long", "body", memory.KindProject)
	op.Summary = strings.Repeat("摘", memory.SummaryMaxRunes+30)
	_, err = store.Write(ctx, user, op)
	assert.Require(t, assert.NoErr(t, err))
	ix, err := store.Index(ctx, user)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, ix.Lines, 2))
	assert.Eq(t, memory.SummaryMaxRunes, len([]rune(ix.Lines[1].Summary)))
	assert.True(t, strings.HasSuffix(ix.Lines[1].Summary, "…"))
}

func TestSetSummary(t *testing.T) {
	dir := t.TempDir()
	store := newStore(t, map[memory.Scope]Layout{user: DirLayout{Dir: filepath.Join(dir, "mem")}})
	e, err := store.Write(ctx, user, add("Use tabs", "Tabs everywhere.\nSecond line.", memory.KindFeedback))
	assert.Require(t, assert.NoErr(t, err))
	entryFile := filepath.Join(dir, "mem", "use-tabs.md")
	bodyBefore := readText(t, entryFile)

	assert.NoErr(t, store.SetSummary(ctx, user, e.ID, "项目缩进约定"))
	assert.StrContains(t, readText(t, filepath.Join(dir, "mem", "MEMORY.md")), "— feedback: 项目缩进约定")
	assert.Eq(t, bodyBefore, readText(t, entryFile))

	t.Run("unknown id", func(t *testing.T) {
		assert.ErrIs(t, store.SetSummary(ctx, user, "missing", "x"), memory.ErrNotFound)
	})
	t.Run("empty summary restores the old line", func(t *testing.T) {
		assert.NoErr(t, store.SetSummary(ctx, user, e.ID, ""))
		assert.StrContains(t, readText(t, filepath.Join(dir, "mem", "MEMORY.md")), "- [Use tabs](use-tabs.md) — feedback\n")
	})
}
