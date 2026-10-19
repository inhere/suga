package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Ledger full-text index (D8).
//
// entries_fts (migration 20261010-234359-add-ledger-fts-memory-meta.sql) is an
// FTS5 trigram table with its own rowid; each row points at its entry through
// the session_id/seq columns. It is written in two places that MUST derive the
// same role/text from an entry:
//
//   - AppendEntries, for every new entry, using the constants below;
//   - the maintenance command `suga data shrink-fts` (fts_shrink.go), which
//     deletes the tool rows written before tool results left the index.
//
// Tool results are NOT indexed any more: they were ~96% of the index (~130MB on
// the 2026-10-19 ledger) while searches mostly look at user and assistant
// messages, and the full text still lives in entries for recall to read.
// ftsIndexedTypes is the single source of that decision, so the write path and
// the maintenance command cannot drift apart.
//
// This expression is deliberately NOT copied into a new migration: the old
// backfill migration (20261010) stays as it was applied, it indexed tool
// results in full, and the shrink command is what deletes those rows.
// A new startup backfill would be the very full-table work on Open that
// v0.31.0 turned into a hang, and this change adds none (design 0.3, 补充第 3 条).
const (
	// ftsIndexedTypes lists the entry types worth searching: conversation
	// messages (EntryUser, EntryAssistant) and the summaries that outlive their
	// raw context (EntryCompaction, EntryHandoff). Tool results are read with
	// recall instead (see above); plan and context_update records are structured
	// state, not prose.
	ftsIndexedTypes = `'user', 'assistant', 'compaction', 'handoff'`

	// ftsRoleExpr stores the entry type as the role.
	ftsRoleExpr = `type`

	// ftsTrimBodySQL is the body of a conversation entry (api.Message JSON):
	// Content, plus ToolResult.Content when it adds something. A stored tool
	// message repeats the result in both fields, so equal values are kept once;
	// tool entries themselves are no longer indexed, so this join only matters
	// for a non-tool entry that carries a tool result. json_extract on invalid
	// JSON would raise; the caller checks first.
	ftsTrimBodySQL = `trim(coalesce(json_extract(json, '$.Content'), '') ||
                CASE WHEN coalesce(json_extract(json, '$.ToolResult.Content'), '') IN ('', coalesce(json_extract(json, '$.Content'), ''))
                    THEN '' ELSE char(10) || json_extract(json, '$.ToolResult.Content') END, char(32, 9, 10, 13))`
)

// ftsTextExpr extracts the searchable text from entries.json:
//   - invalid JSON yields ” (json_extract would raise and fail the write);
//   - compaction and handoff records ({"summary": ...}) index their summary:
//     both are the text that stands for a stretch of past work;
//   - every other indexed type indexes its body as it is.
//
// Surrounding whitespace is trimmed; rows whose text ends up ” are skipped.
const ftsTextExpr = `CASE
            WHEN NOT json_valid(json) THEN ''
            WHEN type IN ('compaction', 'handoff') THEN trim(coalesce(json_extract(json, '$.summary'), ''), char(32, 9, 10, 13))
            ELSE ` + ftsTrimBodySQL + `
        END`

// ftsSourceSelect selects (session_id, seq, role, text) of the indexable
// entries; extra is appended to the inner WHERE (e.g. " AND seq = ?").
func ftsSourceSelect(extra string) string {
	return `SELECT session_id, seq, role, text FROM (SELECT session_id, seq, ` + ftsRoleExpr + ` AS role, ` +
		ftsTextExpr + ` AS text FROM entries WHERE type IN (` + ftsIndexedTypes + `)` + extra + `) WHERE text <> ''`
}

// ftsInsertEntrySQL indexes one just-inserted entry (args: session_id, seq).
// It is a no-op for non-indexed types and entries without text.
var ftsInsertEntrySQL = `INSERT INTO entries_fts(session_id, seq, role, text) ` +
	ftsSourceSelect(` AND session_id = ? AND seq = ?`)

// Search limits and the LIKE-path snippet radius (runes on each side).
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
	snippetRadius      = 60
	// minMatchRunes is the shortest term the trigram tokenizer can match.
	minMatchRunes = 3
)

// EntrySearch is a ledger full-text query.
type EntrySearch struct {
	// Query is split on whitespace; every term must occur (AND). Terms are
	// literal text: FTS5 syntax characters have no special meaning.
	Query string
	// SessionIDs restrict the search to these sessions; empty means all.
	SessionIDs []string
	// CWDPrefix keeps only sessions whose cwd starts with it (plain string
	// prefix, case-sensitive); empty means no restriction.
	CWDPrefix string
	// WorkspaceDir restricts sessions to this directory and its descendants.
	// The comparison is on path-separator boundaries; an empty cwd is excluded.
	WorkspaceDir string
	// CurrentSessionID is always included with WorkspaceDir, even when that
	// session has no cwd. With no WorkspaceDir it restricts results to this ID.
	CurrentSessionID string
	// FoldCase applies SQLite's ASCII-only lower() folding to the workspace
	// comparison. Ledger enables it on Windows to match the approved semantics.
	FoldCase bool
	// Limit caps the hits: <=0 means 20, at most 100.
	Limit int
	// IncludeAgents searches sub-agent sessions too (they belong to their
	// parent session and are left out by default).
	IncludeAgents bool
	// Kinds restricts results to indexed entry roles. An empty slice means all
	// indexed roles; values must be user, assistant, tool, compaction or
	// handoff. tool only matches rows an older index left behind (tool results
	// are no longer indexed).
	Kinds []string
	// Since is an inclusive lower bound on entry creation time. Zero means no
	// lower bound.
	Since time.Time
	// Until is an exclusive upper bound on entry creation time. Zero means no
	// upper bound.
	Until time.Time
	// Cursor is a process-local snapshot position. Invalid or expired cursors
	// are ignored by SearchEntriesPage, which starts a fresh query.
	Cursor string
}

// EntryHit is one matching entry, located by SessionID/Seq.
type EntryHit struct {
	SessionID string
	Seq       int64
	Role      string
	Snippet   string
	CreatedAt time.Time
}

// SearchResult contains one page and the optional continuation metadata. Notice
// is informational: a stale cursor or a snapshot truncated at 500 hits never
// turns a valid search into an error.
type SearchResult struct {
	Hits       []EntryHit
	NextCursor string
	Notice     string
}

// SearchEntries preserves the pre-cursor API for callers that only need hits.
// The page-aware API is SearchEntriesPage; it owns snapshot creation and
// continuation metadata.
func (s *Store) SearchEntries(ctx context.Context, q EntrySearch) ([]EntryHit, error) {
	result, err := s.SearchEntriesPage(ctx, q)
	return result.Hits, err
}

// searchEntries executes one bounded SQL search. Snapshot pagination asks for
// one more than the snapshot cap so the caller can distinguish truncation from
// an exactly-full result without changing the normal page limit.
func (s *Store) searchEntries(ctx context.Context, q EntrySearch, requestedLimit int) ([]EntryHit, error) {
	terms := strings.Fields(q.Query)
	if len(terms) == 0 {
		return nil, errors.New("search query is required")
	}
	if err := validateSearchKinds(q.Kinds); err != nil {
		return nil, err
	}
	limit := requestedLimit
	if limit <= 0 {
		limit = q.Limit
		if limit <= 0 {
			limit = defaultSearchLimit
		}
		limit = min(limit, maxSearchLimit)
	}

	matchTerms := make([]string, 0, len(terms))
	likeTerms := make([]string, 0, len(terms))
	for _, term := range terms {
		if utf8.RuneCountInString(term) >= minMatchRunes {
			matchTerms = append(matchTerms, term)
		} else {
			likeTerms = append(likeTerms, term)
		}
	}

	var (
		cols, where, order string
		args               []any
	)
	if len(matchTerms) > 0 {
		// Column 3 is text; up to 64 tokens (trigram: ~64 runes) of context.
		cols = `snippet(entries_fts, 3, '', '', '…', 64)`
		where = `entries_fts MATCH ?`
		args = append(args, ftsMatchExpr(matchTerms))
		order = `bm25(entries_fts), f.session_id, f.seq`
	} else {
		cols = `f.text`
		order = `e.created_at DESC, f.session_id, f.seq DESC`
	}
	if len(likeTerms) > 0 {
		likes := make([]string, len(likeTerms))
		for i, term := range likeTerms {
			likes[i] = `f.text LIKE ? ESCAPE '\'`
			args = append(args, "%"+escapeLike(term)+"%")
		}
		where = joinWhere(where, strings.Join(likes, ` AND `))
	}
	if len(q.SessionIDs) > 0 {
		where = joinWhere(where, `f.session_id IN (`+placeholders(len(q.SessionIDs))+`)`)
		for _, id := range q.SessionIDs {
			args = append(args, id)
		}
	}
	if !q.IncludeAgents {
		where = joinWhere(where, notAgentSession("ss.meta_json"))
	}
	if q.CWDPrefix != "" {
		// substr/length instead of LIKE: exact, case-sensitive, no escaping.
		where = joinWhere(where, `substr(ss.cwd, 1, length(?)) = ?`)
		args = append(args, q.CWDPrefix, q.CWDPrefix)
	}
	if q.CurrentSessionID != "" || q.WorkspaceDir != "" {
		condition, scopeArgs := workspaceScopeCondition(q)
		where = joinWhere(where, condition)
		args = append(args, scopeArgs...)
	}
	if len(q.Kinds) > 0 {
		where = joinWhere(where, `f.role IN (`+placeholders(len(q.Kinds))+`)`)
		for _, kind := range q.Kinds {
			args = append(args, kind)
		}
	}
	if !q.Since.IsZero() {
		where = joinWhere(where, `e.created_at >= ?`)
		args = append(args, formatTime(q.Since))
	}
	if !q.Until.IsZero() {
		where = joinWhere(where, `e.created_at < ?`)
		args = append(args, formatTime(q.Until))
	}
	// Search results must not recursively contain the two retrieval tools'
	// own transcripts, while other tool results remain searchable.
	where = joinWhere(where, `NOT (f.role = ? AND COALESCE(json_extract(e.json, '$.ToolCall.Name'), '') IN (?, ?))`)
	args = append(args, EntryTool, "session_search", "recall")
	// MATCH, snippet() and bm25() take the FTS hidden column, which is named
	// after the table (entries_fts) and resolves through the alias f.
	sqlText := `SELECT f.session_id, f.seq, f.role, ` + cols + `, e.created_at
		FROM entries_fts AS f
		JOIN entries AS e ON e.session_id = f.session_id AND e.seq = f.seq
		JOIN sessions AS ss ON ss.id = f.session_id
		WHERE ` + where + ` ORDER BY ` + order + fmt.Sprintf(` LIMIT %d`, limit)
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("search entries: %w", err)
	}
	defer rows.Close()
	out := []EntryHit{}
	for rows.Next() {
		var (
			h       EntryHit
			text    string
			created string
		)
		if err := rows.Scan(&h.SessionID, &h.Seq, &h.Role, &text, &created); err != nil {
			return nil, err
		}
		h.CreatedAt = parseTime(created)
		if len(matchTerms) > 0 {
			h.Snippet = text
		} else {
			h.Snippet = likeSnippet(text, terms[0])
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HasSearchableEntries reports whether the scope of q holds any searchable entry
// at all (an indexed type with text): when it does not, no wording can match
// anything here, and the caller says so instead of letting the model try one
// query after another (suag-hpu7). Query, Limit, Cursor, Since and Until are
// ignored; SessionIDs, WorkspaceDir, CurrentSessionID, FoldCase, Kinds and
// IncludeAgents pick the scope exactly as SearchEntriesPage does. The retrieval
// tools' own transcripts count as entries here (only excluding them from a match
// is cheap to skip), so a ledger holding nothing else reports true.
func (s *Store) HasSearchableEntries(ctx context.Context, q EntrySearch) (bool, error) {
	if err := validateSearchKinds(q.Kinds); err != nil {
		return false, err
	}
	var (
		where string
		args  []any
	)
	if len(q.SessionIDs) > 0 {
		where = joinWhere(where, `f.session_id IN (`+placeholders(len(q.SessionIDs))+`)`)
		for _, id := range q.SessionIDs {
			args = append(args, id)
		}
	}
	if !q.IncludeAgents {
		where = joinWhere(where, notAgentSession("ss.meta_json"))
	}
	if q.CurrentSessionID != "" || q.WorkspaceDir != "" {
		condition, scopeArgs := workspaceScopeCondition(q)
		where = joinWhere(where, condition)
		args = append(args, scopeArgs...)
	}
	if len(q.Kinds) > 0 {
		where = joinWhere(where, `f.role IN (`+placeholders(len(q.Kinds))+`)`)
		for _, kind := range q.Kinds {
			args = append(args, kind)
		}
	}
	if where == "" {
		where = "1"
	}
	// The tuning limits and the retrieval-tool exclusion of searchEntries are left
	// out on purpose: this asks whether the scope holds anything to search, and
	// the two extra predicates would make an empty scope scan the whole table.
	sqlText := `SELECT 1 FROM (` + ftsSourceSelect("") + `) AS f
		JOIN sessions AS ss ON ss.id = f.session_id
		WHERE ` + where + ` LIMIT 1`
	var one int
	switch err := s.db.QueryRowContext(ctx, sqlText, args...).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("check searchable entries: %w", err)
	}
	return true, nil
}

// workspaceScopeCondition builds the SQL scope without materializing session
// cwd values in Go. The prefix includes one separator, so /repo does not match
// /repo2; the current session branch intentionally bypasses cwd checks.
func workspaceScopeCondition(q EntrySearch) (string, []any) {
	parts := []string{}
	args := []any{}
	if q.CurrentSessionID != "" {
		parts = append(parts, `f.session_id = ?`)
		args = append(args, q.CurrentSessionID)
	}
	if q.WorkspaceDir != "" {
		dir := filepath.Clean(q.WorkspaceDir)
		separator := string(filepath.Separator)
		prefix := dir
		if !strings.HasSuffix(prefix, separator) {
			prefix += separator
		}
		cwdExpr := "ss.cwd"
		valueExpr := "?"
		if q.FoldCase {
			// SQLite lower() intentionally provides the design's ASCII-only
			// folding; non-ASCII case differences are a documented limitation.
			cwdExpr = "lower(ss.cwd)"
			if strings.Contains(dir, `\`) {
				// Windows accepts slash spellings in persisted legacy cwds;
				// normalize those before applying the case fold.
				cwdExpr = `lower(replace(ss.cwd, '/', char(92)))`
			}
			valueExpr = "lower(?)"
		}
		parts = append(parts, `(`+cwdExpr+` <> '' AND (`+
			valueExpr+` = `+cwdExpr+` OR substr(`+cwdExpr+`, 1, length(`+valueExpr+`)) = `+valueExpr+`))`)
		args = append(args, dir, prefix, prefix)
	}
	return `(` + strings.Join(parts, ` OR `) + `)`, args
}

// validateSearchKinds rejects values that cannot occur in entries_fts. Keeping
// this check in the store protects direct callers before the native tool parser
// is introduced.
func validateSearchKinds(kinds []string) error {
	for _, kind := range kinds {
		switch kind {
		case EntryUser, EntryAssistant, EntryTool, EntryCompaction, EntryHandoff:
		default:
			return fmt.Errorf("invalid search kind %q", kind)
		}
	}
	return nil
}

func joinWhere(base, condition string) string {
	if base == "" {
		return condition
	}
	return base + ` AND ` + condition
}

// ftsMatchExpr turns each term into an FTS5 string (phrase): wrapped in double
// quotes with inner quotes doubled, so `*`, `OR`, `NEAR(`, `-`, `:` ... are
// plain text. Space-separated phrases are an implicit AND.
func ftsMatchExpr(terms []string) string {
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " ")
}

// escapeLike escapes the LIKE wildcards (and the escape char itself) for
// `LIKE ? ESCAPE '\'`.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// likeSnippet cuts text to the first occurrence of term with snippetRadius
// runes on each side. Matching folds case per rune, like SQLite LIKE does for
// ASCII, and keeps rune indexes aligned with text.
func likeSnippet(text, term string) string {
	rs, ts := []rune(text), []rune(term)
	at := indexFold(rs, ts)
	if at < 0 { // cannot happen for a LIKE hit; fall back to the head
		at = 0
	}
	start, end := max(at-snippetRadius, 0), min(at+len(ts)+snippetRadius, len(rs))
	out := string(rs[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(rs) {
		out += "…"
	}
	return out
}

// indexFold returns the rune index of the first case-insensitive occurrence of
// sub in s, or -1.
func indexFold(s, sub []rune) int {
	if len(sub) == 0 {
		return 0
	}
outer:
	for i := 0; i+len(sub) <= len(s); i++ {
		for j, r := range sub {
			if unicode.ToLower(s[i+j]) != unicode.ToLower(r) {
				continue outer
			}
		}
		return i
	}
	return -1
}
