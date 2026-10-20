// Package gofermem is the gofer memory backend: project memory stored as
// gofer notes through the `gofer memory` CLI, so every agent working in the
// repository sees it in its gofer prime context.
//
// Mapping (design "数据", D11, Q2, Q3):
//
//   - entry id   ↔ gofer key "suga-<kind>-<id>"
//   - title      → --summary (gofer limits it to 80 characters)
//   - body       → the content argument
//   - kind       → --kind note always; the suga kind goes into --tag suga,<kind>
//   - provenance → --source suga:<session> ("suga" without a session)
//
// The backend owns only entries that carry the "suga" tag AND have a key of
// the form above. Everything else in the shared gofer memory (rules, notes and
// handoffs of other agents or humans) is never listed, returned, modified or
// removed, even if its key happens to start with "suga-". The tag filter is
// applied locally on every read; the --tag flag passed to gofer only narrows
// the output and is not relied upon.
//
// gofer finds the tracker from its working directory, so Config.Dir must be
// the workspace (repository) root. Only the project scope is served; its key
// is not passed to gofer because the tracker is the project.
package gofermem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/inhere/suga/pkg/agentcore/extproc"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

const (
	// Name is the backend name reported by BackendName.
	Name = "gofer"
	// Tag marks every gofer memory written by suga; only tagged entries are
	// visible to this backend.
	Tag = "suga"
	// KeyPrefix starts every gofer key written by suga.
	KeyPrefix = "suga-"
	// MaxTitleRunes is gofer's summary limit; longer titles are rejected
	// instead of truncated so the title round-trips unchanged.
	MaxTitleRunes = 80

	defaultBin         = "gofer"
	defaultTimeout     = 15 * time.Second
	defaultSearchLimit = 10
	maxStdoutBytes     = 8 << 20
	stderrTailBytes    = 2048
	snippetRadius      = 80
)

// Config configures the backend.
type Config struct {
	// Bin is the gofer executable (default "gofer", looked up in PATH).
	Bin string
	// Dir is the working directory of every gofer call: the workspace root,
	// from which gofer discovers .gofer/tracker. Required.
	Dir string
	// Timeout bounds each gofer call (default 15s).
	Timeout time.Duration
	// Env holds extra environment entries appended after the parent
	// environment has been stripped of credentials (extproc.StripCredentials).
	// gofer's own settings such as GOFER_CONFIG_DIR are not credentials and
	// are inherited anyway.
	Env []string
}

// Store implements memory.Store over the gofer CLI.
type Store struct {
	cfg Config
	mu  sync.Mutex // serializes read-modify-write sequences of this process
	now func() time.Time
}

var (
	_ memory.Store = (*Store)(nil)
	_ memory.Named = (*Store)(nil)
)

// New validates cfg and returns a Store. No gofer process is started here.
func New(cfg Config) (*Store, error) {
	if strings.TrimSpace(cfg.Dir) == "" {
		return nil, fmt.Errorf("%w: gofer memory backend needs the workspace directory", memory.ErrInvalid)
	}
	if cfg.Bin == "" {
		cfg.Bin = defaultBin
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Store{cfg: cfg, now: time.Now}, nil
}

// BackendName implements memory.Named.
func (s *Store) BackendName() string { return Name }

// record is one gofer memory as printed by `gofer memory ... --json`
// (tracker.Memory; only the fields used here).
type record struct {
	Key       string   `json:"key"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
	UpdatedAt string   `json:"updated_at"`
	Kind      string   `json:"kind,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Source    string   `json:"source,omitempty"`
}

// Key returns the gofer key of entry id with kind k.
func Key(k memory.Kind, id string) string { return KeyPrefix + string(k) + "-" + id }

// ParseKey splits a gofer key written by suga into kind and id. Keys that do
// not follow "suga-<kind>-<id>" with a valid kind and id are rejected.
func ParseKey(key string) (memory.Kind, string, bool) {
	rest, ok := strings.CutPrefix(key, KeyPrefix)
	if !ok {
		return "", "", false
	}
	kind, id, ok := strings.Cut(rest, "-")
	if !ok {
		return "", "", false
	}
	k := memory.Kind(kind)
	if !k.Valid() || memory.ValidateID(id) != nil {
		return "", "", false
	}
	return k, id, true
}

// owned reports whether r belongs to suga: it carries the suga tag and its
// key maps to a kind and id. This is the single guard that keeps the backend
// away from every other gofer memory.
func owned(r record) (memory.Kind, string, bool) {
	if !hasTag(r.Tags, Tag) {
		return "", "", false
	}
	return ParseKey(r.Key)
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// title is the stored summary, or the first content line when a tagged entry
// was edited by hand without one.
//
// gofer memories carry a single summary slot, which this backend already uses
// for the title; there is no second field for the suga one-sentence index
// summary, so index lines of this backend carry none (the title itself is the
// one-line description shown in gofer's own index).
func (r record) title() string {
	if t := strings.TrimSpace(r.Summary); t != "" {
		return t
	}
	line, _, _ := strings.Cut(strings.TrimSpace(r.Content), "\n")
	return strings.TrimSpace(line)
}

func (r record) updated() time.Time {
	t, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (r record) entry(k memory.Kind, id string) memory.Entry {
	return memory.Entry{ID: id, Title: r.title(), Body: r.Content, Kind: k, Updated: r.updated()}
}

// ownedEntry pairs an owned record with its parsed kind and id.
type ownedEntry struct {
	rec  record
	kind memory.Kind
	id   string
}

// ownedOnly keeps the suga entries of recs. When a crashed kind change left
// two keys with the same id, the most recently updated one wins.
func ownedOnly(recs []record) map[string]ownedEntry {
	out := map[string]ownedEntry{}
	for _, r := range recs {
		k, id, ok := owned(r)
		if !ok {
			continue
		}
		if prev, dup := out[id]; dup && !r.updated().After(prev.rec.updated()) {
			continue
		}
		out[id] = ownedEntry{rec: r, kind: k, id: id}
	}
	return out
}

func checkScope(sc memory.Scope) error {
	if sc.Kind != memory.ScopeProject {
		return fmt.Errorf("%w: the gofer memory backend only serves the project scope, got %q", memory.ErrInvalid, sc.String())
	}
	return sc.Validate()
}

// Index implements memory.Store: one line per suga entry, sorted by id so the
// rendered index (and its snapshot hash) is stable.
func (s *Store) Index(ctx context.Context, sc memory.Scope) (memory.Index, error) {
	if err := checkScope(sc); err != nil {
		return memory.Index{}, err
	}
	recs, err := s.list(ctx, "", true)
	if err != nil {
		return memory.Index{}, err
	}
	ix := memory.Index{Scope: sc}
	for _, o := range ownedOnly(recs) {
		ix.Lines = append(ix.Lines, memory.IndexLine{ID: o.id, Title: o.rec.title(), Kind: o.kind})
	}
	sort.Slice(ix.Lines, func(i, j int) bool { return ix.Lines[i].ID < ix.Lines[j].ID })
	return ix, nil
}

// Get implements memory.Store: the key is resolved from the suga listing,
// then read with `gofer memory show` and checked again for the suga tag.
func (s *Store) Get(ctx context.Context, sc memory.Scope, id string) (memory.Entry, error) {
	if err := checkScope(sc); err != nil {
		return memory.Entry{}, err
	}
	if err := memory.ValidateID(id); err != nil {
		return memory.Entry{}, err
	}
	o, err := s.get(ctx, id)
	if err != nil {
		return memory.Entry{}, err
	}
	return o.rec.entry(o.kind, o.id), nil
}

// get resolves and reads one owned entry; anything not owned is ErrNotFound.
func (s *Store) get(ctx context.Context, id string) (ownedEntry, error) {
	recs, err := s.list(ctx, "", true)
	if err != nil {
		return ownedEntry{}, err
	}
	o, ok := ownedOnly(recs)[id]
	if !ok {
		return ownedEntry{}, notFound(id)
	}
	out, err := s.run(ctx, "show", nil, o.rec.Key)
	if err != nil {
		if isNotFound(err) {
			return ownedEntry{}, notFound(id)
		}
		return ownedEntry{}, err
	}
	var r record
	if err := json.Unmarshal(out, &r); err != nil {
		return ownedEntry{}, fmt.Errorf("gofermem: decode `gofer memory show %s`: %w", o.rec.Key, err)
	}
	// Re-check on the authoritative read: the entry may have been re-tagged
	// or replaced by someone else since the listing.
	k, rid, ok := owned(r)
	if !ok || rid != id || r.Key != o.rec.Key {
		return ownedEntry{}, notFound(id)
	}
	return ownedEntry{rec: r, kind: k, id: id}, nil
}

// Write implements memory.Store.
//
//   - add: the id is derived from the title and made unique against every
//     key in gofer (suga or not), so an add never overwrites a foreign entry.
//   - replace: the entry must be an owned one (see get); a kind change moves
//     it to the new key (set the new key first, then remove the old one).
//   - remove: the owned entry is read first and returned after `rm`.
func (s *Store) Write(ctx context.Context, sc memory.Scope, op memory.Op) (memory.Entry, error) {
	if err := checkScope(sc); err != nil {
		return memory.Entry{}, err
	}
	if err := op.Validate(); err != nil {
		return memory.Entry{}, err
	}
	if op.Action != memory.ActionRemove && utf8.RuneCountInString(op.Title) > MaxTitleRunes {
		return memory.Entry{}, fmt.Errorf("%w: title is %d characters; gofer memory summaries allow at most %d",
			memory.ErrInvalid, utf8.RuneCountInString(op.Title), MaxTitleRunes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	switch op.Action {
	case memory.ActionAdd:
		all, err := s.list(ctx, "", false)
		if err != nil {
			return memory.Entry{}, err
		}
		keys := keySet(all)
		taken := func(id string) bool {
			for _, k := range memory.Kinds() {
				if keys[Key(k, id)] {
					return true
				}
			}
			return false
		}
		id := memory.UniqueID(memory.Slug(op.Title), taken)
		return s.set(ctx, op.Kind, id, op)

	case memory.ActionReplace:
		old, err := s.get(ctx, op.ID)
		if err != nil {
			return memory.Entry{}, err
		}
		kind := op.Kind
		if kind == "" {
			kind = old.kind
		}
		if kind == old.kind {
			return s.set(ctx, kind, op.ID, op)
		}
		// Kind change: the key embeds the kind. Refuse to land on a key that
		// already exists (foreign or a leftover) rather than overwrite it.
		all, err := s.list(ctx, "", false)
		if err != nil {
			return memory.Entry{}, err
		}
		if newKey := Key(kind, op.ID); keySet(all)[newKey] {
			return memory.Entry{}, fmt.Errorf("%w: gofer key %s already exists; cannot change %s to kind %s",
				memory.ErrInvalid, newKey, op.ID, kind)
		}
		e, err := s.set(ctx, kind, op.ID, op)
		if err != nil {
			return memory.Entry{}, err
		}
		if _, err := s.run(ctx, "rm", nil, old.rec.Key); err != nil && !isNotFound(err) {
			return e, fmt.Errorf("gofermem: %s moved to %s but removing the old key failed: %w", old.rec.Key, Key(kind, op.ID), err)
		}
		return e, nil

	default: // remove
		old, err := s.get(ctx, op.ID)
		if err != nil {
			return memory.Entry{}, err
		}
		if _, err := s.run(ctx, "rm", nil, old.rec.Key); err != nil {
			if isNotFound(err) {
				return memory.Entry{}, notFound(op.ID)
			}
			return memory.Entry{}, err
		}
		return old.rec.entry(old.kind, old.id), nil
	}
}

// set writes one suga entry with `gofer memory set` (create or overwrite).
func (s *Store) set(ctx context.Context, k memory.Kind, id string, op memory.Op) (memory.Entry, error) {
	source := "suga"
	if sid := strings.TrimSpace(op.Prov.SessionID); sid != "" {
		source = "suga:" + sid
	}
	key := Key(k, id)
	flags := []string{"--kind", "note", "--tag", Tag + "," + string(k), "--summary", op.Title, "--source", source}
	out, err := s.run(ctx, "set", flags, key, op.Body)
	if err != nil {
		return memory.Entry{}, err
	}
	e := memory.Entry{ID: id, Title: op.Title, Body: op.Body, Kind: k, Updated: s.now().UTC(), Prov: op.Prov}
	var r record
	if json.Unmarshal(out, &r) == nil && r.Key == key {
		if t := r.updated(); !t.IsZero() {
			e.Updated = t
		}
	}
	return e, nil
}

// Search implements memory.Store: gofer narrows by the first term, then all
// terms must match the title or body (case-insensitive AND, like the files
// backend). Hits are ordered newest first, then by id.
func (s *Store) Search(ctx context.Context, sc memory.Scope, q string, limit int) ([]memory.Hit, error) {
	if err := checkScope(sc); err != nil {
		return nil, err
	}
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) == 0 {
		return nil, fmt.Errorf("%w: empty search query", memory.ErrInvalid)
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	recs, err := s.list(ctx, terms[0], true)
	if err != nil {
		return nil, err
	}
	var hits []memory.Hit
	for _, o := range ownedOnly(recs) {
		title := o.rec.title()
		text := title + "\n" + o.rec.Content
		lower := strings.ToLower(text)
		if !containsAll(lower, terms) {
			continue
		}
		hits = append(hits, memory.Hit{ID: o.id, Title: title, Kind: o.kind, Updated: o.rec.updated(),
			Snippet: snippet(text, terms[0])})
	}
	sort.Slice(hits, func(i, j int) bool {
		if !hits[i].Updated.Equal(hits[j].Updated) {
			return hits[i].Updated.After(hits[j].Updated)
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// list runs `gofer memory ls --json` with an optional keyword. sugaOnly
// passes --tag suga to narrow the output; callers still filter with owned.
func (s *Store) list(ctx context.Context, kw string, sugaOnly bool) ([]record, error) {
	var flags []string
	if sugaOnly {
		flags = []string{"--tag", Tag}
	}
	var pos []string
	if kw != "" {
		pos = []string{kw}
	}
	out, err := s.run(ctx, "ls", flags, pos...)
	if err != nil {
		return nil, err
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 || bytes.Equal(out, []byte("null")) {
		return nil, nil
	}
	var recs []record
	if err := json.Unmarshal(out, &recs); err != nil {
		return nil, fmt.Errorf("gofermem: decode `gofer memory ls --json`: %w", err)
	}
	return recs, nil
}

func keySet(recs []record) map[string]bool {
	m := make(map[string]bool, len(recs))
	for _, r := range recs {
		m[r.Key] = true
	}
	return m
}

// cmdError is a failed gofer call.
type cmdError struct {
	sub    string
	err    error
	stderr string
}

func (e *cmdError) Error() string {
	msg := fmt.Sprintf("gofermem: gofer memory %s failed: %v", e.sub, e.err)
	if e.stderr != "" {
		msg += ": " + e.stderr
	}
	return msg
}

func (e *cmdError) Unwrap() error { return e.err }

// isNotFound recognizes gofer's missing-key errors ("memory not found: k"
// from show, "memory k not found" from rm).
func isNotFound(err error) bool {
	var ce *cmdError
	return errors.As(err, &ce) && strings.Contains(ce.stderr, "not found")
}

func notFound(id string) error {
	return fmt.Errorf("%w: %s (gofer project memory)", memory.ErrNotFound, id)
}

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// run executes `gofer memory <sub> --json <flags...> -- <pos...>` in
// cfg.Dir. Positional arguments always follow "--" so a key or body starting
// with "-" is never parsed as an option. No --global/--project flag is ever
// passed: only the repository tracker is used.
func (s *Store) run(ctx context.Context, sub string, flags []string, pos ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	args := append([]string{"memory", sub, "--json"}, flags...)
	args = append(args, "--")
	args = append(args, pos...)
	cmd := exec.CommandContext(ctx, s.cfg.Bin, args...)
	cmd.Dir = s.cfg.Dir
	cmd.Env = extproc.StripCredentials(os.Environ(), s.cfg.Env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Without WaitDelay a grandchild holding the pipes would block Wait after the kill.
	cmd.WaitDelay = time.Second

	runErr := cmd.Run()
	tail := stderrTail(stderr.Bytes())
	if ctxErr := ctx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
		return nil, &cmdError{sub: sub, err: fmt.Errorf("timed out after %s", s.cfg.Timeout), stderr: tail}
	} else if ctxErr != nil {
		return nil, &cmdError{sub: sub, err: ctxErr, stderr: tail}
	}
	if runErr != nil {
		return nil, &cmdError{sub: sub, err: runErr, stderr: tail}
	}
	if stdout.Len() > maxStdoutBytes {
		return nil, &cmdError{sub: sub, err: fmt.Errorf("output exceeds %d bytes", maxStdoutBytes)}
	}
	return stdout.Bytes(), nil
}

// stderrTail returns the last bytes of stderr without terminal colors.
func stderrTail(b []byte) string {
	s := strings.TrimSpace(ansiPattern.ReplaceAllString(string(b), ""))
	if len(s) > stderrTailBytes {
		s = s[len(s)-stderrTailBytes:]
		for len(s) > 0 && !utf8.RuneStart(s[0]) {
			s = s[1:]
		}
	}
	return s
}

func containsAll(s string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(s, t) {
			return false
		}
	}
	return true
}

// snippet returns the text around the first case-insensitive match of term
// (lowercase), cut on rune boundaries, on one line.
func snippet(text, term string) string {
	runes := []rune(text)
	lower := []rune(strings.ToLower(text))
	tr := []rune(term)
	at := 0
	if len(lower) == len(runes) { // lowercasing kept rune positions aligned
		for i := 0; i+len(tr) <= len(lower); i++ {
			if string(lower[i:i+len(tr)]) == term {
				at = i
				break
			}
		}
	}
	start, end := max(0, at-snippetRadius), min(len(runes), at+len(tr)+snippetRadius)
	out := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
