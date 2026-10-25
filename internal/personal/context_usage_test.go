package personal

import (
	"context"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
)

// TestLoadedFilesListsNewestUse pins :context --files' source: the recorded
// tool events, one line per path under its latest use, newest first, and only
// for the tools whose result is file content.
func TestLoadedFilesListsNewestUse(t *testing.T) {
	ctx := context.Background()
	repo, err := store.Open(newStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := New(Config{Repository: repo, Provider: &rulesProvider{}})
	assert.Require(t, assert.NoErr(t, err))

	for _, event := range []store.Event{
		{SessionID: "s1", TurnID: "t1", Kind: store.EventTool, Name: "file_read",
			Attrs: map[string]any{"target": []string{"a.go"}, "inline_bytes": 100}},
		{SessionID: "s1", TurnID: "t1", Kind: store.EventTool, Name: "shell_exec",
			Attrs: map[string]any{"target": []string{"b.go"}, "inline_bytes": 10}},
		{SessionID: "s1", TurnID: "t2", Kind: store.EventTool, Name: "file_read",
			Attrs: map[string]any{"target": []string{"a.go", "c.go"}, "inline_bytes": 200}},
	} {
		_, err := repo.AppendEvent(ctx, event)
		assert.Require(t, assert.NoErr(t, err))
	}

	files := runner.LoadedFiles(ctx, "s1", 0)
	assert.Require(t, assert.Eq(t, 2, len(files)))
	// Newest use first: c.go joined the second read and a.go keeps its latest
	// read, while shell_exec's target is not a loaded file.
	assert.Eq(t, "c.go", files[0].Path)
	assert.Eq(t, 2, files[0].Turn)
	assert.Eq(t, "a.go", files[1].Path)
	assert.Eq(t, 2, files[1].Turn)
	assert.Eq(t, 200, files[1].Bytes)

	assert.Eq(t, 1, len(runner.LoadedFiles(ctx, "s1", 1)))
	assert.Eq(t, 0, len(runner.LoadedFiles(ctx, "other", 0)))
}

// TestContextUsageReadsTraceAndHistory pins the other half of :context: the
// handoff and history of the segment from loadHistory and the last turn's
// figures from the recorded context event.
func TestContextUsageReadsTraceAndHistory(t *testing.T) {
	ctx := context.Background()
	repo, err := store.Open(newStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := New(Config{Repository: repo, Provider: &rulesProvider{}})
	assert.Require(t, assert.NoErr(t, err))

	empty, err := runner.ContextUsage(ctx, "s1")
	assert.NoErr(t, err)
	assert.Eq(t, 0, empty.HistoryMessages)
	assert.Eq(t, 0, empty.EstTokens)

	_, err = repo.AppendEvent(ctx, store.Event{SessionID: "s1", Kind: store.EventContext, Name: "turn", Status: "ok",
		StartedAt: time.Now(), Attrs: map[string]any{"input": float64(300), "est_tokens": float64(14000), "window": float64(200000)}})
	assert.Require(t, assert.NoErr(t, err))

	usage, err := runner.ContextUsage(ctx, "s1")
	assert.NoErr(t, err)
	assert.Eq(t, 300, usage.Input)
	assert.Eq(t, 14000, usage.EstTokens)
	assert.Eq(t, 200000, usage.Window)
}
