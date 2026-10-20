// Package execstore is a memory.Store backed by an external command that
// speaks the suga exec protocol v1 (see package extproc): every operation runs
// the command once, writes one JSON request line to its stdin and reads one
// JSON response line from its stdout.
//
// # Wire format for third-party implementations
//
// The envelope is {"v":1,"capability":"memory","op":"<op>","args":{...}} and
// the answer {"ok":true,"result":{...}} or {"ok":false,"error":"..."}. The
// scope is always the string "user" or "project:<key>". Per op:
//
//	op "index"   args {"scope"}
//	             result {"lines":[{"id","title","kind","summary"}],"bytes":0}
//	op "get"     args {"scope","id"}
//	             result entry
//	op "write"   args {"scope","op":{"action":"add|replace|remove","id","title","body","kind","summary","applies_when"}}
//	             result entry; for "add" the backend derives the id from the
//	             title (the request id is empty); for "remove" it returns the
//	             entry as it was before removal, with title and body
//	op "search"  args {"scope","query","limit"}  (limit 0: backend default)
//	             result {"hits":[{"id","title","kind","snippet","updated"}]}
//
// An entry is {"id","title","body","kind","summary","applies_when","updated"} with kind one of user,
// feedback, project, reference and updated an RFC 3339 time (optional).
// summary is the one-sentence index line description (optional; a backend
// that ignores it just returns an empty summary).
//
// Ids must satisfy memory.ValidateID (lowercase a-z, 0-9, '-'). A missing
// entry is reported with ok=false and an error text containing "not found".
//
// Everything the command returns is untrusted: ids and kinds are validated
// here, and the host wraps the store in memory.Governed, which sanitizes all
// text before the model sees it.
package execstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/extproc"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// Name is the backend name reported by BackendName.
const Name = "exec"

// capability is the exec protocol capability of this backend.
const capability = "memory"

// Config describes the external command.
type Config struct {
	Command string
	Args    []string
	// Env holds extra KEY=VALUE entries for the child (credentials of the
	// parent environment are stripped by extproc).
	Env []string
	// Timeout bounds each call; zero means extproc.DefaultTimeout.
	Timeout time.Duration
}

// Store implements memory.Store (and memory.Named) over the command.
type Store struct {
	spec extproc.Spec
}

var (
	_ memory.Store = (*Store)(nil)
	_ memory.Named = (*Store)(nil)
)

// New validates cfg and returns the store. It does not run the command.
func New(cfg Config) (*Store, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("execstore: command is required")
	}
	return &Store{spec: extproc.Spec{Command: cfg.Command, Args: cfg.Args, Env: cfg.Env, Timeout: cfg.Timeout}}, nil
}

// BackendName implements memory.Named.
func (s *Store) BackendName() string { return Name }

// wire types -----------------------------------------------------------

type wireLine struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Summary string `json:"summary,omitempty"`
}

type wireIndex struct {
	Lines []wireLine `json:"lines"`
	Bytes int        `json:"bytes"`
}

type wireEntry struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Kind        string    `json:"kind"`
	Summary     string    `json:"summary,omitempty"`
	AppliesWhen []string  `json:"applies_when,omitempty"`
	Updated     time.Time `json:"updated"`
}

type wireHit struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Kind    string    `json:"kind"`
	Snippet string    `json:"snippet"`
	Updated time.Time `json:"updated"`
}

type wireSearch struct {
	Hits []wireHit `json:"hits"`
}

type wireOp struct {
	Action      string   `json:"action"`
	ID          string   `json:"id,omitempty"`
	Title       string   `json:"title,omitempty"`
	Body        string   `json:"body,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	AppliesWhen []string `json:"applies_when,omitempty"`
}

// call runs one request and decodes the result into out. ok=false becomes
// ErrNotFound when the error text says "not found", else a plain error.
func (s *Store) call(ctx context.Context, op string, args map[string]any, out any) error {
	var resp extproc.Response
	req := extproc.Request{V: extproc.ProtocolVersion, Capability: capability, Op: op, Args: args}
	if err := extproc.Call(ctx, s.spec, req, &resp); err != nil {
		return fmt.Errorf("memory exec %s: %w", op, err)
	}
	if !resp.OK {
		msg := strings.TrimSpace(resp.Error)
		if strings.Contains(strings.ToLower(msg), "not found") {
			return fmt.Errorf("%w: %s", memory.ErrNotFound, msg)
		}
		return fmt.Errorf("memory exec %s: backend error: %s", op, msg)
	}
	if len(resp.Result) == 0 {
		return fmt.Errorf("memory exec %s: ok response without result", op)
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("memory exec %s: invalid result: %w", op, err)
	}
	return nil
}

// Index implements memory.Store. Lines with an invalid id or kind are dropped.
func (s *Store) Index(ctx context.Context, sc memory.Scope) (memory.Index, error) {
	if err := sc.Validate(); err != nil {
		return memory.Index{}, err
	}
	var res wireIndex
	if err := s.call(ctx, "index", map[string]any{"scope": sc.String()}, &res); err != nil {
		return memory.Index{}, err
	}
	ix := memory.Index{Scope: sc, Bytes: res.Bytes}
	for _, l := range res.Lines {
		kind := memory.Kind(l.Kind)
		if memory.ValidateID(l.ID) != nil || !kind.Valid() {
			continue
		}
		ix.Lines = append(ix.Lines, memory.IndexLine{ID: l.ID, Title: l.Title, Kind: kind, Summary: l.Summary})
	}
	return ix, nil
}

// Get implements memory.Store. The returned id must equal the requested one.
func (s *Store) Get(ctx context.Context, sc memory.Scope, id string) (memory.Entry, error) {
	if err := sc.Validate(); err != nil {
		return memory.Entry{}, err
	}
	if err := memory.ValidateID(id); err != nil {
		return memory.Entry{}, err
	}
	var res wireEntry
	if err := s.call(ctx, "get", map[string]any{"scope": sc.String(), "id": id}, &res); err != nil {
		return memory.Entry{}, err
	}
	return decodeEntry(res, id)
}

// Write implements memory.Store. For replace and remove the returned id must
// equal op.ID; for add it must be a valid id.
func (s *Store) Write(ctx context.Context, sc memory.Scope, op memory.Op) (memory.Entry, error) {
	if err := sc.Validate(); err != nil {
		return memory.Entry{}, err
	}
	if err := op.Validate(); err != nil {
		return memory.Entry{}, err
	}
	w := wireOp{Action: string(op.Action), ID: op.ID, Title: op.Title, Body: op.Body, Kind: string(op.Kind),
		Summary: op.Summary, AppliesWhen: op.AppliesWhen}
	var res wireEntry
	if err := s.call(ctx, "write", map[string]any{"scope": sc.String(), "op": w}, &res); err != nil {
		return memory.Entry{}, err
	}
	return decodeEntry(res, op.ID)
}

// Search implements memory.Store. Hits with an invalid id or kind are dropped.
func (s *Store) Search(ctx context.Context, sc memory.Scope, q string, limit int) ([]memory.Hit, error) {
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	var res wireSearch
	if err := s.call(ctx, "search", map[string]any{"scope": sc.String(), "query": q, "limit": limit}, &res); err != nil {
		return nil, err
	}
	var hits []memory.Hit
	for _, h := range res.Hits {
		kind := memory.Kind(h.Kind)
		if memory.ValidateID(h.ID) != nil || !kind.Valid() {
			continue
		}
		hits = append(hits, memory.Hit{ID: h.ID, Title: h.Title, Kind: kind, Snippet: h.Snippet, Updated: h.Updated})
	}
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// decodeEntry validates an entry returned by the command. wantID, when not
// empty, is the id the caller asked for.
func decodeEntry(w wireEntry, wantID string) (memory.Entry, error) {
	if err := memory.ValidateID(w.ID); err != nil {
		return memory.Entry{}, fmt.Errorf("memory exec: backend returned a bad entry: %w", err)
	}
	if wantID != "" && w.ID != wantID {
		return memory.Entry{}, fmt.Errorf("%w: backend returned id %q for %q", memory.ErrInvalid, w.ID, wantID)
	}
	kind := memory.Kind(w.Kind)
	if !kind.Valid() {
		return memory.Entry{}, fmt.Errorf("%w: backend returned unknown kind %q", memory.ErrInvalid, w.Kind)
	}
	return memory.Entry{ID: w.ID, Title: w.Title, Body: w.Body, Kind: kind, Summary: w.Summary,
		AppliesWhen: w.AppliesWhen, Updated: w.Updated}, nil
}
