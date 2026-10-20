package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// Tool body maintenance (design 2026-10-19-tool-output-files, 0.3):
//
//	suga data bodies --status          what would move, what moved
//	suga data bodies --move            move the historical bodies out
//	suga data bodies --undo            restore every stub into the ledger
//	suga data bodies --undo --remove-files   ... and drop the files
//	suga data bodies --check           verify every stub, report orphans
//
// --move and --undo walk the entries by (session_id, seq) in small
// batches, each one a single write transaction that also records its
// checkpoint, so a cancelled run resumes where it stopped. The batch of every
// pass is capped by rows and by bytes: the write lock is held while the file is
// appended, so it must stay short even when the rows are large tool outputs.
const (
	// toolBodyBatchRows is the default batch of both passes (100 rows keeps the
	// write lock short; a --batch flag may raise it up to toolBodyMaxBatch).
	toolBodyBatchRows = 100
	// toolBodyMaxBatch caps what a caller may ask for.
	toolBodyMaxBatch = 2000
	// toolBodyBatchBytes caps the body bytes one batch reads, even under the row
	// cap: the rows are read into memory inside the transaction.
	toolBodyBatchBytes = 8 << 20
	// toolBodyCheckBatch is the page size of --check, which never holds the write
	// lock.
	toolBodyCheckBatch = 500
	// toolBodyMoveJob and toolBodyUndoJob are the checkpoint rows of the passes.
	toolBodyMoveJob      = "move"
	toolBodyAssistantJob = "move_assistant"
	toolBodyUndoJob      = "undo"
)

// toolBodyStateDDL is the checkpoint table of the passes: one row per job.
const toolBodyStateDDL = `CREATE TABLE IF NOT EXISTS tool_body_state (
    job             TEXT PRIMARY KEY,
    scanned_session TEXT NOT NULL DEFAULT '',
    scanned_seq     INTEGER NOT NULL DEFAULT 0,
    complete        INTEGER NOT NULL DEFAULT 0,
    rows            INTEGER NOT NULL DEFAULT 0,
    bytes           INTEGER NOT NULL DEFAULT 0,
    started_at      TEXT,
    updated_at      TEXT
)`

// toolBodyMoveWindowSQL reads one batch of tool entries that still hold their
// body inline, after the (session_id, seq) cursor.
const toolBodyMoveWindowSQL = `SELECT session_id, seq, json FROM entries
    WHERE type = 'tool' AND COALESCE(body_ref, '') = '' AND length(CAST(json AS BLOB)) > ?
      AND (session_id > ? OR (session_id = ? AND seq > ?))
    ORDER BY session_id ASC, seq ASC LIMIT ?`

const assistantBodyMoveWindowSQL = `SELECT session_id, seq, json FROM entries
    WHERE type = 'assistant' AND COALESCE(body_ref, '') = ''
      AND length(CAST(json AS BLOB)) - length(CAST(COALESCE(json_extract(json, '$.Content'), '') AS BLOB)) > ?
      AND (session_id > ? OR (session_id = ? AND seq > ?))
    ORDER BY session_id ASC, seq ASC LIMIT ?`

const toolBodyUndoWindowSQL = `SELECT session_id, seq, body_ref FROM entries
    WHERE COALESCE(body_ref, '') <> ''
      AND (session_id > ? OR (session_id = ? AND seq > ?))
    ORDER BY session_id ASC, seq ASC LIMIT ?`

// ToolBodyMaintOptions configures one pass. The zero value is the CLI default.
type ToolBodyMaintOptions struct {
	Batch   int
	OnBatch func(ToolBodyMaintProgress)
}

// ToolBodyMaintProgress is one finished batch.
type ToolBodyMaintProgress struct {
	// SessionID and Seq are the cursor after the batch.
	SessionID string
	Seq       int64
	// Rows and Bytes are the totals of the pass so far.
	Rows  int64
	Bytes int64
}

// ToolBodyMaintReport describes what one pass did.
type ToolBodyMaintReport struct {
	Rows    int64
	Bytes   int64
	Batches int
	// CursorSession and CursorSeq are the checkpoint the pass reached; Complete is
	// false only when the caller stopped it with ctx.
	CursorSession string
	CursorSeq     int64
	Complete      bool
}

// ToolBodyStatus is what --status reports.
type ToolBodyStatus struct {
	// InlineBytes is the threshold in force; MoveEnabled is the persistent switch
	// (false after --undo).
	InlineBytes int64
	MoveEnabled bool
	// PendingRows and PendingBytes are the tool rows that would still move;
	// AssistantPending* the same for assistant entries (the migration of revision
	// 3, an independent pass). AssistantSkippedRows (and its bytes) are the SQL
	// candidates the assistant pass leaves alone because moving them out saves no
	// more than the threshold: counted apart so --status does not ask for a --move
	// run that cannot do anything.
	PendingRows          int64
	PendingBytes         int64
	AssistantPendingRows int64
	AssistantPending     int64
	AssistantSkippedRows int64
	AssistantSkipped     int64
	// MovedRows are the rows whose body lives in a file, FileBytes and Files what
	// the session files hold (both file names).
	MovedRows          int64
	AssistantMovedRows int64
	FileBytes          int64
	Files              int
	// BodyFileNameEnabled is the file name switch (set by v0.40's --rename-files);
	// LegacyNameFiles counts the sessions still holding a tools.jsonl file. Both
	// are read-only information: this version writes whichever name the switch
	// names, so a renamed ledger keeps working.
	BodyFileNameEnabled bool
	LegacyNameFiles     int
	// The checkpoints of both passes.
	MoveStarted, MoveComplete bool
	MoveSession               string
	MoveSeq, MoveRows         int64
	AssistantMoveStarted      bool
	AssistantMoveComplete     bool
	AssistantMoveSession      string
	AssistantMoveSeq          int64
	AssistantMoveRows         int64
	UndoStarted, UndoComplete bool
	UndoRows                  int64
}

// MoveToolBodies moves the historical tool bodies into the session files, at most
// Batch rows (or toolBodyBatchBytes) per write transaction, resumable through a
// (session_id, seq) checkpoint. Rows that are stubs already are skipped, so a
// rerun continues and an interrupted batch is repeated safely.
func (s *Store) MoveToolBodies(ctx context.Context, opts ToolBodyMaintOptions) (ToolBodyMaintReport, error) {
	if s.toolBodyInlineBytes <= 0 {
		return ToolBodyMaintReport{}, errors.New("store.body_inline_bytes is 0: no tool body is moved out")
	}
	// Moving bodies out is also the way back from --undo, which turned the switch
	// off: the pass turns it on again.
	if err := s.SetToolBodyMove(ctx, true); err != nil {
		return ToolBodyMaintReport{}, err
	}
	return s.runToolBodyPass(ctx, toolBodyMoveJob, opts, s.moveToolBodyBatch)
}

// MoveAssistantBodies moves historical assistant bodies with an independent checkpoint.
func (s *Store) MoveAssistantBodies(ctx context.Context, opts ToolBodyMaintOptions) (ToolBodyMaintReport, error) {
	if s.toolBodyInlineBytes <= 0 {
		return ToolBodyMaintReport{}, errors.New("store body threshold is 0")
	}
	if err := s.SetToolBodyMove(ctx, true); err != nil {
		return ToolBodyMaintReport{}, err
	}
	return s.runToolBodyPass(ctx, toolBodyAssistantJob, opts, s.moveToolBodyBatch)
}

// UndoToolBodies restores every moved body into the ledger and disables new moves.
func (s *Store) UndoToolBodies(ctx context.Context, opts ToolBodyMaintOptions) (ToolBodyMaintReport, error) {
	if err := s.SetToolBodyMove(ctx, false); err != nil {
		return ToolBodyMaintReport{}, err
	}
	return s.runToolBodyPass(ctx, toolBodyUndoJob, opts, s.undoToolBodyBatch)
}

// toolBodyBatchFunc applies one batch; it returns the rows/bytes it handled and
// the cursor it reached.
type toolBodyBatchFunc func(ctx context.Context, job string, opts ToolBodyMaintOptions) (rows, bytes int64, last *toolBodyRow, more bool, err error)

// toolBodyRow is one row of a batch window.
type toolBodyRow struct {
	SessionID string
	Seq       int64
	JSON      []byte
	Ref       string
}

// cursorState is one pass's checkpoint: the (session_id, seq) cursor it reached
// and what it did, as the tool_body_state row holds them.
type cursorState struct {
	SessionID string
	Seq       int64
	Started   bool
	Complete  bool
	Rows      int64
}

// runToolBodyPass is the shared loop of --move and --undo.
func (s *Store) runToolBodyPass(ctx context.Context, job string, opts ToolBodyMaintOptions, batch toolBodyBatchFunc) (ToolBodyMaintReport, error) {
	if err := s.ensureToolBodyState(ctx); err != nil {
		return ToolBodyMaintReport{}, err
	}
	cursor, err := s.toolBodyCursor(ctx, job)
	if err != nil {
		return ToolBodyMaintReport{}, err
	}
	if cursor.Complete {
		// A completed pass must not resume: rows and stubs were written since it
		// finished, and they can sit at any (session_id, seq) key. Start over;
		// markToolBodyStarted drops the saved cursor with it.
		cursor = cursorState{}
	}
	report := ToolBodyMaintReport{CursorSession: cursor.SessionID, CursorSeq: cursor.Seq}
	if err := s.markToolBodyStarted(ctx, job); err != nil {
		return report, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return report, err // resumable: the checkpoint of the last batch stands
		}
		rows, bytes, last, more, err := batch(ctx, job, opts)
		if err != nil {
			return report, err
		}
		if last == nil {
			break
		}
		report.Rows += rows
		report.Bytes += bytes
		report.Batches++
		report.CursorSession, report.CursorSeq = last.SessionID, last.Seq
		if opts.OnBatch != nil {
			opts.OnBatch(ToolBodyMaintProgress{SessionID: last.SessionID, Seq: last.Seq, Rows: report.Rows, Bytes: report.Bytes})
		}
		if !more {
			break
		}
	}
	if err := s.markToolBodyComplete(ctx, job, report.Rows, report.Bytes); err != nil {
		return report, err
	}
	report.Complete = true
	return report, nil
}

// toolBodyBatchSize resolves the batch of a pass.
func toolBodyBatchSize(opts ToolBodyMaintOptions) int {
	if opts.Batch <= 0 {
		return toolBodyBatchRows
	}
	return min(opts.Batch, toolBodyMaxBatch)
}

// moveToolBodyBatch moves one batch: inside one write transaction it reads the
// rows, appends their bodies to the per-session files, syncs, swaps the rows to
// stubs and writes the checkpoint. A failure rolls the transaction back and
// truncates the files to the length each one had.
func (s *Store) moveToolBodyBatch(ctx context.Context, job string, opts ToolBodyMaintOptions) (int64, int64, *toolBodyRow, bool, error) {
	batch := toolBodyBatchSize(opts)
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return 0, 0, nil, false, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	committed := false
	writers := map[string]*toolBodyWriter{}
	defer func() {
		for _, w := range writers {
			_ = w.Close(committed)
		}
	}()
	cursor, err := s.toolBodyCursorTx(ctx, tx, job)
	if err != nil {
		return 0, 0, nil, false, err
	}
	rows, err := toolBodyWindowTx(ctx, tx, cursor, s.toolBodyInlineBytes, batch, job == toolBodyAssistantJob)
	if err != nil {
		return 0, 0, nil, false, err
	}
	if len(rows) == 0 {
		return 0, 0, nil, false, nil
	}
	var written int64
	var movedBytes int64
	var last *toolBodyRow
	for i := range rows {
		row := &rows[i]
		if movedBytes > 0 && movedBytes+int64(len(row.JSON)) > toolBodyBatchBytes {
			break // keep the transaction short, the rest is the next batch
		}
		entryType := EntryTool
		if job == toolBodyAssistantJob {
			entryType = EntryAssistant
		}
		stub, ok, err := buildToolBodyStub(entryType, row.JSON)
		if err != nil {
			return 0, 0, nil, false, fmt.Errorf("build stub of %s/%d: %w", row.SessionID, row.Seq, err)
		}
		writer, ok2 := writers[row.SessionID]
		if !ok2 {
			writer, err = s.openToolBodyWriter(row.SessionID)
			if err != nil {
				return 0, 0, nil, false, err
			}
			writers[row.SessionID] = writer
		}
		if !ok {
			continue // undecodable row: left inline, counted as scanned
		}
		if job == toolBodyAssistantJob && int64(len(row.JSON)-len(stub)) <= s.toolBodyInlineBytes {
			continue
		}
		ref, err := writer.WriteRecord(row.Seq, row.JSON)
		if err != nil {
			return 0, 0, nil, false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET json = ?, body_ref = ? WHERE session_id = ? AND seq = ?`,
			string(stub), ref.String(), row.SessionID, row.Seq); err != nil {
			return 0, 0, nil, false, fmt.Errorf("store tool body stub of %s/%d: %w", row.SessionID, row.Seq, err)
		}
		written++
		movedBytes += int64(len(row.JSON))
		last = row
	}
	if last == nil { // every row of the window was undecodable
		last = &rows[min(len(rows), batch)-1]
	}
	for _, w := range writers {
		if err := w.Synced(); err != nil {
			return 0, 0, nil, false, err
		}
	}
	if err := s.checkpointToolBody(ctx, tx, job, last, written, movedBytes); err != nil {
		return 0, 0, nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, nil, false, err
	}
	committed = true
	return written, movedBytes, last, len(rows) == batch, nil
}

// undoToolBodyBatch restores one batch of stubs from the files, in one write
// transaction with its checkpoint.
func (s *Store) undoToolBodyBatch(ctx context.Context, job string, opts ToolBodyMaintOptions) (int64, int64, *toolBodyRow, bool, error) {
	batch := toolBodyBatchSize(opts)
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return 0, 0, nil, false, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	cursor, err := s.toolBodyCursorTx(ctx, tx, job)
	if err != nil {
		return 0, 0, nil, false, err
	}
	rows, err := toolBodyStubWindow(ctx, tx, cursor, batch)
	if err != nil {
		return 0, 0, nil, false, err
	}
	if len(rows) == 0 {
		return 0, 0, nil, false, nil
	}
	readers := map[string]*toolBodyReader{}
	defer func() {
		for _, r := range readers {
			_ = r.Close()
		}
	}()
	var restored, restoredBytes int64
	var last *toolBodyRow
	for i := range rows {
		row := &rows[i]
		ref, err := parseBodyRef(row.Ref)
		if err != nil {
			return 0, 0, nil, false, fmt.Errorf("read body_ref of %s/%d: %w", row.SessionID, row.Seq, err)
		}
		reader, ok := readers[row.SessionID]
		if !ok {
			reader, err = s.openToolBodyReader(row.SessionID)
			if err != nil {
				return 0, 0, nil, false, err
			}
			readers[row.SessionID] = reader
		}
		body, err := reader.Read(row.SessionID, row.Seq, ref)
		if err != nil {
			return 0, 0, nil, false, fmt.Errorf("restore %s/%d: %w", row.SessionID, row.Seq, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET json = ?, body_ref = NULL WHERE session_id = ? AND seq = ?`,
			string(body), row.SessionID, row.Seq); err != nil {
			return 0, 0, nil, false, fmt.Errorf("restore entry %s/%d: %w", row.SessionID, row.Seq, err)
		}
		restored++
		restoredBytes += int64(len(body))
		last = row
	}
	if err := s.checkpointToolBody(ctx, tx, job, last, restored, restoredBytes); err != nil {
		return 0, 0, nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, nil, false, err
	}
	return restored, restoredBytes, last, len(rows) == batch, nil
}

// countAssistantBodyRows decides in Go which SQL candidates the assistant pass
// moves (issue suga-t4ou). The window condition (json minus Content above the
// threshold) only estimates the saving: the stub keeps the whole Content plus the
// call id/name, so a row whose real saving is at most the threshold is never
// moved. Those are counted apart instead of in the pending figure, and an
// undecodable row counts apart too (the pass skips it). It only reads, paging
// like the pass, so --status works next to a running chat.
func (s *Store) countAssistantBodyRows(ctx context.Context) (moveRows, moveBytes, skipRows, skipBytes int64, err error) {
	var cursor cursorState
	for {
		if err := ctx.Err(); err != nil {
			return moveRows, moveBytes, skipRows, skipBytes, err
		}
		window, err := toolBodyWindowTx(ctx, s.db, cursor, s.toolBodyInlineBytes, toolBodyCheckBatch, true)
		if err != nil {
			return moveRows, moveBytes, skipRows, skipBytes, err
		}
		for i := range window {
			raw := window[i].JSON
			stub, ok, buildErr := buildToolBodyStub(EntryAssistant, raw)
			if buildErr != nil {
				return moveRows, moveBytes, skipRows, skipBytes,
					fmt.Errorf("build stub of %s/%d: %w", window[i].SessionID, window[i].Seq, buildErr)
			}
			if ok && int64(len(raw)-len(stub)) > s.toolBodyInlineBytes {
				moveRows++
				moveBytes += int64(len(raw))
				continue
			}
			skipRows++
			skipBytes += int64(len(raw))
		}
		if len(window) < toolBodyCheckBatch {
			return moveRows, moveBytes, skipRows, skipBytes, nil
		}
		cursor.SessionID, cursor.Seq = window[len(window)-1].SessionID, window[len(window)-1].Seq
	}
}

// toolBodyWindowTx reads up to limit tool rows that still hold their body, after
// the cursor. A read-only caller pages with s.db, the passes with their tx.
func toolBodyWindowTx(ctx context.Context, q querier, cursor cursorState, threshold int64, limit int, assistant bool) ([]toolBodyRow, error) {
	query := toolBodyMoveWindowSQL
	if assistant {
		query = assistantBodyMoveWindowSQL
	}
	rows, err := q.QueryContext(ctx, query, threshold, cursor.SessionID, cursor.SessionID, cursor.Seq, limit)
	if err != nil {
		return nil, fmt.Errorf("read tool rows after %s/%d: %w", cursor.SessionID, cursor.Seq, err)
	}
	defer rows.Close()
	var out []toolBodyRow
	for rows.Next() {
		var row toolBodyRow
		var body string
		if err := rows.Scan(&row.SessionID, &row.Seq, &body); err != nil {
			return nil, err
		}
		row.JSON = []byte(body)
		out = append(out, row)
	}
	return out, rows.Err()
}

// toolBodyStubWindow reads up to limit stub rows after the cursor, inside tx.
func toolBodyStubWindow(ctx context.Context, tx querier, cursor cursorState, limit int) ([]toolBodyRow, error) {
	rows, err := tx.QueryContext(ctx, toolBodyUndoWindowSQL, cursor.SessionID, cursor.SessionID, cursor.Seq, limit)
	if err != nil {
		return nil, fmt.Errorf("read tool stubs after %s/%d: %w", cursor.SessionID, cursor.Seq, err)
	}
	defer rows.Close()
	var out []toolBodyRow
	for rows.Next() {
		var row toolBodyRow
		if err := rows.Scan(&row.SessionID, &row.Seq, &row.Ref); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// checkpointToolBody records the cursor of a finished batch in the same
// transaction as its rows: the checkpoint never runs ahead of them.
func (s *Store) checkpointToolBody(ctx context.Context, tx *sql.Tx, job string, last *toolBodyRow, rows, bytes int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE tool_body_state SET scanned_session = ?, scanned_seq = ?,
        complete = 0, rows = rows + ?, bytes = bytes + ?, updated_at = ? WHERE job = ?`,
		last.SessionID, last.Seq, rows, bytes, formatTime(time.Now()), job); err != nil {
		return fmt.Errorf("record tool body checkpoint: %w", err)
	}
	return nil
}

// toolBodyFile is one session file found on disk; name is the file name (the
// current one or the v0.36 one), so a session holding both is reported twice and
// its orphans are counted per file.
type toolBodyFile struct {
	sessionID string
	name      string
	path      string
}

// toolBodyFiles lists the session files under the sessions root, both names.
func (s *Store) toolBodyFiles() ([]toolBodyFile, error) {
	dirs, err := os.ReadDir(s.sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions root: %w", err)
	}
	var out []toolBodyFile
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		for _, name := range []string{BodyFileName, LegacyBodyFileName} {
			path, ok := s.toolBodyPathName(dir.Name(), name)
			if !ok {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				continue
			}
			out = append(out, toolBodyFile{sessionID: dir.Name(), name: name, path: path})
		}
	}
	return out, nil
}

// ToolBodyStatus reports what is left to move, what moved and how large the
// session files are.
func (s *Store) ToolBodyStatus(ctx context.Context) (ToolBodyStatus, error) {
	status := ToolBodyStatus{InlineBytes: s.toolBodyInlineBytes, MoveEnabled: true}
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key = ?`, toolBodyMoveKey).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return status, fmt.Errorf("read tool body switch: %w", err)
	default:
		status.MoveEnabled = value != "0"
	}
	if status.BodyFileNameEnabled, err = s.bodyFileNameSwitch(ctx); err != nil {
		return status, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(CAST(json AS BLOB))), 0) FROM entries
        WHERE type = 'tool' AND COALESCE(body_ref, '') = '' AND length(CAST(json AS BLOB)) > ?`,
		s.toolBodyInlineBytes).Scan(&status.PendingRows, &status.PendingBytes)
	if err != nil {
		return status, fmt.Errorf("count pending tool bodies: %w", err)
	}
	// Assistant rows move when the stub saves more than the threshold; the SQL
	// length of Content only estimates the stub, so the candidates are decided in
	// Go (issue suga-t4ou): what the pass will move, and what it leaves alone.
	status.AssistantPendingRows, status.AssistantPending, status.AssistantSkippedRows, status.AssistantSkipped, err =
		s.countAssistantBodyRows(ctx)
	if err != nil {
		return status, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN type = 'tool' THEN 1 ELSE 0 END), 0),
        COALESCE(SUM(CASE WHEN type = 'assistant' THEN 1 ELSE 0 END), 0) FROM entries WHERE COALESCE(body_ref, '') <> ''`).
		Scan(&status.MovedRows, &status.AssistantMovedRows)
	if err != nil {
		return status, fmt.Errorf("count moved tool bodies: %w", err)
	}
	files, err := s.toolBodyFiles()
	if err != nil {
		return status, err
	}
	for _, file := range files {
		if file.name == LegacyBodyFileName {
			status.LegacyNameFiles++
		}
	}
	status.Files = len(files)
	for _, file := range files {
		if info, err := os.Stat(file.path); err == nil {
			status.FileBytes += info.Size()
		}
	}
	if err := s.ensureToolBodyState(ctx); err != nil {
		return status, err
	}
	move, err := s.toolBodyCursor(ctx, toolBodyMoveJob)
	if err != nil {
		return status, err
	}
	status.MoveStarted, status.MoveComplete = move.Started, move.Complete
	status.MoveSession, status.MoveSeq, status.MoveRows = move.SessionID, move.Seq, move.Rows
	assistant, err := s.toolBodyCursor(ctx, toolBodyAssistantJob)
	if err != nil {
		return status, err
	}
	status.AssistantMoveStarted, status.AssistantMoveComplete = assistant.Started, assistant.Complete
	status.AssistantMoveSession, status.AssistantMoveSeq = assistant.SessionID, assistant.Seq
	status.AssistantMoveRows = assistant.Rows
	undo, err := s.toolBodyCursor(ctx, toolBodyUndoJob)
	if err != nil {
		return status, err
	}
	status.UndoStarted, status.UndoComplete, status.UndoRows = undo.Started, undo.Complete, undo.Rows
	return status, nil
}

// RemoveToolBodyFiles deletes the session files of sessions that keep no stub,
// each confirmed under the write lock so a writer that moved a body out in the
// meantime is not robbed of its file. It is the --undo --remove-files step.
func (s *Store) RemoveToolBodyFiles(ctx context.Context) (removed int, err error) {
	files, err := s.toolBodyFiles()
	if err != nil {
		return 0, err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		tx, err := s.beginWriteTx(ctx)
		if err != nil {
			return removed, err
		}
		var stubs int
		queryErr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE session_id = ? AND COALESCE(body_ref, '') <> ''`,
			file.sessionID).Scan(&stubs)
		if queryErr != nil {
			_ = tx.Rollback()
			return removed, fmt.Errorf("count stubs of %s: %w", file.sessionID, queryErr)
		}
		if stubs > 0 {
			_ = tx.Rollback()
			continue
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = tx.Rollback()
			return removed, fmt.Errorf("remove %s: %w", file.path, err)
		}
		if err := tx.Commit(); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// DeleteSessionFiles removes the files of sessions the ledger no longer has
// (called by the catalogues that delete a session outside `data gc`, which
// removes the whole session directory itself).
func (s *Store) DeleteSessionFiles(sessionIDs ...string) error {
	for _, id := range sessionIDs {
		dir, ok := sessionDir(s.sessionsDir, id)
		if !ok {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove session files of %s: %w", id, err)
		}
	}
	return nil
}

// ensureToolBodyState creates the checkpoint table if it is missing (the command
// creates it, never a migration).
func (s *Store) ensureToolBodyState(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, toolBodyStateDDL); err != nil {
		return fmt.Errorf("create tool body state: %w", err)
	}
	return nil
}

// toolBodyCursor reads the checkpoint of a job outside a transaction.
func (s *Store) toolBodyCursor(ctx context.Context, job string) (cursorState, error) {
	return s.toolBodyCursorQuery(ctx, s.db, job)
}

// toolBodyCursorTx reads the checkpoint of a job inside the pass's transaction,
// so a batch reads and writes the cursor under the same lock.
func (s *Store) toolBodyCursorTx(ctx context.Context, tx *sql.Tx, job string) (cursorState, error) {
	return s.toolBodyCursorQuery(ctx, tx, job)
}

// querier is what a cursor read needs (*sql.DB and *sql.Tx both match).
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// toolBodyCursorQuery reads one job's checkpoint row (empty when it never ran).
func (s *Store) toolBodyCursorQuery(ctx context.Context, q querier, job string) (cursorState, error) {
	var state cursorState
	err := q.QueryRowContext(ctx, `SELECT scanned_session, scanned_seq, complete, rows FROM tool_body_state WHERE job = ?`, job).
		Scan(&state.SessionID, &state.Seq, &state.Complete, &state.Rows)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return cursorState{}, nil
	case err != nil:
		return cursorState{}, fmt.Errorf("read tool body checkpoint %q: %w", job, err)
	}
	state.Started = true
	return state, nil
}

// markToolBodyStarted writes the job's checkpoint row as an unfinished pass,
// keeping the cursor of an earlier unfinished run so a rerun continues. The
// cursor of an earlier completed (complete = 1) run is dropped instead: that
// pass ended at the last row, so a rerun must scan the new rows from the start.
func (s *Store) markToolBodyStarted(ctx context.Context, job string) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_body_state(job, scanned_session, scanned_seq, complete, rows, bytes, started_at, updated_at)
        VALUES(?, '', 0, 0, 0, 0, ?, ?)
        ON CONFLICT(job) DO UPDATE SET
          complete = 0, rows = 0, bytes = 0,
          scanned_session = CASE WHEN tool_body_state.complete = 1 THEN '' ELSE tool_body_state.scanned_session END,
          scanned_seq = CASE WHEN tool_body_state.complete = 1 THEN 0 ELSE tool_body_state.scanned_seq END,
          started_at = excluded.started_at, updated_at = excluded.updated_at`,
		job, now, now)
	if err != nil {
		return fmt.Errorf("start tool body pass %q: %w", job, err)
	}
	return nil
}

// markToolBodyComplete records that the pass reached the last row.
func (s *Store) markToolBodyComplete(ctx context.Context, job string, rows, bytes int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tool_body_state SET complete = 1, rows = ?, bytes = ?, updated_at = ? WHERE job = ?`,
		rows, bytes, formatTime(time.Now()), job)
	if err != nil {
		return fmt.Errorf("finish tool body pass %q: %w", job, err)
	}
	return nil
}

// ResetToolBodyState drops both checkpoints (--abort): the next pass starts at
// the first row again.
func (s *Store) ResetToolBodyState(ctx context.Context) error {
	if err := s.ensureToolBodyState(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM tool_body_state`)
	return err
}
