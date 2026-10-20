// Package memory defines long-term memory for agents: entry types, the Store
// interface every backend implements, the Governed wrapper that applies the
// write policy (taint gate, sanitizing, tombstones, budget, events) and the
// Router that dispatches scopes to backends.
//
// The package only depends on the standard library and agentcore/redact so
// that hosts and backends (files, gofer, exec) can share it.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Kind classifies an entry. The four kinds follow the memory tool contract.
type Kind string

const (
	KindUser      Kind = "user"      // who the user is: role, preferences, expertise
	KindFeedback  Kind = "feedback"  // guidance on how to work: corrections and confirmed choices
	KindProject   Kind = "project"   // facts about ongoing work that the code and git do not show
	KindReference Kind = "reference" // pointers to external resources
)

// Kinds lists the valid kinds in their canonical order.
func Kinds() []Kind { return []Kind{KindUser, KindFeedback, KindProject, KindReference} }

// Valid reports whether k is one of the four kinds.
func (k Kind) Valid() bool {
	switch k {
	case KindUser, KindFeedback, KindProject, KindReference:
		return true
	}
	return false
}

// ParseKind converts s to a Kind, rejecting unknown values.
func ParseKind(s string) (Kind, error) {
	k := Kind(strings.TrimSpace(strings.ToLower(s)))
	if !k.Valid() {
		return "", fmt.Errorf("%w: unknown kind %q (want user, feedback, project or reference)", ErrInvalid, s)
	}
	return k, nil
}

// Scope kinds. people is reserved for a later phase; it parses but no backend
// is registered for it by default.
const (
	ScopeUser    = "user"
	ScopeProject = "project"
	ScopePeople  = "people"
)

// Scope selects one memory namespace: {"user",""}, {"project","<key>"} or the
// reserved {"people","<name>"}.
type Scope struct {
	Kind string
	Key  string
}

// UserScope returns the user scope.
func UserScope() Scope { return Scope{Kind: ScopeUser} }

// ProjectScope returns the scope of the project identified by key.
func ProjectScope(key string) Scope { return Scope{Kind: ScopeProject, Key: key} }

// String renders the scope as "user" or "<kind>:<key>".
func (s Scope) String() string {
	if s.Key == "" {
		return s.Kind
	}
	return s.Kind + ":" + s.Key
}

// Validate checks the scope kind and key: user has no key, project and
// people require one, and keys must be a single printable line.
func (s Scope) Validate() error {
	switch s.Kind {
	case ScopeUser:
		if s.Key != "" {
			return fmt.Errorf("%w: user scope takes no key, got %q", ErrInvalid, s.Key)
		}
		return nil
	case ScopeProject, ScopePeople:
		if strings.TrimSpace(s.Key) == "" {
			return fmt.Errorf("%w: %s scope needs a key", ErrInvalid, s.Kind)
		}
		if strings.IndexFunc(s.Key, unicode.IsControl) >= 0 {
			return fmt.Errorf("%w: scope key %q contains control characters", ErrInvalid, s.Key)
		}
		return nil
	}
	return fmt.Errorf("%w: unknown scope %q (want user, project:<key> or people:<name>)", ErrInvalid, s.Kind)
}

// ParseScope parses "user", "project:<key>" or "people:<name>". The key is
// everything after the first colon.
func ParseScope(v string) (Scope, error) {
	kind, key, _ := strings.Cut(strings.TrimSpace(v), ":")
	s := Scope{Kind: kind, Key: key}
	if err := s.Validate(); err != nil {
		return Scope{}, err
	}
	return s, nil
}

// Provenance records where an entry came from.
type Provenance struct {
	SessionID string
	TurnID    string
	Source    string // source category, e.g. "tool", "user", "import"
	Tainted   bool   // the writing session had seen external content
}

// Entry is one memory item.
type Entry struct {
	ID, Title, Body string
	Kind            Kind
	// Summary is the one-sentence description carried by the index line; it
	// is metadata, not part of the body.
	Summary     string
	AppliesWhen []string
	Updated     time.Time
	Prov        Provenance
}

// Action is a write operation.
type Action string

const (
	ActionAdd     Action = "add"
	ActionReplace Action = "replace"
	ActionRemove  Action = "remove"
)

// Op is a write request.
//
//   - add: Title, Body and Kind are required; ID must be empty because the
//     backend derives it from the title (see Slug and UniqueID).
//   - replace: ID, Title and Body are required; the entry is rewritten as a
//     whole. An empty Kind keeps the stored kind.
//   - remove: only ID is used. Backends return the removed entry (with its
//     title and body) so the governance layer can tombstone its content.
type Op struct {
	Action Action
	ID     string
	Title  string
	Body   string
	Kind   Kind
	// Summary is the one-sentence description stored in the index line. add
	// fills it from the body's first sentence when it is empty; replace keeps
	// the stored summary when it is empty.
	Summary string
	// AppliesWhen records versions, environments or other conditions under
	// which this entry remains useful. It is optional and never enters Index.
	AppliesWhen []string
	Prov        Provenance
}

// Validate checks the shape of the op before any policy runs. Title and body
// emptiness is re-checked after sanitizing by Governed.
func (op Op) Validate() error {
	switch op.Action {
	case ActionAdd:
		if op.ID != "" {
			return fmt.Errorf("%w: add derives the id from the title; omit id", ErrInvalid)
		}
		if !op.Kind.Valid() {
			return fmt.Errorf("%w: add needs a kind (user, feedback, project or reference), got %q", ErrInvalid, op.Kind)
		}
		return op.validateText()
	case ActionReplace:
		if err := ValidateID(op.ID); err != nil {
			return err
		}
		if op.Kind != "" && !op.Kind.Valid() {
			return fmt.Errorf("%w: unknown kind %q", ErrInvalid, op.Kind)
		}
		return op.validateText()
	case ActionRemove:
		return ValidateID(op.ID)
	}
	return fmt.Errorf("%w: unknown action %q (want add, replace or remove)", ErrInvalid, op.Action)
}

func (op Op) validateText() error {
	if strings.TrimSpace(op.Title) == "" || strings.TrimSpace(op.Body) == "" {
		return fmt.Errorf("%w: %s needs a title and a body", ErrInvalid, op.Action)
	}
	if strings.ContainsAny(op.Title, "\r\n") {
		return fmt.Errorf("%w: title must be a single line", ErrInvalid)
	}
	if len(op.AppliesWhen) > 10 {
		return fmt.Errorf("%w: applies_when allows at most 10 items", ErrInvalid)
	}
	for _, condition := range op.AppliesWhen {
		if strings.ContainsAny(condition, "\r\n") {
			return fmt.Errorf("%w: applies_when items must be single lines", ErrInvalid)
		}
		if utf8.RuneCountInString(condition) > 200 {
			return fmt.Errorf("%w: applies_when items must be at most 200 characters", ErrInvalid)
		}
	}
	return nil
}

// SummaryMaxRunes bounds a one-line summary; longer text is cut and ends
// with "…" so one entry can never flood the resident index.
const SummaryMaxRunes = 120

// CleanSummary folds s onto one line (runs of whitespace become one space)
// and truncates it to SummaryMaxRunes runes.
func CleanSummary(s string) string {
	return truncateRunes(strings.Join(strings.Fields(s), " "), SummaryMaxRunes)
}

// truncateRunes keeps at most max runes and marks the cut with "…".
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// FirstSentence returns the first sentence of body on one line. It is the
// fallback summary when a write carries none; a '.' only ends a sentence when
// it ends the text or is followed by a space, so versions and paths survive.
func FirstSentence(body string) string {
	one := strings.Join(strings.Fields(body), " ")
	runes := []rune(one)
	for i, r := range runes {
		switch r {
		case '。', '！', '？', '!', '?':
			return CleanSummary(string(runes[:i+1]))
		case '.':
			if i+1 == len(runes) || runes[i+1] == ' ' {
				return CleanSummary(string(runes[:i+1]))
			}
		}
	}
	return CleanSummary(one)
}

// IndexLine is the resident one-line summary of an entry.
type IndexLine struct {
	ID    string
	Title string
	Kind  Kind
	// Summary is the optional one-sentence description shown after the kind.
	// Empty renders exactly like the line format before summaries existed.
	Summary string
}

// Render formats the line as it appears in the prompt index.
func (l IndexLine) Render() string {
	line := "- " + l.ID + " (" + string(l.Kind) + "): " + l.Title
	if l.Summary != "" {
		line += " — " + l.Summary
	}
	return line
}

// Index is the resident part of a scope: one line per entry.
type Index struct {
	Scope Scope
	Lines []IndexLine
	// Bytes is the size of the index as the backend stores it (for the files
	// backend: MEMORY.md including hand-written lines). Backends without a
	// stored index may leave it 0; budget checks then use len(Render()).
	Bytes int
}

// Render returns the text injected into the system prompt: a heading naming
// the scope followed by one line per entry. An empty index renders as "".
func (ix Index) Render() string {
	if len(ix.Lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Memory: " + ix.Scope.String() + "\n")
	for _, l := range ix.Lines {
		b.WriteString(l.Render())
		b.WriteByte('\n')
	}
	return b.String()
}

// size is the byte size used for budget checks.
func (ix Index) size() int {
	if ix.Bytes > 0 {
		return ix.Bytes
	}
	return len(ix.Render())
}

// Hit is one search result.
type Hit struct {
	ID      string
	Title   string
	Kind    Kind
	Snippet string
	Updated time.Time
}

// MaxIDLen bounds entry ids; slugs are cut shorter to leave room for suffixes.
const MaxIDLen = 80

const slugBaseLen = 64

// idPattern allows lowercase ASCII letters, digits and inner dashes only, so
// an id can never contain a path separator, "..", a drive letter or a dot.
var idPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// reservedIDs would collide with the index file on case-insensitive file
// systems (memory.md vs MEMORY.md).
var reservedIDs = map[string]bool{"memory": true}

// ValidateID rejects ids that are not safe file-name stems.
func ValidateID(id string) error {
	if len(id) > MaxIDLen || !idPattern.MatchString(id) || reservedIDs[id] {
		return fmt.Errorf("%w: invalid id %q (lowercase a-z, 0-9 and '-', at most %d chars)", ErrInvalid, id, MaxIDLen)
	}
	return nil
}

// Slug derives a stable id stem from a title: lowercase ASCII letters and
// digits joined by '-'. Titles with non-ASCII letters (e.g. Chinese) get a
// short hash of the normalized title appended so distinct titles stay
// distinct; a title with no ASCII letters at all becomes the hash alone.
func Slug(title string) string {
	var b strings.Builder
	nonASCII, dash := false, false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		case r > unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsNumber(r)):
			nonASCII, dash = true, true
		default:
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > slugBaseLen {
		slug = strings.TrimRight(slug[:slugBaseLen], "-")
	}
	if nonASCII || slug == "" {
		sum := sha256.Sum256([]byte(normalize(title)))
		h := hex.EncodeToString(sum[:4])
		if slug == "" {
			return h
		}
		return slug + "-" + h
	}
	return slug
}

// UniqueID returns base, or base-2, base-3, ... until the id is valid and
// taken reports false. A base that is not a valid id stem is slugged first,
// and long bases are cut so the suffix always fits MaxIDLen.
func UniqueID(base string, taken func(id string) bool) string {
	if !idPattern.MatchString(base) || len(base) > MaxIDLen {
		base = Slug(base)
	}
	id := base
	for n := 2; ValidateID(id) != nil || taken(id); n++ {
		suffix := "-" + strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > MaxIDLen {
			stem = strings.TrimRight(stem[:MaxIDLen-len(suffix)], "-")
		}
		id = stem + suffix
	}
	return id
}

// Fingerprint identifies entry content for tombstones: sha256 over the
// lowercased, whitespace-collapsed title and body.
func Fingerprint(title, body string) string {
	sum := sha256.Sum256([]byte(normalize(title) + "\n" + normalize(body)))
	return hex.EncodeToString(sum[:])
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
