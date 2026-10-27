package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// segmentChange is one segment break of a run, read from
// `suga inspect trace <sid> --analyze --json` (field segment_sections): the
// segment it started, the sections of its context message that changed since
// the previous segment and the bytes of every section then. The first recorded
// segment of a run has no previous one, so its Changed list is empty and it
// says nothing about a section (see breaksOf).
type segmentChange struct {
	Segment  int           `json:"segment"`
	Reason   string        `json:"reason,omitempty"`
	Changed  []string      `json:"changed,omitempty"`
	Sections []sectionSize `json:"sections"`
}

// sectionSize is one section of a segment's context message and its bytes.
type sectionSize struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
}

// bytesOf is the size of one section in this segment; ok is false when the
// segment does not have it (a section dropped at the break changed as well, but
// its bytes left the message and are not part of the new one).
func (s segmentChange) bytesOf(name string) (n int, ok bool) {
	for _, sec := range s.Sections {
		if sec.Name == name {
			return sec.Bytes, true
		}
	}
	return 0, false
}

// parseSegmentChanges reads the segment part of an --analyze JSON object. It is
// nil when the object carries no segment sections (an older binary, a session
// that never broke a segment, or another agent's trace).
func parseSegmentChanges(raw []byte) []segmentChange {
	var rep struct {
		Segments []segmentChange `json:"segment_sections"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &rep) != nil {
		return nil
	}
	return rep.Segments
}

// breaksOf flattens the segment breaks of rs that have a previous segment to
// differ from: the first recorded segment of a run has none, so it stays out of
// the counts and the shares.
func breaksOf(rs []Result) []segmentChange {
	var out []segmentChange
	for _, r := range rs {
		if len(r.SegmentChanges) < 2 {
			continue
		}
		out = append(out, r.SegmentChanges[1:]...)
	}
	return out
}

// runChangedSection reports whether a break of r changed the section (always
// false for a run that broke no segment: nothing to compare).
func runChangedSection(r Result, section string) bool {
	if len(r.SegmentChanges) < 2 {
		return false
	}
	for _, seg := range r.SegmentChanges[1:] {
		for _, name := range seg.Changed {
			if name == section {
				return true
			}
		}
	}
	return false
}

// sectionBreakStat is one section across the breaks of a group of runs: how
// often it changed, how many breaks that is a share of, and how big it was when
// it did.
type sectionBreakStat struct {
	Name   string
	Breaks int
	// Changed is the number of breaks that changed the section, Share that
	// number over Breaks.
	Changed int
	Share   float64
	// AvgBytes is the mean size of the section at the breaks that changed it and
	// still had it (a dropped section counts as changed with no bytes).
	AvgBytes int64
}

// sectionBreakStats counts, per section name, the breaks of rs that changed it,
// sorted by how often it changed (then by name, so a report is reproducible).
func sectionBreakStats(rs []Result) []sectionBreakStat {
	breaks := breaksOf(rs)
	if len(breaks) == 0 {
		return nil
	}
	byName := map[string]*sectionBreakStat{}
	present := map[string]int{}
	for _, seg := range breaks {
		for _, name := range seg.Changed {
			st := byName[name]
			if st == nil {
				st = &sectionBreakStat{Name: name}
				byName[name] = st
			}
			st.Changed++
			if n, ok := seg.bytesOf(name); ok {
				st.AvgBytes += int64(n)
				present[name]++
			}
		}
	}
	out := make([]sectionBreakStat, 0, len(byName))
	for _, st := range byName {
		st.Breaks = len(breaks)
		st.Share = float64(st.Changed) / float64(len(breaks))
		if n := present[st.Name]; n > 0 {
			st.AvgBytes /= int64(n)
		}
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Changed != out[j].Changed {
			return out[i].Changed > out[j].Changed
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// segmentStartSplit is the pooled segment_start cache bucket of the runs a
// section changed in and of the runs it did not: the bucket says how much of
// the prefix the provider served on a segment's first call, so a difference
// between the two sides is what a change of that section costs at a break.
type segmentStartSplit struct {
	ChangedRuns, UnchangedRuns int
	Changed, Unchanged         cacheBucket
}

// segmentStartOf is the split for one section. The analyze view reports one
// segment_start bucket per run, so a run whose breaks changed the section at
// least once counts on the changed side.
func segmentStartOf(rs []Result, section string) segmentStartSplit {
	var changed, unchanged []Result
	for _, r := range rs {
		if runChangedSection(r, section) {
			changed = append(changed, r)
		} else {
			unchanged = append(unchanged, r)
		}
	}
	return segmentStartSplit{
		ChangedRuns: len(changed), UnchangedRuns: len(unchanged),
		Changed: bucketOf(changed, "segment_start"), Unchanged: bucketOf(unchanged, "segment_start"),
	}
}

// bucketOf is the pooled bucket name of rs; a zero bucket when no row carries
// it (the hit and reuse cells then render "-").
func bucketOf(rs []Result, name string) cacheBucket {
	for _, b := range poolBuckets(rs) {
		if b.Bucket == name {
			return b
		}
	}
	return cacheBucket{Bucket: name}
}

// hitCellOf renders one pooled bucket's hit rate: "-" when the bucket has no
// calls or its provider reported no cache.
func hitCellOf(b cacheBucket) string {
	if !b.CacheReported || b.prompt() == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", hitOf(b.CacheRead, b.prompt())*100)
}

// reuseCellOf renders one pooled bucket's prefix reuse rate: "-" when no call
// of the bucket carried a prefix estimate (an older binary's trace).
func reuseCellOf(b cacheBucket) string {
	if b.PrefixTokens == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", reuseOf(b.CacheRead, b.PrefixTokens)*100)
}

// splitRateCells renders the changed / unchanged pair of one rate ("88.0/92.0").
func splitRateCells(s segmentStartSplit) (hit, reuse string) {
	return hitCellOf(s.Changed) + "/" + hitCellOf(s.Unchanged), reuseCellOf(s.Changed) + "/" + reuseCellOf(s.Unchanged)
}

// printSegmentChanges writes the section breaks of a run set: which section of
// the segment context message changed how often, how big it is, and the
// segment_start cache bucket of the runs it changed in versus the runs it did
// not. Nothing is printed when no run broke a segment (a session without a
// compaction, a provider other than suga, an older binary).
func printSegmentChanges(w io.Writer, rs []Result) {
	stats := sectionBreakStats(rs)
	if len(stats) == 0 {
		return
	}
	fmt.Fprintf(w, "\nsegment sections changing at a break (%d breaks; a run's first segment has no previous one and is not counted):\n", len(breaksOf(rs)))
	fmt.Fprintf(w, "  %-16s %7s %7s %6s %10s  %-13s %-13s %s\n",
		"SECTION", "CHANGED", "BREAKS", "SHARE", "AVG-BYTES", "HIT% chg/unchg", "REUSE% chg/unchg", "RUNS chg/unchg")
	for _, st := range stats {
		s := segmentStartOf(rs, st.Name)
		hit, reuse := splitRateCells(s)
		fmt.Fprintf(w, "  %-16s %7d %7d %5.1f%% %10d  %-13s %-13s %d/%d\n",
			st.Name, st.Changed, st.Breaks, st.Share*100, st.AvgBytes, hit, reuse, s.ChangedRuns, s.UnchangedRuns)
	}
	fmt.Fprintln(w, "  (HIT%/REUSE% are the segment_start bucket: runs that changed the section at a break / runs that did not)")
}

// segmentGroup is one column of the segment break tables: a variant name, or
// 基线 / 候选 of a comparison, and its rows.
type segmentGroup struct {
	Label string
	Rows  []Result
}

// segmentChangeSection renders the markdown section "换段时变化的节" for groups:
// per section how often it changed at a break, its mean bytes and the
// segment_start hit/reuse rate of the runs it changed in versus those it did
// not. It returns "" when no group broke a segment with a previous one.
func segmentChangeSection(groups []segmentGroup) string {
	stats := make([][]sectionBreakStat, len(groups))
	any := false
	for i, g := range groups {
		stats[i] = sectionBreakStats(g.Rows)
		any = any || len(stats[i]) > 0
	}
	if !any {
		return ""
	}
	breaks := make([]int, len(groups))
	var b strings.Builder
	b.WriteString("换段次数（首段没有可比的上一个段，不计入占比）：")
	parts := make([]string, 0, len(groups))
	for i, g := range groups {
		breaks[i] = len(breaksOf(g.Rows))
		parts = append(parts, fmt.Sprintf("%s %d", g.Label, breaks[i]))
	}
	fmt.Fprintf(&b, "%s\n\n", strings.Join(parts, "、"))

	// How often a section changed, per group, with its size when it did.
	b.WriteString("| 节 |")
	for _, g := range groups {
		fmt.Fprintf(&b, " %s 变化/占比·平均字节 |", g.Label)
	}
	b.WriteString("\n|---|")
	for range groups {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, name := range sectionOrder(stats) {
		fmt.Fprintf(&b, "| %s |", name)
		for i := range groups {
			fmt.Fprintf(&b, " %s |", sectionCell(stats[i], name))
		}
		b.WriteString("\n")
	}

	// The segment_start cache bucket: what a section costs at a break.
	fmt.Fprintf(&b, "\nsegment_start 桶的命中率与前缀复用率（该节在换段时变化 / 未变化）：\n\n")
	b.WriteString("| 节 |")
	for _, g := range groups {
		fmt.Fprintf(&b, " %s 变化时 hit%%/reuse%% | %s 未变化时 hit%%/reuse%% |", g.Label, g.Label)
	}
	b.WriteString("\n|---|")
	for range groups {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, name := range sectionOrder(stats) {
		fmt.Fprintf(&b, "| %s |", name)
		for _, g := range groups {
			s := segmentStartOf(g.Rows, name)
			fmt.Fprintf(&b, " %s/%s (%d 轮) | %s/%s (%d 轮) |",
				hitCellOf(s.Changed), reuseCellOf(s.Changed), s.ChangedRuns,
				hitCellOf(s.Unchanged), reuseCellOf(s.Unchanged), s.UnchangedRuns)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// sectionOrder is the union of the section names of every group's stats: by the
// pooled number of breaks that changed them, then by name.
func sectionOrder(stats [][]sectionBreakStat) []string {
	changed := map[string]int{}
	for _, list := range stats {
		for _, st := range list {
			changed[st.Name] += st.Changed
		}
	}
	out := make([]string, 0, len(changed))
	for name := range changed {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		if changed[out[i]] != changed[out[j]] {
			return changed[out[i]] > changed[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// sectionCell renders one section's row of one group: which share of the breaks
// changed it and its mean bytes, "-" when that group never changed it.
func sectionCell(list []sectionBreakStat, name string) string {
	for _, st := range list {
		if st.Name != name {
			continue
		}
		return fmt.Sprintf("%d/%.1f%% · %dB", st.Changed, st.Share*100, st.AvgBytes)
	}
	return "-"
}
