package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// Tool result dedupe maintenance (data governance P2, design 0.4).
//
// New tool messages are stored once (EncodeMessage) and decoded back into both
// fields; the rows written before that still hold the same body twice. This is
// the explicit maintenance job that rewrites them, and the one that restores
// them for an older suga (which reads only ToolResult.Content):
//
//	suga data dedupe-tool-results            rewrite the double-stored rows
//	suga data dedupe-tool-results --undo     restore every row carrying the marker
//
// Both walk the entries by their primary key (session_id, seq) in batches of
// toolDedupeBatchRows rows, one short write transaction per batch, with a
// checkpoint in a small table the command creates (never a migration: opening a
// ledger must never start full-table work, see v0.31.0). Bodies are decoded and
// re-encoded in Go rather than patched with json_set/json_remove, because Go's
// encoder is what wrote them: --undo then reproduces the pre-dedupe bytes
// exactly (key order and escaping included).
const (
	// toolDedupeBatchRows is the default batch: 2,000 rows of a short write
	// transaction stay well below the busy timeout of a concurrent writer.
	toolDedupeBatchRows = 2000
	// toolDedupeMaxBatch caps what a caller (or a --batch flag) may ask for.
	toolDedupeMaxBatch = 20000
	// toolDedupeJob and toolUndoJob are the checkpoint rows of the two passes.
	toolDedupeJob = "dedupe"
	toolUndoJob   = "undo"
)

// toolDedupeStateDDL is the checkpoint table of both passes: one row per job.
const toolDedupeStateDDL = `CREATE TABLE IF NOT EXISTS tool_dedupe_state (
    job             TEXT PRIMARY KEY,
    scanned_session TEXT NOT NULL DEFAULT '',
    scanned_seq     INTEGER NOT NULL DEFAULT 0,
    complete        INTEGER NOT NULL DEFAULT 0,
    rows            INTEGER NOT NULL DEFAULT 0,
    started_at      TEXT,
    updated_at      TEXT
)`

// toolEntryWindowSQL reads one batch of tool entries after the (session_id, seq)
// cursor. Only tool entries can be rewritten, so the cursor walks them and a
// batch is at most one index range of the primary key. Rows whose body moved to
// a tool body file (body_ref set) are skipped: their JSON is a stub and the P2
// rewrites must never touch it (design 0.3).
const toolEntryWindowSQL = `SELECT session_id, seq, json FROM entries
    WHERE type = 'tool' AND COALESCE(body_ref, '') = '' AND (session_id > ? OR (session_id = ? AND seq > ?))
    ORDER BY session_id ASC, seq ASC LIMIT ?`

// toolEntryRow is one tool entry body of a batch.
type toolEntryRow struct {
	SessionID string
	Seq       int64
	JSON      []byte
}

// ToolDedupeProgress is one finished batch, for progress output.
type ToolDedupeProgress struct {
	// SessionID and Seq are the cursor after the batch: every tool entry up to
	// it has been considered by this pass.
	SessionID string
	Seq       int64
	// Rows is the number of rows this pass rewrote so far.
	Rows int64
}

// ToolDedupeOptions configures one pass. The zero value is the CLI default:
// toolDedupeBatchRows per batch, no progress callback.
type ToolDedupeOptions struct {
	Batch   int
	OnBatch func(ToolDedupeProgress)
}

// ToolDedupeReport describes what one pass did.
type ToolDedupeReport struct {
	// Rows rewritten and the number of batches (write transactions) they took.
	Rows    int64
	Batches int
	// CursorSession and CursorSeq are the checkpoint the pass reached.
	CursorSession string
	CursorSeq     int64
	// Complete is false only when the caller stopped the pass with ctx, in which
	// case a rerun continues at the cursor.
	Complete bool
}

// ToolDedupeStatus is what --status reports about both passes.
type ToolDedupeStatus struct {
	// Dedupe: started/complete of the rewriting pass, its cursor and rows.
	Started       bool
	Complete      bool
	CursorSession string
	CursorSeq     int64
	Rows          int64
	// PendingRows are the tool rows still holding the same body twice, and
	// PendingBytes the duplicate text they hold (an estimate of what the file
	// gives back after VACUUM).
	PendingRows  int64
	PendingBytes int64
	// MarkedRows are the rows carrying the marker: what --undo restores.
	MarkedRows int64
	// Undo: started/complete of the restore pass and the rows it restored.
	UndoStarted  bool
	UndoComplete bool
	UndoRows     int64
}

// Remaining reports whether the ledger still holds double-stored tool rows.
func (st ToolDedupeStatus) Remaining() bool { return st.PendingRows > 0 }

// DedupeToolResults rewrites the double-stored tool entries to the deduped form,
// at most Batch rows per write transaction, resumable through a (session_id,
// seq) checkpoint. It stores exactly what EncodeMessage stores for a new tool
// message, so the decoded messages do not change.
func (s *Store) DedupeToolResults(ctx context.Context, opts ToolDedupeOptions) (ToolDedupeReport, error) {
	return s.runToolRewrite(ctx, toolDedupeJob, opts, dedupeToolRow)
}

// UndoToolDedupe restores ToolResult.Content on every row carrying the marker,
// old or newly written, so that an older suga (which reads only
// ToolResult.Content) sees the tool output again. The restored bytes are the
// ones a plain json.Marshal of the message produces, i.e. what the row held
// before the dedupe.
func (s *Store) UndoToolDedupe(ctx context.Context, opts ToolDedupeOptions) (ToolDedupeReport, error) {
	return s.runToolRewrite(ctx, toolUndoJob, opts, undoToolRow)
}

// toolRowRewrite returns the new body of one entry and whether it changes.
type toolRowRewrite func(raw []byte) (out []byte, changed bool, err error)

// dedupeToolRow dedupes one tool entry body: a message whose ToolResult repeats
// a non-empty Content is re-encoded with EncodeMessage. Anything else (another
// type, no result, different result, an undecodable row) is left alone.
func dedupeToolRow(raw []byte) ([]byte, bool, error) {
	var m api.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, nil // a foreign row: never rewrite what cannot be decoded
	}
	if m.ToolResult == nil || m.Content == "" || m.ToolResult.Content != m.Content {
		return nil, false, nil // already deduped, or not a double-stored result
	}
	out, err := EncodeMessage(m)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// undoToolRow restores one deduped tool entry: the marker is read first (the
// decoder clears it), then the message is re-encoded exactly as the store wrote
// it before the dedupe.
func undoToolRow(raw []byte) ([]byte, bool, error) {
	var marker struct {
		ResultSameAsContent bool
	}
	if err := json.Unmarshal(raw, &marker); err != nil || !marker.ResultSameAsContent {
		return nil, false, nil
	}
	var m api.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, nil
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// runToolRewrite is the shared pass of both jobs: walk the tool entries by
// (session_id, seq), rewrite the ones the job's rewrite applies to, and commit
// each batch with its checkpoint so that a cancelled run resumes where it
// stopped.
func (s *Store) runToolRewrite(ctx context.Context, job string, opts ToolDedupeOptions, rewrite toolRowRewrite) (ToolDedupeReport, error) {
	batch := opts.Batch
	if batch <= 0 {
		batch = toolDedupeBatchRows
	}
	batch = min(batch, toolDedupeMaxBatch)
	if err := s.ensureToolDedupeState(ctx); err != nil {
		return ToolDedupeReport{}, err
	}
	cursor, err := s.toolDedupeCursor(ctx, job)
	if err != nil {
		return ToolDedupeReport{}, err
	}
	report := ToolDedupeReport{CursorSession: cursor.SessionID, CursorSeq: cursor.Seq}
	if err := s.markToolDedupeStarted(ctx, job); err != nil {
		return report, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return report, err // resumable: the checkpoint of the last batch stands
		}
		rows, err := s.toolEntryWindow(ctx, report.CursorSession, report.CursorSeq, batch)
		if err != nil {
			return report, err
		}
		if len(rows) == 0 {
			break
		}
		last := rows[len(rows)-1]
		written, err := s.rewriteToolBatch(ctx, job, rows, rewrite)
		if err != nil {
			return report, err
		}
		report.Rows += written
		report.Batches++
		report.CursorSession, report.CursorSeq = last.SessionID, last.Seq
		if opts.OnBatch != nil {
			opts.OnBatch(ToolDedupeProgress{SessionID: last.SessionID, Seq: last.Seq, Rows: report.Rows})
		}
		if len(rows) < batch {
			break // the pass reached the last tool entry
		}
	}
	if err := s.markToolDedupeComplete(ctx, job, report.Rows); err != nil {
		return report, err
	}
	report.Complete = true
	return report, nil
}

// rewriteToolBatch rewrites the rows of one batch in a single short write
// transaction and commits the checkpoint with them: the checkpoint never runs
// ahead of the rows it describes.
func (s *Store) rewriteToolBatch(ctx context.Context, job string, rows []toolEntryRow, rewrite toolRowRewrite) (int64, error) {
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	written := int64(0)
	for _, row := range rows {
		out, changed, err := rewrite(row.JSON)
		if err != nil {
			return 0, fmt.Errorf("rewrite entry %s/%d: %w", row.SessionID, row.Seq, err)
		}
		if !changed || bytes.Equal(out, row.JSON) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET json = ? WHERE session_id = ? AND seq = ?`,
			string(out), row.SessionID, row.Seq); err != nil {
			return 0, fmt.Errorf("update entry %s/%d: %w", row.SessionID, row.Seq, err)
		}
		written++
	}
	last := rows[len(rows)-1]
	if _, err := tx.ExecContext(ctx, `UPDATE tool_dedupe_state SET scanned_session = ?, scanned_seq = ?,
        complete = 0, rows = rows + ?, updated_at = ? WHERE job = ?`,
		last.SessionID, last.Seq, written, formatTime(time.Now()), job); err != nil {
		return 0, fmt.Errorf("record tool dedupe checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// toolEntryWindow reads up to limit tool entries after the cursor.
func (s *Store) toolEntryWindow(ctx context.Context, sessionID string, seq int64, limit int) ([]toolEntryRow, error) {
	rows, err := s.db.QueryContext(ctx, toolEntryWindowSQL, sessionID, sessionID, seq, limit)
	if err != nil {
		return nil, fmt.Errorf("read tool entries after %s/%d: %w", sessionID, seq, err)
	}
	defer rows.Close()
	var out []toolEntryRow
	for rows.Next() {
		var row toolEntryRow
		var body string
		if err := rows.Scan(&row.SessionID, &row.Seq, &body); err != nil {
			return nil, err
		}
		row.JSON = []byte(body)
		out = append(out, row)
	}
	return out, rows.Err()
}

// ToolDedupeStatus reports how much rewriting is left and what an undo would
// restore. The counts decode every tool row's JSON in SQL (a report command,
// not a write path).
func (s *Store) ToolDedupeStatus(ctx context.Context) (ToolDedupeStatus, error) {
	var status ToolDedupeStatus
	rows := map[string]cursorState{
		toolDedupeJob: {},
		toolUndoJob:   {},
	}
	exists, err := s.tableExists(ctx, "tool_dedupe_state")
	if err != nil {
		return status, err
	}
	if exists {
		for job := range rows {
			cursor, err := s.toolDedupeCursor(ctx, job)
			if err != nil {
				return status, err
			}
			state := rows[job]
			state.SessionID, state.Seq = cursor.SessionID, cursor.Seq
			state.Started, state.Complete, state.Rows = cursor.Started, cursor.Complete, cursor.Rows
			rows[job] = state
		}
	}
	dedupe := rows[toolDedupeJob]
	status.Started, status.Complete = dedupe.Started, dedupe.Complete
	status.CursorSession, status.CursorSeq, status.Rows = dedupe.SessionID, dedupe.Seq, dedupe.Rows
	undo := rows[toolUndoJob]
	status.UndoStarted, status.UndoComplete, status.UndoRows = undo.Started, undo.Complete, undo.Rows
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(dup_bytes), 0) FROM (
            SELECT CASE WHEN json_valid(json) THEN
                CASE WHEN json_type(json, '$.ResultSameAsContent') IS NULL
                     AND coalesce(json_extract(json, '$.Content'), '') <> ''
                     AND coalesce(json_extract(json, '$.Content'), '') = coalesce(json_extract(json, '$.ToolResult.Content'), '')
                    THEN length(CAST(json_extract(json, '$.ToolResult.Content') AS BLOB)) END
            END AS dup_bytes FROM entries WHERE type = 'tool') WHERE dup_bytes IS NOT NULL`).
		Scan(&status.PendingRows, &status.PendingBytes)
	if err != nil {
		return status, fmt.Errorf("count double-stored tool rows: %w", err)
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE type = 'tool'
        AND CASE WHEN json_valid(json) THEN json_type(json, '$.ResultSameAsContent') = 'true' ELSE 0 END`).
		Scan(&status.MarkedRows)
	if err != nil {
		return status, fmt.Errorf("count deduped tool rows: %w", err)
	}
	return status, nil
}

// ResetToolDedupe drops both checkpoints (--abort). It changes no entry: the
// next pass starts at the first tool entry again, which is what an operator
// wants after a partly finished run looks wrong.
func (s *Store) ResetToolDedupe(ctx context.Context) error {
	if err := s.ensureToolDedupeState(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM tool_dedupe_state`)
	return err
}

// cursorState is one job's checkpoint.
type cursorState struct {
	SessionID string
	Seq       int64
	Started   bool
	Complete  bool
	Rows      int64
}

// toolDedupeCursor reads the checkpoint of a job (an empty cursor when the job
// never ran; the table exists once ensureToolDedupeState ran).
func (s *Store) toolDedupeCursor(ctx context.Context, job string) (cursorState, error) {
	var state cursorState
	err := s.db.QueryRowContext(ctx, `SELECT scanned_session, scanned_seq, complete, rows
        FROM tool_dedupe_state WHERE job = ?`, job).
		Scan(&state.SessionID, &state.Seq, &state.Complete, &state.Rows)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return cursorState{}, nil
	case err != nil:
		return cursorState{}, fmt.Errorf("read tool dedupe checkpoint %q: %w", job, err)
	}
	state.Started = true
	return state, nil
}

// ensureToolDedupeState creates the checkpoint table if it is missing. The table
// is created by the command, never by a migration (see the package comment).
func (s *Store) ensureToolDedupeState(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, toolDedupeStateDDL); err != nil {
		return fmt.Errorf("create tool dedupe state: %w", err)
	}
	return nil
}

// markToolDedupeStarted writes the job's checkpoint row as an unfinished pass,
// keeping the cursor of an earlier run so a rerun continues.
func (s *Store) markToolDedupeStarted(ctx context.Context, job string) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_dedupe_state(job, scanned_session, scanned_seq, complete, rows, started_at, updated_at)
        VALUES(?, '', 0, 0, 0, ?, ?)
        ON CONFLICT(job) DO UPDATE SET complete = 0, rows = 0, started_at = excluded.started_at, updated_at = excluded.updated_at`,
		job, now, now)
	if err != nil {
		return fmt.Errorf("start tool dedupe %q: %w", job, err)
	}
	return nil
}

// markToolDedupeComplete records that the pass reached the last tool entry.
func (s *Store) markToolDedupeComplete(ctx context.Context, job string, rows int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tool_dedupe_state SET complete = 1, rows = ?, updated_at = ? WHERE job = ?`,
		rows, formatTime(time.Now()), job)
	if err != nil {
		return fmt.Errorf("finish tool dedupe %q: %w", job, err)
	}
	return nil
}
