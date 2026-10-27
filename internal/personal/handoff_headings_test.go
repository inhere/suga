package personal

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore"
)

// translatedBody is reuseBody written the way the 2026-10-27 evaluation answers
// looked (H6): the conversation runs in English, so the headings are translated
// while the section order stays the template's.
const translatedBody = "## Current task\nfix the parser\n## Coordinates\n- internal/personal/compact.go summarize\n" +
	"## Adopted or rejected\n- user words: do not change the protocol\n" +
	"## Verified so far\n- go test ./internal/personal passed\n## Progress and blockers\nnone\n## Next steps\nnone\n" +
	"## Related references\n- none\n"

// testCatalog returns an initialized catalog of one language (the embedded
// resources, as a real run wires them).
func testCatalog(t *testing.T, lang i18n.Language) *i18n.Catalog {
	t.Helper()
	cat, err := i18n.New(lang)
	assert.NoErr(t, err)
	return cat
}

// TestHandoffHeadingsFromCatalog: the accepted heading names come from the model
// catalog, so a package whose headings are translated is recognized whatever UI
// language the session runs in, and no heading name is written in Go (H6).
func TestHandoffHeadingsFromCatalog(t *testing.T) {
	for _, lang := range []i18n.Language{i18n.LanguageZH, i18n.LanguageEN} {
		t.Run(string(lang), func(t *testing.T) {
			h := handoffHeadingsFor(testCatalog(t, lang))
			t.Run("every canonical name is accepted", func(t *testing.T) {
				for _, name := range handoffSectionNames {
					got, ok := h.canonicalName(name)
					assert.Eq(t, true, ok)
					assert.Eq(t, name, got)
				}
			})
			t.Run("the translated names map to the canonical ones", func(t *testing.T) {
				for want, name := range map[string]string{
					"Current task":          handoffSectionTask,
					"Coordinates":           handoffSectionCoords,
					"Adopted or rejected":   handoffProtectedSection,
					"Verified so far":       handoffSectionVerified,
					"Progress and blockers": handoffSectionProgress,
					"Next steps":            handoffSectionNext,
					"Related references":    handoffSectionRefs,
				} {
					got, ok := h.canonicalName(want)
					assert.Eq(t, true, ok)
					if want == "Adopted or rejected" {
						assert.Eq(t, handoffProtectedSection, got)
						continue
					}
					assert.Eq(t, name, got)
				}
			})
			t.Run("case and spacing do not matter", func(t *testing.T) {
				got, ok := h.canonicalName("current   TASK")
				assert.Eq(t, true, ok)
				assert.Eq(t, handoffSectionTask, got)
			})
			t.Run("an unknown heading is not a section", func(t *testing.T) {
				_, ok := h.canonicalName("Other notes")
				assert.Eq(t, false, ok)
			})
		})
	}

	t.Run("a missing catalog adds no alias", func(t *testing.T) {
		// The zero value is what a stored package is read with: canonical only.
		_, ok := handoffHeadings{}.canonicalName("Current task")
		assert.Eq(t, false, ok)
		_, ok = handoffHeadings{}.canonicalName(handoffSectionTask)
		assert.Eq(t, true, ok)
	})
}

// TestCanonicalHandoffHeadingsTranslated: a translated heading (and a numbered,
// or noted one) is rewritten to the canonical stored heading, while prose stays
// prose.
func TestCanonicalHandoffHeadingsTranslated(t *testing.T) {
	h := handoffHeadingsFor(testCatalog(t, i18n.LanguageEN))
	for name, tc := range map[string]struct{ in, want string }{
		"translated heading": {
			"## Current task\n修解析器",
			"## 当前任务\n修解析器",
		},
		"translated with an ascii note": {
			"## Coordinates (files / symbols; 2 KB)\n- src/a.go: Parse",
			"## 坐标\n- src/a.go: Parse",
		},
		"numbered translated heading": {
			"## 2. Verified so far\n- go vet ./...",
			"## 验证到哪\n- go vet ./...",
		},
		"bullet before a translated heading": {
			"### - Next steps\n无",
			"## 下一步\n无",
		},
		"bold translated heading": {
			"**Progress and blockers**\n无",
			"## 进展与阻塞\n无",
		},
		"prose in english is left alone": {
			"Current task: fix the parser.\nCoordinates: src/a.go",
			"Current task: fix the parser.\nCoordinates: src/a.go",
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Eq(t, tc.want, canonicalHandoffHeadings(tc.in, h))
		})
	}
}

// TestHandoffShapeCheckTranslated: a translated package carries every section,
// so the shape check accepts it and the reason names no missing section; an
// answer that merely answers the task in English is still no package - widening
// the headings must not let a real answer through.
func TestHandoffShapeCheckTranslated(t *testing.T) {
	h := handoffHeadingsFor(testCatalog(t, i18n.LanguageEN))

	t.Run("a translated package is a package", func(t *testing.T) {
		assert.Eq(t, 0, len(handoffMissingSections(translatedBody, h)))
		assert.Eq(t, "", handoffAnswerReason(translatedBody, 0, h))
	})

	t.Run("a translated package with one section dropped is not", func(t *testing.T) {
		body := strings.Replace(translatedBody, "## Next steps\nnone\n", "", 1)
		assert.Eq(t, handoffFallbackMissingSections+":"+handoffSectionNext, handoffAnswerReason(body, 0, h))
	})

	t.Run("an answer that continues the task is still discarded", func(t *testing.T) {
		prose := "The parser now returns the error instead of the zero value.\n\n" +
			"Current task: fix the parser.\nCoordinates: internal/personal/compact.go summarize\n" +
			"Verified: go test passed.\n"
		why := handoffAnswerReason(prose, 0, h)
		assert.StrContains(t, why, handoffFallbackMissingSections+":")
		assert.StrContains(t, why, handoffSectionTask)
		assert.StrContains(t, why, handoffSectionRefs)
	})
}

// translatedHeadingsProv answers the reuse path's handoff call with a package
// whose headings are translated and the old path's with a marker text, and
// records the kind of every call. reuseProv's own classifier keys on the Chinese
// instruction marker, which the English catalog does not carry.
type translatedHeadingsProv struct {
	reuseProv
	mu    sync.Mutex
	kinds []string
}

func (p *translatedHeadingsProv) Generate(ctx context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	last := ""
	if len(req.Messages) > 0 {
		last = req.Messages[len(req.Messages)-1].Content
	}
	kind := "main"
	switch {
	case req.System == compactSystemPrompt:
		kind = "old"
	case strings.Contains(last, "## Current task"): // the reuse instruction lists the headings
		kind = "reuse"
	}
	p.mu.Lock()
	p.kinds = append(p.kinds, kind)
	n := len(p.kinds)
	p.mu.Unlock()
	switch kind {
	case "reuse":
		return finalText(translatedBody)
	case "old":
		return finalText("old path answer")
	}
	return reuseMain(n, req)
}

// TestHandoffReuseAcceptsTranslatedHeadings follows the real segment break of an
// English session (the evaluation's case): the reuse answer's translated
// headings are accepted, the old path is not asked again, and the stored package
// carries the canonical headings the budgets, the fold and parseRefs read.
func TestHandoffReuseAcceptsTranslatedHeadings(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &translatedHeadingsProv{}
	runner := compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 100000, 1000
		c.HandoffReusePrefix = true
		c.Translator = testCatalog(t, i18n.LanguageEN)
	})
	ctx := context.Background()
	sid := oneTurn(t, runner)
	_, err := runner.Compact(ctx, sid)
	assert.NoErr(t, err)
	assert.Eq(t, []string{"main", "main", "reuse"}, prov.kinds)
	assert.Eq(t, []string{handoffReusePrefix}, handoffReuseAttrs(t, repo, sid))
	summary := latestSegment(t, repo, sid).Summary
	assert.StrContains(t, summary, "## "+handoffSectionTask)
	assert.StrContains(t, summary, "## "+handoffSectionRefs)
	assert.Eq(t, false, strings.Contains(summary, "## Current task"))
	assert.StrContains(t, summary, "fix the parser")
}

// TestRunnerHandoffHeadingsWireTheCatalog: the two places a generated package
// enters the store - the reuse path's shape check (handoffAnswerReason) and the
// summarize path's canonicalization - read the heading set of the run's catalog
// through the runner, so a real assembly accepts translated headings (H6).
func TestRunnerHandoffHeadingsWireTheCatalog(t *testing.T) {
	repo := openObserveRepo(t)
	runner := compactRunner(t, repo, &reuseProv{main: reuseMain}, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 100000, 1000
		c.HandoffReusePrefix = true
		c.Translator = testCatalog(t, i18n.LanguageEN)
	})
	h := runner.handoffHeadings()
	assert.Eq(t, "", handoffAnswerReason(translatedBody, 0, h))
	assert.Eq(t, false, strings.Contains(canonicalHandoffHeadings(translatedBody, h), "## Current task"))

	t.Run("a run without the catalog keeps the canonical names only", func(t *testing.T) {
		plain := compactRunner(t, openObserveRepo(t), &reuseProv{main: reuseMain}, nil, nil)
		// The fallback catalog carries the same alias set, so an English heading is
		// accepted there too; what must never widen is prose.
		assert.StrContains(t, handoffAnswerReason("Current task: fix the parser", 0, plain.handoffHeadings()),
			handoffFallbackMissingSections)
	})
}
