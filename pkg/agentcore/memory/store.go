package memory

import (
	"context"
	"errors"
	"fmt"
)

// Store is implemented by every memory backend (files, gofer, exec) and by
// the Governed and Router wrappers.
//
// Backends report a missing entry with an error wrapping ErrNotFound and
// invalid input with ErrInvalid. Content returned by a backend is treated as
// untrusted; Governed sanitizes it on the way out.
type Store interface {
	// Index returns the resident part of the scope: one line per entry.
	Index(ctx context.Context, s Scope) (Index, error)
	// Get returns one entry by id.
	Get(ctx context.Context, s Scope, id string) (Entry, error)
	// Write applies an add, replace or remove op and returns the resulting
	// entry; for remove it returns the entry as it was before removal.
	Write(ctx context.Context, s Scope, op Op) (Entry, error)
	// Search returns entries matching q, at most limit (<= 0: backend default).
	Search(ctx context.Context, s Scope, q string, limit int) ([]Hit, error)
}

// Named is optionally implemented by backends to report a stable name such
// as "files", "gofer" or "exec" (used in provenance rows and the manifest).
type Named interface {
	BackendName() string
}

// Enumerator is optionally implemented by a backend that can enumerate all
// readable entries in a scope. It supports rebuildable derived indexes without
// changing Store's four-method contract.
type Enumerator interface {
	Entries(ctx context.Context, s Scope) ([]Entry, error)
}

// Summarizer is optionally implemented by a backend that can rewrite the
// one-sentence index summary of an entry without touching its body or title.
// It backs the maintenance command that fills missing summaries.
type Summarizer interface {
	SetSummary(ctx context.Context, s Scope, id, summary string) error
}

// ProvenanceStore keeps provenance and tombstones for entries. A backend that
// can store them itself implements it; otherwise the host injects one through
// Policy.Provenance (suga uses a SQLite side table). backend is the backend
// name (see Named) so one store can serve several backends.
type ProvenanceStore interface {
	// Record upserts the provenance of an added or replaced entry together
	// with the fingerprint of its content (see Fingerprint).
	Record(ctx context.Context, backend string, s Scope, e Entry, fingerprint string) error
	// Tombstone marks a removed entry; fingerprint is its content fingerprint
	// so the same content cannot be written back silently.
	Tombstone(ctx context.Context, backend string, s Scope, id, fingerprint string) error
	// IsTombstoned reports whether content with this fingerprint was removed
	// from the scope before.
	IsTombstoned(ctx context.Context, backend string, s Scope, fingerprint string) (bool, error)
}

// Resolver is implemented by stores that dispatch scopes to other stores
// (Router). Governed uses it to find the concrete backend of a scope for its
// name and ProvenanceStore capability.
type Resolver interface {
	Route(s Scope) (Store, error)
}

// Errors returned by this package; test them with errors.Is.
var (
	// ErrInvalid marks malformed input: unknown kind/scope/action, bad id,
	// empty title or body.
	ErrInvalid = errors.New("memory: invalid request")
	// ErrNotFound marks a missing entry.
	ErrNotFound = errors.New("memory: entry not found")
	// ErrNeedsConfirmation: the session is tainted and the host offers no
	// way to confirm (Policy.Confirm is nil), so the write is refused.
	ErrNeedsConfirmation = errors.New("memory: write from a tainted session needs confirmation")
	// ErrDenied: the session is tainted and Confirm refused the write.
	ErrDenied = errors.New("memory: write denied")
	// ErrTombstoned: the content matches an entry that was removed before.
	ErrTombstoned = errors.New("memory: content matches a removed entry")
	// ErrOverBudget: the write would push the scope index over its budget.
	ErrOverBudget = errors.New("memory: index budget exceeded")
	// ErrNoBackend: no backend is registered for the scope.
	ErrNoBackend = errors.New("memory: no backend for scope")
)

// BudgetError details an ErrOverBudget refusal.
type BudgetError struct {
	Scope              Scope
	Lines, Bytes       int // index size after the write
	MaxLines, MaxBytes int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("memory: %s index would have %d entries / %d bytes (limit %d / %d); merge or remove old entries first",
		e.Scope, e.Lines, e.Bytes, e.MaxLines, e.MaxBytes)
}

// Unwrap lets errors.Is(err, ErrOverBudget) match.
func (e *BudgetError) Unwrap() error { return ErrOverBudget }

// BackendName returns s's Named name, or "unknown".
func BackendName(s Store) string {
	if n, ok := s.(Named); ok {
		if name := n.BackendName(); name != "" {
			return name
		}
	}
	return "unknown"
}
