package tools

import (
	"sort"
	"strings"
	"sync"
)

// External-origin tracking (suga-42m8): text a web or MCP tool brought into the
// context is remembered as hashes of its 12-byte windows, so an approval can
// tell that the arguments of a write or exec call carry a verbatim piece of it
// (the practical prompt-injection signal: the model copies a fetched command
// straight into shell_exec or file_write).
const (
	// OriginWindow is the shortest verbatim piece that counts as copied.
	OriginWindow = 12
	// originMinSignificant is how many non-space bytes a window needs. A window
	// of spaces and punctuation ("  ----------") says nothing about origin.
	originMinSignificant = 8
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
	// Snippet is the start of the copied piece, as found in the arguments.
	Snippet string
}

// Find looks for a piece of at least OriginWindow bytes of a remembered result
// in the string values of args (nested maps and lists included). Whitespace runs
// are compared as one space.
func (o *ExternalOrigin) Find(session string, args map[string]any) (OriginHit, bool) {
	if o == nil || len(args) == 0 {
		return OriginHit{}, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.sessions[session]
	if s == nil || len(s.index) == 0 {
		return OriginHit{}, false
	}
	var hit OriginHit
	var found bool
	visitStrings(args, func(v string) bool {
		norm := normalizeOrigin(v)
		for i, h := range originWindowHashes(norm) {
			if h == 0 {
				continue
			}
			if id, ok := s.index[h]; ok {
				hit, found = OriginHit{Tool: s.names[id], Snippet: originSnippet(norm[i:])}, true
				return false
			}
		}
		return true
	})
	return hit, found
}

// originSnippetBytes is how much of a copied piece a prompt shows.
const originSnippetBytes = 48

// originSnippet cuts s to a short, valid UTF-8 string.
func originSnippet(s string) string {
	if len(s) <= originSnippetBytes {
		return s
	}
	cut := originSnippetBytes
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

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
