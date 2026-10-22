package personal

import (
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
)

// hostSource is a HostContextSource whose text and hash a test changes between
// turns (an embedding host re-renders its persona and topic index).
type hostSource struct {
	content, hash string
}

func (h *hostSource) source() (string, string) { return h.content, h.hash }

// okProvider is a provider that always answers with a final text.
func okProvider() *scriptProv {
	return &scriptProv{name: "main", normal: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText("ok")
	}}
}

// lastRequest is the last request the provider answered.
func lastRequest(prov *scriptProv) *agentcore.GenerateRequest {
	return prov.reqs[len(prov.reqs)-1]
}

// TestHostContextFirstSegment: the host's text is frozen into the segment and
// rendered as its own section after the additional instructions.
func TestHostContextFirstSegment(t *testing.T) {
	repo := openObserveRepo(t)
	host := &hostSource{content: "人物设定：XH 是一位向导", hash: "h1"}
	prov := okProvider()
	runner := compactRunner(t, repo, prov, nil, func(c *Config) {
		c.HostContextSource = host.source
		c.AppendSystemPrompt = "APPEND-TEXT"
	})
	first, err := runner.Run(context.Background(), RunRequest{Prompt: "one"})
	assert.Require(t, assert.NoErr(t, err))

	seg := segmentText(lastRequest(prov))
	assert.StrContains(t, seg, hostContextHeading)
	assert.StrContains(t, seg, "人物设定：XH 是一位向导")
	// After the additional instructions, before the deferred tool listing (which
	// this runtime has none of).
	assert.True(t, strings.Index(seg, hostContextHeading) > strings.Index(seg, appendHeading))
	assert.Eq(t, 0, countUpdates(lastRequest(prov).Messages))
	assert.Eq(t, "人物设定：XH 是一位向导", effectiveOf(t, runner, first.SessionID).HostContext)
	assert.Eq(t, "h1", effectiveOf(t, runner, first.SessionID).HostContextHash)
}

// TestHostContextUnchangedWritesNoUpdate: equal hash inside a segment means the
// segment's frozen text stays and no context_update entry is written.
func TestHostContextUnchangedWritesNoUpdate(t *testing.T) {
	repo := openObserveRepo(t)
	host := &hostSource{content: "CTX-V1", hash: "h1"}
	prov := okProvider()
	runner := compactRunner(t, repo, prov, nil, func(c *Config) { c.HostContextSource = host.source })
	ctx := context.Background()
	first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
	assert.Require(t, assert.NoErr(t, err))

	for _, prompt := range []string{"two", "three"} {
		_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: prompt})
		assert.NoErr(t, err)
		assert.Eq(t, 0, countUpdates(lastRequest(prov).Messages))
		assert.StrContains(t, segmentText(lastRequest(prov)), "CTX-V1")
	}
	assert.Eq(t, 0, len(contextUpdateEntries(t, repo, first.SessionID)))
}

// TestHostContextChangeWritesUpdate: a new text (new hash) inside a segment is
// appended as a context_update at the next turn and reaches the model, while the
// frozen segment keeps the old text.
func TestHostContextChangeWritesUpdate(t *testing.T) {
	repo := openObserveRepo(t)
	host := &hostSource{content: "CTX-V1", hash: "h1"}
	prov := okProvider()
	runner := compactRunner(t, repo, prov, nil, func(c *Config) { c.HostContextSource = host.source })
	ctx := context.Background()
	first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
	assert.Require(t, assert.NoErr(t, err))

	host.content, host.hash = "CTX-V2 新主题", "h2"
	_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "two"})
	assert.NoErr(t, err)
	req := lastRequest(prov)
	assert.Eq(t, 1, countUpdates(req.Messages))
	assert.StrContains(t, updateTexts(req.Messages), "CTX-V2 新主题")
	assert.StrContains(t, updateTexts(req.Messages), hostContextHeading)
	assert.StrContains(t, segmentText(req), "CTX-V1") // the snapshot is frozen
	assert.Eq(t, "CTX-V2 新主题", effectiveOf(t, runner, first.SessionID).HostContext)

	// Stable again: the update is replayed, never re-written.
	_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "three"})
	assert.NoErr(t, err)
	assert.Eq(t, 1, countUpdates(lastRequest(prov).Messages))
	assert.Eq(t, 1, len(contextUpdateEntries(t, repo, first.SessionID)))

	t.Run("a new hash with the same text is a change", func(t *testing.T) {
		host.hash = "h3"
		_, err := runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "four"})
		assert.NoErr(t, err)
		assert.Eq(t, 2, countUpdates(lastRequest(prov).Messages))
		assert.Eq(t, 2, len(contextUpdateEntries(t, repo, first.SessionID)))
		assert.Eq(t, "h3", effectiveOf(t, runner, first.SessionID).HostContextHash)
	})

	t.Run("empty content clears the section", func(t *testing.T) {
		host.content, host.hash = "", ""
		_, err := runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "five"})
		assert.NoErr(t, err)
		assert.StrContains(t, updateTexts(lastRequest(prov).Messages), "宿主上下文已清除")
		assert.Eq(t, "", effectiveOf(t, runner, first.SessionID).HostContext)
		// Nothing to clear any more: no further update.
		_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "six"})
		assert.NoErr(t, err)
		assert.Eq(t, 3, countUpdates(lastRequest(prov).Messages))
	})
}

// TestHostContextNewSegmentReadsLatest: a compaction ends the segment, so the
// next segment freezes the current host text and needs no update.
func TestHostContextNewSegmentReadsLatest(t *testing.T) {
	repo := openObserveRepo(t)
	host := &hostSource{content: "CTX-OLD", hash: "h1"}
	prov := okProvider()
	runner := compactRunner(t, repo, prov, nil, func(c *Config) { c.HostContextSource = host.source })
	ctx := context.Background()
	first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
	assert.Require(t, assert.NoErr(t, err))
	host.content, host.hash = "CTX-NEW", "h2"
	_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "two"})
	assert.NoErr(t, err)
	assert.Eq(t, 1, countUpdates(lastRequest(prov).Messages))

	_, err = runner.Compact(ctx, first.SessionID)
	assert.NoErr(t, err)
	_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "after break"})
	assert.NoErr(t, err)
	req := lastRequest(prov)
	assert.Eq(t, 0, countUpdates(req.Messages))
	assert.StrContains(t, segmentText(req), "CTX-NEW")
	// The compaction record froze the same text.
	records := segmentRecords(t, repo, first.SessionID)
	assert.StrContains(t, records[len(records)-1].Context.HostContext, "CTX-NEW")
}

// TestHostContextHandoffReadsLatest: a --from handoff creates a new session, so
// its first segment freezes the host text as it is now (not the source's).
func TestHostContextHandoffReadsLatest(t *testing.T) {
	repo := openObserveRepo(t)
	host := &hostSource{content: "CTX-OLD", hash: "h1"}
	prov := okProvider()
	runner := compactRunner(t, repo, prov, nil, func(c *Config) { c.HostContextSource = host.source })
	ctx := context.Background()
	src, err := runner.Run(ctx, RunRequest{Prompt: "one"})
	assert.Require(t, assert.NoErr(t, err))

	host.content, host.hash = "CTX-NEW", "h2"
	handed, err := runner.Handoff(ctx, HandoffRequest{SourceID: src.SessionID})
	assert.Require(t, assert.NoErr(t, err))
	_, err = runner.Run(ctx, RunRequest{SessionID: handed.ID, Prompt: "continue"})
	assert.NoErr(t, err)
	req := lastRequest(prov)
	assert.Eq(t, 0, countUpdates(req.Messages))
	assert.StrContains(t, segmentText(req), "CTX-NEW")
	assert.Eq(t, "CTX-NEW", effectiveOf(t, runner, handed.ID).HostContext)
}

// TestHostContextUnsetKeepsRendering: no source, and a source with empty content,
// render exactly the same segment context - the option changes nothing when the
// host provides no text (no empty section, no update entry).
func TestHostContextUnsetKeepsRendering(t *testing.T) {
	env := func() string { return "# Environment\n- OS: test" }
	rules := &ruleFile{text: "RULE"}
	run := func(t *testing.T, host func() (string, string)) (string, int) {
		t.Helper()
		repo := openObserveRepo(t)
		prov := okProvider()
		runner := compactRunner(t, repo, prov, nil, func(c *Config) {
			c.EnvironmentSource = env
			c.RuleSource = rules.source
			c.HostContextSource = host
		})
		first, err := runner.Run(context.Background(), RunRequest{Prompt: "one"})
		assert.Require(t, assert.NoErr(t, err))
		_, err = runner.Run(context.Background(), RunRequest{SessionID: first.SessionID, Prompt: "two"})
		assert.NoErr(t, err)
		return segmentText(lastRequest(prov)), countUpdates(lastRequest(prov).Messages)
	}

	t.Run("no source", func(t *testing.T) {
		seg, updates := run(t, nil)
		assert.NotContains(t, seg, hostContextHeading)
		assert.Eq(t, 0, updates)
	})

	t.Run("an empty source renders the same bytes", func(t *testing.T) {
		plain, plainUpdates := run(t, nil)
		empty, emptyUpdates := run(t, func() (string, string) { return "", "" })
		assert.Eq(t, plain, empty)
		assert.Eq(t, plainUpdates, emptyUpdates)
		assert.NotContains(t, empty, hostContextHeading)
	})
}
