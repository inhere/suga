package native

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Ledger search scopes (session_search scope argument).
const (
	LedgerScopeCurrent   = "current"
	LedgerScopeWorkspace = "workspace"
	LedgerScopeAll       = "all"
)

// OutputRefPrefix starts every ref to an externalised tool output
// (out:<session_id>:<16 hex>); other refs point at ledger messages
// (<session_id>/<seq>).
const OutputRefPrefix = "out:"

// LedgerQuery is one session_search request.
type LedgerQuery struct {
	// Query is split on whitespace; every term must occur (AND). Terms are
	// literal text, never search syntax.
	Query string
	// Scope is current, workspace or all ("" = workspace).
	Scope string
	// Ref, when set, is an out:... ref: the search then scans that one
	// externalised output line by line and Scope is ignored.
	Ref string
	// Limit caps the hits (<= 0 = the implementation default).
	Limit int
	// Cursor resumes a process-local search snapshot returned by next_cursor.
	Cursor string
	// Kinds restrict message hits to the indexed entry roles.
	Kinds []string
	// Since is inclusive and Until is exclusive; zero means no bound.
	Since time.Time
	Until time.Time
}

// LedgerHit is one search match.
type LedgerHit struct {
	// Ref locates the match: <session_id>/<seq> for a message, the out:...
	// ref for a line of an externalised output.
	Ref string
	// Role is the entry type of a message hit (user, assistant, tool,
	// compaction, handoff); empty for line hits.
	Role string
	// Line is the 1-based line number of a hit inside an externalised output;
	// 0 for message hits.
	Line int
	// Snippet is a short excerpt around the match.
	Snippet string
}

// LedgerSearchResult is one page of ledger hits and its optional continuation
// metadata. Notice explains a stale cursor or a bounded snapshot without making
// an otherwise valid search fail.
type LedgerSearchResult struct {
	Hits       []LedgerHit
	NextCursor string
	Notice     string
	// HistoryEmpty is set by the searcher when the query's scope holds no
	// searchable entry at all: the search found nothing because there is nothing
	// there, so another wording cannot match either. It is never set for a
	// narrowed query (cursor, kind or a time bound) or for a ref search, where a
	// scope with no hit still holds history (suag-hpu7).
	HistoryEmpty bool
}

// LedgerSearcher reads the session ledger and externalised tool outputs. The
// host implements it (one real implementation, so the interface is declared
// here at the consumer, like PlanSink and ToolSearcher).
type LedgerSearcher interface {
	Search(ctx context.Context, q LedgerQuery) (LedgerSearchResult, error)
	// Fetch returns a readable window: for <session_id>/<seq> the messages
	// from seq on (offset skips messages, limit counts them); for out:... the
	// lines starting at line offset (1-based; 0 = 1), limit lines.
	Fetch(ctx context.Context, ref string, offset, limit int) (string, error)
}

// SessionSearchTool searches earlier conversation history and externalised
// outputs. Results are old, possibly external content: the tool is External
// so the session is flagged as tainted when it is used.
type SessionSearchTool struct {
	searcher LedgerSearcher
}

// NewSessionSearchTool creates session_search backed by searcher.
func NewSessionSearchTool(searcher LedgerSearcher) *SessionSearchTool {
	return &SessionSearchTool{searcher: searcher}
}

func (t *SessionSearchTool) Name() string { return "session_search" }
func (t *SessionSearchTool) Description() string {
	return "Full-text search of earlier conversation history (messages, tool results and compaction/handoff summaries). " +
		"Every whitespace-separated word must occur; words are matched literally. scope: workspace (default), " +
		"current or all. Each hit is `<ref>  <role>  <snippet>`; read it with recall(ref). " +
		"kind is comma-separated user, assistant, tool, compaction or handoff; since is inclusive and until exclusive " +
		"(7d/36h, date or RFC3339). When results include next_cursor, pass it back verbatim as cursor; a stale " +
		"cursor restarts from the beginning. For Chinese, prefer terms of two or more characters. With " +
		"ref=out:<session>:<hex> it searches saved output lines (case-insensitive); ref cannot combine with " +
		"cursor, kind or time filters. When the result says the scope holds no stored history, stop searching " +
		"rather than trying other words."
}

// ToolMeta: read-only search; results are external content (taint).
func (*SessionSearchTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategorySearch, ReadOnly: true, External: true}
}

func (t *SessionSearchTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Words to find (all must occur)"},
				"scope": map[string]any{"type": "string", "enum": []any{LedgerScopeWorkspace, LedgerScopeCurrent, LedgerScopeAll},
					"description": "Sessions to search (default workspace); ignored with ref"},
				"ref":    map[string]any{"type": "string", "description": "Optional out:<session>:<hex> ref: search inside that saved output"},
				"limit":  map[string]any{"type": "integer", "description": "Max hits (default 20, at most 100)"},
				"cursor": map[string]any{"type": "string", "description": "Value from a previous next_cursor; pass it back verbatim"},
				"kind":   map[string]any{"type": "string", "description": "Comma-separated entry kinds: user, assistant, tool, compaction, handoff"},
				"since":  map[string]any{"type": "string", "description": "Inclusive lower time bound: 7d, 36h, date or RFC3339"},
				"until":  map[string]any{"type": "string", "description": "Exclusive upper time bound: 7d, 36h, date or RFC3339"},
			},
			"required": []any{"query"},
		},
	}
}

func (t *SessionSearchTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	q, err := parseLedgerQuery(call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("session_search: %w", err)
	}
	if t.searcher == nil {
		return nil, errors.New("session_search: no ledger configured")
	}
	result, err := t.searcher.Search(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("session_search: %w", err)
	}
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: redact.Clean(renderLedgerHits(q, result))}, nil
}

// parseLedgerQuery validates and normalizes the session_search arguments.
func parseLedgerQuery(args map[string]any) (LedgerQuery, error) {
	var q LedgerQuery
	q.Query, _ = args["query"].(string)
	q.Query = strings.TrimSpace(q.Query)
	if q.Query == "" {
		return q, errors.New("query is required")
	}
	scope, _ := args["scope"].(string)
	switch scope = strings.TrimSpace(scope); scope {
	case "":
		q.Scope = LedgerScopeWorkspace
	case LedgerScopeCurrent, LedgerScopeWorkspace, LedgerScopeAll:
		q.Scope = scope
	default:
		return q, fmt.Errorf("scope %q must be current, workspace or all", scope)
	}
	ref, _ := args["ref"].(string)
	if q.Ref = strings.TrimSpace(ref); q.Ref != "" && !strings.HasPrefix(q.Ref, OutputRefPrefix) {
		return q, fmt.Errorf("ref %q: only out:<session>:<hex> refs can be searched; read a message ref with recall", q.Ref)
	}
	cursor, cursorSet, err := ledgerOptionalString(args, "cursor")
	if err != nil {
		return q, err
	}
	kindText, kindSet, err := ledgerOptionalString(args, "kind")
	if err != nil {
		return q, err
	}
	sinceText, sinceSet, err := ledgerOptionalString(args, "since")
	if err != nil {
		return q, err
	}
	untilText, untilSet, err := ledgerOptionalString(args, "until")
	if err != nil {
		return q, err
	}
	if q.Ref != "" && (cursorSet || kindSet || sinceSet || untilSet) {
		return q, errors.New("ref cannot be combined with cursor, kind, since or until")
	}
	if cursorSet {
		q.Cursor = cursor
	}
	if kindSet {
		q.Kinds, err = parseLedgerKinds(kindText)
		if err != nil {
			return q, err
		}
	}
	now := time.Now()
	if sinceSet {
		q.Since, err = parseLedgerTime(sinceText, "since", now)
		if err != nil {
			return q, err
		}
	}
	if untilSet {
		q.Until, err = parseLedgerTime(untilText, "until", now)
		if err != nil {
			return q, err
		}
	}
	if !q.Since.IsZero() && !q.Until.IsZero() && !q.Since.Before(q.Until) {
		return q, errors.New("since must be before until")
	}
	if n, ok := numberArg(args["limit"]); ok && n >= 1 {
		q.Limit = int(n)
	}
	return q, nil
}

func ledgerOptionalString(args map[string]any, name string) (string, bool, error) {
	raw, present := args[name]
	if !present {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("%s must be a string", name)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		// Models often fill every optional parameter, sending "" for the ones
		// they do not mean to use (seen with a real provider: three wasted calls
		// on "cursor/kind/since must not be empty"). An empty optional value is
		// therefore the same as leaving it out.
		return "", false, nil
	}
	return value, true, nil
}

func parseLedgerKinds(raw string) ([]string, error) {
	allowed := map[string]bool{
		"user": true, "assistant": true, "tool": true, "compaction": true, "handoff": true,
	}
	seen := make(map[string]bool)
	kinds := make([]string, 0, len(allowed))
	for _, part := range strings.Split(raw, ",") {
		kind := strings.TrimSpace(part)
		if kind == "" || !allowed[kind] {
			return nil, fmt.Errorf("invalid kind %q; use user, assistant, tool, compaction or handoff", kind)
		}
		if !seen[kind] {
			seen[kind] = true
			kinds = append(kinds, kind)
		}
	}
	return kinds, nil
}

// parseLedgerTime accepts the approved relative and absolute forms while
// keeping the parser in the native tool layer to avoid an internal dependency.
func parseLedgerTime(raw, name string, now time.Time) (time.Time, error) {
	if len(raw) > 1 {
		switch raw[len(raw)-1] {
		case 'h', 'd':
			n, err := strconv.ParseInt(raw[:len(raw)-1], 10, 64)
			if err == nil && n > 0 {
				unit := time.Hour
				if raw[len(raw)-1] == 'd' {
					unit = 24 * time.Hour
				}
				if n <= int64(time.Duration(1<<63-1)/unit) {
					return now.Add(-time.Duration(n) * unit), nil
				}
			}
			return time.Time{}, fmt.Errorf("%s %q is invalid; use 7d, 36h, a date or RFC3339", name, raw)
		}
	}
	if value, err := time.Parse(time.RFC3339, raw); err == nil {
		return value, nil
	}
	if value, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
		return value, nil
	}
	return time.Time{}, fmt.Errorf("%s %q is invalid; use 7d, 36h, a date or RFC3339", name, raw)
}

// renderLedgerHits formats hits one per line; snippets are flattened to a
// single line so a hit can never look like several. Notices explain a stale
// cursor or bounded snapshot without changing the otherwise valid result. An
// empty result says what it means: a scope with no stored history cannot be
// searched into a hit by another wording (suag-hpu7).
func renderLedgerHits(q LedgerQuery, result LedgerSearchResult) string {
	var b strings.Builder
	if result.Notice != "" {
		b.WriteString(result.Notice)
		b.WriteByte('\n')
	}
	if len(result.Hits) == 0 {
		switch {
		case q.Ref != "":
			fmt.Fprintf(&b, "No line of %s matched %q.", q.Ref, q.Query)
		case result.HistoryEmpty:
			fmt.Fprintf(&b, "No local history in scope %s: nothing is stored here to search, so another wording "+
				"cannot match either - stop searching this scope (long-term memory is a different tool).", q.Scope)
		default:
			fmt.Fprintf(&b, "No message matched %q in scope %s.", q.Query, q.Scope)
			b.WriteString("\n[the search is literal and every word must occur: before trying other words, " +
				"confirm that the stored history really holds them]")
		}
	} else {
		if q.Ref != "" {
			fmt.Fprintf(&b, "%d matching line(s) in %s; read around one with recall(ref, offset=<line>):\n", len(result.Hits), q.Ref)
		} else {
			fmt.Fprintf(&b, "%d hit(s) in scope %s; read one with recall(ref):\n", len(result.Hits), q.Scope)
		}
		for _, h := range result.Hits {
			snippet := strings.Join(strings.Fields(h.Snippet), " ")
			if h.Line > 0 {
				fmt.Fprintf(&b, "%s  L%d  %s\n", h.Ref, h.Line, snippet)
			} else {
				fmt.Fprintf(&b, "%s  %s  %s\n", h.Ref, h.Role, snippet)
			}
		}
	}
	if result.NextCursor != "" {
		fmt.Fprintf(&b, "\nnext_cursor: %s", result.NextCursor)
	}
	return strings.TrimRight(b.String(), "\n")
}

// RecallTool reads a window of earlier history or of an externalised output
// by ref. It pages on its own (Rereadable): its result is capped at the
// inline threshold here, so the output layer never externalises it again.
type RecallTool struct {
	searcher LedgerSearcher
	maxOut   int // inline threshold in bytes; 0 = DefaultMaxOutputBytes
}

// NewRecallTool creates recall backed by searcher; maxOut caps one result
// (normally the inline threshold, tools.max_output_bytes).
func NewRecallTool(searcher LedgerSearcher, maxOut int) *RecallTool {
	return &RecallTool{searcher: searcher, maxOut: maxOut}
}

func (t *RecallTool) Name() string { return "recall" }
func (t *RecallTool) Description() string {
	return "Read earlier history by ref. A message ref <session>/<seq> (from session_search) returns the " +
		"messages from that seq on: offset skips messages, limit counts them (default 20, at most 100). " +
		"An output ref out:<session>:<hex> (a saved large tool output) returns numbered lines: offset is " +
		"the 1-based start line, limit the line count (default 200, at most 2000). For a long output, first " +
		"locate lines with session_search(query, ref) and then recall a small window around them; do not page " +
		"through the whole output. Refs come from session_search, never from guessing: a ref whose session " +
		"holds no stored history has nothing to read."
}

// ToolMeta: read-only, pages on its own, results are external content.
func (*RecallTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategorySearch, ReadOnly: true, External: true, Rereadable: true}
}

func (t *RecallTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ref":    map[string]any{"type": "string", "description": "<session>/<seq> or out:<session>:<hex>"},
				"offset": map[string]any{"type": "integer", "description": "Messages to skip, or 1-based start line for out: refs"},
				"limit":  map[string]any{"type": "integer", "description": "Messages (default 20) or lines (default 200) to return"},
			},
			"required": []any{"ref"},
		},
	}
}

func (t *RecallTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	ref, _ := call.Arguments["ref"].(string)
	if ref = strings.TrimSpace(ref); ref == "" {
		return nil, errors.New("recall: ref is required")
	}
	offset, limit := 0, 0
	if n, ok := numberArg(call.Arguments["offset"]); ok {
		if n < 0 {
			return nil, errors.New("recall: offset must not be negative")
		}
		offset = int(n)
	}
	if n, ok := numberArg(call.Arguments["limit"]); ok && n >= 1 {
		limit = int(n)
	}
	if t.searcher == nil {
		return nil, errors.New("recall: no ledger configured")
	}
	text, err := t.searcher.Fetch(ctx, ref, offset, limit)
	if err != nil {
		return nil, fmt.Errorf("recall: %w", err)
	}
	text = redact.Clean(text)
	// Whole lines only, so the last line shown tells where to continue.
	if kept, _, cut := TruncateLines(text, outputLimit(t.maxOut)); cut {
		text = strings.TrimRight(kept, "\n") + fmt.Sprintf("\n[output cut at %d bytes; continue after the last line shown "+
			"(a later offset, or the next message ref) or pass a smaller limit]", outputLimit(t.maxOut))
	}
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: text}, nil
}
