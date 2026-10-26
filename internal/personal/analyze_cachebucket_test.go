package personal

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/store"
)

// cacheModel is a main-loop model event with a prefix reason and its tokens
// (input includes cache, the store's raw convention).
func cacheModel(prefix string, input, cacheRead int64) store.Event {
	return store.Event{Kind: store.EventModel, Status: eventOK, InputTokens: input, CacheReadTokens: cacheRead,
		Attrs: map[string]any{"prefix": prefix}}
}

// TestAnalyzeTraceCacheBuckets covers the bucket part of `trace --analyze`: the
// class of every model call, the hit rate per bucket, the steady low-hit list
// with the events that preceded it, and the JSON the eval reads.
func TestAnalyzeTraceCacheBuckets(t *testing.T) {
	t.Run("one bucket per call", func(t *testing.T) {
		a := AnalyzeTrace([]store.Event{
			cacheModel(prefixFirst, 1000, 0),
			cacheModel(prefixAppend, 1200, 1000),
			{Kind: store.EventFold, Name: "fold", Status: eventOK},
			cacheModel(prefixFold, 1500, 1000),
			{Kind: store.EventModel, Status: eventOK, InputTokens: 9000, Attrs: map[string]any{"purpose": "compact"}},
			{Kind: store.EventCompaction, Name: "compact", Status: eventOK},
			cacheModel(prefixSegment, 2000, 100),
			cacheModel(prefixFirst, 2000, 1200), // a new process: unclassifiable
		})
		got := make(map[string]int, len(a.CacheBuckets))
		for _, b := range a.CacheBuckets {
			got[b.Bucket] = b.Calls
		}
		assert.Eq(t, 1, got[store.BucketCold])
		assert.Eq(t, 1, got[store.BucketSteady])
		assert.Eq(t, 1, got[store.BucketAfterFold])
		assert.Eq(t, 1, got[store.BucketHandoff])
		assert.Eq(t, 1, got[store.BucketSegmentStart])
		assert.Eq(t, 1, got[store.BucketUnknown])
		// Report order: cold, then the prefix breaks, then steady.
		assert.Eq(t, store.BucketCold, a.CacheBuckets[0].Bucket)
		assert.Eq(t, store.BucketSteady, a.CacheBuckets[3].Bucket)

		var out bytes.Buffer
		assert.NoErr(t, WriteTraceAnalysis(&out, a, false))
		assert.StrContains(t, out.String(), "cache by bucket")
		assert.StrContains(t, out.String(), "steady 1 calls hit 83.3%")
		// The one steady call (1000 of 1200) is under the target.
		assert.StrContains(t, out.String(), "1 steady calls under 90%: step 2 uncached 200 of 1.2k (83.3%, no fold/segment/tool event in between)")
	})

	t.Run("a provider without cache shows no rate", func(t *testing.T) {
		a := AnalyzeTrace([]store.Event{
			cacheModel(prefixFirst, 1000, 0),
			cacheModel(prefixAppend, 1200, 0),
		})
		assert.Eq(t, 0, len(a.SteadyLowHit)) // no cache data, so no miss to judge
		var out bytes.Buffer
		assert.NoErr(t, WriteTraceAnalysis(&out, a, false))
		assert.StrContains(t, out.String(), "steady 1 calls hit - ")
		assert.StrNotContains(t, out.String(), "steady low hit")
	})

	t.Run("nothing to classify stays silent", func(t *testing.T) {
		a := AnalyzeTrace([]store.Event{{Kind: store.EventTool, Name: "file_read", Status: eventOK}})
		assert.Eq(t, 0, len(a.CacheBuckets))
		var out bytes.Buffer
		assert.NoErr(t, WriteTraceAnalysis(&out, a, false))
		assert.StrNotContains(t, out.String(), "cache by bucket")
	})

	t.Run("json field names are stable", func(t *testing.T) {
		raw, err := json.Marshal(AnalyzeTrace([]store.Event{
			cacheModel(prefixFirst, 1000, 0),
			cacheModel(prefixAppend, 2000, 1000),
		}))
		assert.NoErr(t, err)
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
		assert.Eq(t, store.BucketSteady, decoded.Buckets[1].Bucket)
		assert.Eq(t, int64(1000), decoded.Buckets[1].InputTokens)
		assert.Eq(t, int64(1000), decoded.Buckets[1].CacheRead)
		assert.Eq(t, 0.5, decoded.Buckets[1].CacheHitRate)
		assert.Require(t, assert.Eq(t, 1, len(decoded.Low)))
		assert.Eq(t, 2, decoded.Low[0].Step)
		assert.Eq(t, int64(1000), decoded.Low[0].Uncached)
		assert.Eq(t, int64(2000), decoded.Low[0].Prompt)
		assert.Eq(t, 0.5, decoded.Low[0].HitRate)
	})
}
