package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// modelEvent is one main-loop model call event with the attrs the bucket
// classification reads.
func modelEvent(input, cacheRead int64, attrs map[string]any) Event {
	return Event{Kind: EventModel, Name: "p", Status: "ok", InputTokens: input, CacheReadTokens: cacheRead, Attrs: attrs}
}

// TestClassifyResumeBuckets covers the second step of the cache buckets: the
// first call of a resumed session is resume_same or resume_changed according to
// the persisted fingerprint, and old data without one stays resume.
func TestClassifyResumeBuckets(t *testing.T) {
	t.Run("resume_same is a continued conversation", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(100, 0, map[string]any{"prefix": "first"}),
			modelEvent(2000, 1900, map[string]any{"prefix": "append", "prefix_resume": "same"}),
		})
		assert.Eq(t, BucketCold, calls[0].Bucket)
		assert.Eq(t, BucketResumeSame, calls[1].Bucket)
		assert.Eq(t, 2, calls[1].Step)
	})
	t.Run("resume_changed keeps the cause", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(100, 0, map[string]any{"prefix": "first"}),
			modelEvent(2000, 0, map[string]any{"prefix": "system", "prefix_resume": "changed", "prefix_resume_reason": "system"}),
		})
		assert.Eq(t, BucketResumeChanged, calls[1].Bucket)
		assert.Eq(t, "system", calls[1].Reason)
	})
	t.Run("a session without a fingerprint stays resume", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(100, 0, map[string]any{"prefix": "first"}),
			modelEvent(2000, 1900, map[string]any{"prefix": "first"}),
		})
		assert.Eq(t, BucketResume, calls[1].Bucket)
	})
	t.Run("a segment break wins over the resume flag", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(100, 0, map[string]any{"prefix": "first"}),
			{Kind: EventCompaction, Name: "compact", Status: "ok"},
			modelEvent(2000, 0, map[string]any{"prefix": "segment", "prefix_resume": "changed"}),
		})
		assert.Eq(t, BucketSegmentStart, calls[1].Bucket)
	})
}

// TestClassifyProviderSwitch covers the three triggers: the served model
// changed, the pool fell back after the previous attempt, and a model_switch
// session event in between.
func TestClassifyProviderSwitch(t *testing.T) {
	t.Run("the served model changed", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(100, 0, map[string]any{"prefix": "first", "model": "m1"}),
			modelEvent(2000, 1900, map[string]any{"prefix": "append", "model": "m2"}),
		})
		assert.Eq(t, BucketProviderSwitch, calls[1].Bucket)
		assert.Eq(t, BucketCold, calls[0].Bucket) // the first call has nothing to switch from
	})
	t.Run("the provider changed", func(t *testing.T) {
		first := modelEvent(100, 0, map[string]any{"prefix": "first", "model": "m1"})
		second := modelEvent(2000, 1900, map[string]any{"prefix": "append", "model": "m1"})
		second.Name = "other"
		calls := ClassifyEvents([]Event{first, second})
		assert.Eq(t, BucketProviderSwitch, calls[1].Bucket)
	})
	t.Run("the pool fell back", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(100, 0, map[string]any{"prefix": "first", "model": "m1"}),
			modelEvent(2000, 1900, map[string]any{"prefix": "append", "model": "m1", "fallback": "next, transient"}),
		})
		assert.Eq(t, BucketProviderSwitch, calls[1].Bucket)
		assert.Eq(t, "m1", calls[1].Reason)
	})
	t.Run("a model_switch in between", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(100, 0, map[string]any{"prefix": "first", "model": "m1"}),
			{Kind: EventSession, Name: "model_switch", Status: "ok", Attrs: map[string]any{"from": "p/m1", "to": "p/m2"}},
			modelEvent(2000, 1900, map[string]any{"prefix": "append", "model": "m2"}),
		})
		assert.Eq(t, BucketProviderSwitch, calls[1].Bucket)
	})
	t.Run("the same model keeps steady", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(100, 0, map[string]any{"prefix": "first", "model": "m1"}),
			modelEvent(2000, 1900, map[string]any{"prefix": "append", "model": "m1"}),
		})
		assert.Eq(t, BucketSteady, calls[1].Bucket)
	})
}

// TestCachePrefixReuse covers the reuse rate: cache read over the estimated
// cacheable prefix of the previous request, capped at 100%.
func TestCachePrefixReuse(t *testing.T) {
	t.Run("half of the cacheable prefix was served", func(t *testing.T) {
		// 1000 chars of the request, 900 of them reused → 90% of the 1000 prompt
		// tokens could come from cache; the provider served 400.
		events := []Event{
			modelEvent(1000, 0, map[string]any{"prefix": "first"}),
			modelEvent(1000, 400, map[string]any{"prefix": "append", "prefix_reused": 900, "prefix_rebuilt": 0, "prefix_fresh": 100}),
		}
		calls := ClassifyEvents(events)
		assert.Eq(t, int64(900), calls[1].PrefixTokens)
		got := AggregateCacheBuckets(calls, 0)
		assert.Require(t, assert.Eq(t, 2, len(got.Buckets)))
		steady := got.Buckets[1]
		assert.Eq(t, BucketSteady, steady.Bucket)
		assert.Eq(t, int64(900), steady.PrefixTokens)
		assert.Eq(t, 400.0/900.0, steady.PrefixReuse)
	})
	t.Run("serving more than the estimate is capped", func(t *testing.T) {
		calls := ClassifyEvents([]Event{
			modelEvent(1000, 0, map[string]any{"prefix": "first"}),
			modelEvent(1000, 1000, map[string]any{"prefix": "append", "prefix_reused": 100, "prefix_fresh": 900}),
		})
		got := AggregateCacheBuckets(calls, 0)
		assert.Eq(t, 1.0, got.Buckets[1].PrefixReuse)
	})
	t.Run("calls without a prefix estimate report no reuse", func(t *testing.T) {
		calls := ClassifyEvents([]Event{modelEvent(1000, 0, map[string]any{"prefix": "first"})})
		got := AggregateCacheBuckets(calls, 0)
		assert.Eq(t, int64(0), got.Buckets[0].PrefixTokens)
		assert.Eq(t, 0.0, got.Buckets[0].PrefixReuse)
	})
}

// TestCacheBucketJSONFieldNames locks the JSON field names of the new numbers:
// the eval and the trace analysis read them by name.
func TestCacheBucketJSONFieldNames(t *testing.T) {
	calls := ClassifyEvents([]Event{
		modelEvent(100, 0, map[string]any{"prefix": "first", "model": "m1"}),
		modelEvent(1000, 900, map[string]any{"prefix": "system", "prefix_resume": "changed",
			"prefix_resume_reason": "system", "prefix_reused": 10, "prefix_fresh": 90}),
	})
	raw, err := json.Marshal(AggregateCacheBuckets(calls, 0))
	assert.Require(t, assert.NoErr(t, err))
	out := string(raw)
	for _, want := range []string{`"prefix_reused_tokens"`, `"prefix_reuse_rate"`, `"reasons":{"system":1}`} {
		assert.StrContains(t, out, want)
	}
}

// TestBuildCacheBucketsResume reads the new attrs back through the SQL pass: the
// resumed-session outcome, the model switch, the prefix sizes and their reason.
func TestBuildCacheBucketsResume(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db", "sessions.db"))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = s.Close() })
	createSession(t, s, "a", "/w")

	base := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	add := func(minutes int, e Event) {
		t.Helper()
		e.SessionID = "a"
		e.StartedAt = base.Add(time.Duration(minutes) * time.Minute)
		_, err := s.AppendEvent(ctx, e)
		assert.Require(t, assert.NoErr(t, err))
	}
	add(0, modelCall(prefixReasonFirst, 1000, 0, 0))
	add(1, Event{Kind: EventModel, Name: "p", Status: "ok", InputTokens: 1000, CacheReadTokens: 800,
		Attrs: map[string]any{"model": "m", "prefix": "append", "prefix_resume": "same",
			"prefix_reused": 800, "prefix_rebuilt": 0, "prefix_fresh": 200}})
	add(2, Event{Kind: EventSession, Name: modelSwitchEvent, Status: "ok"})
	add(3, Event{Kind: EventModel, Name: "p", Status: "ok", InputTokens: 1000, CacheReadTokens: 0,
		Attrs: map[string]any{"model": "m2", "prefix": "system", "prefix_resume": "changed",
			"prefix_resume_reason": "system"}})

	got, err := s.BuildCacheBuckets(ctx, EventFilter{SessionID: "a"}, 5)
	assert.Require(t, assert.NoErr(t, err))
	byName := map[string]CacheBucketStat{}
	for _, b := range got.Buckets {
		byName[b.Bucket] = b
	}
	assert.Require(t, assert.Eq(t, 3, len(got.Buckets)))
	assert.Eq(t, BucketResumeSame, got.Buckets[1].Bucket)
	assert.Eq(t, int64(800), got.Buckets[1].PrefixTokens)
	assert.Eq(t, 1.0, got.Buckets[1].PrefixReuse)
	// The third call changed model and followed a model_switch: the switch wins
	// over the resume flag, and the change cause is kept.
	assert.Eq(t, BucketProviderSwitch, byName[BucketProviderSwitch].Bucket)
	assert.Eq(t, 1, byName[BucketProviderSwitch].Reasons["system"])
}
