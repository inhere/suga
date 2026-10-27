package sugasdk

// The memory API of an engine: a thin wrapper over the same governed memory
// store the agent's memory tool writes through (same backends, same write
// policy, same provenance records).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/personal/store"
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
	repo, err := m.engine.memoryDatabase()
	if err != nil {
		return nil, err
	}
	backend, err := gov.Route(s)
	if err != nil {
		return nil, err
	}
	hits, err := gov.Search(ctx, s, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]MemoryEntry, 0, len(hits))
	for _, h := range hits {
		uuid, err := repo.EnsureMemoryUUID(ctx, memory.BackendName(backend), s, h.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, MemoryEntry{UUID: uuid, ID: h.ID, Title: h.Title, Kind: string(h.Kind), Summary: h.Snippet,
			Scope: s.String(), Updated: h.Updated})
	}
	return out, nil
}

// Add writes a new memory entry (title, body and kind are required; the id is
// derived from the title) and returns it as stored, including its UUID.
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
	repo, err := m.engine.memoryDatabase()
	if err != nil {
		return MemoryEntry{}, err
	}
	stored, err := gov.Write(ctx, s, memory.Op{Action: memory.ActionAdd, Title: entry.Title, Body: entry.Body,
		Kind: kind, Summary: entry.Summary})
	if err != nil {
		return MemoryEntry{}, err
	}
	return m.withUUID(ctx, repo, gov, s, stored)
}

// Get returns a live memory entry by its stable UUID. Deleted UUIDs remain in
// the metadata table as tombstones but cannot be used to resurrect content.
func (m *Memory) Get(ctx context.Context, uuid string) (MemoryEntry, error) {
	repo, err := m.engine.memoryDatabase()
	if err != nil {
		return MemoryEntry{}, err
	}
	ref, err := repo.MemoryRefByUUID(ctx, strings.TrimSpace(uuid))
	if err != nil {
		return MemoryEntry{}, err
	}
	s, err := memory.ParseScope(ref.Scope)
	if err != nil {
		return MemoryEntry{}, fmt.Errorf("sugasdk: invalid memory scope for uuid: %w", err)
	}
	gov, err := m.engine.memoryStore()
	if err != nil {
		return MemoryEntry{}, err
	}
	stored, err := gov.Get(ctx, s, ref.EntryID)
	if err != nil {
		return MemoryEntry{}, err
	}
	out := convertMemoryEntry(s, stored)
	out.UUID = ref.UUID
	return out, nil
}

// Update replaces the content addressed by uuid. The operation goes through
// Governed, so taint confirmation, sanitization, budgets and tombstones remain
// identical to memory-tool writes.
func (m *Memory) Update(ctx context.Context, uuid string, entry MemoryEntry) (MemoryEntry, error) {
	repo, gov, scope, ref, err := m.resolveUUID(ctx, uuid)
	if err != nil {
		return MemoryEntry{}, err
	}
	kind, err := parseOptionalMemoryKind(entry.Kind)
	if err != nil {
		return MemoryEntry{}, err
	}
	stored, err := gov.Write(ctx, scope, memory.Op{Action: memory.ActionReplace, ID: ref.EntryID,
		Title: entry.Title, Body: entry.Body, Kind: kind, Summary: entry.Summary})
	if err != nil {
		return MemoryEntry{}, err
	}
	return m.withUUID(ctx, repo, gov, scope, stored)
}

// Remove tombstones the memory addressed by uuid through the governed store.
func (m *Memory) Remove(ctx context.Context, uuid string) error {
	_, gov, scope, ref, err := m.resolveUUID(ctx, uuid)
	if err != nil {
		return err
	}
	_, err = gov.Write(ctx, scope, memory.Op{Action: memory.ActionRemove, ID: ref.EntryID})
	return err
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
	repo, err := m.engine.memoryDatabase()
	if err != nil {
		return nil, err
	}
	backend, err := gov.Route(s)
	if err != nil {
		return nil, err
	}
	backendName := memory.BackendName(backend)
	if enumerator, ok := backend.(memory.Enumerator); ok {
		entries, err := enumerator.Entries(ctx, s)
		if err != nil {
			return nil, err
		}
		out := make([]MemoryEntry, 0, len(entries))
		for _, e := range entries {
			item, err := m.withUUID(ctx, repo, gov, s, e)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	}
	index, err := gov.Index(ctx, s)
	if err != nil {
		return nil, err
	}
	out := make([]MemoryEntry, 0, len(index.Lines))
	for _, line := range index.Lines {
		uuid, err := repo.EnsureMemoryUUID(ctx, backendName, s, line.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, MemoryEntry{UUID: uuid, ID: line.ID, Title: line.Title, Kind: string(line.Kind),
			Summary: line.Summary, Scope: s.String()})
	}
	return out, nil
}

func (m *Memory) resolveUUID(ctx context.Context, uuid string) (*store.Store, *memory.Governed, memory.Scope, store.MemoryRef, error) {
	repo, err := m.engine.memoryDatabase()
	if err != nil {
		return nil, nil, memory.Scope{}, store.MemoryRef{}, err
	}
	ref, err := repo.MemoryRefByUUID(ctx, strings.TrimSpace(uuid))
	if err != nil {
		return nil, nil, memory.Scope{}, store.MemoryRef{}, err
	}
	scope, err := memory.ParseScope(ref.Scope)
	if err != nil {
		return nil, nil, memory.Scope{}, store.MemoryRef{}, err
	}
	gov, err := m.engine.memoryStore()
	if err != nil {
		return nil, nil, memory.Scope{}, store.MemoryRef{}, err
	}
	return repo, gov, scope, ref, nil
}

func (m *Memory) withUUID(ctx context.Context, repo *store.Store, gov *memory.Governed, scope memory.Scope, entry memory.Entry) (MemoryEntry, error) {
	backend, err := gov.Route(scope)
	if err != nil {
		return MemoryEntry{}, err
	}
	uuid, err := repo.EnsureMemoryUUID(ctx, memory.BackendName(backend), scope, entry.ID)
	if err != nil {
		return MemoryEntry{}, err
	}
	out := convertMemoryEntry(scope, entry)
	out.UUID = uuid
	return out, nil
}

func parseOptionalMemoryKind(kind string) (memory.Kind, error) {
	if strings.TrimSpace(kind) == "" {
		return "", nil
	}
	return memory.ParseKind(kind)
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
