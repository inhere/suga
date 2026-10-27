package personal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
)

// TestAnalyzeSegmentSections covers the per-segment section report: every
// segment start event carries the sections of its context message, and the
// analysis names the sections that changed since the previous segment.
func TestAnalyzeSegmentSections(t *testing.T) {
	sections := func(items map[string]string) []any {
		out := make([]any, 0, len(items))
		// deterministic order: environment, rules, memory_index, skills
		for _, name := range []string{"environment", "rules", "memory_index", "skills"} {
			text, ok := items[name]
			if !ok {
				continue
			}
			out = append(out, sectionAttr(name, text))
		}
		return out
	}
	seg := func(ordinal int, items map[string]string) store.Event {
		return store.Event{Kind: store.EventCompaction, Name: "compact", Status: "ok",
			Attrs: map[string]any{"segment": float64(ordinal), "reason": "threshold", "sections": sections(items)}}
	}
	base := map[string]string{"environment": "env", "rules": "rules v1", "memory_index": "mem v1", "skills": "skills"}
	later := map[string]string{"environment": "env", "rules": "rules v1", "memory_index": "mem v2", "skills": "skills"}

	t.Run("the first segment names no change", func(t *testing.T) {
		a := AnalyzeTrace([]store.Event{seg(1, base)})
		assert.Require(t, assert.Eq(t, 1, len(a.Segments)))
		assert.Eq(t, 1, a.Segments[0].Segment)
		assert.Eq(t, 0, len(a.Segments[0].Changed))
		assert.Eq(t, 4, len(a.Segments[0].Sections))
		assert.Gt(t, a.Segments[0].Bytes, 0)
	})
	t.Run("a changed section is named", func(t *testing.T) {
		a := AnalyzeTrace([]store.Event{seg(1, base), seg(2, later)})
		assert.Require(t, assert.Eq(t, 2, len(a.Segments)))
		assert.Eq(t, "memory_index", a.Segments[1].Changed[0])
		assert.Eq(t, 1, len(a.Segments[1].Changed))
	})
	t.Run("a dropped section counts as changed", func(t *testing.T) {
		without := map[string]string{"environment": "env", "rules": "rules v1", "skills": "skills"}
		a := AnalyzeTrace([]store.Event{seg(1, base), seg(2, without)})
		assert.Eq(t, "memory_index", a.Segments[1].Changed[0])
	})
	t.Run("a segment without the attr is skipped", func(t *testing.T) {
		old := store.Event{Kind: store.EventCompaction, Name: "compact", Status: "ok"}
		a := AnalyzeTrace([]store.Event{old, seg(1, base)})
		assert.Require(t, assert.Eq(t, 1, len(a.Segments)))
		assert.Eq(t, 0, len(a.Segments[0].Changed))
	})
	t.Run("the text and JSON report carry it", func(t *testing.T) {
		a := AnalyzeTrace([]store.Event{seg(1, base), seg(2, later)})
		var buf strings.Builder
		assert.NoErr(t, WriteTraceAnalysis(&buf, a, false))
		assert.StrContains(t, buf.String(), "segment sections")
		assert.StrContains(t, buf.String(), "changed memory_index")
		var js strings.Builder
		assert.NoErr(t, WriteTraceAnalysis(&js, a, true))
		var back TraceAnalysis
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(js.String()), &back)))
		assert.Require(t, assert.Eq(t, 2, len(back.Segments)))
		assert.Eq(t, "memory_index", back.Segments[1].Changed[0])
	})
}

// TestCachePrefixReuseAnalysis covers the overall prefix reuse rate of a trace:
// cache read over the estimated cacheable prefix of the previous requests.
func TestCachePrefixReuseAnalysis(t *testing.T) {
	call := func(input, cacheRead int64, attrs map[string]any) store.Event {
		return store.Event{Kind: store.EventModel, Name: "p", Status: "ok",
			InputTokens: input, CacheReadTokens: cacheRead, Attrs: attrs}
	}
	a := AnalyzeTrace([]store.Event{
		call(1000, 0, map[string]any{"prefix": "first"}),
		// 1000 of 2000 prompt tokens could have come from cache, 800 did.
		call(2000, 800, map[string]any{"prefix": "append", "prefix_reused": 1000, "prefix_fresh": 1000}),
	})
	assert.Eq(t, int64(1000), a.PrefixTokens)
	assert.Eq(t, int64(800), a.PrefixReuseRead)
	assert.Eq(t, 0.8, a.PrefixReuse)
}
