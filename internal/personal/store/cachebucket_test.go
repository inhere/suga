package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// modelCall builds a main-loop model event with the given prefix reason and
// token counts (input includes cache, the store's raw convention).
func modelCall(prefix string, input, cacheRead, cacheWrite int64) Event {
	return Event{Kind: EventModel, Name: "p", Status: "ok", InputTokens: input,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
		Attrs: map[string]any{"model": "m", "prefix": prefix}}
}

func TestClassifyEvents(t *testing.T) {
	t.Run("first call is cold", func(t *testing.T) {
		calls := ClassifyEvents([]Event{modelCall(prefixReasonFirst, 100, 0, 0)})
		assert.Require(t, assert.Eq(t, 1, len(calls)))
		assert.Eq(t, BucketCold, calls[0].Bucket)
		assert.Eq(t, 1, calls[0].Step)
	})

	t.Run("append is steady, fold and tools are after_fold", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelCall(prefixReasonFirst, 100, 0, 0),
			modelCall(prefixReasonAppend, 200, 100, 0),
			Event{Kind: EventFold, Name: "fold", Status: "ok"},
			modelCall(prefixReasonFold, 300, 100, 0),
			Event{Kind: EventToolSearch, Name: "search", Status: "ok", Attrs: map[string]any{"cache_invalidated": true}},
			modelCall(prefixReasonTools, 400, 100, 0),
			Event{Kind: EventToolSearch, Name: "search", Status: "ok", Attrs: map[string]any{"cache_invalidated": false}},
			modelCall(prefixReasonAppend, 500, 400, 0),
		})
		got := make([]string, 0, len(calls))
		for _, c := range calls {
			got = append(got, c.Bucket)
		}
		assert.Eq(t, []string{BucketCold, BucketSteady, BucketAfterFold, BucketAfterFold, BucketSteady}, got)
		// The fold event between the calls is recorded on the call that follows it.
		assert.Eq(t, []string{"fold"}, calls[2].After)
		assert.Eq(t, []string{"tools"}, calls[3].After)
		// A tool_search that loaded nothing breaks no prefix.
		assert.Eq(t, 0, len(calls[4].After))
	})

	t.Run("segment break and handoff call", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelCall(prefixReasonFirst, 100, 0, 0),
			modelCall(prefixReasonAppend, 200, 100, 0),
			// The handoff package is written before the segment record.
			Event{Kind: EventModel, Name: "p", Status: "ok", InputTokens: 9000, CacheReadTokens: 0,
				Attrs: map[string]any{"model": "m", "purpose": "compact"}},
			Event{Kind: EventCompaction, Name: "compact", Status: "ok"},
			modelCall(prefixReasonSegment, 300, 0, 200),
		})
		got := make([]string, 0, len(calls))
		for _, c := range calls {
			got = append(got, c.Bucket)
		}
		assert.Eq(t, []string{BucketCold, BucketSteady, BucketHandoff, BucketSegmentStart}, got)
		// The handoff side call is no step of the loop: the next call keeps counting.
		assert.Eq(t, 2, calls[2].Step)
		assert.Eq(t, 3, calls[3].Step)
	})

	t.Run("a cancelled compaction breaks nothing", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelCall(prefixReasonFirst, 100, 0, 0),
			Event{Kind: EventCompaction, Name: "compact", Status: "cancelled"},
			modelCall(prefixReasonAppend, 200, 100, 0),
		})
		assert.Eq(t, BucketSteady, calls[1].Bucket)
	})

	t.Run("truncated tool results count as a fold", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelCall(prefixReasonFirst, 100, 0, 0),
			Event{Kind: EventCompaction, Name: "truncate", Status: "error"},
			modelCall(prefixReasonRewrite, 200, 50, 0),
		})
		assert.Eq(t, BucketAfterFold, calls[1].Bucket)
		assert.Eq(t, []string{"truncate"}, calls[1].After)
	})

	t.Run("unclassifiable calls are unknown, not dropped", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelCall(prefixReasonFirst, 100, 0, 0),
			// A resumed session in a new process: prefix=first again, no events.
			modelCall(prefixReasonFirst, 200, 150, 0),
		})
		assert.Eq(t, 2, len(calls))
		assert.Eq(t, BucketUnknown, calls[1].Bucket)

		// The same, but a segment break lies in between: that is known.
		withSegment := ClassifyEvents([]Event{
			modelCall(prefixReasonFirst, 100, 0, 0),
			Event{Kind: EventCompaction, Name: "compact", Status: "ok"},
			modelCall(prefixReasonFirst, 200, 0, 0),
		})
		assert.Eq(t, BucketSegmentStart, withSegment[1].Bucket)
	})

	t.Run("older sessions fall back to the events", func(t *testing.T) {
		noAttrs := func() Event {
			return Event{Kind: EventModel, Name: "p", Status: "ok", InputTokens: 100, CacheReadTokens: 90,
				Attrs: map[string]any{"model": "m"}}
		}
		calls := ClassifyEvents([]Event{
			noAttrs(),
			noAttrs(),
			Event{Kind: EventFold, Name: "fold", Status: "ok"},
			noAttrs(),
		})
		assert.Eq(t, BucketCold, calls[0].Bucket)
		assert.Eq(t, BucketSteady, calls[1].Bucket)
		assert.Eq(t, BucketAfterFold, calls[2].Bucket)
	})
}

func TestAggregateCacheBuckets(t *testing.T) {
	t.Run("per bucket counts, tokens and hit rate", func(t *testing.T) {
		calls := []CacheCall{
			{Bucket: BucketCold, Step: 1, Input: 1000, CacheRead: 0, CacheWrite: 0},
			{Bucket: BucketSteady, Step: 2, Input: 1100, CacheRead: 1000, CacheWrite: 0},
			{Bucket: BucketSteady, Step: 3, Input: 1200, CacheRead: 1150, CacheWrite: 50},
		}
		got := AggregateCacheBuckets(calls, 0)
		assert.Require(t, assert.Eq(t, 2, len(got.Buckets)))
		assert.Eq(t, BucketCold, got.Buckets[0].Bucket) // report order
		assert.Eq(t, 1, got.Buckets[0].Calls)
		assert.Eq(t, false, got.Buckets[0].CacheReported)
		assert.Eq(t, BucketSteady, got.Buckets[1].Bucket)
		assert.Eq(t, 2, got.Buckets[1].Calls)
		// 100 + 0 uncached input, cache read pool 2150 of 2300 prompt tokens.
		assert.Eq(t, int64(100), got.Buckets[1].InputTokens)
		assert.Eq(t, int64(2150), got.Buckets[1].CacheRead)
		assert.Eq(t, 2150.0/2300.0, got.Buckets[1].CacheHitRate)
		assert.Eq(t, true, got.Buckets[1].CacheReported)
	})

	t.Run("steady calls under the target are listed", func(t *testing.T) {
		calls := []CacheCall{
			{Bucket: BucketSteady, Step: 2, Input: 1000, CacheRead: 800},           // 80%: listed
			{Bucket: BucketSteady, Step: 3, Input: 1000, CacheRead: 980},           // 98%: not listed
			{Bucket: BucketSteady, Step: 4, Input: 1000, CacheRead: 0, After: nil}, // no cache data: skipped
			{Bucket: BucketAfterFold, Step: 5, Input: 1000, CacheRead: 100},        // other bucket: skipped
			{Bucket: BucketSteady, Step: 6, Input: 2000, CacheRead: 1000, After: []string{"fold"}},
		}
		calls[2].CacheWrite = 0
		got := AggregateCacheBuckets(calls, 5)
		assert.Require(t, assert.Eq(t, 2, len(got.LowSteady)))
		assert.Eq(t, 2, got.LowSteady[0].Step)
		assert.Eq(t, int64(200), got.LowSteady[0].Uncached)
		assert.Eq(t, 0.8, got.LowSteady[0].HitRate)
		assert.Eq(t, 6, got.LowSteady[1].Step)
		assert.Eq(t, []string{"fold"}, got.LowSteady[1].Events)

		limited := AggregateCacheBuckets(calls, 1)
		assert.Eq(t, 1, len(limited.LowSteady))
		// A limit of 0 keeps the numbers but lists nothing.
		assert.Eq(t, 0, len(AggregateCacheBuckets(calls, 0).LowSteady))
	})
}

// TestCacheBucketsJSON pins the field names: `suga inspect stats --json` and
// `inspect trace --analyze --json` feed scripts/eval and other readers.
func TestCacheBucketsJSON(t *testing.T) {
	got := AggregateCacheBuckets([]CacheCall{
		{Bucket: BucketCold, Step: 1, Input: 10},
		{Bucket: BucketSteady, Step: 2, Input: 100, CacheRead: 50, CacheWrite: 10, After: []string{"fold"}},
	}, 5)
	raw, err := json.Marshal(got)
	assert.Require(t, assert.NoErr(t, err))
	var decoded struct {
		Buckets []struct {
			Bucket        string  `json:"bucket"`
			Calls         int     `json:"calls"`
			InputTokens   int64   `json:"input_tokens"`
			CacheRead     int64   `json:"cache_read_tokens"`
			CacheWrite    int64   `json:"cache_write_tokens"`
			CacheHitRate  float64 `json:"cache_hit_rate"`
			CacheReported bool    `json:"cache_reported"`
		} `json:"cache_buckets"`
		Low []struct {
			Step     int      `json:"step"`
			Uncached int64    `json:"uncached_input_tokens"`
			Prompt   int64    `json:"prompt_tokens"`
			HitRate  float64  `json:"hit_rate"`
			Events   []string `json:"events"`
		} `json:"steady_low_hit"`
	}
	assert.Require(t, assert.NoErr(t, json.Unmarshal(raw, &decoded)))
	assert.Require(t, assert.Eq(t, 2, len(decoded.Buckets)))
	assert.Eq(t, "steady", decoded.Buckets[1].Bucket)
	assert.Eq(t, int64(40), decoded.Buckets[1].InputTokens)
	assert.Eq(t, int64(50), decoded.Buckets[1].CacheRead)
	assert.Eq(t, int64(10), decoded.Buckets[1].CacheWrite)
	assert.Eq(t, true, decoded.Buckets[1].CacheReported)
	// The steady call's 50% hit is under the target.
	assert.Require(t, assert.Eq(t, 1, len(decoded.Low)))
	assert.Eq(t, 2, decoded.Low[0].Step)
	assert.Eq(t, int64(100), decoded.Low[0].Prompt)
	assert.Eq(t, int64(40), decoded.Low[0].Uncached)
	assert.Eq(t, []string{"fold"}, decoded.Low[0].Events)
}

// TestBuildCacheBuckets reads the classification back from the store: the same
// event sequence as ClassifyEvents, one session per bucket to pin the per
// session cold bucket, plus the session filter.
func TestBuildCacheBuckets(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = s.Close() })
	createSession(t, s, "a", "/w")
	createSession(t, s, "b", "/w")

	base := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	add := func(sid string, minutes int, e Event) {
		t.Helper()
		e.SessionID = sid
		e.StartedAt = base.Add(time.Duration(minutes) * time.Minute)
		_, err := s.AppendEvent(ctx, e)
		assert.Require(t, assert.NoErr(t, err))
	}
	add("a", 0, modelCall(prefixReasonFirst, 1000, 0, 0))
	add("a", 1, modelCall(prefixReasonAppend, 2000, 1800, 0))
	add("a", 2, Event{Kind: EventFold, Name: "fold", Status: "ok"})
	add("a", 3, modelCall(prefixReasonFold, 2000, 1000, 0))
	add("a", 4, modelCall(prefixReasonAppend, 2000, 500, 0)) // 25% hit: under the target
	add("a", 5, Event{Kind: EventModel, Name: "p", Status: "ok", InputTokens: 100, Attrs: map[string]any{"purpose": "compact"}})
	add("a", 6, Event{Kind: EventCompaction, Name: "compact", Status: "ok"})
	add("a", 7, modelCall(prefixReasonSegment, 2000, 0, 0))
	// A second session: its own first call is cold again.
	add("b", 8, modelCall(prefixReasonFirst, 500, 0, 0))
	add("b", 9, modelCall(prefixReasonAppend, 600, 550, 0))

	all, err := s.BuildCacheBuckets(ctx, EventFilter{}, 5)
	assert.Require(t, assert.NoErr(t, err))
	counts := map[string]int{}
	for _, b := range all.Buckets {
		counts[b.Bucket] = b.Calls
	}
	assert.Eq(t, 2, counts[BucketCold])
	assert.Eq(t, 3, counts[BucketSteady])
	assert.Eq(t, 1, counts[BucketAfterFold])
	assert.Eq(t, 1, counts[BucketHandoff])
	assert.Eq(t, 1, counts[BucketSegmentStart])
	assert.Eq(t, 0, counts[BucketUnknown]) // an empty bucket is not reported
	// One steady call of session "a" (500 of 2000) is under the target. No
	// fold/segment event came between it and the previous call: that is the case
	// the list exists for.
	assert.Require(t, assert.Eq(t, 1, len(all.LowSteady)))
	assert.Eq(t, 4, all.LowSteady[0].Step)
	assert.Eq(t, 0, len(all.LowSteady[0].Events))

	one, err := s.BuildCacheBuckets(ctx, EventFilter{SessionID: "b"}, 5)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 2, len(one.Buckets)))
	assert.Eq(t, BucketCold, one.Buckets[0].Bucket)
	assert.Eq(t, BucketSteady, one.Buckets[1].Bucket)
	// 600 prompt tokens, 550 of them cache read: 91.7% hit.
	assert.Eq(t, 550.0/600.0, one.Buckets[1].CacheHitRate)
}
