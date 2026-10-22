package sugasdk

// The memory API of an engine: a thin wrapper over the same governed memory
// store the agent's memory tool writes through (same backends, same write
// policy, same provenance records).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/memory"
)

// Memory is the engine's memory API. It reads and writes the user scope and the
// project scope of the process directory, with the configured write policy
// (memory.write / memory.write_tainted).
type Memory struct {
	engine *Engine
}

// Search finds memory entries of a scope. scope is "user" or "project:<key>";
// empty means "user". limit <= 0 uses the backend's default.
func (m *Memory) Search(ctx context.Context, scope, query string, limit int) ([]MemoryEntry, error) {
	s, err := memoryScope(scope)
	if err != nil {
		return nil, err
	}
	gov, err := m.engine.memoryStore()
	if err != nil {
		return nil, err
	}
	hits, err := gov.Search(ctx, s, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]MemoryEntry, 0, len(hits))
	for _, h := range hits {
		out = append(out, MemoryEntry{ID: h.ID, Title: h.Title, Kind: string(h.Kind), Summary: h.Snippet,
			Scope: s.String(), Updated: h.Updated})
	}
	return out, nil
}

// Add writes a new memory entry (title, body and kind are required; the id is
// derived from the title) and returns it as stored.
func (m *Memory) Add(ctx context.Context, scope string, entry MemoryEntry) (MemoryEntry, error) {
	s, err := memoryScope(scope)
	if err != nil {
		return MemoryEntry{}, err
	}
	kind, err := memory.ParseKind(entry.Kind)
	if err != nil {
		return MemoryEntry{}, err
	}
	gov, err := m.engine.memoryStore()
	if err != nil {
		return MemoryEntry{}, err
	}
	stored, err := gov.Write(ctx, s, memory.Op{Action: memory.ActionAdd, Title: entry.Title, Body: entry.Body,
		Kind: kind, Summary: entry.Summary})
	if err != nil {
		return MemoryEntry{}, err
	}
	return convertMemoryEntry(s, stored), nil
}

// List returns the entries of a scope. Backends that can enumerate hand back
// the bodies too; the others are asked for their index, so only id, kind, title
// and summary are filled.
func (m *Memory) List(ctx context.Context, scope string) ([]MemoryEntry, error) {
	s, err := memoryScope(scope)
	if err != nil {
		return nil, err
	}
	gov, err := m.engine.memoryStore()
	if err != nil {
		return nil, err
	}
	backend, err := gov.Route(s)
	if err != nil {
		return nil, err
	}
	if enumerator, ok := backend.(memory.Enumerator); ok {
		entries, err := enumerator.Entries(ctx, s)
		if err != nil {
			return nil, err
		}
		out := make([]MemoryEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, convertMemoryEntry(s, e))
		}
		return out, nil
	}
	index, err := gov.Index(ctx, s)
	if err != nil {
		return nil, err
	}
	out := make([]MemoryEntry, 0, len(index.Lines))
	for _, line := range index.Lines {
		out = append(out, MemoryEntry{ID: line.ID, Title: line.Title, Kind: string(line.Kind),
			Summary: line.Summary, Scope: s.String()})
	}
	return out, nil
}

// memoryScope parses an SDK scope name; empty means the user scope.
func memoryScope(scope string) (memory.Scope, error) {
	name := strings.TrimSpace(scope)
	if name == "" {
		return memory.UserScope(), nil
	}
	s, err := memory.ParseScope(name)
	if err != nil {
		return memory.Scope{}, fmt.Errorf("sugasdk: %w", err)
	}
	if s.Kind == memory.ScopePeople {
		return memory.Scope{}, errors.New("sugasdk: the people memory scope is not available")
	}
	return s, nil
}
