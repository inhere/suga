package native

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Actions of the memory tool.
const (
	MemoryActionAdd     = "add"
	MemoryActionReplace = "replace"
	MemoryActionRemove  = "remove"
	MemoryActionGet     = "get"
	MemoryActionSearch  = "search"
)

const (
	memoryToolName          = "memory"
	memorySearchLimit       = 10
	memorySearchLimitMax    = 50
	memoryInternalBackend   = "files"
	memoryResultExternalKey = "external"
)

// MemoryToolOptions configures the memory tool.
type MemoryToolOptions struct {
	// Writable allows add/replace/remove. false (read-only runs) refuses every
	// write before the store is called; get/search stay available.
	Writable bool
	// ProjectKey is the key of the project scope (scope=project); empty
	// means the project scope is unavailable.
	ProjectKey string
	// Backend names the backend serving a scope ("files", "gofer", "exec").
	// Results read from any backend other than files are marked external so
	// the host flags the session as tainted. nil or an unknown name counts as
	// external (fail closed).
	Backend func(memory.Scope) string
	// ProvenanceFrom returns the session/turn of the call from ctx (set by
	// the host); nil leaves the provenance empty apart from Source.
	ProvenanceFrom func(ctx context.Context) memory.Provenance
}

// MemoryTool reads and writes long-term memory through a memory.Store
// (normally memory.Governed over a Router). It only changes the agent's own
// state, so it is AgentState: approval layers let it through and the tool
// enforces the read-only mode itself.
type MemoryTool struct {
	store memory.Store
	opts  MemoryToolOptions
}

// NewMemoryTool creates the memory tool over store.
func NewMemoryTool(store memory.Store, opts MemoryToolOptions) *MemoryTool {
	return &MemoryTool{store: store, opts: opts}
}

func (t *MemoryTool) Name() string { return memoryToolName }

// Description is the tool contract for the model (design D5): the four
// kinds, what is worth storing and what to leave out, and facts-vs-procedures.
func (t *MemoryTool) Description() string {
	return "Long-term memory shared across sessions. Each segment's context message carries a snapshot of the memory " +
		"index (ids, kinds and titles), frozen until the next segment; writes are searchable immediately with " +
		"action=search, and action=get reads an entry's body.\n" +
		"Kinds: user (who the user is: role, preferences, expertise), feedback (corrections and confirmed " +
		"choices about how to work), project (goals, constraints, decisions and progress the code and git " +
		"do not show), reference (where external resources live: URLs, dashboards, tickets).\n" +
		"Do NOT store: what the repository already states or makes derivable (code, README, git history); " +
		"what AGENTS.md or other rule files already say; secrets, tokens or passwords; this turn's " +
		"transient state, including a question you are waiting for the user to answer.\n" +
		"Worth storing: what will come up again, is not trivial, and is cheaper to look up than to work " +
		"out again. A dead end counts (the path and why it failed). For experience, state the conditions " +
		"it applies under (versions, environment, endpoints).\n" +
		"Facts go into memory; procedures and how-tos belong in skills.\n" +
		"Actions: add (kind, title, body, summary; optional applies_when conditions; the id is derived from the title), replace (id, title, body, summary; " +
		"rewrites the whole entry), remove (id), get (id), search (query). scope: project (default) or user. " +
		"summary is one sentence (at most 120 characters) saying what the entry covers and when to read it; it is " +
		"what the memory index shows, so write it even when the title seems clear. It is required for add: when it " +
		"is omitted the first sentence of the body is used and the result tells you so. " +
		"Prefer replace to merge an existing entry over adding a near-duplicate. When the project memory " +
		"is backed by gofer, every project entry is a gofer note that enters the start-up context of all " +
		"agents working in the repository: keep it short and generally useful."
}

// ToolMeta: an agent-state write tool (memory is suga's own state, not the
// workspace); read-only mode is enforced inside Execute.
func (*MemoryTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryEdit, AgentState: true}
}

func (t *MemoryTool) Schema() api.ToolSchema {
	kinds := make([]any, 0, 4)
	for _, k := range memory.Kinds() {
		kinds = append(kinds, string(k))
	}
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []any{MemoryActionAdd, MemoryActionReplace,
					MemoryActionRemove, MemoryActionGet, MemoryActionSearch}},
				"scope": map[string]any{"type": "string", "enum": []any{memory.ScopeProject, memory.ScopeUser},
					"description": "project (default): this repository; user: about the user, across projects"},
				"kind":  map[string]any{"type": "string", "enum": kinds, "description": "Entry kind (add; optional for replace)"},
				"title": map[string]any{"type": "string", "description": "One-line title (add, replace)"},
				"summary": map[string]any{"type": "string", "description": "One sentence (at most 120 characters) for the memory index: " +
					"what the entry covers and when to read it (add required; replace optional, an empty value keeps the stored summary)"},
				"body":         map[string]any{"type": "string", "description": "Entry text (add, replace)"},
				"applies_when": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Conditions for which this entry applies (optional, add/replace)"},
				"id":           map[string]any{"type": "string", "description": "Entry id from the index or search (replace, remove, get)"},
				"query":        map[string]any{"type": "string", "description": "Words to find (search)"},
				"limit":        map[string]any{"type": "integer", "description": "Max hits (search; default 10, at most 50)"},
			},
			"required": []any{"action"},
		},
	}
}

// memoryRequest is the validated tool input.
type memoryRequest struct {
	action string
	scope  memory.Scope
	op     memory.Op
	query  string
	limit  int
	// derivedSummary records that the model gave no summary and the body's
	// first sentence was used, so the result can say so.
	derivedSummary bool
}

func (t *MemoryTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	req, err := t.parse(call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	if t.store == nil {
		return nil, errors.New("memory: no memory store is configured")
	}
	result := func(content string, external bool) *api.ToolResult {
		res := &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: content}
		if external {
			// Per-call source declaration (D7): the observer taints the session.
			res.Metadata = map[string]any{memoryResultExternalKey: true}
		}
		return res
	}
	switch req.action {
	case MemoryActionGet:
		e, err := t.store.Get(ctx, req.scope, req.op.ID)
		if err != nil {
			return nil, t.mapError(req, err)
		}
		return result(renderMemoryEntry(req.scope, e), t.external(req.scope)), nil
	case MemoryActionSearch:
		hits, err := t.store.Search(ctx, req.scope, req.query, req.limit)
		if err != nil {
			return nil, t.mapError(req, err)
		}
		return result(renderMemoryHits(req.scope, req.query, hits), t.external(req.scope)), nil
	}

	// Writes. Read-only runs refuse before the store (and its taint gate,
	// confirmation and events) is involved at all.
	if !t.opts.Writable {
		return nil, fmt.Errorf("memory: 只读模式下不能写入记忆（%s 被拒绝）；get/search 仍可使用", req.action)
	}
	op := req.op
	if t.opts.ProvenanceFrom != nil {
		op.Prov = t.opts.ProvenanceFrom(ctx)
	}
	if op.Prov.Source == "" {
		op.Prov.Source = "tool"
	}
	// The host's confirmation (ACP permission request) needs the call id.
	ctx = context.WithValue(ctx, memoryCallKey{}, call.CallID)
	e, err := t.store.Write(ctx, req.scope, op)
	if err != nil {
		if e.ID == "" {
			return nil, t.mapError(req, err)
		}
		// The write happened; only bookkeeping (provenance) failed.
		content := renderMemoryChange(req.action, req.scope, e, req.summaryNote()) + "\nwarning: " + err.Error()
		return result(content, false), nil
	}
	return result(renderMemoryChange(req.action, req.scope, e, req.summaryNote()), false), nil
}

// summaryNote is the extra result line about a derived summary.
func (req memoryRequest) summaryNote() string {
	if !req.derivedSummary {
		return ""
	}
	return "已用正文首句作摘要：" + req.op.Summary
}

// memoryCallKey carries the tool call id of a memory write in ctx.
type memoryCallKey struct{}

// MemoryCallID returns the id of the memory tool call whose write is in
// progress (set for the store's Write, so a confirmation can refer to it).
func MemoryCallID(ctx context.Context) string {
	id, _ := ctx.Value(memoryCallKey{}).(string)
	return id
}

// parse validates the arguments per action.
func (t *MemoryTool) parse(args map[string]any) (memoryRequest, error) {
	var req memoryRequest
	str := func(key string) string {
		v, _ := args[key].(string)
		return strings.TrimSpace(v)
	}
	req.action = str("action")
	switch scope := str("scope"); scope {
	case "", memory.ScopeProject:
		if t.opts.ProjectKey == "" {
			return req, errors.New("no project memory is available here; use scope=user")
		}
		req.scope = memory.ProjectScope(t.opts.ProjectKey)
	case memory.ScopeUser:
		req.scope = memory.UserScope()
	default:
		return req, fmt.Errorf("scope %q must be project or user", scope)
	}
	id := str("id")
	switch req.action {
	case MemoryActionAdd, MemoryActionReplace:
		req.op = memory.Op{Action: memory.Action(req.action), ID: id, Title: str("title"), Body: str("body")}
		var err error
		req.op.AppliesWhen, err = parseAppliesWhen(args["applies_when"])
		if err != nil {
			return req, err
		}
		if kind := str("kind"); kind != "" {
			k, err := memory.ParseKind(kind)
			if err != nil {
				return req, err
			}
			req.op.Kind = k
		}
		if req.action == MemoryActionAdd && id != "" {
			return req, errors.New("add derives the id from the title; omit id (use replace to rewrite an entry)")
		}
		if req.action == MemoryActionAdd && req.op.Kind == "" {
			return req, errors.New("add needs kind: user, feedback, project or reference")
		}
		req.op.Summary = memory.CleanSummary(str("summary"))
		if req.action == MemoryActionAdd && req.op.Summary == "" {
			// The index line needs a description; the first sentence of the
			// body is the fallback and the result reports it.
			req.op.Summary = memory.FirstSentence(req.op.Body)
			req.derivedSummary = true
		}
		if req.action == MemoryActionReplace && id == "" {
			return req, errors.New("replace needs the id of the entry to rewrite")
		}
		if req.op.Title == "" || req.op.Body == "" {
			return req, fmt.Errorf("%s needs a title and a body", req.action)
		}
	case MemoryActionRemove, MemoryActionGet:
		if id == "" {
			return req, fmt.Errorf("%s needs an id", req.action)
		}
		if err := memory.ValidateID(id); err != nil {
			return req, err
		}
		req.op = memory.Op{ID: id}
		if req.action == MemoryActionRemove {
			req.op.Action = memory.ActionRemove
		}
	case MemoryActionSearch:
		if req.query = str("query"); req.query == "" {
			return req, errors.New("search needs a query")
		}
		req.limit = memorySearchLimit
		if n, ok := numberArg(args["limit"]); ok && n >= 1 {
			req.limit = min(int(n), memorySearchLimitMax)
		}
	case "":
		return req, errors.New("action is required: add, replace, remove, get or search")
	default:
		return req, fmt.Errorf("unknown action %q (want add, replace, remove, get or search)", req.action)
	}
	return req, nil
}

// parseAppliesWhen decodes the optional JSON array while leaving detailed
// shape and length checks to memory.Op.Validate.
func parseAppliesWhen(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	var raw []any
	switch v := value.(type) {
	case []any:
		raw = v
	case []string:
		raw = make([]any, len(v))
		for i := range v {
			raw[i] = v[i]
		}
	default:
		return nil, fmt.Errorf("%w: applies_when must be an array of strings", memory.ErrInvalid)
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%w: applies_when item %d must be a string", memory.ErrInvalid, i+1)
		}
		out[i] = strings.TrimSpace(text)
	}
	return out, nil
}

// external reports whether reads from s come from outside suga's own files.
func (t *MemoryTool) external(s memory.Scope) bool {
	if t.opts.Backend == nil {
		return true
	}
	return t.opts.Backend(s) != memoryInternalBackend
}

// mapError turns store errors into tool errors the model can act on.
// Cancellation (an aborted confirmation) is returned as tools.ErrAborted so
// the turn ends instead of feeding the cancelled question back to the model.
func (t *MemoryTool) mapError(req memoryRequest, err error) error {
	scope := req.scope.Kind
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, tools.ErrAborted):
		return fmt.Errorf("memory: %w: %w", tools.ErrAborted, err)
	case errors.Is(err, memory.ErrNeedsConfirmation):
		return errors.New("memory: 会话读过外部内容，非交互模式不能写记忆（需要用户确认的写入被拒绝）")
	case errors.Is(err, memory.ErrDenied):
		return fmt.Errorf("memory: 会话读过外部内容，写入需要确认，未获同意：%v", err)
	case errors.Is(err, memory.ErrTombstoned):
		return fmt.Errorf("memory: 该内容与之前删除的条目相同，重新写入前先征得用户确认（%v）", err)
	case errors.Is(err, memory.ErrOverBudget):
		return fmt.Errorf("memory: %s 记忆索引超出上限，先用 replace 合并或用 remove 删除旧条目再写入（%v）", scope, err)
	case errors.Is(err, memory.ErrNotFound):
		return fmt.Errorf("memory: %s 中没有条目 %q；用 action=search 查找 id", scope, req.op.ID)
	case errors.Is(err, memory.ErrNoBackend):
		return fmt.Errorf("memory: 没有为 %s 配置记忆后端", scope)
	case errors.Is(err, memory.ErrInvalid):
		return fmt.Errorf("memory: 参数无效：%v", err)
	}
	return fmt.Errorf("memory: %s %s 失败：%w", req.action, scope, err)
}

// MemoryChange is a successful memory write as reported in the tool result,
// parsed back by ParseMemoryResult (chat shows "已记住：..." from it).
type MemoryChange struct {
	Action string // add, replace or remove
	Scope  string // scope kind: project or user
	ID     string
	Title  string
}

var memoryChangeVerbs = map[string]string{
	MemoryActionAdd:     "Saved",
	MemoryActionReplace: "Updated",
	MemoryActionRemove:  "Removed",
}

// renderMemoryChange renders a write result; its first line is the stable
// "<Verb> memory <id> (<scope>): <title>" that ParseMemoryResult reads. note,
// when not empty, is appended as an extra line.
func renderMemoryChange(action string, s memory.Scope, e memory.Entry, note string) string {
	line := fmt.Sprintf("%s memory %s (%s): %s", memoryChangeVerbs[action], e.ID, s.Kind, e.Title)
	if note != "" {
		line += "\n" + note
	}
	if action == MemoryActionRemove {
		return line
	}
	return line + "\nkind: " + string(e.Kind) +
		"\nThe memory index of the current segment does not show this change; it is re-read when the " +
		"next segment starts."
}

// ParseMemoryResult reads the first line of a successful memory write result.
func ParseMemoryResult(content string) (MemoryChange, bool) {
	first, _, _ := strings.Cut(content, "\n")
	for action, verb := range memoryChangeVerbs {
		rest, ok := strings.CutPrefix(first, verb+" memory ")
		if !ok {
			continue
		}
		id, rest, ok := strings.Cut(rest, " (")
		if !ok {
			return MemoryChange{}, false
		}
		scope, title, ok := strings.Cut(rest, "): ")
		if !ok || memory.ValidateID(id) != nil || (scope != memory.ScopeProject && scope != memory.ScopeUser) {
			return MemoryChange{}, false
		}
		return MemoryChange{Action: action, Scope: scope, ID: id, Title: title}, true
	}
	return MemoryChange{}, false
}

func renderMemoryEntry(s memory.Scope, e memory.Entry) string {
	meta := fmt.Sprintf("id: %s · kind: %s · scope: %s", e.ID, e.Kind, s.Kind)
	if !e.Updated.IsZero() {
		meta += " · updated: " + e.Updated.UTC().Format(time.DateOnly)
	}
	if len(e.AppliesWhen) > 0 {
		meta += " · applies_when: " + strings.Join(e.AppliesWhen, " · ")
	}
	return "# " + e.Title + "\n" + meta + "\n\n" + e.Body
}

func renderMemoryHits(s memory.Scope, q string, hits []memory.Hit) string {
	if len(hits) == 0 {
		return fmt.Sprintf("No %s memory matched %q.", s.Kind, q)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s memory hit(s); read one with action=get:\n", len(hits), s.Kind)
	for _, h := range hits {
		fmt.Fprintf(&b, "- %s (%s): %s", h.ID, h.Kind, h.Title)
		if snippet := strings.Join(strings.Fields(h.Snippet), " "); snippet != "" {
			b.WriteString(" — " + snippet)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
