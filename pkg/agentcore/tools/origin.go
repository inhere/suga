package tools

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// External-origin tracking (suga-42m8): text a web or MCP tool brought into the
// context is remembered as hashes of its 40-byte windows, so an approval can
// tell that the arguments of a write or exec call carry a verbatim piece of it
// (the practical prompt-injection signal: the model copies a fetched command
// straight into shell_exec or file_write).
const (
	// OriginWindow is the shortest verbatim piece that counts as copied. 12 bytes
	// (until suga-we1v) matched common phrases a model writes on its own
	// ("API definitions", " credentials: ") once a spec had been fetched; a
	// copied command or script is usually far longer than 40 bytes.
	OriginWindow = 40
	// originMinSignificant is how many non-space bytes a window needs. A window
	// of spaces and punctuation ("  ----------") says nothing about origin.
	originMinSignificant = 32
	// originMaxHashes caps the windows kept per session (about 10 MB). When a
	// new result does not fit, the oldest results are forgotten first.
	originMaxHashes = 256 * 1024
	// originMaxResultBytes caps the text taken from one result; a result longer
	// than this is remembered by its head.
	originMaxResultBytes = 128 * 1024
	// originRollBase is the multiplier of the rolling hash (an odd 64-bit prime).
	originRollBase = 1099511628211
)

// originSource is one remembered result.
type originSource struct {
	id     uint64
	tool   string
	hashes []uint64
}

// ExternalOrigin remembers the text of external tool results per session. The
// zero value is not usable; use NewExternalOrigin. It is safe for concurrent use.
type ExternalOrigin struct {
	mu       sync.Mutex
	sessions map[string]*originSession
}

type originSession struct {
	nextID  uint64
	sources []*originSource
	count   int
	// index maps a window hash to the newest source holding it.
	index map[uint64]uint64
	names map[uint64]string
	// allowed holds the windows the user let through for this session, keyed by
	// the tool whose result held them. It outlives the sources it came from.
	allowed map[string]map[uint64]struct{}
}

// originWindow is one 40-byte window of a copied piece and the tool whose
// result held it.
type originWindow struct {
	hash uint64
	tool string
}

// NewExternalOrigin returns an empty tracker.
func NewExternalOrigin() *ExternalOrigin {
	return &ExternalOrigin{sessions: map[string]*originSession{}}
}

// Record remembers text, the result of tool, for session.
func (o *ExternalOrigin) Record(session, tool, text string) {
	if o == nil {
		return
	}
	norm := normalizeOrigin(text)
	if len(norm) > originMaxResultBytes {
		norm = norm[:originMaxResultBytes]
	}
	hashes := originWindows(norm)
	if len(hashes) == 0 {
		return
	}
	if len(hashes) > originMaxHashes {
		hashes = hashes[:originMaxHashes]
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.sessions[session]
	if s == nil {
		s = &originSession{index: map[uint64]uint64{}, names: map[uint64]string{}}
		o.sessions[session] = s
	}
	// Forget the oldest results until the new one fits.
	for s.count+len(hashes) > originMaxHashes && len(s.sources) > 0 {
		old := s.sources[0]
		s.sources = s.sources[1:]
		s.count -= len(old.hashes)
		for _, h := range old.hashes {
			if s.index[h] == old.id {
				delete(s.index, h)
			}
		}
		delete(s.names, old.id)
	}
	s.nextID++
	src := &originSource{id: s.nextID, tool: tool, hashes: hashes}
	s.sources = append(s.sources, src)
	s.names[src.id] = tool
	s.count += len(hashes)
	for _, h := range hashes {
		s.index[h] = src.id
	}
}

// OriginHit is a verbatim piece of an external result found in call arguments.
type OriginHit struct {
	// Tool is the tool whose result held the text.
	Tool string
	// Snippet is the part of the copied piece a prompt shows: it starts at the
	// first window not yet allowed and runs over the not-yet-allowed text that
	// follows, at most originPromptSnippetBytes.
	Snippet string
	// Remembered reports that every window of the copied piece was allowed
	// earlier in this session (see Allow).
	Remembered bool
	// windows are the windows the Snippet covers; Allow records exactly these.
	windows []originWindow
}

// Find looks for a piece of at least OriginWindow bytes of a remembered result
// in the string values of args (nested maps and lists included). Whitespace runs
// are compared as one space.
func (o *ExternalOrigin) Find(session string, args map[string]any) (OriginHit, bool) {
	return o.find(session, args, false)
}

// FindIgnoringURLs is Find with every http(s) URL in args left out of the
// comparison (suga-we1v): a file write that names a fetched page's URL in a
// comment or a config is not a copied instruction, while a command that runs
// a fetched URL is still checked with Find.
func (o *ExternalOrigin) FindIgnoringURLs(session string, args map[string]any) (OriginHit, bool) {
	return o.find(session, args, true)
}

// find returns the first hit segment that is not yet allowed. When every
// segment is allowed it returns the first one with Remembered set.
func (o *ExternalOrigin) find(session string, args map[string]any, ignoreURLs bool) (OriginHit, bool) {
	hits := o.findAll(session, args, ignoreURLs)
	for _, h := range hits {
		if !h.Remembered {
			return h, true
		}
	}
	if len(hits) > 0 {
		return hits[0], true
	}
	return OriginHit{}, false
}

// findAll returns every hit segment of args in order: a run of adjacent windows
// that all match and come from the same tool is one segment. A window with hash 0
// ends a segment.
func (o *ExternalOrigin) findAll(session string, args map[string]any, ignoreURLs bool) []OriginHit {
	if o == nil || len(args) == 0 {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.sessions[session]
	if s == nil || len(s.index) == 0 {
		return nil
	}
	var hits []OriginHit
	visitStrings(args, func(v string) bool {
		if ignoreURLs {
			v = originURL.ReplaceAllString(v, " ")
		}
		norm := normalizeOrigin(v)
		hashes := originWindowHashes(norm)
		for i := 0; i < len(hashes); {
			id, ok := s.index[hashes[i]]
			if hashes[i] == 0 || !ok {
				i++
				continue
			}
			tool := s.names[id]
			j := i + 1
			for j < len(hashes) && hashes[j] != 0 {
				id2, ok2 := s.index[hashes[j]]
				if !ok2 || s.names[id2] != tool {
					break
				}
				j++
			}
			hits = append(hits, originSegmentHit(s.allowed[tool], norm, hashes, i, j, tool))
			i = j
		}
		return true
	})
	return hits
}

// originSegmentHit builds the hit for the segment of windows [from, to) of norm.
// The shown snippet starts at the first window not in allowed and ends with the
// last of the not-allowed windows that follow it without a gap; when every
// window is allowed it spans the whole segment. It is cut to
// originPromptSnippetBytes on a UTF-8 boundary, and the windows it covers are
// counted on that cut (the "…" is not part of it).
func originSegmentHit(allowed map[uint64]struct{}, norm string, hashes []uint64, from, to int, tool string) OriginHit {
	isAllowed := func(k int) bool { _, ok := allowed[hashes[k]]; return ok }
	start, end, remembered := from, to-1+OriginWindow, true
	for k := from; k < to; k++ {
		if isAllowed(k) {
			continue
		}
		remembered = false
		g := k
		for g+1 < to && !isAllowed(g+1) {
			g++
		}
		start, end = k, g+OriginWindow
		break
	}
	cut, truncated := end-start, false
	if cut > originPromptSnippetBytes {
		cut, truncated = originPromptSnippetBytes, true
		for cut > 0 && !isRuneStart(norm[start+cut]) {
			cut--
		}
	}
	hit := OriginHit{Tool: tool, Snippet: norm[start : start+cut], Remembered: remembered}
	if truncated {
		hit.Snippet += "…"
	}
	// A window covered by the cut starts at or before start+cut-OriginWindow.
	for k := start; k < to && k+OriginWindow <= start+cut; k++ {
		hit.windows = append(hit.windows, originWindow{hash: hashes[k], tool: tool})
	}
	return hit
}

// Allow records the windows hit covers as allowed for session, under the tool
// whose result held them, so a later Find reports a piece made only of such
// windows as Remembered. Windows outside the shown snippet are not recorded.
func (o *ExternalOrigin) Allow(session string, hit OriginHit) {
	if o == nil || len(hit.windows) == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.sessions[session]
	if s == nil {
		return
	}
	if s.allowed == nil {
		s.allowed = map[string]map[uint64]struct{}{}
	}
	for _, w := range hit.windows {
		set := s.allowed[w.tool]
		if set == nil {
			set = map[uint64]struct{}{}
			s.allowed[w.tool] = set
		}
		set[w.hash] = struct{}{}
	}
}

// originURL matches an http(s) URL up to whitespace, a quote or a bracket.
var originURL = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>()\[\]{}]+`)

// originPromptSnippetBytes is the most of a copied piece a prompt shows.
const originPromptSnippetBytes = 200

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// visitStrings calls fn for every string in v (maps in key order); fn returns
// false to stop. It reports whether the walk ran to the end.
func visitStrings(v any, fn func(string) bool) bool {
	switch t := v.(type) {
	case string:
		return fn(t)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !visitStrings(t[k], fn) {
				return false
			}
		}
	case []any:
		for _, e := range t {
			if !visitStrings(e, fn) {
				return false
			}
		}
	case []string:
		for _, e := range t {
			if !fn(e) {
				return false
			}
		}
	}
	return true
}

// normalizeOrigin trims text and folds every whitespace run into one space, so
// a copied piece matches whatever line wrapping or indentation it was pasted with.
func normalizeOrigin(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// originWindows returns the hashes of the significant OriginWindow-byte windows
// of s (duplicates removed).
func originWindows(s string) []uint64 {
	all := originWindowHashes(s)
	seen := make(map[uint64]struct{}, len(all))
	out := make([]uint64, 0, len(all))
	for _, h := range all {
		if h == 0 {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

// originWindowHashes returns one hash per window start of s: the rolling hash
// of the window, or 0 for a window without enough significant bytes (so the
// result lines up with the byte offsets of s).
func originWindowHashes(s string) []uint64 {
	if len(s) < OriginWindow {
		return nil
	}
	// pow is base^(window-1), the weight of the byte that leaves the window.
	pow := uint64(1)
	for i := 0; i < OriginWindow-1; i++ {
		pow *= originRollBase
	}
	out := make([]uint64, len(s)-OriginWindow+1)
	var h uint64
	significant := 0
	for i := 0; i < len(s); i++ {
		if i >= OriginWindow {
			old := s[i-OriginWindow]
			h -= uint64(old) * pow
			if old != ' ' {
				significant--
			}
		}
		h = h*originRollBase + uint64(s[i])
		if s[i] != ' ' {
			significant++
		}
		if i >= OriginWindow-1 && significant >= originMinSignificant {
			out[i-OriginWindow+1] = h
		}
	}
	return out
}
