package cli

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/inhere/suga/pkg/agentcore/memory"
)

// Candidate kinds reported by `suga memory lint`.
const (
	LintDuplicate = "duplicate" // same text after normalization, or nearly the same
	LintConflict  = "conflict"  // same title, different content: the two may contradict each other
	LintStale     = "stale"     // not updated for a long time
)

// Suggested actions attached to a candidate; the command never executes them.
const (
	LintActionMerge  = "merge"
	LintActionDelete = "delete"
	LintActionReview = "review"
)

const (
	// lintNearDuplicateThreshold is the Jaccard similarity of two token sets
	// at which entries count as near duplicates. 0.7 flagged restatements
	// ("use tabs, not spaces" vs "prefer tabs over spaces") in manual runs,
	// while 0.5 already flagged entries that only share a topic; the value is
	// deliberately conservative so the report stays short.
	lintNearDuplicateThreshold = 0.7
	// lintMinHashParts is the number of MinHash components used for blocking.
	// Two token sets with similarity s land in the same bucket per component
	// with probability s, so 4 parts miss a pair above the 0.7 threshold with
	// probability 0.3^4 ≈ 0.8%.
	lintMinHashParts = 4
	// lintMaxSignatureBucket skips a blocking bucket that holds more entries
	// than this: near-identical entries in such a bucket are already caught by
	// the exact-text groups, and comparing the rest pairwise would go quadratic.
	lintMaxSignatureBucket = 64
	// lintMaxPairComparisons caps the total near-duplicate comparisons of one
	// scope (a guard for many medium-sized buckets); the report notes a cut.
	lintMaxPairComparisons = 200_000
	// lintMaxIDsPerCandidate bounds the ids listed per candidate; the rest is
	// summarised as "more".
	lintMaxIDsPerCandidate = 20
	// lintMaxStaleCandidates bounds the stale list, which would otherwise be
	// one candidate per old entry.
	lintMaxStaleCandidates = 50
	// lintStaleAfter is the age at which an entry is reported as possibly
	// stale. Memory entries carry no expiry field, only the time they were
	// last written, so staleness is a hint: a year without an update.
	lintStaleAfter = 365 * 24 * time.Hour
)

// lintEntry is one entry as the lint report sees it. hasBody is false when the
// backend cannot enumerate entries and only the index lines were read.
type lintEntry struct {
	ID, Title, Body, Summary string
	Kind                     memory.Kind
	Updated                  time.Time
	hasBody                  bool
}

// lintCandidate is one finding; IDs and Reason are always set.
type lintCandidate struct {
	Kind       string   `json:"kind"`                 // duplicate, conflict or stale
	IDs        []string `json:"ids"`                  // entry ids involved (capped)
	More       int      `json:"more,omitempty"`       // ids not listed because of the cap
	Title      string   `json:"title,omitempty"`      // representative title
	Similarity float64  `json:"similarity,omitempty"` // 0..1 token Jaccard; 1 = identical text
	Reason     string   `json:"reason"`               // why it was flagged, with the rule applied
	Action     string   `json:"action"`               // merge, delete or review
}

// lintScanScope finds the candidates of one scope. It is a pure function over
// the entries (plus the clock) so tests can drive it with synthetic data.
func lintScanScope(entries []lintEntry, now time.Time) ([]lintCandidate, []string) {
	if len(entries) == 0 {
		return []lintCandidate{}, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	texts := make([]string, len(entries))
	keys := make([]string, len(entries))
	tokens := make([]map[string]struct{}, len(entries))
	for i := range entries {
		texts[i] = lintText(entries[i])
		keys[i] = lintNormalize(texts[i])
		tokens[i] = lintTokens(texts[i])
	}
	hasBodies := entries[0].hasBody

	candidates := exactDuplicateCandidates(entries, keys)
	near, nearNotes := nearDuplicateCandidates(entries, keys, tokens)
	candidates = append(candidates, near...)
	candidates = append(candidates, conflictCandidates(entries, hasBodies)...)
	stale, staleNotes := staleCandidates(entries, now)
	candidates = append(candidates, stale...)

	notes := append(nearNotes, staleNotes...)
	sort.SliceStable(candidates, func(i, j int) bool { return lintLess(candidates[i], candidates[j]) })
	return candidates, notes
}

// lintText is the text compared by the report. Without bodies (an index-only
// backend) the summary stands in for the body.
func lintText(e lintEntry) string {
	if e.hasBody {
		return e.Title + "\n" + e.Body
	}
	return e.Title + "\n" + e.Summary
}

// lintNormalize lowercases the text and folds every run of punctuation,
// symbols and whitespace into one space, so case, spacing and punctuation do
// not hide a duplicate.
func lintNormalize(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
			continue
		}
		space = true
	}
	return b.String()
}

// lintTokens splits text into the token set used for similarity: runs of
// ASCII letters and digits as one token, and one token per non-ASCII letter or
// number (Chinese has no spaces, so a rune is the smallest useful unit).
func lintTokens(s string) map[string]struct{} {
	out := make(map[string]struct{})
	var word []rune
	flush := func() {
		if len(word) > 0 {
			out[string(word)] = struct{}{}
			word = word[:0]
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r < 128 && ((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')):
			word = append(word, r)
		case r >= 128 && (unicode.IsLetter(r) || unicode.IsNumber(r)):
			flush()
			out[string(r)] = struct{}{}
		default:
			flush()
		}
	}
	flush()
	return out
}

// lintJaccard is the similarity of two token sets: the size of their
// intersection over the size of their union.
func lintJaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if _, ok := b[t]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// lintRound keeps the similarity stable at two decimals in the JSON output.
func lintRound(v float64) float64 { return math.Round(v*100) / 100 }

// lintMinHash is the MinHash signature of a token set.
func lintMinHash(tokens map[string]struct{}) [lintMinHashParts]uint64 {
	sig := [lintMinHashParts]uint64{}
	for i := range sig {
		sig[i] = math.MaxUint64
	}
	for token := range tokens {
		for i := 0; i < lintMinHashParts; i++ {
			if h := lintHash(token, uint64(i)); h < sig[i] {
				sig[i] = h
			}
		}
	}
	return sig
}

// lintHash hashes token with the seed of one MinHash component.
func lintHash(token string, seed uint64) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(token))
	_, _ = h.Write([]byte{byte(seed), byte(seed >> 8), byte(seed >> 16)})
	return h.Sum64()
}

// lintBucketKey is one MinHash component value used as a blocking bucket.
type lintBucketKey struct {
	part int
	hash uint64
}

// exactDuplicateCandidates groups entries whose normalized text is identical.
func exactDuplicateCandidates(entries []lintEntry, keys []string) []lintCandidate {
	groups := make(map[string][]int, len(entries))
	for i := range entries {
		if keys[i] == "" {
			continue
		}
		groups[keys[i]] = append(groups[keys[i]], i)
	}
	var out []lintCandidate
	for _, key := range sortedGroupKeys(groups) {
		group := groups[key]
		if len(group) < 2 {
			continue
		}
		ids, more := lintIDs(entries, group)
		reason := "normalized title and body are identical (case, whitespace and punctuation ignored)"
		if !entries[group[0]].hasBody {
			reason = "normalized title and index summary are identical (bodies were not read: the backend does not enumerate entries), case, whitespace and punctuation ignored"
		}
		out = append(out, lintCandidate{Kind: LintDuplicate, IDs: ids, More: more,
			Title: entries[group[0]].Title, Similarity: 1, Reason: reason, Action: LintActionMerge})
	}
	return out
}

// nearDuplicateCandidates compares entries that share a MinHash bucket and
// reports pairs whose token similarity reaches the threshold. Exact groups are
// reported separately, so pairs of entries with the same normalized text are
// skipped here.
func nearDuplicateCandidates(entries []lintEntry, keys []string, tokens []map[string]struct{}) ([]lintCandidate, []string) {
	buckets := make(map[lintBucketKey][]int)
	for i := range entries {
		if len(tokens[i]) == 0 {
			continue
		}
		sig := lintMinHash(tokens[i])
		for part := 0; part < lintMinHashParts; part++ {
			k := lintBucketKey{part: part, hash: sig[part]}
			buckets[k] = append(buckets[k], i)
		}
	}
	keysOrder := make([]lintBucketKey, 0, len(buckets))
	for k := range buckets {
		keysOrder = append(keysOrder, k)
	}
	sort.Slice(keysOrder, func(i, j int) bool {
		if keysOrder[i].part != keysOrder[j].part {
			return keysOrder[i].part < keysOrder[j].part
		}
		return keysOrder[i].hash < keysOrder[j].hash
	})

	var out []lintCandidate
	var notes []string
	budget := lintMaxPairComparisons
	exhausted := false
	seen := make(map[[2]int]bool)
	for _, k := range keysOrder {
		bucket := buckets[k]
		if len(bucket) < 2 {
			continue
		}
		if len(bucket) > lintMaxSignatureBucket {
			continue // see lintMaxSignatureBucket: identical text is already grouped
		}
		for a := 0; a < len(bucket) && budget > 0; a++ {
			for b := a + 1; b < len(bucket) && budget > 0; b++ {
				i, j := bucket[a], bucket[b]
				if keys[i] == keys[j] {
					continue
				}
				pair := [2]int{min(i, j), max(i, j)}
				if seen[pair] {
					continue
				}
				seen[pair] = true
				budget--
				if budget == 0 {
					exhausted = true
				}
				sim := lintJaccard(tokens[i], tokens[j])
				if sim < lintNearDuplicateThreshold {
					continue
				}
				out = append(out, lintCandidate{Kind: LintDuplicate,
					IDs: []string{entries[i].ID, entries[j].ID}, Title: entries[i].Title,
					Similarity: lintRound(sim),
					Reason: fmt.Sprintf("token similarity %.2f is at or above the %.2f near-duplicate threshold",
						sim, lintNearDuplicateThreshold),
					Action: LintActionMerge})
			}
		}
	}
	if exhausted {
		notes = append(notes, fmt.Sprintf("near-duplicate comparison stopped after %d pairs; narrow the scope or clean up the largest groups first", lintMaxPairComparisons))
	}
	return out, notes
}

// conflictCandidates flags entries that share a title but differ in content:
// with bodies read that is "the bodies differ", otherwise "the index summaries
// differ". Both are heuristics that ask for a human decision, never a verdict.
func conflictCandidates(entries []lintEntry, hasBodies bool) []lintCandidate {
	groups := make(map[string][]int, len(entries))
	for i := range entries {
		key := lintNormalize(entries[i].Title)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], i)
	}
	var out []lintCandidate
	for _, key := range sortedGroupKeys(groups) {
		group := groups[key]
		if len(group) < 2 {
			continue
		}
		parts := make(map[string][]int, 2)
		for _, i := range group {
			body := entries[i].Body
			if !hasBodies {
				body = entries[i].Summary
			}
			k := lintNormalize(body)
			parts[k] = append(parts[k], i)
		}
		if len(parts) < 2 {
			continue // identical content: reported as a duplicate
		}
		var first int
		sim := 1.0
		field := "bodies"
		if !hasBodies {
			field = "index summaries (bodies were not read)"
		}
		for n, part := range sortedGroupKeys(parts) {
			if n == 0 {
				first = parts[part][0]
				continue
			}
			sim = math.Min(sim, lintJaccard(lintTokens(lintText(entries[first])), lintTokens(lintText(entries[parts[part][0]]))))
		}
		ids, more := lintIDs(entries, group)
		out = append(out, lintCandidate{Kind: LintConflict, IDs: ids, More: more, Title: entries[group[0]].Title,
			Similarity: lintRound(sim),
			Reason: fmt.Sprintf("same title after normalization but the %s differ (token similarity %.2f); they may contradict each other",
				field, sim),
			Action: LintActionReview})
	}
	return out
}

// staleCandidates lists entries not updated for lintStaleAfter. Memory entries
// have no expiry field, so this is a hint based on the last update time.
func staleCandidates(entries []lintEntry, now time.Time) ([]lintCandidate, []string) {
	var stale []int
	for i := range entries {
		if !entries[i].hasBody || entries[i].Updated.IsZero() {
			continue
		}
		if now.Sub(entries[i].Updated) > lintStaleAfter {
			stale = append(stale, i)
		}
	}
	if len(stale) == 0 {
		return nil, nil
	}
	sort.SliceStable(stale, func(i, j int) bool { return entries[stale[i]].Updated.Before(entries[stale[j]].Updated) })
	var notes []string
	if len(stale) > lintMaxStaleCandidates {
		notes = append(notes, fmt.Sprintf("%d possibly stale entries are not listed (limit %d per scope)",
			len(stale)-lintMaxStaleCandidates, lintMaxStaleCandidates))
		stale = stale[:lintMaxStaleCandidates]
	}
	out := make([]lintCandidate, 0, len(stale))
	for _, i := range stale {
		e := entries[i]
		out = append(out, lintCandidate{Kind: LintStale, IDs: []string{e.ID}, Title: e.Title,
			Reason: fmt.Sprintf("last updated %s, older than %d days; check it still holds",
				e.Updated.UTC().Format(time.RFC3339), int(lintStaleAfter.Hours()/24)),
			Action: LintActionReview})
	}
	return out, notes
}

// lintIDs lists at most lintMaxIDsPerCandidate ids and reports how many were
// left out.
func lintIDs(entries []lintEntry, idx []int) ([]string, int) {
	n := min(len(idx), lintMaxIDsPerCandidate)
	ids := make([]string, 0, n)
	for _, i := range idx[:n] {
		ids = append(ids, entries[i].ID)
	}
	return ids, len(idx) - n
}

// lintLess orders candidates by kind and then by their first id, so the text
// and JSON reports are deterministic.
func lintLess(a, b lintCandidate) bool {
	if ra, rb := lintKindRank(a.Kind), lintKindRank(b.Kind); ra != rb {
		return ra < rb
	}
	return strings.Join(a.IDs, ",") < strings.Join(b.IDs, ",")
}

func lintKindRank(kind string) int {
	switch kind {
	case LintDuplicate:
		return 0
	case LintConflict:
		return 1
	case LintStale:
		return 2
	}
	return 3
}

// sortedGroupKeys returns the keys of a group map in sorted order.
func sortedGroupKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
