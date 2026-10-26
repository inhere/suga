package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// cacheBucket is one cache bucket of a run, read from
// `suga inspect trace <sid> --analyze --json` (field cache_buckets). The bucket
// says what happened to the request prefix since the previous model call; the
// steady bucket (an unchanged conversation inside one segment) is the one the
// 95% hit target is about. Input excludes cache, so prompt = input + cache read
// + cache write.
type cacheBucket struct {
	Bucket        string  `json:"bucket"`
	Calls         int     `json:"calls"`
	Input         int64   `json:"input_tokens"`
	CacheRead     int64   `json:"cache_read_tokens"`
	CacheWrite    int64   `json:"cache_write_tokens"`
	Hit           float64 `json:"cache_hit_rate"`
	CacheReported bool    `json:"cache_reported"`
}

// prompt is every prompt token the bucket sent.
func (b cacheBucket) prompt() int64 { return b.Input + b.CacheRead + b.CacheWrite }

// cacheLowHit is one steady call under the target hit rate (field
// steady_low_hit): its step, its uncached input and the events between it and
// the previous call.
type cacheLowHit struct {
	Step     int      `json:"step"`
	Uncached int64    `json:"uncached_input_tokens"`
	Prompt   int64    `json:"prompt_tokens"`
	Hit      float64  `json:"hit_rate"`
	Events   []string `json:"events,omitempty"`
}

// parseCacheBuckets reads the bucket part of an --analyze JSON object. Both
// results are nil when the object carries no buckets (an older binary, an agent
// whose trace the eval cannot read, or a session without model events).
func parseCacheBuckets(raw []byte) ([]cacheBucket, []cacheLowHit) {
	var rep struct {
		Buckets []cacheBucket `json:"cache_buckets"`
		Low     []cacheLowHit `json:"steady_low_hit"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &rep) != nil {
		return nil, nil
	}
	return rep.Buckets, rep.Low
}

// bucketCell renders one bucket's hit rate of a run: "-" when the bucket has no
// calls or its provider reported no cache. The rate comes from the tokens, so a
// caller need not have filled the bucket's own rate in.
func bucketCell(buckets []cacheBucket, name string) string {
	for _, b := range buckets {
		if b.Bucket != name {
			continue
		}
		if !b.CacheReported || b.prompt() == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1f", hitOf(b.CacheRead, b.prompt())*100)
	}
	return "-"
}

// poolBuckets sums the buckets of rs (all runs and rounds) by name and returns
// them in the canonical bucket order plus an "overall" row over every call.
func poolBuckets(rs []Result) []cacheBucket {
	sums := map[string]*cacheBucket{}
	for _, r := range rs {
		for _, b := range r.CacheBuckets {
			st := sums[b.Bucket]
			if st == nil {
				st = &cacheBucket{Bucket: b.Bucket}
				sums[b.Bucket] = st
			}
			st.Calls += b.Calls
			st.Input += b.Input
			st.CacheRead += b.CacheRead
			st.CacheWrite += b.CacheWrite
			st.CacheReported = st.CacheReported || b.CacheReported
		}
	}
	if len(sums) == 0 {
		return nil
	}
	out := make([]cacheBucket, 0, len(sums)+1)
	var overall cacheBucket
	for _, name := range cacheBucketNames() {
		st := sums[name]
		if st == nil {
			continue
		}
		st.Hit = hitOf(st.CacheRead, st.Input+st.CacheRead+st.CacheWrite)
		out = append(out, *st)
		overall.Calls += st.Calls
		overall.Input += st.Input
		overall.CacheRead += st.CacheRead
		overall.CacheWrite += st.CacheWrite
		overall.CacheReported = overall.CacheReported || st.CacheReported
	}
	overall.Bucket = "overall"
	overall.Hit = hitOf(overall.CacheRead, overall.Input+overall.CacheRead+overall.CacheWrite)
	return append(out, overall)
}

// cacheBucketNames is the report order of the buckets, as suga names them
// (store.Bucket*): the session's first call, the prefix breaks, the steady
// calls and the buckets that are not a step of the loop.
func cacheBucketNames() []string {
	return []string{"cold", "segment_start", "after_fold", "resume", "steady", "handoff", "unknown"}
}

// hitOf is cache read over prompt tokens, 0 when the bucket sent nothing.
func hitOf(read, prompt int64) float64 {
	if prompt <= 0 {
		return 0
	}
	return float64(read) / float64(prompt)
}

// printCacheBuckets writes the cache hit rate per bucket, pooled over every run
// and round, with the overall row of the same calls beside it: the steady
// bucket is the one the 95% target is about, so the two are comparable here.
// Nothing is printed when no run carries bucket data (another agent, an older
// suga binary).
func printCacheBuckets(w io.Writer, rs []Result) {
	buckets := poolBuckets(rs)
	if len(buckets) == 0 {
		return
	}
	fmt.Fprintln(w, "\ncache by bucket (pooled over all runs; steady = unchanged context inside one segment, the 95% target; overall = every call):")
	fmt.Fprintln(w, "  BUCKET         CALLS   PROMPT     CACHE-R    HIT%")
	for _, b := range buckets {
		hit := "-"
		if b.CacheReported && b.prompt() > 0 {
			hit = fmt.Sprintf("%.1f", b.Hit*100)
		}
		fmt.Fprintf(w, "  %-14s %5d   %-9d  %-9d  %s\n", b.Bucket, b.Calls, b.prompt(), b.CacheRead, hit)
	}
	printSteadyLowHits(w, rs)
}

// printSteadyLowHits lists the steady calls under the target hit rate of every
// run, per step, with what happened between that call and the previous one: the
// starting point of a lookup into a low steady hit rate.
func printSteadyLowHits(w io.Writer, rs []Result) {
	rows := 0
	var b strings.Builder
	for _, r := range rs {
		for _, h := range r.SteadyLowHit {
			events := "-"
			if len(h.Events) > 0 {
				events = strings.Join(h.Events, "+")
			}
			if rows == 0 {
				fmt.Fprintf(&b, "\nsteady calls under the target hit rate:\n  %-28s %6s %10s %10s %7s  %s\n",
					"RUN", "STEP", "UNCACHED", "PROMPT", "HIT%", "EVENTS BEFORE")
			}
			fmt.Fprintf(&b, "  %-28s %6d %10d %10d %7.1f  %s\n", runLabel(r, false), h.Step, h.Uncached, h.Prompt, h.Hit*100, events)
			rows++
		}
	}
	if rows > 0 {
		_, _ = io.WriteString(w, b.String())
	}
}
