package personal

import (
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// knobSession runs one turn per prompt in one session, then breaks the segment
// by hand (the same path as :compact) and returns the written package and the
// summary requests the provider saw.
func knobSession(t *testing.T, prompts []string, truncated bool, mut func(*Config)) (summary string, prov *echoGrowthProv) {
	t.Helper()
	repo := openObserveRepo(t)
	prov = &echoGrowthProv{truncated: truncated}
	runner := compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 100000, 1000
		if mut != nil {
			mut(c)
		}
	})
	var session string
	for _, prompt := range prompts {
		res, err := runner.Run(context.Background(), RunRequest{SessionID: session, CreateIfMissing: true, Prompt: prompt})
		assert.Require(t, assert.NoErr(t, err))
		session = res.SessionID
	}
	_, err := runner.Compact(context.Background(), session)
	assert.Require(t, assert.NoErr(t, err))
	recs := compactionEntries(t, repo, session)
	assert.Require(t, assert.Len(t, recs, 1))
	return recs[0].Summary, prov
}

var knobPrompts = []string{
	"first task: port the order parser to the new API",
	"second request: add tests for the order parser",
	"third request: fix the failing parser test",
	"fourth request: update the parser docs",
	"fifth request: run the full test suite",
}

// TestCompactPinFirstUser: with context.pin_first_user the session's first
// request is in every package next to the recent ones; off (the default) the
// package carries the 3 latest requests only.
func TestCompactPinFirstUser(t *testing.T) {
	t.Run("default keeps the three latest", func(t *testing.T) {
		summary, _ := knobSession(t, knobPrompts, false, nil)
		assert.NotContains(t, summary, "first task: port")
		assert.StrContains(t, summary, "fifth request")
	})
	t.Run("pinned first request stays in the package", func(t *testing.T) {
		summary, _ := knobSession(t, knobPrompts, false, func(c *Config) { c.PinFirstUser = true })
		assert.StrContains(t, summary, "first task: port the order parser")
		assert.StrContains(t, summary, "fifth request")
		assert.Eq(t, 1, strings.Count(summary, "first task: port"))
	})
	t.Run("a first request already among the recent ones is not repeated", func(t *testing.T) {
		summary, _ := knobSession(t, knobPrompts[:2], false, func(c *Config) { c.PinFirstUser = true })
		assert.Eq(t, 1, strings.Count(summary, "first task: port"))
	})
}

// TestCompactKeepUserWordsBudget: the user's original words get a byte budget
// of their own: a larger one keeps more than three requests, a small one drops
// the oldest, and the default keeps the fixed three.
func TestCompactKeepUserWordsBudget(t *testing.T) {
	t.Run("default is the fixed three", func(t *testing.T) {
		summary, _ := knobSession(t, knobPrompts, false, nil)
		assert.NotContains(t, summary, "second request")
		assert.StrContains(t, summary, "third request")
	})
	t.Run("a large budget keeps all of them", func(t *testing.T) {
		summary, _ := knobSession(t, knobPrompts, false, func(c *Config) { c.KeepUserWordsBudget = 4000 })
		for _, want := range []string{"first task", "second request", "third request", "fourth request", "fifth request"} {
			assert.StrContains(t, summary, want)
		}
	})
	t.Run("a small budget keeps the newest that fit", func(t *testing.T) {
		// "fifth request: run the full test suite" is 39 bytes, "fourth ..." 36.
		summary, _ := knobSession(t, knobPrompts, false, func(c *Config) { c.KeepUserWordsBudget = 80 })
		assert.StrContains(t, summary, "fifth request")
		assert.StrContains(t, summary, "fourth request")
		assert.NotContains(t, summary, "third request")
	})
	t.Run("the newest is kept even when it alone exceeds the budget", func(t *testing.T) {
		summary, _ := knobSession(t, knobPrompts, false, func(c *Config) { c.KeepUserWordsBudget = 10 })
		assert.NotContains(t, summary, "fourth request")
		assert.StrContains(t, summary, "fifth")
	})
}

// TestCompactSummaryMaxTokens: the cap goes on the summary request; a summary
// the provider cut at the cap is asked for once more with twice the cap. The
// default sets no cap, and the re-ask stays uncapped too.
func TestCompactSummaryMaxTokens(t *testing.T) {
	t.Run("default sets no cap", func(t *testing.T) {
		_, prov := knobSession(t, knobPrompts[:1], true, nil)
		assert.Require(t, assert.Len(t, prov.sumReqs, 2))
		assert.Eq(t, 0, prov.sumReqs[0].MaxTokens)
		assert.Eq(t, 0, prov.sumReqs[1].MaxTokens)
	})
	t.Run("a cap applies and the re-ask doubles it", func(t *testing.T) {
		_, prov := knobSession(t, knobPrompts[:1], true, func(c *Config) { c.SummaryMaxTokens = 3000 })
		assert.Require(t, assert.Len(t, prov.sumReqs, 2))
		assert.Eq(t, 3000, prov.sumReqs[0].MaxTokens)
		assert.Eq(t, 6000, prov.sumReqs[1].MaxTokens)
		assert.StrContains(t, prov.sumReqs[1].Messages[0].Content, completeProtectedNote)
	})
	t.Run("an answer within the cap is asked for once", func(t *testing.T) {
		_, prov := knobSession(t, knobPrompts[:1], false, func(c *Config) { c.SummaryMaxTokens = 3000 })
		assert.Len(t, prov.sumReqs, 1)
		assert.Eq(t, 3000, prov.sumReqs[0].MaxTokens)
	})
}
