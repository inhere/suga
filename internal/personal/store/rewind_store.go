package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Session rewind, store side (session rewind design 0.2, plan T1).
//
// A conversation rewind appends one EntryRewind record {cut_seq, target_turn,
// scope, files}. The entries with a seq strictly between cut_seq and the
// record's own seq are masked: readers that build model context skip them. The
// cut entry and the record itself stay visible (the record is how a reader
// finds the interval). Nothing is deleted and no seq is reused, so the log stays
// append-only and every other reader (trace, sugasdk Entries) sees the rows as
// before. This file holds the interval types and the lookup of the records; the
// interval-aware reads live next to their unmasked twins (entries.go, turns.go)
// and the search option in fts.go.

// RewindRecord is one stored EntryRewind record.
type RewindRecord struct {
	// Seq is the record's own seq; the masked interval is (CutSeq, Seq).
	Seq int64
	// CutSeq is the seq of the last entry kept before the rewound turn.
	CutSeq     int64
	TargetTurn string
	Scope      string
}

// rewindBody is the JSON body of an EntryRewind row (only the fields the store
// reads; "files" is for trace readers).
type rewindBody struct {
	CutSeq     int64  `json:"cut_seq"`
	TargetTurn string `json:"target_turn"`
	Scope      string `json:"scope"`
}

// RewindRecords returns a session's rewind records, oldest first (empty when the
// session was never rewound). A row whose body does not decode is an error: a
// reader that skipped it would show masked content again.
//
// The query drives from idx_entries_type: rewind rows are rare in the whole
// ledger, while the session's primary-key range would read every row of a long
// session on each call (BenchmarkRewindRecords on 5 sessions of 20k entries: 2.4ms
// for the key range, 9us for the type index; TestRewindRecordsPlan pins the plan).
// The unary + on session_id keeps the planner off the primary key without
// INDEXED BY, so a store opened read-only on a schema without the index still
// answers, only slower.
const rewindRecordsSQL = `SELECT seq, json FROM entries WHERE type = ? AND +session_id = ? ORDER BY seq ASC`

func (s *Store) RewindRecords(ctx context.Context, sid string) ([]RewindRecord, error) {
	rows, err := s.db.QueryContext(ctx, rewindRecordsSQL, EntryRewind, sid)
	if err != nil {
		return nil, fmt.Errorf("list rewind records: %w", err)
	}
	defer rows.Close()
	var out []RewindRecord
	for rows.Next() {
		var (
			seq  int64
			body string
		)
		if err := rows.Scan(&seq, &body); err != nil {
			return nil, err
		}
		var b rewindBody
		if err := json.Unmarshal([]byte(body), &b); err != nil {
			return nil, fmt.Errorf("decode rewind record %s/%d: %w", sid, seq, err)
		}
		out = append(out, RewindRecord{Seq: seq, CutSeq: b.CutSeq, TargetTurn: b.TargetTurn, Scope: b.Scope})
	}
	return out, rows.Err()
}

// SeqRange is the open interval (Lo, Hi): the seqs Lo < seq < Hi are masked, Lo
// and Hi themselves are not.
type SeqRange struct{ Lo, Hi int64 }

// SeqRanges is a sorted list of disjoint masked intervals; build it with
// NewSeqRanges. The zero value masks nothing.
type SeqRanges []SeqRange

// NewSeqRanges sorts rs, drops the intervals that mask no integer (Hi-Lo <= 1)
// and merges overlapping ones. (1,5) and (4,9) merge into (1,9); (1,5) and
// (5,9) stay apart because seq 5 is masked by neither.
func NewSeqRanges(rs ...SeqRange) SeqRanges {
	list := make([]SeqRange, 0, len(rs))
	for _, r := range rs {
		if r.Hi-r.Lo > 1 {
			list = append(list, r)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Lo < list[j].Lo })
	var out SeqRanges
	for _, r := range list {
		if n := len(out); n > 0 && r.Lo < out[n-1].Hi {
			out[n-1].Hi = max(out[n-1].Hi, r.Hi)
			continue
		}
		out = append(out, r)
	}
	return out
}

// RangesOf is the masked intervals of a session's rewind records.
func RangesOf(recs []RewindRecord) SeqRanges {
	rs := make([]SeqRange, 0, len(recs))
	for _, r := range recs {
		rs = append(rs, SeqRange{Lo: r.CutSeq, Hi: r.Seq})
	}
	return NewSeqRanges(rs...)
}

// Hidden reports whether seq lies inside a masked interval (binary search).
func (r SeqRanges) Hidden(seq int64) bool {
	i := sort.Search(len(r), func(i int) bool { return r[i].Hi > seq })
	return i < len(r) && r[i].Lo < seq
}

// visibleSegments is the complement of r over seq >= 1 as closed [lo, hi]
// segments in ascending order: the pieces a bounded read walks one primary-key
// range at a time, instead of filtering masked rows one by one.
func (r SeqRanges) visibleSegments() [][2]int64 {
	segs := make([][2]int64, 0, len(r)+1)
	prev := int64(1)
	for _, m := range r {
		if prev <= m.Lo {
			segs = append(segs, [2]int64{prev, m.Lo})
		}
		prev = max(prev, m.Hi)
	}
	return append(segs, [2]int64{prev, math.MaxInt64})
}

// maskedSQL is a condition that is true for a masked seq of column col (false
// for an empty list), with its args; callers wrap it in NOT for "visible".
func (r SeqRanges) maskedSQL(col string) (string, []any) {
	if len(r) == 0 {
		return "0", nil
	}
	parts := make([]string, len(r))
	args := make([]any, 0, 2*len(r))
	for i, m := range r {
		parts[i] = "(" + col + " > ? AND " + col + " < ?)"
		args = append(args, m.Lo, m.Hi)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// ExcludeSession drops one session's masked rows from a search (EntrySearch.
// ExcludeSession). The zero value excludes nothing: sugasdk and the CLI search
// as before.
type ExcludeSession struct {
	SessionID string
	Ranges    SeqRanges
}

// normalized returns the exclusion with its ranges sorted and merged; the SQL
// builder and the cursor fingerprint both use it, so equal masks are equal.
func (e ExcludeSession) normalized() ExcludeSession {
	e.Ranges = NewSeqRanges(e.Ranges...)
	return e
}

func (e ExcludeSession) active() bool { return e.SessionID != "" && len(e.Ranges) > 0 }

// condition is the WHERE term that drops the masked rows of the session (alias
// f of the search query) and keeps every other row.
func (e ExcludeSession) condition() (string, []any) {
	masked, args := e.Ranges.maskedSQL("f.seq")
	return "NOT (f.session_id = ? AND " + masked + ")", append([]any{e.SessionID}, args...)
}
