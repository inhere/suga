package store

import (
	"context"
	"fmt"
)

// Cache buckets: one class per model call, decided by what happened to the
// request prefix since the session's previous main-loop call. A provider prompt
// cache serves the prefix it already saw, so a call that only extends the
// conversation reuses most of the prompt (steady) while a prefix break has to
// be sent again (the handoff package of a new segment, old tool results folded,
// a loaded deferred tool, a changed system prompt). The steady bucket is the
// one the 95% hit target is about; see docs/research for the definition and the
// measured numbers.
const (
	// BucketCold is the session's first call: the provider has nothing cached
	// for it.
	BucketCold = "cold"
	// BucketSegmentStart is the first call after a segment break: compaction
	// wrote a handoff package and the request starts from the new segment.
	BucketSegmentStart = "segment_start"
	// BucketAfterFold is the first call after old tool results were folded or
	// the prefix changed some other way (a truncation, a loaded deferred tool, a
	// changed system prompt, a rewritten message).
	BucketAfterFold = "after_fold"
	// BucketSteady is a call whose request only extends the previous one inside
	// a running segment: the bucket the 95% target is about.
	BucketSteady = "steady"
	// BucketHandoff is a side call that writes a handoff package
	// (attrs.purpose=compact): it sends its own prompt, not the conversation.
	BucketHandoff = "handoff"
	// BucketUnknown is a call the trace cannot classify: a new process starts
	// with prefix=first (what the old process left in the provider cache is
	// unknown) and an older session carries neither prefix attrs nor the
	// segment/fold events.
	BucketUnknown = "unknown"
)

// cacheBucketOrder is the order the buckets are reported in: the first call,
// then the prefix breaks by cause, then the steady calls and the specials.
var cacheBucketOrder = []string{BucketCold, BucketSegmentStart, BucketAfterFold, BucketSteady, BucketHandoff, BucketUnknown}

// SteadyLowHitBelow is the per-call hit rate under which a steady call is
// listed as a low hit (the target inside one context is higher, see BucketSteady).
const SteadyLowHitBelow = 0.90

// The request-prefix reasons recorded on model events (attrs.prefix, written by
// personal.prefixTracker). They are repeated here because the store cannot
// import personal; the names must stay in sync with personal/prefix.go.
const (
	prefixReasonFirst   = "first"   // no earlier request in that process
	prefixReasonAppend  = "append"  // the previous request is a prefix of this one
	prefixReasonFold    = "fold"    // an earlier message got shorter (history folding)
	prefixReasonSegment = "segment" // a new segment replaced the history
	prefixReasonSystem  = "system"  // the system prompt changed
	prefixReasonTools   = "tools"   // the tool set changed
	prefixReasonRewrite = "rewrite" // any other change to an earlier message
)

// CacheCall is one classified model call.
type CacheCall struct {
	Bucket string `json:"bucket"`
	// Step is the 1-based ordinal of the main-loop model calls (the same step
	// number the trace analysis uses); a handoff side call keeps the step of the
	// call before it, since it is not a step of the loop.
	Step int `json:"step"`
	// Input includes the cache read/write of the call (the store's raw
	// convention); the bucket statistics report it without them.
	Input      int64 `json:"input_tokens"`
	CacheRead  int64 `json:"cache_read_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
	// After names the prefix-changing events (fold, segment, truncate, tools)
	// recorded between the previous main-loop call and this one.
	After []string `json:"after,omitempty"`
}

// CacheBucketStat aggregates the calls of one cache bucket. InputTokens follows
// the report convention (cache excluded), so prompt = input + cache read +
// cache write; CacheHitRate = cache read / prompt.
type CacheBucketStat struct {
	Bucket       string  `json:"bucket"`
	Calls        int     `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	CacheWrite   int64   `json:"cache_write_tokens"`
	CacheHitRate float64 `json:"cache_hit_rate"`
	// CacheReported is false when no call of the bucket reported cache
	// read/write: a provider without cache then shows "-" instead of 0%.
	CacheReported bool `json:"cache_reported"`
}

// CacheLowHit is one steady call whose hit rate stayed below SteadyLowHitBelow:
// where the next investigation of the hit rate starts.
type CacheLowHit struct {
	Step     int      `json:"step"`
	Uncached int64    `json:"uncached_input_tokens"`
	Prompt   int64    `json:"prompt_tokens"`
	HitRate  float64  `json:"hit_rate"`
	Events   []string `json:"events,omitempty"` // events between the previous call and this one
}

// CacheBuckets is the cache classification of one session or of several runs.
type CacheBuckets struct {
	Buckets   []CacheBucketStat `json:"cache_buckets"`
	LowSteady []CacheLowHit     `json:"steady_low_hit,omitempty"`
}

// bucketRow is what the classification reads of one event: the token counts of
// a model call and the attrs that say why the request prefix changed.
type bucketRow struct {
	Kind, Name, Status           string
	Input, CacheRead, CacheWrite int64
	Purpose, Prefix              string
	CacheInvalidated             bool
}

// bucketKinds are the event kinds the cache classification reads: the model
// calls themselves and the events that break the request prefix.
var bucketKinds = []string{EventModel, EventFold, EventCompaction, EventToolSearch}

// ClassifyEvents classifies the model calls of one session's events (oldest
// first, as ListEvents returns them).
func ClassifyEvents(events []Event) []CacheCall {
	rows := make([]bucketRow, 0, len(events))
	for _, e := range events {
		rows = append(rows, eventBucketRow(e))
	}
	return classifyRows(rows)
}

// eventBucketRow narrows one stored event to the fields the classification
// reads.
func eventBucketRow(e Event) bucketRow {
	r := bucketRow{Kind: e.Kind, Name: e.Name, Status: e.Status,
		Input: e.InputTokens, CacheRead: e.CacheReadTokens, CacheWrite: e.CacheWriteTokens}
	switch e.Kind {
	case EventModel:
		r.Purpose, _ = e.Attrs["purpose"].(string)
		r.Prefix, _ = e.Attrs["prefix"].(string)
	case EventToolSearch:
		r.CacheInvalidated, _ = e.Attrs["cache_invalidated"].(bool)
	}
	return r
}

// classifyRows assigns one bucket to every model call of ONE session's events
// (oldest first). The prefix reason the recorder wrote on the call decides; the
// events since the previous call are the fallback for sessions recorded before
// that attr existed. Nothing is dropped: a call the trace cannot classify lands
// in BucketUnknown.
func classifyRows(rows []bucketRow) []CacheCall {
	out := make([]CacheCall, 0, len(rows)/4+1)
	step, seenMain := 0, false
	// pending are the prefix-changing events since the previous main-loop call.
	var pending []string
	for _, r := range rows {
		switch r.Kind {
		case EventModel:
			if r.Purpose != "" {
				// A side call (purpose=compact writes the handoff package) sends a
				// prompt of its own and does not interrupt the conversation, so it
				// takes no step and clears nothing.
				out = append(out, CacheCall{Bucket: BucketHandoff, Step: step,
					Input: r.Input, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite})
				continue
			}
			step++
			out = append(out, CacheCall{Bucket: r.bucketOf(!seenMain, pending), Step: step,
				Input: r.Input, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, After: pending})
			seenMain, pending = true, nil
		case EventFold:
			pending = append(pending, "fold")
		case EventCompaction:
			switch {
			case r.Name == "compact" && r.Status == "ok":
				pending = append(pending, "segment")
			case r.Name == "truncate":
				// The truncation ladder cut old tool results, not a new segment.
				pending = append(pending, "truncate")
			}
		case EventToolSearch:
			// Only a search that loaded a deferred tool changes the tool table
			// (and so the request prefix).
			if r.CacheInvalidated {
				pending = append(pending, "tools")
			}
		}
	}
	return out
}

// bucketOf decides the bucket of one main-loop model call. isFirst marks the
// session's first call; pending lists the prefix-changing events since the
// previous one.
func (r bucketRow) bucketOf(isFirst bool, pending []string) string {
	if isFirst {
		return BucketCold
	}
	switch r.Prefix {
	case prefixReasonAppend:
		return BucketSteady
	case prefixReasonSegment:
		return BucketSegmentStart
	case prefixReasonFold, prefixReasonSystem, prefixReasonTools, prefixReasonRewrite:
		return BucketAfterFold
	case prefixReasonFirst:
		// A new process resumed the session: the provider cache may still hold
		// the prefix, the trace cannot tell. A segment break in between is known.
		if hasPending(pending, "segment") {
			return BucketSegmentStart
		}
		return BucketUnknown
	}
	// No prefix attr (a session recorded before it existed): the events alone
	// decide; lacking any, the request is assumed to extend the previous one.
	switch {
	case hasPending(pending, "segment"):
		return BucketSegmentStart
	case len(pending) > 0:
		return BucketAfterFold
	}
	return BucketSteady
}

// hasPending reports whether the prefix-changing events include name.
func hasPending(pending []string, name string) bool {
	for _, p := range pending {
		if p == name {
			return true
		}
	}
	return false
}

// AggregateCacheBuckets pools classified calls into the per-bucket numbers plus
// the first lowHitLimit steady calls below SteadyLowHitBelow (0 lists none).
func AggregateCacheBuckets(calls []CacheCall, lowHitLimit int) CacheBuckets {
	acc := make(map[string]*CacheBucketStat, len(cacheBucketOrder))
	for _, c := range calls {
		st := acc[c.Bucket]
		if st == nil {
			st = &CacheBucketStat{Bucket: c.Bucket}
			acc[c.Bucket] = st
		}
		st.Calls++
		// Stored input includes cache; report it without (floored at 0).
		st.InputTokens += max(c.Input-c.CacheRead-c.CacheWrite, 0)
		st.CacheRead += c.CacheRead
		st.CacheWrite += c.CacheWrite
		if c.CacheRead+c.CacheWrite > 0 {
			st.CacheReported = true
		}
	}
	out := CacheBuckets{Buckets: make([]CacheBucketStat, 0, len(acc))}
	for _, name := range cacheBucketOrder {
		st := acc[name]
		if st == nil {
			continue // a bucket this session never reached
		}
		st.CacheHitRate = ratio(st.CacheRead, st.InputTokens+st.CacheRead+st.CacheWrite)
		out.Buckets = append(out.Buckets, *st)
	}
	if lowHitLimit > 0 {
		out.LowSteady = lowSteadyCalls(calls, lowHitLimit)
	}
	return out
}

// lowSteadyCalls lists the steady calls under SteadyLowHitBelow in call order.
// A call without cache read/write is skipped: its provider reports no cache, so
// it is no miss.
func lowSteadyCalls(calls []CacheCall, limit int) []CacheLowHit {
	var out []CacheLowHit
	for _, c := range calls {
		if c.Bucket != BucketSteady || c.Input <= 0 || c.CacheRead+c.CacheWrite == 0 {
			continue
		}
		if hit := ratio(c.CacheRead, c.Input); hit >= SteadyLowHitBelow {
			continue
		}
		out = append(out, CacheLowHit{Step: c.Step, Uncached: max(c.Input-c.CacheRead-c.CacheWrite, 0),
			Prompt: c.Input, HitRate: ratio(c.CacheRead, c.Input), Events: c.After})
		if len(out) == limit {
			break
		}
	}
	return out
}

// BuildCacheBuckets classifies the model calls matching f and aggregates them
// per cache bucket. The buckets are per session: the cold bucket is a session's
// own first call, so the query is grouped by session id and the rows of one
// session reach the classifier in time order. lowHitLimit caps the listed
// steady low-hit calls (0 lists none).
func (s *Store) BuildCacheBuckets(ctx context.Context, f EventFilter, lowHitLimit int) (CacheBuckets, error) {
	where, args := bucketFilter(f)
	q := `SELECT session_id, kind, name, status, input_tokens, cache_read_tokens, cache_write_tokens,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.purpose') = 'text'
			THEN json_extract(attrs_json, '$.purpose') ELSE '' END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.prefix') = 'text'
			THEN json_extract(attrs_json, '$.prefix') ELSE '' END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.cache_invalidated') = 'true' THEN 1 ELSE 0 END
		FROM events WHERE ` + where + ` ORDER BY session_id ASC, started_at ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return CacheBuckets{}, fmt.Errorf("cache buckets: %w", err)
	}
	defer rows.Close()
	var (
		all     []CacheCall
		batch   []bucketRow
		session string
		flush   = func() {
			if len(batch) > 0 {
				all = append(all, classifyRows(batch)...)
				batch = nil
			}
		}
	)
	for rows.Next() {
		var (
			sid         string
			r           bucketRow
			invalidated int
		)
		if err := rows.Scan(&sid, &r.Kind, &r.Name, &r.Status, &r.Input, &r.CacheRead, &r.CacheWrite,
			&r.Purpose, &r.Prefix, &invalidated); err != nil {
			return CacheBuckets{}, fmt.Errorf("cache buckets: %w", err)
		}
		r.CacheInvalidated = invalidated != 0
		if sid != session {
			flush()
			session = sid
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return CacheBuckets{}, fmt.Errorf("cache buckets: %w", err)
	}
	flush()
	return AggregateCacheBuckets(all, lowHitLimit), nil
}

// bucketFilter builds the WHERE clause of the bucket query: the report filter
// with the bucket kinds.
func bucketFilter(f EventFilter) (string, []any) { return kindFilter(bucketKinds, f) }
