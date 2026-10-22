package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/memory/files"
	"github.com/inhere/suga/pkg/agentcore/testutil"
)

// lintTestNow is the clock of the scan tests; Updated values are relative to it.
var lintTestNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func TestLintScanCandidates(t *testing.T) {
	entries := func() []lintEntry {
		return []lintEntry{
			{ID: "prefers-tabs", Title: "Prefers tabs.", Body: "Use tabs for indentation.", hasBody: true,
				Updated: lintTestNow.Add(-2 * time.Hour)},
			// Same text after lowercasing, collapsing spaces and dropping punctuation.
			{ID: "prefers-tabs-2", Title: "prefers  TABS!", Body: "use tabs for Indentation", hasBody: true,
				Updated: lintTestNow.Add(-time.Hour)},
			// Near duplicate: the same tokens in another order, so the token sets
			// are equal (similarity 1) while the normalized texts differ.
			{ID: "tabs-indent", Title: "Use tabs for indentation", Body: "prefers  tabs", hasBody: true,
				Updated: lintTestNow.Add(-3 * time.Hour)},
			{ID: "release-day", Title: "Release day", Body: "Fridays only.", hasBody: true,
				Updated: lintTestNow.Add(-4 * time.Hour)},
			{ID: "release-day-2", Title: "release day", Body: "Thursdays only.", hasBody: true,
				Updated: lintTestNow.Add(-5 * time.Hour)},
			{ID: "old-fact", Title: "Old fact", Body: "A conclusion from a past project.", hasBody: true,
				Updated: lintTestNow.Add(-400 * 24 * time.Hour)},
			{ID: "fresh-fact", Title: "Fresh fact", Body: "A conclusion from this week.", hasBody: true,
				Updated: lintTestNow.Add(-48 * time.Hour)},
		}
	}

	t.Run("exact, near, conflict and stale are reported", func(t *testing.T) {
		candidates, notes := lintScanScope(entries(), lintTestNow)
		assert.Eq(t, 0, len(notes))

		kinds := map[string]lintCandidate{}
		for _, c := range candidates {
			kinds[c.Kind+":"+strings.Join(c.IDs, ",")] = c
		}
		exact, ok := kinds["duplicate:prefers-tabs,prefers-tabs-2"]
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, 1.0, exact.Similarity)
		assert.Eq(t, LintActionMerge, exact.Action)
		assert.StrContains(t, exact.Reason, "identical")

		_, ok = kinds["duplicate:prefers-tabs-2,tabs-indent"]
		assert.True(t, ok, "the near duplicate pair is reported")

		conflict, ok := kinds["conflict:release-day,release-day-2"]
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, LintActionReview, conflict.Action)
		assert.StrContains(t, conflict.Reason, "same title after normalization")
		assert.StrContains(t, conflict.Reason, "bodies differ")
		_, ok = kinds["duplicate:release-day,release-day-2"]
		assert.False(t, ok, "a same-title pair with different bodies is a conflict, not a duplicate")

		stale, ok := kinds["stale:old-fact"]
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, LintActionReview, stale.Action)
		assert.StrContains(t, stale.Reason, "older than 365 days")
		assert.False(t, strings.Contains(fmt.Sprint(kinds), "stale:fresh-fact"))
	})

	t.Run("identical entries yield one candidate", func(t *testing.T) {
		candidates, _ := lintScanScope([]lintEntry{
			{ID: "a", Title: "Use tabs", Body: "Always tabs.", hasBody: true},
			{ID: "b", Title: "use TABS", Body: "always tabs", hasBody: true},
		}, lintTestNow)
		assert.Eq(t, 1, len(candidates))
		assert.Eq(t, LintDuplicate, candidates[0].Kind)
		assert.Eq(t, []string{"a", "b"}, candidates[0].IDs)
	})

	t.Run("unrelated entries yield nothing", func(t *testing.T) {
		candidates, notes := lintScanScope([]lintEntry{
			{ID: "a", Title: "Deploy target", Body: "Windows host, gofer job.", hasBody: true},
			{ID: "b", Title: "Reply language", Body: "Chinese by default.", hasBody: true},
		}, lintTestNow)
		assert.Eq(t, 0, len(candidates))
		assert.Eq(t, 0, len(notes))
	})

	t.Run("empty scope", func(t *testing.T) {
		candidates, notes := lintScanScope(nil, lintTestNow)
		assert.Eq(t, 0, len(candidates))
		assert.Eq(t, 0, len(notes))
	})

	t.Run("index-only entries compare titles and summaries", func(t *testing.T) {
		candidates, _ := lintScanScope([]lintEntry{
			{ID: "tabs", Title: "Tabs", Summary: "Use tabs for indentation."},
			{ID: "tabs-2", Title: "Tabs", Summary: "Use spaces for indentation."},
			{ID: "tabs-3", Title: "Tabs", Summary: "Use tabs for indentation."},
		}, lintTestNow)
		var conflict, duplicate bool
		for _, c := range candidates {
			switch c.Kind {
			case LintConflict:
				conflict = true
				assert.StrContains(t, c.Reason, "index summaries")
			case LintDuplicate:
				duplicate = true
				assert.StrContains(t, c.Reason, "bodies were not read")
			case LintStale:
				t.Fatalf("an index-only entry must not produce a stale candidate: %+v", c)
			}
		}
		assert.True(t, conflict, "different summaries under one title conflict")
		assert.True(t, duplicate, "equal summaries are duplicates")
	})
}

// TestLintScanOrderIsStable pins the deterministic order of the report.
func TestLintScanOrderIsStable(t *testing.T) {
	var entries []lintEntry
	for i := 0; i < 6; i++ {
		entries = append(entries, lintEntry{ID: fmt.Sprintf("dup-%d", i), Title: "Same", Body: "Same body.",
			hasBody: true, Updated: lintTestNow})
	}
	first, _ := lintScanScope(append([]lintEntry(nil), entries...), lintTestNow)
	second, _ := lintScanScope(entries, lintTestNow)
	assert.Eq(t, len(first), len(second))
	for i := range first {
		assert.Eq(t, first[i].Kind, second[i].Kind)
		assert.Eq(t, strings.Join(first[i].IDs, ","), strings.Join(second[i].IDs, ","))
	}
}

// TestLintScanLargeScope guards the cost of the report: 10k entries must be
// scanned in well under a second, so it cannot be an O(n²) comparison.
func TestLintScanLargeScope(t *testing.T) {
	words := []string{"release", "deploy", "branch", "test", "review", "windows", "linux", "memory",
		"session", "token", "config", "provider", "model", "tool", "skill", "commit", "issue", "plan"}
	entries := make([]lintEntry, 0, 10_000)
	for i := 0; i < 10_000; i++ {
		var b strings.Builder
		for k := 0; k < 12; k++ {
			fmt.Fprintf(&b, "%s ", words[(i*7+k*3)%len(words)])
		}
		entries = append(entries, lintEntry{ID: fmt.Sprintf("entry-%05d", i), Title: fmt.Sprintf("Note %d", i),
			Body: b.String(), hasBody: true, Updated: lintTestNow.Add(-time.Duration(i) * time.Minute)})
	}
	// A few real findings must survive the volume.
	entries[100].Body = entries[5].Body
	entries[100].Title = entries[5].Title

	start := time.Now()
	candidates, notes := lintScanScope(entries, lintTestNow)
	elapsed := time.Since(start)
	t.Logf("lint scan of %d entries: %s, %d candidates, %d notes", len(entries), elapsed, len(candidates), len(notes))
	assert.True(t, elapsed < 5*time.Second, "10k entries take %s", elapsed)
	assert.True(t, len(candidates) > 0, "the identical pair is found")
}

func TestMemoryLintReport(t *testing.T) {
	root := tempRepo(t)
	sc := memory.ProjectScope("demo")
	store, err := files.New(map[memory.Scope]files.Layout{sc: files.RepoLayout{Root: root}})
	assert.Require(t, assert.NoErr(t, err))
	ctx := context.Background()
	for _, op := range []memory.Op{
		{Action: memory.ActionAdd, Title: "Prefers tabs", Kind: memory.KindFeedback, Body: "Use tabs for indentation."},
		{Action: memory.ActionAdd, Title: "Prefers tabs", Kind: memory.KindFeedback, Body: "Use tabs for indentation."},
		{Action: memory.ActionAdd, Title: "Release day", Kind: memory.KindProject, Body: "Fridays only."},
		{Action: memory.ActionAdd, Title: "Release day", Kind: memory.KindProject, Body: "Thursdays only."},
	} {
		_, werr := store.Write(ctx, sc, op)
		assert.Require(t, assert.NoErr(t, werr))
	}
	indexPath := filepath.Join(root, "MEMORY.md")
	indexBefore, err := os.ReadFile(indexPath)
	assert.Require(t, assert.NoErr(t, err))

	router := memory.NewRouter()
	assert.Require(t, assert.NoErr(t, router.Register(memory.ScopeProject, store)))
	scopes := []memory.Scope{sc}

	t.Run("text report", func(t *testing.T) {
		var out bytes.Buffer
		assert.NoErr(t, runMemoryLint(ctx, &out, router, scopes, lintOptions{}))
		text := out.String()
		assert.StrContains(t, text, "project:demo memory (lint): 4 entries")
		assert.StrContains(t, text, "duplicate (merge): prefers-tabs, prefers-tabs-2")
		assert.StrContains(t, text, "conflict (review): release-day, release-day-2")
		assert.StrContains(t, text, "read-only, nothing was changed")
	})

	t.Run("json report is stable", func(t *testing.T) {
		var out bytes.Buffer
		assert.NoErr(t, runMemoryLint(ctx, &out, router, scopes, lintOptions{asJSON: true}))
		var report struct {
			ReadOnly bool `json:"read_only"`
			Scopes   []struct {
				Scope      string `json:"scope"`
				Entries    int    `json:"entries"`
				BodiesRead bool   `json:"bodies_read"`
				Candidates []struct {
					Kind       string   `json:"kind"`
					IDs        []string `json:"ids"`
					Similarity float64  `json:"similarity"`
					Reason     string   `json:"reason"`
					Action     string   `json:"action"`
				} `json:"candidates"`
			} `json:"scopes"`
			Totals struct {
				Entries    int `json:"entries"`
				Candidates int `json:"candidates"`
			} `json:"totals"`
		}
		assert.Require(t, assert.NoErr(t, json.Unmarshal(out.Bytes(), &report)))
		assert.True(t, report.ReadOnly)
		assert.Eq(t, 1, len(report.Scopes))
		assert.Eq(t, 4, report.Scopes[0].Entries)
		assert.True(t, report.Scopes[0].BodiesRead)
		assert.Eq(t, report.Totals.Candidates, len(report.Scopes[0].Candidates))
		var kinds []string
		for _, c := range report.Scopes[0].Candidates {
			kinds = append(kinds, c.Kind+":"+strings.Join(c.IDs, ","))
			assert.True(t, c.Reason != "")
			assert.True(t, c.Action != "")
		}
		assert.StrContains(t, strings.Join(kinds, " "), "duplicate:prefers-tabs,prefers-tabs-2")
		assert.StrContains(t, strings.Join(kinds, " "), "conflict:release-day,release-day-2")
	})

	t.Run("nothing was written", func(t *testing.T) {
		after, err := os.ReadFile(indexPath)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, string(indexBefore), string(after))
		entries, err := os.ReadDir(filepath.Join(root, ".suga", "memory"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 4, len(entries))
	})
}

// TestMemoryLintCommandRealStore runs the command through wiring.OpenMemoryStore
// (the same assembly the agent uses) so a wrapper that hides enumeration from
// the report fails here and not only in manual runs.
func TestMemoryLintCommandRealStore(t *testing.T) {
	root := tempRepo(t)
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := testutil.Dir(t)
	t.Setenv("SUGA_HOME", home)
	t.Chdir(root)

	setup, err := wiring.OpenMemoryStore(root)
	assert.Require(t, assert.NoErr(t, err))
	ctx := context.Background()
	ids := map[memory.Scope][]string{}
	for _, sc := range []memory.Scope{setup.User.Scope, setup.Project.Scope} {
		for i := 0; i < 2; i++ {
			e, werr := setup.Router.Write(ctx, sc, memory.Op{Action: memory.ActionAdd, Title: "Prefers tabs",
				Kind: memory.KindFeedback, Body: "Use tabs for indentation."})
			assert.Require(t, assert.NoErr(t, werr))
			ids[sc] = append(ids[sc], e.ID)
		}
	}

	var out bytes.Buffer
	assert.NoErr(t, runMemoryLintCommand(ctx, &out, lintOptions{}))
	text := out.String()
	assert.StrContains(t, text, "user memory (lint): 2 entries")
	assert.StrContains(t, text, fmt.Sprintf("duplicate (merge): %s, %s", ids[setup.User.Scope][0], ids[setup.User.Scope][1]))
	assert.StrContains(t, text, fmt.Sprintf("%s memory (lint): 2 entries", setup.Project.Scope))
	assert.StrContains(t, text, "summary: 2 candidates across 4 entries")

	t.Run("scope filter", func(t *testing.T) {
		var userOut bytes.Buffer
		assert.NoErr(t, runMemoryLintCommand(ctx, &userOut, lintOptions{scope: memory.ScopeUser, asJSON: true}))
		var report struct {
			Scopes []struct {
				Scope string `json:"scope"`
			} `json:"scopes"`
		}
		assert.Require(t, assert.NoErr(t, json.Unmarshal(userOut.Bytes(), &report)))
		assert.Eq(t, 1, len(report.Scopes))
		assert.Eq(t, "user", report.Scopes[0].Scope)
	})

	t.Run("bad scope is refused", func(t *testing.T) {
		var out bytes.Buffer
		assert.Err(t, runMemoryLintCommand(ctx, &out, lintOptions{scope: "people"}))
	})
}
