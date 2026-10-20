// Package files is the built-in memory backend: per scope one MEMORY.md
// index plus one Markdown file per entry. It needs nothing but the standard
// library, so memory works out of the box.
//
// Two layouts are supported:
//
//   - DirLayout{Dir}: index <Dir>/MEMORY.md, entries <Dir>/<id>.md; used for
//     user memory under <SUGA_HOME>/memory/user and for project memory without
//     a repository MEMORY.md, under <SUGA_HOME>/projects/<key>/memory.
//   - RepoLayout{Root}: index <Root>/MEMORY.md, entries
//     <Root>/.suga/memory/<id>.md (project memory kept in the repository).
//
// Index lines have the form "- [Title](relative/path.md) — kind". Lines that
// do not match (headings, prose, other lists) are hand-written and preserved
// verbatim at their position; they are read-only for the backend. New
// entries are appended at the end of MEMORY.md.
//
// All file access goes through os.Root, so ids and symlinks cannot reach
// outside the layout root. Directories are created 0700, files 0600, and
// every file is replaced atomically (temp file + rename). A Store serializes
// its own writes; concurrent writers in other processes are not coordinated
// (last index write wins).
package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/inhere/suga/pkg/agentcore/memory"
)

// Name is the backend name reported by BackendName.
const Name = "files"

const (
	dirPerm  = 0o700
	filePerm = 0o600
	// repoEntryDir is where RepoLayout keeps entry files, relative to Root.
	repoEntryDir = ".suga/memory"
	// defaultSearchLimit applies when Search is called with limit <= 0.
	defaultSearchLimit = 10
)

// Layout places a scope's index and entry files. It is implemented by
// DirLayout and RepoLayout only.
type Layout interface {
	paths() layoutPaths
}

// DirLayout keeps MEMORY.md and <id>.md files side by side in Dir. Dir is
// created on the first write.
type DirLayout struct{ Dir string }

// RepoLayout keeps MEMORY.md at the repository root and entries under
// .suga/memory/. Root must already exist; it is never created.
type RepoLayout struct{ Root string }

type layoutPaths struct {
	root       string // absolute directory all access is confined to
	entryDir   string // entry directory relative to root, slash separated ("" = root)
	createRoot bool   // create root on write when missing
}

func (l DirLayout) paths() layoutPaths  { return layoutPaths{root: l.Dir, createRoot: true} }
func (l RepoLayout) paths() layoutPaths { return layoutPaths{root: l.Root, entryDir: repoEntryDir} }

// Store implements memory.Store over files.
type Store struct {
	mu      sync.Mutex // serializes read-modify-write of indexes
	layouts map[memory.Scope]layoutPaths
	now     func() time.Time
}

var (
	_ memory.Store      = (*Store)(nil)
	_ memory.Named      = (*Store)(nil)
	_ memory.Enumerator = (*Store)(nil)
)

// Option configures a Store.
type Option func(*Store)

// WithClock sets the time source for Updated (tests).
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// New builds a Store with one layout per scope (exact Scope match, e.g.
// memory.UserScope() and memory.ProjectScope("<key>")). Layout roots must be
// absolute paths.
func New(layouts map[memory.Scope]Layout, opts ...Option) (*Store, error) {
	s := &Store{layouts: map[memory.Scope]layoutPaths{}, now: time.Now}
	for sc, l := range layouts {
		if err := sc.Validate(); err != nil {
			return nil, err
		}
		if l == nil {
			return nil, fmt.Errorf("%w: nil layout for scope %s", memory.ErrInvalid, sc)
		}
		p := l.paths()
		if p.root == "" || !filepath.IsAbs(p.root) {
			return nil, fmt.Errorf("%w: layout root for scope %s must be an absolute path, got %q", memory.ErrInvalid, sc, p.root)
		}
		p.root = filepath.Clean(p.root)
		s.layouts[sc] = p
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// BackendName implements memory.Named.
func (s *Store) BackendName() string { return Name }

// Index implements memory.Store. A missing layout root or MEMORY.md is an
// empty index.
func (s *Store) Index(_ context.Context, sc memory.Scope) (memory.Index, error) {
	ix := memory.Index{Scope: sc}
	root, lp, err := s.open(sc, false)
	if err != nil || root == nil {
		return ix, err
	}
	defer root.Close()
	data, err := readFile(root, indexFile)
	if err != nil {
		return ix, err
	}
	ix.Lines = parseIndex(data, lp.entryDir).entries()
	ix.Bytes = len(data)
	return ix, nil
}

// Get implements memory.Store. Missing title or kind in the entry file fall
// back to the index line.
func (s *Store) Get(_ context.Context, sc memory.Scope, id string) (memory.Entry, error) {
	if err := memory.ValidateID(id); err != nil {
		return memory.Entry{}, err
	}
	root, lp, err := s.open(sc, false)
	if err != nil {
		return memory.Entry{}, err
	}
	if root == nil {
		return memory.Entry{}, notFound(sc, id)
	}
	defer root.Close()
	e, err := readEntry(root, lp, id)
	if err != nil {
		return memory.Entry{}, notFoundOr(err, sc, id)
	}
	if e.Title == "" || !e.Kind.Valid() || e.Summary == "" {
		data, ierr := readFile(root, indexFile)
		if ierr != nil {
			return memory.Entry{}, ierr
		}
		doc := parseIndex(data, lp.entryDir)
		if i := doc.find(id); i >= 0 {
			fillFromIndex(&e, doc.lines[i].line)
		}
	}
	return e, nil
}

// Write implements memory.Store: the entry file is written first, then the
// index, so a crash leaves at most an unlisted file, never a dangling line.
func (s *Store) Write(_ context.Context, sc memory.Scope, op memory.Op) (memory.Entry, error) {
	if err := op.Validate(); err != nil {
		return memory.Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	root, lp, err := s.open(sc, op.Action != memory.ActionRemove)
	if err != nil {
		return memory.Entry{}, err
	}
	if root == nil { // remove from a scope that has no files yet
		return memory.Entry{}, notFound(sc, op.ID)
	}
	defer root.Close()

	data, err := readFile(root, indexFile)
	if err != nil {
		return memory.Entry{}, err
	}
	doc := parseIndex(data, lp.entryDir)

	switch op.Action {
	case memory.ActionAdd:
		id := memory.UniqueID(memory.Slug(op.Title), func(id string) bool {
			return doc.find(id) >= 0 || exists(root, entryPath(lp, id))
		})
		e := memory.Entry{ID: id, Title: op.Title, Body: op.Body, Kind: op.Kind, Summary: memory.CleanSummary(op.Summary),
			AppliesWhen: append([]string(nil), op.AppliesWhen...), Updated: s.stamp(), Prov: op.Prov}
		return e, s.store(root, lp, doc, e)

	case memory.ActionReplace:
		old, rerr := readEntry(root, lp, op.ID)
		i := doc.find(op.ID)
		if rerr != nil && (!errors.Is(rerr, fs.ErrNotExist) || i < 0) {
			return memory.Entry{}, notFoundOr(rerr, sc, op.ID)
		}
		if i >= 0 {
			fillFromIndex(&old, doc.lines[i].line)
		}
		kind := op.Kind
		if kind == "" {
			kind = old.Kind
		}
		if !kind.Valid() {
			return memory.Entry{}, fmt.Errorf("%w: entry %s has no valid kind; pass one", memory.ErrInvalid, op.ID)
		}
		// An empty summary on replace keeps the stored one, like the kind.
		summary := memory.CleanSummary(op.Summary)
		if summary == "" {
			summary = old.Summary
		}
		e := memory.Entry{ID: op.ID, Title: op.Title, Body: op.Body, Kind: kind, Summary: summary,
			AppliesWhen: append([]string(nil), op.AppliesWhen...), Updated: s.stamp(), Prov: op.Prov}
		return e, s.store(root, lp, doc, e)

	default: // remove
		old, rerr := readEntry(root, lp, op.ID)
		i := doc.find(op.ID)
		if rerr != nil && (!errors.Is(rerr, fs.ErrNotExist) || i < 0) {
			return memory.Entry{}, notFoundOr(rerr, sc, op.ID)
		}
		if i >= 0 {
			fillFromIndex(&old, doc.lines[i].line)
		}
		old.ID = op.ID
		if rerr == nil {
			if err := root.Remove(entryPath(lp, op.ID)); err != nil {
				return memory.Entry{}, fmt.Errorf("memory/files: remove %s: %w", op.ID, err)
			}
		}
		if i >= 0 {
			doc.remove(op.ID)
			if err := writeAtomic(root, indexFile, doc.render()); err != nil {
				return memory.Entry{}, err
			}
		}
		return old, nil
	}
}

// Search implements memory.Store: every entry file is scanned and matched
// case-insensitively against all whitespace-separated terms of q (AND) in
// its title and body. Hits are ordered by Updated (newest first), then id.
func (s *Store) Search(ctx context.Context, sc memory.Scope, q string, limit int) ([]memory.Hit, error) {
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) == 0 {
		return nil, fmt.Errorf("%w: empty search query", memory.ErrInvalid)
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	entries, err := s.Entries(ctx, sc)
	if err != nil {
		return nil, err
	}

	var hits []memory.Hit
	for _, e := range entries {
		text := e.Title + "\n" + e.Body
		if len(e.AppliesWhen) > 0 {
			text += "\napplies_when: " + strings.Join(e.AppliesWhen, "\napplies_when: ")
		}
		lower := strings.ToLower(text)
		if !containsAll(lower, terms) {
			continue
		}
		hits = append(hits, memory.Hit{ID: e.ID, Title: e.Title, Kind: e.Kind, Updated: e.Updated,
			Snippet: snippet(text, lower, terms[0])})
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

// store writes the entry file then the updated index.
func (s *Store) store(root *os.Root, lp layoutPaths, doc *indexDoc, e memory.Entry) error {
	if lp.entryDir != "" {
		if err := root.MkdirAll(filepath.FromSlash(lp.entryDir), dirPerm); err != nil {
			return fmt.Errorf("memory/files: create entry dir: %w", err)
		}
	}
	if err := writeAtomic(root, entryPath(lp, e.ID), renderEntry(e)); err != nil {
		return err
	}
	doc.set(memory.IndexLine{ID: e.ID, Title: e.Title, Kind: e.Kind, Summary: e.Summary}, lp.entryDir)
	return writeAtomic(root, indexFile, doc.render())
}

// SetSummary implements memory.Summarizer: only the MEMORY.md line changes,
// the entry file keeps its body untouched. An empty summary removes the
// summary from the line (back to the old format).
func (s *Store) SetSummary(_ context.Context, sc memory.Scope, id, summary string) error {
	if err := memory.ValidateID(id); err != nil {
		return err
	}
	summary = memory.CleanSummary(summary)
	s.mu.Lock()
	defer s.mu.Unlock()
	root, lp, err := s.open(sc, false)
	if err != nil {
		return err
	}
	if root == nil {
		return notFound(sc, id)
	}
	defer root.Close()
	data, err := readFile(root, indexFile)
	if err != nil {
		return err
	}
	doc := parseIndex(data, lp.entryDir)
	i := doc.find(id)
	if i < 0 {
		return notFound(sc, id)
	}
	line := doc.lines[i].line
	line.Summary = summary
	doc.set(line, lp.entryDir)
	return writeAtomic(root, indexFile, doc.render())
}

func (s *Store) stamp() time.Time { return s.now().UTC().Truncate(time.Second) }

// open returns the os.Root of the scope's layout. With create, a DirLayout
// root is created; otherwise a missing root yields (nil, paths, nil).
func (s *Store) open(sc memory.Scope, create bool) (*os.Root, layoutPaths, error) {
	lp, ok := s.layouts[sc]
	if !ok {
		return nil, lp, fmt.Errorf("%w: files backend has no layout for scope %s", memory.ErrNoBackend, sc)
	}
	if create && lp.createRoot {
		if err := os.MkdirAll(lp.root, dirPerm); err != nil {
			return nil, lp, fmt.Errorf("memory/files: create %s: %w", lp.root, err)
		}
	}
	root, err := os.OpenRoot(lp.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !create {
			return nil, lp, nil
		}
		return nil, lp, fmt.Errorf("memory/files: open %s: %w", lp.root, err)
	}
	return root, lp, nil
}

// entryPath is the OS path of an entry file relative to the root. id must
// already be validated (memory.ValidateID), so it cannot contain separators.
func entryPath(lp layoutPaths, id string) string {
	return filepath.Join(filepath.FromSlash(lp.entryDir), id+".md")
}

func readEntry(root *os.Root, lp layoutPaths, id string) (memory.Entry, error) {
	data, err := root.ReadFile(entryPath(lp, id))
	if err != nil {
		return memory.Entry{}, err
	}
	e := parseEntry(string(data))
	e.ID = id
	return e, nil
}

// readFile returns "" for a missing file.
func readFile(root *os.Root, name string) (string, error) {
	data, err := root.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("memory/files: read %s: %w", name, err)
	}
	return string(data), nil
}

func exists(root *os.Root, name string) bool {
	_, err := root.Lstat(name)
	return err == nil
}

// writeAtomic writes data to a temp file in the target directory (0600) and
// renames it over name, so readers never see a partial file.
func writeAtomic(root *os.Root, name string, data []byte) error {
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmp := filepath.Join(filepath.Dir(name), "."+filepath.Base(name)+".tmp-"+hex.EncodeToString(rnd[:]))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return fmt.Errorf("memory/files: write %s: %w", name, err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("memory/files: write %s: %w", name, err)
	}
	return nil
}

func fillFromIndex(e *memory.Entry, l memory.IndexLine) {
	if e.Title == "" {
		e.Title = l.Title
	}
	if !e.Kind.Valid() {
		e.Kind = l.Kind
	}
	if e.Summary == "" {
		e.Summary = l.Summary
	}
}

func notFound(sc memory.Scope, id string) error {
	return fmt.Errorf("%w: %s in %s", memory.ErrNotFound, id, sc)
}

// notFoundOr maps a missing file to memory.ErrNotFound and keeps other
// errors (e.g. a symlink escaping the root) as they are.
func notFoundOr(err error, sc memory.Scope, id string) error {
	if errors.Is(err, fs.ErrNotExist) {
		return notFound(sc, id)
	}
	return fmt.Errorf("memory/files: read %s: %w", id, err)
}

func containsAll(s string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(s, t) {
			return false
		}
	}
	return true
}

// snippet returns about 160 bytes of text around the first match of term,
// on one line. Offsets come from the lowercased text; when lowercasing
// changed byte lengths the snippet is cut from the lowercased text instead.
func snippet(text, lower, term string) string {
	const before, after = 60, 100
	src := text
	if len(lower) != len(text) {
		src = lower
	}
	i := strings.Index(lower, term)
	if i < 0 {
		i = 0
	}
	start, end := max(0, i-before), min(len(src), i+len(term)+after)
	for start > 0 && !utf8.RuneStart(src[start]) {
		start--
	}
	for end < len(src) && !utf8.RuneStart(src[end]) {
		end++
	}
	out := strings.Join(strings.Fields(src[start:end]), " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(src) {
		out += "…"
	}
	return out
}
