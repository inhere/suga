package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/redact"
)

// Default index budget per scope (design D6). Over-budget writes fail; the
// index is never truncated silently.
const (
	DefaultMaxLines = 200
	DefaultMaxBytes = 16 * 1024
)

// Budget limits the index of one scope. Zero or negative fields use the
// defaults.
type Budget struct {
	MaxLines int
	MaxBytes int
}

// Event types passed to Policy.Observe.
const (
	EventWrite  = "memory_write"
	EventDenied = "memory_denied"
)

// Deny reasons carried by memory_denied events.
const (
	ReasonInvalid           = "invalid"
	ReasonNeedsConfirmation = "needs_confirmation"
	ReasonDenied            = "denied"
	ReasonTombstoned        = "tombstoned"
	ReasonOverBudget        = "over_budget"
)

// Event describes a governed write or refusal.
type Event struct {
	Type    string // EventWrite or EventDenied
	Scope   Scope
	Backend string
	Action  Action
	ID      string // entry id; empty for a refused add
	Kind    Kind
	Tainted bool
	Source  string
	Reason  string // Reason* code, only for EventDenied
	Detail  string // error text, only for EventDenied
}

// Policy carries everything Governed needs from the host. agentcore does not
// know how sessions, taint or confirmations are stored.
type Policy struct {
	// Tainted reports whether the current session has seen external content.
	// nil means never tainted.
	Tainted func(ctx context.Context) bool
	// Confirm asks the user to approve a write from a tainted session; a nil
	// error approves. A nil Confirm refuses every tainted write
	// (ErrNeedsConfirmation). It receives the op as requested, before
	// sanitizing, so implementations should sanitize what they display.
	Confirm func(ctx context.Context, s Scope, op Op) error
	// Sanitize cleans titles and bodies on write and all text read back from
	// the backend. Defaults to redact.Clean.
	Sanitize func(string) string
	// Budget limits each scope's index. Zero fields use the defaults.
	Budget Budget
	// Observe receives memory_write / memory_denied events. Optional.
	Observe func(ctx context.Context, ev Event)
	// Provenance stores provenance and tombstones for backends that do not
	// implement ProvenanceStore themselves. Optional: without either, writes
	// are not recorded and tombstones are not enforced.
	Provenance ProvenanceStore
}

// Governed applies Policy to every write of the wrapped store and sanitizes
// everything it reads back. Wrap a Router (or a single backend) with it; all
// backends get the same governance, the built-in one included.
type Governed struct {
	next Store
	p    Policy
}

var _ Store = (*Governed)(nil)

// NewGoverned wraps next with policy p, filling in defaults.
func NewGoverned(next Store, p Policy) *Governed {
	if p.Sanitize == nil {
		p.Sanitize = redact.Clean
	}
	if p.Budget.MaxLines <= 0 {
		p.Budget.MaxLines = DefaultMaxLines
	}
	if p.Budget.MaxBytes <= 0 {
		p.Budget.MaxBytes = DefaultMaxBytes
	}
	return &Governed{next: next, p: p}
}

// BackendName reports the wrapped store's name.
func (g *Governed) BackendName() string { return BackendName(g.next) }

// Route exposes the wrapped store's routing so Governed can itself be
// wrapped or inspected like a Router.
func (g *Governed) Route(s Scope) (Store, error) { return g.backend(s) }

// Write runs the governed write pipeline. The order is part of the contract:
//
//  1. taint gate (Tainted → Confirm), first so nothing below can bypass it
//  2. Sanitize title and body
//  3. tombstone check on the content fingerprint (add/replace)
//  4. budget check on the simulated index (add/replace)
//  5. backend write
//  6. provenance: Record for add/replace, Tombstone for remove
//  7. memory_write event
//
// Refusals in steps 1-4 never reach the backend and emit memory_denied.
func (g *Governed) Write(ctx context.Context, s Scope, op Op) (Entry, error) {
	if err := s.Validate(); err != nil {
		return Entry{}, g.deny(ctx, s, op, false, ReasonInvalid, err)
	}
	if err := op.Validate(); err != nil {
		return Entry{}, g.deny(ctx, s, op, false, ReasonInvalid, err)
	}
	backend, err := g.backend(s)
	if err != nil {
		return Entry{}, err
	}

	// 1. Taint gate.
	tainted := g.p.Tainted != nil && g.p.Tainted(ctx)
	if tainted {
		op.Prov.Tainted = true
		if g.p.Confirm == nil {
			return Entry{}, g.deny(ctx, s, op, true, ReasonNeedsConfirmation,
				fmt.Errorf("%w: the session has read external content and cannot ask the user", ErrNeedsConfirmation))
		}
		if cerr := g.p.Confirm(ctx, s, op); cerr != nil {
			return Entry{}, g.deny(ctx, s, op, true, ReasonDenied, fmt.Errorf("%w: %w", ErrDenied, cerr))
		}
	}

	name := BackendName(backend)
	prov := g.provenance(backend)
	var fp string
	if op.Action != ActionRemove {
		// 2. Sanitize, then re-check that something is left.
		op.Title = strings.TrimSpace(g.p.Sanitize(op.Title))
		op.Body = strings.TrimSpace(g.p.Sanitize(op.Body))
		op.Summary = CleanSummary(g.p.Sanitize(op.Summary))
		for i, condition := range op.AppliesWhen {
			if i == 0 {
				op.AppliesWhen = append([]string(nil), op.AppliesWhen...)
			}
			op.AppliesWhen[i] = strings.TrimSpace(g.p.Sanitize(condition))
		}
		if verr := op.validateText(); verr != nil {
			return Entry{}, g.deny(ctx, s, op, tainted, ReasonInvalid, verr)
		}

		// 3. Tombstones: removed content must not come back silently.
		fp = Fingerprint(op.Title, op.Body)
		if prov != nil {
			hit, terr := prov.IsTombstoned(ctx, name, s, fp)
			if terr != nil {
				return Entry{}, fmt.Errorf("memory: check tombstones: %w", terr)
			}
			if hit {
				return Entry{}, g.deny(ctx, s, op, tainted, ReasonTombstoned,
					fmt.Errorf("%w: %q was removed from %s before; ask the user before storing it again", ErrTombstoned, op.Title, s))
			}
		}

		// 4. Budget on the index as it would be after the write.
		if berr := g.checkBudget(ctx, s, op); berr != nil {
			if errors.Is(berr, ErrOverBudget) {
				return Entry{}, g.deny(ctx, s, op, tainted, ReasonOverBudget, berr)
			}
			return Entry{}, berr
		}
	}

	// 5. Backend write.
	e, err := g.next.Write(ctx, s, op)
	if err != nil {
		return Entry{}, err
	}
	if e.Prov == (Provenance{}) {
		e.Prov = op.Prov
	}
	e.Prov.Tainted = e.Prov.Tainted || tainted

	// 6. Provenance and tombstones. The write already happened, so a failure
	// here is reported together with the entry.
	var perr error
	if prov != nil {
		if op.Action == ActionRemove {
			rfp := ""
			if e.Title != "" || e.Body != "" {
				rfp = Fingerprint(g.p.Sanitize(e.Title), g.p.Sanitize(e.Body))
			}
			perr = prov.Tombstone(ctx, name, s, op.ID, rfp)
		} else {
			perr = prov.Record(ctx, name, s, e, fp)
		}
		if perr != nil {
			perr = fmt.Errorf("memory: %s %s done, but recording provenance failed: %w", op.Action, e.ID, perr)
		}
	}

	// 7. Event.
	g.observe(ctx, Event{Type: EventWrite, Scope: s, Backend: name, Action: op.Action, ID: e.ID,
		Kind: e.Kind, Tainted: tainted, Source: op.Prov.Source})
	return g.cleanEntry(e), perr
}

// Index returns the backend index with sanitized titles; lines whose id is
// not a valid id are dropped because the backend output is untrusted.
func (g *Governed) Index(ctx context.Context, s Scope) (Index, error) {
	if err := s.Validate(); err != nil {
		return Index{}, err
	}
	ix, err := g.next.Index(ctx, s)
	if err != nil {
		return Index{}, err
	}
	lines := make([]IndexLine, 0, len(ix.Lines))
	for _, l := range ix.Lines {
		if ValidateID(l.ID) != nil {
			continue
		}
		l.Title = g.clean(l.Title)
		l.Summary = CleanSummary(g.clean(l.Summary))
		lines = append(lines, l)
	}
	ix.Scope, ix.Lines = s, lines
	return ix, nil
}

// Get returns a sanitized entry.
func (g *Governed) Get(ctx context.Context, s Scope, id string) (Entry, error) {
	if err := s.Validate(); err != nil {
		return Entry{}, err
	}
	if err := ValidateID(id); err != nil {
		return Entry{}, err
	}
	e, err := g.next.Get(ctx, s, id)
	if err != nil {
		return Entry{}, err
	}
	return g.cleanEntry(e), nil
}

// Search returns sanitized hits; hits with invalid ids are dropped.
func (g *Governed) Search(ctx context.Context, s Scope, q string, limit int) ([]Hit, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(q) == "" {
		return nil, fmt.Errorf("%w: empty search query", ErrInvalid)
	}
	hits, err := g.next.Search(ctx, s, q, limit)
	if err != nil {
		return nil, err
	}
	out := hits[:0:0]
	for _, h := range hits {
		if ValidateID(h.ID) != nil {
			continue
		}
		h.Title, h.Snippet = g.clean(h.Title), g.clean(h.Snippet)
		out = append(out, h)
	}
	return out, nil
}

// checkBudget simulates the index after op and fails with *BudgetError when
// either limit would be exceeded. Byte deltas use the generic line rendering,
// so for backends with a richer index format the byte check is an estimate.
func (g *Governed) checkBudget(ctx context.Context, s Scope, op Op) error {
	ix, err := g.next.Index(ctx, s)
	if err != nil {
		return fmt.Errorf("memory: read index for budget: %w", err)
	}
	lines, size := len(ix.Lines), ix.size()
	newLine := IndexLine{ID: op.ID, Title: op.Title, Kind: op.Kind, Summary: op.Summary}
	switch op.Action {
	case ActionAdd:
		newLine.ID = Slug(op.Title)
		lines++
		size += len(newLine.Render()) + 1
	case ActionReplace:
		for _, l := range ix.Lines {
			if l.ID == op.ID {
				if newLine.Kind == "" {
					newLine.Kind = l.Kind
				}
				size += len(newLine.Render()) - len(l.Render())
				break
			}
		}
	}
	b := g.p.Budget
	if lines > b.MaxLines || size > b.MaxBytes {
		return &BudgetError{Scope: s, Lines: lines, Bytes: size, MaxLines: b.MaxLines, MaxBytes: b.MaxBytes}
	}
	return nil
}

// backend resolves the concrete store for s when next is a Resolver.
func (g *Governed) backend(s Scope) (Store, error) {
	if r, ok := g.next.(Resolver); ok {
		return r.Route(s)
	}
	return g.next, nil
}

// provenance prefers the backend's own ProvenanceStore over the injected one.
func (g *Governed) provenance(backend Store) ProvenanceStore {
	if ps, ok := backend.(ProvenanceStore); ok {
		return ps
	}
	return g.p.Provenance
}

func (g *Governed) deny(ctx context.Context, s Scope, op Op, tainted bool, reason string, err error) error {
	name := ""
	if b, berr := g.backend(s); berr == nil {
		name = BackendName(b)
	}
	g.observe(ctx, Event{Type: EventDenied, Scope: s, Backend: name, Action: op.Action, ID: op.ID,
		Kind: op.Kind, Tainted: tainted, Source: op.Prov.Source, Reason: reason, Detail: err.Error()})
	return err
}

func (g *Governed) observe(ctx context.Context, ev Event) {
	if g.p.Observe != nil {
		g.p.Observe(ctx, ev)
	}
}

func (g *Governed) clean(s string) string { return g.p.Sanitize(s) }

func (g *Governed) cleanEntry(e Entry) Entry {
	e.Title, e.Body = g.clean(e.Title), g.clean(e.Body)
	e.Summary = CleanSummary(g.clean(e.Summary))
	if len(e.AppliesWhen) > 0 {
		cleaned := make([]string, len(e.AppliesWhen))
		for i, condition := range e.AppliesWhen {
			cleaned[i] = g.clean(condition)
		}
		e.AppliesWhen = cleaned
	}
	return e
}
