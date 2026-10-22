// Package memindex adds a rebuildable SQLite FTS projection to a memory Store.
package memindex

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// Store is an index-first wrapper for an enumerable backend. It deliberately
// keeps the underlying Store as the fallback so manifest paths, unbound
// runtimes, and damaged derived data retain the original search behavior.
type Store struct {
	base memory.Store
	enum memory.Enumerator

	mu sync.Mutex
	db *store.Store
}

var (
	_ memory.Store      = (*Store)(nil)
	_ memory.Named      = (*Store)(nil)
	_ memory.Enumerator = (*Store)(nil)
)

// New wraps base when it can enumerate entries; non-enumerable backends remain
// fully supported and simply delegate Search.
func New(base memory.Store) *Store {
	indexed := &Store{base: base}
	if enum, ok := base.(memory.Enumerator); ok {
		indexed.enum = enum
	}
	return indexed
}

// Bind attaches the session database used for the derived index. Binding is
// late because manifest construction intentionally has no session database.
func (s *Store) Bind(repo *store.Store) {
	s.mu.Lock()
	s.db = repo
	s.mu.Unlock()
}

// BackendName preserves the concrete backend name through the wrapper.
func (s *Store) BackendName() string { return memory.BackendName(s.base) }

func (s *Store) Index(ctx context.Context, scope memory.Scope) (memory.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.Index(ctx, scope)
}

func (s *Store) Get(ctx context.Context, scope memory.Scope, id string) (memory.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.Get(ctx, scope, id)
}

func (s *Store) Write(ctx context.Context, scope memory.Scope, op memory.Op) (memory.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.Write(ctx, scope, op)
}

// SetSummary implements memory.Summarizer by forwarding to the base backend, so
// the derived FTS layer never hides a capability of the Markdown files it wraps.
func (s *Store) SetSummary(ctx context.Context, scope memory.Scope, id, summary string) error {
	sum, ok := s.base.(memory.Summarizer)
	if !ok {
		return fmt.Errorf("memory: backend %s cannot store a summary separately", memory.BackendName(s.base))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return sum.SetSummary(ctx, scope, id, summary)
}

// Entries implements memory.Enumerator by forwarding to the wrapped backend.
// The wrapper only needs to add Search, and hiding enumeration from callers
// (e.g. the read-only memory lint report) would force one Get per entry.
func (s *Store) Entries(ctx context.Context, scope memory.Scope) ([]memory.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enum == nil {
		return nil, fmt.Errorf("memory: backend %s cannot enumerate entries", memory.BackendName(s.base))
	}
	return s.enum.Entries(ctx, scope)
}

// Search synchronizes from Markdown before querying FTS. Any enumeration,
// synchronization, or query failure is non-fatal: memory search falls back to
// the backend's established substring behavior rather than hiding entries.
func (s *Store) Search(ctx context.Context, scope memory.Scope, q string, limit int) ([]memory.Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil || s.enum == nil {
		return s.fallback(ctx, scope, q, limit, "index is not bound")
	}

	entries, err := s.enum.Entries(ctx, scope)
	if err != nil {
		return s.fallback(ctx, scope, q, limit, "enumerate entries", err)
	}
	sources := make([]store.MemorySource, 0, len(entries))
	byID := make(map[string]memory.Entry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
		// applies_when is searchable like the title and body (design: an entry
		// is found by the versions or environments it applies to).
		text := entry.Title + "\n" + entry.Body
		if len(entry.AppliesWhen) > 0 {
			text += "\n" + strings.Join(entry.AppliesWhen, "\n")
		}
		sources = append(sources, store.MemorySource{ID: entry.ID, Text: text, Updated: entry.Updated})
	}
	if _, _, err := s.db.SyncMemoryIndex(ctx, scope.String(), sources); err != nil {
		return s.fallback(ctx, scope, q, limit, "sync index", err)
	}
	hits, err := s.db.SearchMemoryIndex(ctx, scope.String(), q, limit)
	if err != nil {
		return s.fallback(ctx, scope, q, limit, "query index", err)
	}
	out := make([]memory.Hit, 0, len(hits))
	for _, hit := range hits {
		entry, ok := byID[hit.ID]
		if !ok {
			continue // a file disappeared between enumeration and query.
		}
		out = append(out, memory.Hit{ID: entry.ID, Title: entry.Title, Kind: entry.Kind,
			Snippet: hit.Snippet, Updated: hit.Updated})
	}
	return out, nil
}

func (s *Store) fallback(ctx context.Context, scope memory.Scope, q string, limit int, reason string, cause ...error) ([]memory.Hit, error) {
	if len(cause) > 0 {
		slog.Debug("memory FTS search unavailable; using files substring search", "scope", scope, "reason", reason, "err", cause[0])
	} else {
		slog.Debug("memory FTS search unavailable; using files substring search", "scope", scope, "reason", reason)
	}
	return s.base.Search(ctx, scope, q, limit)
}
