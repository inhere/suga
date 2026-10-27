package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
	// PrefixTokens is the estimated cacheable prefix of the calls' previous
	// requests (tokens) and Reuse = cache read / that: of what the provider could
	// have served, how much it did. It does not depend on how much a call added,
	// so the steady 95% target is read with it (reuse it is, see Reuse).
	PrefixTokens int64   `json:"prefix_reused_tokens"`
	Reuse        float64 `json:"prefix_reuse_rate"`
	// HandoffReuse counts the handoff-package calls of segment breaks by how they
	// were made (prefix, fallback:<reason>): whether writing the package
	// continued the segment's main-loop prefix (design 2026-10-27).
	HandoffReuse map[string]int `json:"handoff_reuse,omitempty"`
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
func parseCacheBuckets(raw []byte) ([]cacheBucket, []cacheLowHit, int) {
	var rep struct {
		Buckets        []cacheBucket `json:"cache_buckets"`
		Low            []cacheLowHit `json:"steady_low_hit"`
		FailedAttempts int           `json:"failed_attempts"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &rep) != nil {
		return nil, nil, 0
	}
	return rep.Buckets, rep.Low, rep.FailedAttempts
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
			st.PrefixTokens += b.PrefixTokens
			st.CacheReported = st.CacheReported || b.CacheReported
			for why, n := range b.HandoffReuse {
				if st.HandoffReuse == nil {
					st.HandoffReuse = map[string]int{}
				}
				st.HandoffReuse[why] += n
			}
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
		st.Reuse = reuseOf(st.CacheRead, st.PrefixTokens)
		out = append(out, *st)
		overall.Calls += st.Calls
		overall.Input += st.Input
		overall.CacheRead += st.CacheRead
		overall.CacheWrite += st.CacheWrite
		overall.PrefixTokens += st.PrefixTokens
		overall.CacheReported = overall.CacheReported || st.CacheReported
	}
	overall.Bucket = "overall"
	overall.Hit = hitOf(overall.CacheRead, overall.Input+overall.CacheRead+overall.CacheWrite)
	overall.Reuse = reuseOf(overall.CacheRead, overall.PrefixTokens)
	return append(out, overall)
}

// cacheBucketNames is the report order of the buckets, as suga names them
// (store.Bucket*): the session's first call, the prefix breaks, the resumed and
// steady calls and the buckets that are not a step of the loop.
func cacheBucketNames() []string {
	return []string{"cold", "segment_start", "after_fold", "resume", "resume_same", "resume_changed",
		"provider_switch", "steady", "handoff", "unknown"}
}

// hitOf is cache read over prompt tokens, 0 when the bucket sent nothing.
func hitOf(read, prompt int64) float64 {
	if prompt <= 0 {
		return 0
	}
	return float64(read) / float64(prompt)
}

// reuseOf is cache read over the estimated cacheable prefix of the previous
// requests, capped at 100% (the estimate is derived from JSON sizes, so a
// provider serving the whole prompt is not read as more than all).
func reuseOf(read, prefix int64) float64 {
	if prefix <= 0 {
		return 0
	}
	return float64(min(read, prefix)) / float64(prefix)
}

// reuseCell renders one pooled bucket's prefix reuse rate of rs: "-" when no
// row of the bucket carried a prefix estimate (an older binary's trace).
func reuseCell(buckets []cacheBucket, name string) string {
	for _, b := range buckets {
		if b.Bucket != name || b.PrefixTokens == 0 {
			continue
		}
		return fmt.Sprintf("%.1f", b.Reuse*100)
	}
	return "-"
}

// printCacheBuckets writes the cache hit rate per bucket, pooled over every run
// and round, with the overall row of the same calls beside it: the steady
// bucket is the one the 95% target is about, so the two are comparable here.
// Nothing is printed when no run carries bucket data (another agent, an older
// suga binary), except a nonzero failed-attempt count, which is still useful.
func printCacheBuckets(w io.Writer, rs []Result) {
	buckets := poolBuckets(rs)
	var failedAttempts int
	for _, r := range rs {
		failedAttempts += r.FailedAttempts
	}
	if len(buckets) == 0 {
		if failedAttempts > 0 {
			fmt.Fprintf(w, "failed attempts: %d (omitted from cache buckets)\n", failedAttempts)
		}
		return
	}
	fmt.Fprintln(w, "\ncache by bucket (pooled over all runs; steady = unchanged context inside one segment, the 95% target; overall = every call):")
	fmt.Fprintln(w, "  BUCKET         CALLS   PROMPT     CACHE-R    HIT%   REUSE%")
	for _, b := range buckets {
		hit := "-"
		if b.CacheReported && b.prompt() > 0 {
			hit = fmt.Sprintf("%.1f", b.Hit*100)
		}
		reuse := "-"
		if b.PrefixTokens > 0 {
			reuse = fmt.Sprintf("%.1f", b.Reuse*100)
		}
		fmt.Fprintf(w, "  %-14s %5d   %-9d  %-9d  %-5s  %s\n", b.Bucket, b.Calls, b.prompt(), b.CacheRead, hit, reuse)
	}
	if line := handoffReuseLine(buckets); line != "" {
		fmt.Fprintf(w, "handoff package calls: %s\n", line)
	}
	if failedAttempts > 0 {
		// Provider attempts with no usage of their own: they are in no bucket row
		// above, so their count is printed apart (a downgraded attempt then reads as
		// provider_switch however often the pool retried first).
		fmt.Fprintf(w, "  failed attempts: %d (omitted from cache buckets)\n", failedAttempts)
	}
	printSteadyLowHits(w, rs)
}

// handoffReuseLine renders how the handoff-package calls of segment breaks were
// made, pooled over every run: prefix (the call continued the segment's
// main-loop request) and the fallback reasons ("" without such data).
func handoffReuseLine(buckets []cacheBucket) string {
	for _, b := range buckets {
		if b.Bucket != "handoff" || len(b.HandoffReuse) == 0 {
			continue
		}
		names := make([]string, 0, len(b.HandoffReuse))
		for why := range b.HandoffReuse {
			names = append(names, why)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, why := range names {
			parts = append(parts, fmt.Sprintf("%s %d", why, b.HandoffReuse[why]))
		}
		return strings.Join(parts, ", ")
	}
	return ""
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
