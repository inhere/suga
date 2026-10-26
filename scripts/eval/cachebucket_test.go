package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// analyzeJSON is a trimmed `suga inspect trace --analyze --json` object with the
// bucket fields the eval reads.
const analyzeJSON = `{"model_calls":3,"cache_buckets":[
 {"bucket":"cold","calls":1,"input_tokens":1000,"cache_read_tokens":0,"cache_write_tokens":0,"cache_hit_rate":0,"cache_reported":false},
 {"bucket":"steady","calls":2,"input_tokens":200,"cache_read_tokens":1800,"cache_write_tokens":0,"cache_hit_rate":0.9,"cache_reported":true}],
 "steady_low_hit":[{"step":2,"uncached_input_tokens":400,"prompt_tokens":2000,"hit_rate":0.8}]}`

func TestParseCacheBuckets(t *testing.T) {
	t.Run("buckets and low hits", func(t *testing.T) {
		buckets, low := parseCacheBuckets([]byte(analyzeJSON))
		assert.Require(t, assert.Eq(t, 2, len(buckets)))
		assert.Eq(t, "steady", buckets[1].Bucket)
		assert.Eq(t, int64(1800), buckets[1].CacheRead)
		assert.Eq(t, 0.9, buckets[1].Hit)
		assert.Require(t, assert.Eq(t, 1, len(low)))
		assert.Eq(t, 2, low[0].Step)
		assert.Eq(t, int64(400), low[0].Uncached)
	})
	t.Run("no buckets, no error", func(t *testing.T) {
		for _, raw := range []string{`[]`, `{"model_calls":1}`, `not json`} {
			buckets, low := parseCacheBuckets([]byte(raw))
			assert.Eq(t, 0, len(buckets))
			assert.Eq(t, 0, len(low))
		}
	})
}

// TestPrintCacheBuckets covers the pooled summary and the low-hit list: the
// steady bucket sits beside the overall row so the two are comparable.
func TestPrintCacheBuckets(t *testing.T) {
	t.Run("steady and overall side by side", func(t *testing.T) {
		buckets, low := parseCacheBuckets([]byte(analyzeJSON))
		rs := []Result{{Task: "a", CacheBuckets: buckets, SteadyLowHit: low}}
		var buf bytes.Buffer
		printCacheBuckets(&buf, rs)
		out := buf.String()
		assert.StrContains(t, out, "cache by bucket")
		assert.StrContains(t, out, "steady")
		assert.StrContains(t, out, "overall")
		// cold reported no cache: its rate is "-", not 0.
		assert.StrContains(t, out, "cold               1   1000       0          -")
		assert.StrContains(t, out, "steady calls under the target hit rate")
		assert.StrContains(t, out, "  a                                 2        400       2000    80.0  -")
	})
	t.Run("nothing pooled prints nothing", func(t *testing.T) {
		var buf bytes.Buffer
		printCacheBuckets(&buf, []Result{{Task: "omp-run"}})
		assert.Eq(t, "", buf.String())
	})
}

// TestPoolBuckets sums the runs of one agent: the hit rate of a bucket is the
// hit rate of the pooled tokens, not the mean of the runs' rates.
func TestPoolBuckets(t *testing.T) {
	rs := []Result{
		{Task: "a", CacheBuckets: []cacheBucket{{Bucket: "steady", Calls: 1, Input: 100, CacheRead: 900, CacheReported: true}}},
		{Task: "a", CacheBuckets: []cacheBucket{{Bucket: "steady", Calls: 1, Input: 1000, CacheRead: 0, CacheReported: true}}},
	}
	got := poolBuckets(rs)
	assert.Require(t, assert.Eq(t, 2, len(got))) // steady + overall
	assert.Eq(t, "steady", got[0].Bucket)
	assert.Eq(t, 2, got[0].Calls)
	assert.Eq(t, 900.0/2000.0, got[0].Hit)
	assert.Eq(t, "overall", got[1].Bucket)
	assert.Eq(t, 900.0/2000.0, got[1].Hit)
	assert.Eq(t, "90.0", bucketCell(rs[0].CacheBuckets, "steady"))
	assert.Eq(t, "-", bucketCell(rs[0].CacheBuckets, "after_fold"))
}

// TestWriteCompareBuckets: compare.md carries the steady bucket of each variant
// next to its overall rate.
func TestWriteCompareBuckets(t *testing.T) {
	rs := []Result{
		{Task: "a", Variant: "v1", CacheBuckets: []cacheBucket{{Bucket: "steady", Calls: 2, Input: 100, CacheRead: 900, CacheReported: true}}},
		{Task: "a", Variant: "v2", CacheBuckets: []cacheBucket{{Bucket: "steady", Calls: 2, Input: 500, CacheRead: 500, CacheReported: true}}},
	}
	var b strings.Builder
	writeCompareBuckets(&b, []*Variant{{Name: "v1"}, {Name: "v2"}}, rs)
	out := b.String()
	assert.StrContains(t, out, "按桶命中率")
	assert.StrContains(t, out, "| steady | 90.0 | 50.0 |")
	assert.StrContains(t, out, "| overall | 90.0 | 50.0 |")

	// The variant overview column uses the same pooled steady rate.
	assert.Eq(t, "90.0", steadyCell(variantResults(rs, "v1")))
	assert.Eq(t, "-", steadyCell(nil))
}
