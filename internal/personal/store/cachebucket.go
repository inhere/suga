package store

import (
	"context"
	"fmt"
	"strings"
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
	// BucketResume is the first call of a new process that resumed the session
	// inside the same segment (prefix=first, no segment break in between) without
	// a persisted request fingerprint (a session recorded before it existed):
	// `suga run --resume` from gofer and the per-turn processes of the eval land
	// here when nothing was persisted to compare with.
	BucketResume = "resume"
	// BucketResumeSame is that first call when the persisted fingerprint of the
	// previous process is a prefix of it: the request was only appended to, so a
	// provider cache that still holds the session serves it like a steady call.
	BucketResumeSame = "resume_same"
	// BucketResumeChanged is that first call when the persisted fingerprint
	// differs (system prompt, tool table or an earlier message): the prefix has
	// to be sent again however warm the cache is.
	BucketResumeChanged = "resume_changed"
	// BucketProviderSwitch is a call the previous main-loop call's cache cannot
	// serve because another model or provider answered it: the model attr changed,
	// the pool fell back to the next provider, or a model_switch session event lies
	// in between. The request bytes only grow (fold/rewrite would be needed to
	// change them), so without this bucket such a call reads as steady.
	BucketProviderSwitch = "provider_switch"
	// BucketUnknown is a call the trace cannot classify; today only kept for a
	// prefix reason this code does not know.
	BucketUnknown = "unknown"
)

// cacheBucketOrder is the order the buckets are reported in: the first call,
// then the prefix breaks by cause, then the resumed and steady calls and the
// specials.
var cacheBucketOrder = []string{BucketCold, BucketSegmentStart, BucketAfterFold, BucketResume, BucketResumeSame,
	BucketResumeChanged, BucketProviderSwitch, BucketSteady, BucketHandoff, BucketUnknown}

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

// The persisted-fingerprint outcome of a resumed session (attrs.prefix_resume,
// written by personal.prefixTracker): same = the previous process's request is a
// prefix of this one, changed = an earlier part differs (attrs.prefix_resume_reason
// then names it). They must stay in sync with personal/prefix.go.
const (
	prefixResumeSame    = "same"
	prefixResumeChanged = "changed"
)

// modelSwitchEvent is the session event recording a :model switch; a model call
// after it is a provider_switch however much its bytes only append.
const modelSwitchEvent = "model_switch"

// CacheCall is one classified model call.
type CacheCall struct {
	Bucket string `json:"bucket"`
	// FailedAttempt marks a provider attempt with no usable token usage. It is
	// retained for the aggregate's failed_attempts counter, never a bucket row.
	FailedAttempt bool `json:"-"`
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
	// Reason names why the call is in its bucket when the bucket is about a prefix
	// change: the request-prefix reason (system / tools / fold / segment / rewrite),
	// from attrs.prefix_resume_reason for a changed resume.
	Reason string `json:"reason,omitempty"`
	// PrefixTokens is the estimated size of the cacheable prefix of the previous
	// request, in tokens: what a provider cache could have served of this call.
	PrefixTokens int64 `json:"prefix_reused_tokens,omitempty"`
	// HandoffReuse is how a handoff-package call of a segment break was made
	// (attrs.handoff_reuse): prefix when it continued the segment's main-loop
	// request, fallback:<reason> when the old path asked instead.
	HandoffReuse string `json:"handoff_reuse,omitempty"`
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
	// PrefixTokens is the summed estimated cacheable prefix of the calls' previous
	// requests (tokens) and PrefixReuseRate = cache read / that: of what the
	// provider could have served from its cache, how much it did serve. Unlike the
	// hit rate it does not depend on how much a call added, so the steady 95%
	// target is measured with it (docs/research/2026-10-26-cache-hit-buckets.md).
	// A provider serving more than the estimate is counted as 100%, not more.
	PrefixTokens int64   `json:"prefix_reused_tokens"`
	PrefixReuse  float64 `json:"prefix_reuse_rate"`
	// Reasons counts the calls of a prefix-change bucket by cause (system, tools,
	// fold, segment, rewrite): what to fix first.
	Reasons map[string]int `json:"reasons,omitempty"`
	// HandoffReuse counts the handoff-package calls of segment breaks by how they
	// were made (prefix, fallback:<reason>): whether the reuse of the segment
	// prefix held up (design 2026-10-27).
	HandoffReuse map[string]int `json:"handoff_reuse,omitempty"`
}

// HandoffReuseKey is the aggregation key of a handoff_reuse value: the value
// itself, or its reason when it carries a detail suffix (fallback:prefix_changed
// :msg:12 names where the rebuilt request differs, which the aggregate counts
// with the plain reason).
func HandoffReuseKey(value string) string {
	parts := strings.Split(value, ":")
	if len(parts) < 2 {
		return value
	}
	// prefix has no reason behind it; a fallback value keeps its reason and
	// drops the detail after it.
	if parts[0] == "prefix" {
		return value
	}
	return strings.Join(parts[:2], ":")
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
	Buckets        []CacheBucketStat `json:"cache_buckets"`
	LowSteady      []CacheLowHit     `json:"steady_low_hit,omitempty"`
	FailedAttempts int               `json:"failed_attempts,omitempty"`
}

// bucketRow is what the classification reads of one event: the token counts of
// a model call and the attrs that say why the request prefix changed.
type bucketRow struct {
	Kind, Name, Status                   string
	Input, Output, CacheRead, CacheWrite int64
	Purpose, Prefix                      string
	// PrefixResume / ResumeReason: the persisted-fingerprint outcome of a first
	// call of a resumed process (attrs prefix_resume / prefix_resume_reason).
	PrefixResume, ResumeReason string
	// Reused, Rebuilt, Fresh are the JSON sizes of the request parts (attrs
	// prefix_reused / prefix_rebuilt / prefix_fresh), Model the model that served
	// the call and Fallback whether the pool moved on after it (provider_switch).
	Reused, Rebuilt, Fresh int
	Model                  string
	Fallback               bool
	CacheInvalidated       bool
	// HandoffReuse is attrs.handoff_reuse of a handoff-package call (prefix or
	// fallback:<reason>): how the handoff package of a segment break was written.
	HandoffReuse string
}

// bucketKinds are the event kinds the cache classification reads: the model
// calls themselves, the events that break the request prefix and the session
// events that switch the model.
var bucketKinds = []string{EventModel, EventFold, EventCompaction, EventToolSearch, EventSession}

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
		Input: e.InputTokens, Output: e.OutputTokens, CacheRead: e.CacheReadTokens, CacheWrite: e.CacheWriteTokens}
	switch e.Kind {
	case EventModel:
		r.Purpose, _ = e.Attrs["purpose"].(string)
		r.Prefix, _ = e.Attrs["prefix"].(string)
		r.PrefixResume, _ = e.Attrs["prefix_resume"].(string)
		r.ResumeReason, _ = e.Attrs["prefix_resume_reason"].(string)
		r.Model, _ = e.Attrs["model"].(string)
		r.HandoffReuse, _ = e.Attrs["handoff_reuse"].(string)
		_, r.Fallback = e.Attrs["fallback"]
		r.Reused, r.Rebuilt, r.Fresh = attrInt(e.Attrs["prefix_reused"]), attrInt(e.Attrs["prefix_rebuilt"]), attrInt(e.Attrs["prefix_fresh"])
	case EventToolSearch:
		r.CacheInvalidated, _ = e.Attrs["cache_invalidated"].(bool)
	}
	return r
}

// attrInt reads an integer attr (JSON numbers come back as float64).
func attrInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// bucketState is what the classification carries from one call of a session to
// the next.
type bucketState struct {
	step     int      // 1-based ordinal of the main-loop calls
	seenMain bool     // a main-loop call happened
	pending  []string // prefix-changing events since the previous main-loop call
	// What served the previous main-loop call, to spot a provider switch (a
	// model_switch event in between counts as one too).
	lastProvider, lastModel string
	switched                bool
}

// classifyRows assigns one bucket to every model call of ONE session's events
// (oldest first). The prefix reason the recorder wrote on the call decides; the
// events since the previous call are the fallback for sessions recorded before
// that attr existed. Nothing is dropped: a call the trace cannot classify lands
// in BucketUnknown. Attempts that failed or supplied no usage are retained as
// FailedAttempt markers for the separate failed_attempts count.
func classifyRows(rows []bucketRow) []CacheCall {
	out := make([]CacheCall, 0, len(rows)/4+1)
	st := &bucketState{}
	for _, r := range rows {
		switch r.Kind {
		case EventModel:
			if !r.usageAvailable() || r.Status != "ok" {
				// A failed attempt, or one whose provider reported no usage, says
				// nothing about the cache: it stays out of every bucket. A fallback on
				// it still explains the next call (the pool moved on, so the previous
				// request's cache cannot serve it), and a main-loop attempt counts as
				// a step exactly as it does in the trace analysis (AnalyzeTrace), so
				// the step numbers of the two views stay comparable.
				if r.Fallback {
					st.switched = true
				}
				if r.Purpose == "" {
					st.step++
				}
				out = append(out, CacheCall{FailedAttempt: true})
				continue
			}
			if r.Purpose != "" {
				// A side call (purpose=compact writes the handoff package) sends a
				// prompt of its own and does not interrupt the conversation, so it
				// takes no step and clears nothing.
				out = append(out, CacheCall{Bucket: BucketHandoff, Step: st.step,
					Input: r.Input, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, HandoffReuse: r.HandoffReuse})
				continue
			}
			st.step++
			out = append(out, CacheCall{Bucket: r.bucketOf(st), Step: st.step,
				Input: r.Input, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, After: st.pending,
				Reason: r.bucketReason(), PrefixTokens: r.prefixTokens()})
			st.seenMain, st.pending, st.switched = true, nil, false
			st.lastProvider, st.lastModel = r.Name, r.Model
		case EventSession:
			if r.Name == modelSwitchEvent {
				st.switched = true
			}
		case EventFold:
			st.pending = append(st.pending, "fold")
		case EventCompaction:
			switch {
			case r.Name == "compact" && r.Status == "ok":
				st.pending = append(st.pending, "segment")
			case r.Name == "truncate":
				// The truncation ladder cut old tool results, not a new segment.
				st.pending = append(st.pending, "truncate")
			}
		case EventToolSearch:
			// Only a search that loaded a deferred tool changes the tool table
			// (and so the request prefix).
			if r.CacheInvalidated {
				st.pending = append(st.pending, "tools")
			}
		}
	}
	return out
}

// usageAvailable reports whether the provider supplied any token usage. A
// successful response with no usage is not useful for cache attribution.
func (r bucketRow) usageAvailable() bool {
	return r.Input > 0 || r.Output > 0 || r.CacheRead > 0 || r.CacheWrite > 0
}

// bucketOf decides the bucket of one main-loop model call; st carries what the
// previous calls of the session left.
func (r bucketRow) bucketOf(st *bucketState) string {
	if !st.seenMain {
		return BucketCold
	}
	// A segment break replaces the history: that is the structural cause of the
	// low hit rate of this call, whatever else changed beside it.
	if r.Prefix == prefixReasonSegment || hasPending(st.pending, "segment") {
		return BucketSegmentStart
	}
	if r.providerSwitched(st) {
		return BucketProviderSwitch
	}
	switch r.PrefixResume {
	case prefixResumeSame:
		return BucketResumeSame
	case prefixResumeChanged:
		return BucketResumeChanged
	}
	switch r.Prefix {
	case prefixReasonAppend:
		return BucketSteady
	case prefixReasonSegment:
		return BucketSegmentStart
	case prefixReasonFold, prefixReasonSystem, prefixReasonTools, prefixReasonRewrite:
		return BucketAfterFold
	case prefixReasonFirst:
		// A new process resumed the session without a persisted fingerprint: the
		// history is the same, the provider cache may still hold the prefix. A
		// segment break in between makes it a segment start instead (checked above).
		return BucketResume
	}
	if r.Prefix != "" {
		// A prefix reason this code does not know: count it rather than guess
		// steady, which would bias the bucket the 95% target is about.
		return BucketUnknown
	}
	// No prefix attr (a session recorded before it existed): the events alone
	// decide; lacking any, the request is assumed to extend the previous one.
	if len(st.pending) > 0 {
		return BucketAfterFold
	}
	return BucketSteady
}

// providerSwitched reports whether the previous main-loop call's cache cannot
// serve this one: another model or provider answered (attrs.model / the event's
// provider), the pool moved on after the previous attempt (attrs.fallback), or a
// model_switch session event lies in between.
func (r bucketRow) providerSwitched(st *bucketState) bool {
	if st.switched || r.Fallback {
		return true
	}
	if r.Model != "" && st.lastModel != "" && r.Model != st.lastModel {
		return true
	}
	return r.Name != "" && st.lastProvider != "" && r.Name != st.lastProvider
}

// bucketReason names why the call sits in its bucket: the change cause for the
// buckets that are about a prefix break (system / tools / fold / segment /
// rewrite, or the model for a provider switch).
func (r bucketRow) bucketReason() string {
	if r.PrefixResume == prefixResumeChanged && r.ResumeReason != "" {
		return r.ResumeReason
	}
	if r.Model != "" && r.Fallback {
		return r.Model
	}
	return ""
}

// prefixTokens estimates in tokens how much of the previous request the provider
// could have served from its cache: the reused part of the request (prefix
// attrs, JSON characters) scaled by the call's reported prompt tokens. The sizes
// are only a proxy for tokens, so the reuse rate caps its numerator at this
// estimate rather than trusting a higher cache read.
func (r bucketRow) prefixTokens() int64 {
	chars := r.Reused + r.Rebuilt + r.Fresh
	if chars <= 0 || r.Input <= 0 {
		return 0
	}
	// Input includes the cache read/write (the store's raw convention).
	return r.Input * int64(r.Reused) / int64(chars)
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
	failedAttempts := 0
	for _, c := range calls {
		if c.FailedAttempt {
			failedAttempts++
			continue
		}
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
		st.PrefixTokens += c.PrefixTokens
		if c.Reason != "" {
			if st.Reasons == nil {
				st.Reasons = map[string]int{}
			}
			st.Reasons[c.Reason]++
		}
		if c.HandoffReuse != "" {
			if st.HandoffReuse == nil {
				st.HandoffReuse = map[string]int{}
			}
			st.HandoffReuse[HandoffReuseKey(c.HandoffReuse)]++
		}
		if c.CacheRead+c.CacheWrite > 0 {
			st.CacheReported = true
		}
	}
	out := CacheBuckets{Buckets: make([]CacheBucketStat, 0, len(acc)), FailedAttempts: failedAttempts}
	for _, name := range cacheBucketOrder {
		st := acc[name]
		if st == nil {
			continue // a bucket this session never reached
		}
		st.CacheHitRate = ratio(st.CacheRead, st.InputTokens+st.CacheRead+st.CacheWrite)
		// Prefix reuse: of the prefix the provider could have served, how much it
		// did. Capped at 100%: the estimate is in tokens derived from JSON sizes,
		// so a provider serving the whole prompt is not read as more than all.
		st.PrefixReuse = ratio(min(st.CacheRead, st.PrefixTokens), st.PrefixTokens)
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
	q := `SELECT session_id, kind, name, status, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.purpose') = 'text'
			THEN json_extract(attrs_json, '$.purpose') ELSE '' END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.prefix') = 'text'
			THEN json_extract(attrs_json, '$.prefix') ELSE '' END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.cache_invalidated') = 'true' THEN 1 ELSE 0 END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.prefix_resume') = 'text'
			THEN json_extract(attrs_json, '$.prefix_resume') ELSE '' END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.prefix_resume_reason') = 'text'
			THEN json_extract(attrs_json, '$.prefix_resume_reason') ELSE '' END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.model') = 'text'
			THEN json_extract(attrs_json, '$.model') ELSE '' END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.fallback') = 'text' THEN 1 ELSE 0 END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.prefix_reused') = 'integer'
			THEN json_extract(attrs_json, '$.prefix_reused') ELSE 0 END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.prefix_rebuilt') = 'integer'
			THEN json_extract(attrs_json, '$.prefix_rebuilt') ELSE 0 END,
		CASE WHEN json_valid(attrs_json) AND json_type(attrs_json, '$.prefix_fresh') = 'integer'
			THEN json_extract(attrs_json, '$.prefix_fresh') ELSE 0 END
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
			fallback    int
		)
		if err := rows.Scan(&sid, &r.Kind, &r.Name, &r.Status, &r.Input, &r.Output, &r.CacheRead, &r.CacheWrite,
			&r.Purpose, &r.Prefix, &invalidated, &r.PrefixResume, &r.ResumeReason, &r.Model, &fallback,
			&r.Reused, &r.Rebuilt, &r.Fresh); err != nil {
			return CacheBuckets{}, fmt.Errorf("cache buckets: %w", err)
		}
		r.CacheInvalidated, r.Fallback = invalidated != 0, fallback != 0
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
