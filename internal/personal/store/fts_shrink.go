package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// FTS shrink maintenance (data governance P1, design 0.3).
//
// Tool results were ~96% of entries_fts (42,735 rows, 120MB of text, ~500MB of
// index+content on the 2026-10-19 ledger). New rows are indexed truncated
// (ftsTextExpr); the rows written before that are rewritten in place here,
// batch by batch, because a 500MB FTS table cannot be rebuilt in one
// transaction (and shadow-table swaps break the delete trigger).
//
// The command keeps its own rowid checkpoint in a small table it creates, NOT
// in a migration: opening a ledger must never start full-table work
// (v0.31.0 hung that way). Batches walk entries_fts' OWN rowid, never the
// entries' implicit rowid, which VACUUM renumbers.
const (
	// ftsShrinkBatchRows is the default batch: 2,000 rows of an UPDATE on one
	// FTS table stay well below the busy timeout of a concurrent writer.
	ftsShrinkBatchRows = 2000
	// ftsShrinkMaxBatch caps what a caller (or a --batch flag) may ask for.
	ftsShrinkMaxBatch = 20000
	// ftsShrinkIndexRatio is how the estimate of --status relates the characters
	// a rewrite removes to the bytes the file gives back: the trigram index plus
	// the content copy of entries_fts, measured as 79.9MB less entries_fts_data
	// (and the same amount less content) for 79.45M characters removed by the
	// 20,000-row benchmark. It only sizes the estimate -- the real number comes
	// from entries_fts_data (the command prints both).
	ftsShrinkIndexRatio = 2
)

// Shrink state DDL. id is pinned to 1: the table holds exactly one checkpoint.
const ftsShrinkStateDDL = `CREATE TABLE IF NOT EXISTS fts_shrink_state (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    scanned_rowid INTEGER NOT NULL,
    complete      INTEGER NOT NULL DEFAULT 0,
    shrunk_rows   INTEGER NOT NULL DEFAULT 0,
    started_at    TEXT,
    updated_at    TEXT
)`

// ftsShrinkUpdateSQL rewrites the text of up to one batch of tool rows: the
// rows whose text is longer than ftsToolMaxChars are replaced by exactly the
// expression AppendEntries applies to a new tool result (ftsTruncateExpr over
// the text column of entries_fts).
var ftsShrinkUpdateSQL = `UPDATE entries_fts SET text = ` + ftsTruncateExpr("text") +
	` WHERE rowid > ? AND rowid <= ? AND role = 'tool' AND length(text) > ` + strconv.Itoa(ftsToolShrinkMinChars)

// FTSShrinkProgress is one finished batch, for progress output.
type FTSShrinkProgress struct {
	// ScannedRowID is the checkpoint after the batch: every FTS row up to it
	// has been considered by this pass.
	ScannedRowID int64
	// HighWater is the highest rowid this pass walks.
	HighWater int64
	// Rows is the number of rows actually rewritten in this pass so far.
	Rows int64
}

// FTSShrinkOptions configures one shrink pass. The zero value is the CLI
// default: ftsShrinkBatchRows per batch, no progress callback.
type FTSShrinkOptions struct {
	Batch   int
	OnBatch func(FTSShrinkProgress)
}

// FTSShrinkReport describes what one pass did.
type FTSShrinkReport struct {
	// Rows rewritten, and the number of batches (write transactions) they took.
	Rows    int64
	Batches int
	// ScannedRowID and HighWater are the checkpoint reached and the rowid the
	// pass walked up to. ScannedRowID < HighWater only when the caller stopped
	// the pass with ctx, in which case resuming continues at ScannedRowID.
	ScannedRowID int64
	HighWater    int64
	// BytesBefore and BytesAfter measure entries_fts_data (its own pages), the
	// figure that matters for the file size; the merge passes run in between.
	BytesBefore int64
	BytesAfter  int64
	// Merges is the number of 'merge' passes before the index stopped shrinking.
	Merges int
}

// FTSShrinkStatus is what --status reports: how much shrinking is left.
type FTSShrinkStatus struct {
	// Started is false when no pass ever wrote a checkpoint.
	Started bool
	// Complete is true when the last pass reached its high-water rowid.
	Complete     bool
	ScannedRowID int64
	// PendingRows and PendingChars count the tool rows still holding more than
	// ftsToolShrinkMinChars characters and their text length.
	PendingRows  int64
	PendingChars int64
	// EstimatedReclaimBytes is the space the remaining rows are expected to
	// free in the file (content copy plus trigram index, see ftsShrinkIndexRatio).
	EstimatedReclaimBytes int64
}

// Remaining reports whether the ledger still holds tool rows that a shrink
// pass would rewrite.
func (st FTSShrinkStatus) Remaining() bool { return st.PendingRows > 0 }

// FTSToolMaxChars is the length at or below which a tool result is indexed as
// it is (see ftsToolMaxChars); the CLI reports it to the user.
const FTSToolMaxChars = ftsToolMaxChars

// ShrinkFTSToolOutputs rewrites the tool rows of entries_fts to the truncated
// form, at most Batch rows per write transaction, resumable through a rowid
// checkpoint. A cancelled context stops between batches and keeps the
// checkpoint of the last committed one; the next call continues there. Rows
// written concurrently are already truncated and are skipped (their text is
// short), rows deleted concurrently (session removed) simply match nothing.
func (s *Store) ShrinkFTSToolOutputs(ctx context.Context, opts FTSShrinkOptions) (FTSShrinkReport, error) {
	batch := opts.Batch
	if batch <= 0 {
		batch = ftsShrinkBatchRows
	}
	batch = min(batch, ftsShrinkMaxBatch)
	if err := s.ensureShrinkState(ctx); err != nil {
		return FTSShrinkReport{}, err
	}
	// The high-water mark is fixed when the pass starts: rows created later are
	// written by the truncating code path and do not belong to this pass.
	highWater, err := s.maxFTSRowID(ctx)
	if err != nil {
		return FTSShrinkReport{}, err
	}
	last, err := s.shrinkRowID(ctx)
	if err != nil {
		return FTSShrinkReport{}, err
	}
	if last > highWater {
		last = highWater
	}
	report := FTSShrinkReport{ScannedRowID: last, HighWater: highWater}
	report.BytesBefore, err = s.ftsDataBytes(ctx)
	if err != nil {
		return report, err
	}
	if err := s.markShrinkStarted(ctx); err != nil {
		return report, err
	}
	for last < highWater {
		if err := ctx.Err(); err != nil {
			return report, err // resumable: the checkpoint of the last batch stands
		}
		next := min(last+int64(batch), highWater)
		rows, err := s.shrinkBatch(ctx, last, next)
		if err != nil {
			return report, err
		}
		last = next
		report.Rows += rows
		report.Batches++
		report.ScannedRowID = last
		if opts.OnBatch != nil {
			opts.OnBatch(FTSShrinkProgress{ScannedRowID: last, HighWater: highWater, Rows: report.Rows})
		}
	}
	if err := s.markShrinkComplete(ctx, report.Rows); err != nil {
		return report, err
	}
	report.Merges, err = s.mergeFTS(ctx)
	if err != nil {
		return report, err
	}
	report.BytesAfter, err = s.ftsDataBytes(ctx)
	return report, err
}

// shrinkBatch runs one batch in its own write transaction (BEGIN IMMEDIATE with
// the store's busy retry) and commits the checkpoint with it: the checkpoint
// never runs ahead of the rows it describes.
func (s *Store) shrinkBatch(ctx context.Context, from, to int64) (int64, error) {
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	result, err := tx.ExecContext(ctx, ftsShrinkUpdateSQL, from, to)
	if err != nil {
		return 0, fmt.Errorf("shrink entries_fts rows (%d, %d]: %w", from, to, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fts_shrink_state SET scanned_rowid = ?, complete = 0,
        shrunk_rows = shrunk_rows + ?, updated_at = ? WHERE id = 1`, to, rows, formatTime(time.Now())); err != nil {
		return 0, fmt.Errorf("record fts shrink checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rows, nil
}

// mergeFTS folds the segments the rewrites left behind into one, which is what
// actually frees the text of the old rows: an UPDATE only marks the old terms
// deleted, the space comes back when their segments are rebuilt without them.
//
// It runs FTS5's 'optimize' (one full merge) instead of the design's repeated
// `('merge', 500)`: a merge pass only consolidates a bounded number of pages,
// and on the 20,000-row benchmark a 500-page pass did not reduce
// entries_fts_data at all (107.9MB -> 116.7MB) -- reclaiming a 27,000-page
// index that way would need hundreds of passes. 'optimize' is the documented
// "merge everything" command and stays one bounded pass; a caller that wants
// gentler passes can rerun the command later.
func (s *Store) mergeFTS(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO entries_fts(entries_fts) VALUES('optimize')`); err != nil {
		return 0, fmt.Errorf("optimize entries_fts: %w", err)
	}
	return 1, nil
}

// FTSShrinkStatus reports how much shrinking is left. It reads two counters
// over entries_fts (only tool rows longer than the limit qualify).
func (s *Store) FTSShrinkStatus(ctx context.Context) (FTSShrinkStatus, error) {
	var status FTSShrinkStatus
	exists, err := s.tableExists(ctx, "fts_shrink_state")
	if err != nil {
		return status, err
	}
	if exists {
		err := s.db.QueryRowContext(ctx, `SELECT scanned_rowid, complete FROM fts_shrink_state WHERE id = 1`).
			Scan(&status.ScannedRowID, &status.Complete)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			status.Complete = false
		case err != nil:
			return status, fmt.Errorf("read fts shrink state: %w", err)
		default:
			status.Started = true
		}
	}
	status.PendingRows = 0
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(text)), 0) FROM entries_fts
        WHERE role = 'tool' AND length(text) > `+strconv.Itoa(ftsToolShrinkMinChars)).
		Scan(&status.PendingRows, &status.PendingChars)
	if err != nil {
		return status, fmt.Errorf("count unshrunk tool rows: %w", err)
	}
	// Each remaining row keeps at most ftsToolMaxChars characters plus the
	// ellipsis (three characters), so everything above that is what the rewrite
	// removes from the content copy; the index shrinks with it.
	removed := status.PendingChars - status.PendingRows*int64(ftsToolShrinkMinChars)
	if removed > 0 {
		status.EstimatedReclaimBytes = removed * ftsShrinkIndexRatio
	}
	return status, nil
}

// FTSShrinkPending reports whether the ledger holds tool rows the shrink
// command has not rewritten yet: the search notice tells the model that old
// hits may still be full-text matches. It is asked once per store handle (see
// noteShrinkPending) and answers from the checkpoint when a pass already
// completed, so the expensive count is not run on every search.
func (s *Store) FTSShrinkPending(ctx context.Context) (bool, error) {
	status, err := s.FTSShrinkStatus(ctx)
	if err != nil {
		return false, err
	}
	if status.Complete {
		return false, nil
	}
	var one int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM entries_fts
        WHERE role = 'tool' AND length(text) > `+strconv.Itoa(ftsToolShrinkMinChars)+` LIMIT 1`).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil // nothing to shrink: a fresh or already short ledger
	case err != nil:
		return false, fmt.Errorf("check unshrunk tool rows: %w", err)
	}
	return true, nil
}

// ResetFTSShrink drops the checkpoint (--abort). It changes no row of the
// index: the next pass starts at the first rowid again, which is what an
// operator wants after a partly finished run looks wrong.
func (s *Store) ResetFTSShrink(ctx context.Context) error {
	if err := s.ensureShrinkState(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM fts_shrink_state WHERE id = 1`)
	return err
}

// ensureShrinkState creates the checkpoint table if it is missing. The table is
// created by the command, never by a migration (see the package comment above).
func (s *Store) ensureShrinkState(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, ftsShrinkStateDDL); err != nil {
		return fmt.Errorf("create fts shrink state: %w", err)
	}
	return nil
}

// shrinkRowID returns the checkpointed rowid (0 when there is none).
func (s *Store) shrinkRowID(ctx context.Context) (int64, error) {
	var rowid int64
	err := s.db.QueryRowContext(ctx, `SELECT scanned_rowid FROM fts_shrink_state WHERE id = 1`).Scan(&rowid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("read fts shrink checkpoint: %w", err)
	}
	return rowid, nil
}

// markShrinkStarted writes the checkpoint row as an unfinished pass.
func (s *Store) markShrinkStarted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO fts_shrink_state(id, scanned_rowid, complete, shrunk_rows, started_at, updated_at)
        VALUES(1, 0, 0, 0, ?, ?)
        ON CONFLICT(id) DO UPDATE SET complete = 0, shrunk_rows = 0, started_at = excluded.started_at, updated_at = excluded.updated_at`,
		formatTime(time.Now()), formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("start fts shrink: %w", err)
	}
	return nil
}

// markShrinkComplete flags the pass as done so that searches stop announcing
// unfinished shrinking.
func (s *Store) markShrinkComplete(ctx context.Context, rows int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE fts_shrink_state SET complete = 1, shrunk_rows = ?, updated_at = ? WHERE id = 1`,
		rows, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("finish fts shrink: %w", err)
	}
	return nil
}

// maxFTSRowID is the highest rowid of entries_fts (0 for an empty index).
func (s *Store) maxFTSRowID(ctx context.Context) (int64, error) {
	var rowid int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid), 0) FROM entries_fts`).Scan(&rowid); err != nil {
		return 0, fmt.Errorf("read entries_fts rowid: %w", err)
	}
	return rowid, nil
}

// ftsDataBytes is the size of the FTS index's own pages, the number a shrink
// pass and its benchmark compare (the whole file only shrinks after VACUUM).
func (s *Store) ftsDataBytes(ctx context.Context) (int64, error) {
	var size int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(block)), 0) FROM entries_fts_data`).Scan(&size); err != nil {
		return 0, fmt.Errorf("measure entries_fts_data: %w", err)
	}
	return size, nil
}

// FTSShrinkPendingNotice is the note a search result carries while old tool
// outputs are still indexed in full: a hit in the middle of such a result is a
// real match that a later re-index would not find, which the model should know
// before it reads "not found" as "not stored".
const FTSShrinkPendingNotice = "historical tool outputs are not shrunk yet: some older results still match their full text"

// noteShrinkPending returns FTSShrinkPendingNotice while the ledger holds
// unshrunk tool rows, plain text after a completed pass or on a ledger that
// never had a long tool result. The answer is computed once per store handle:
// only another process's maintenance command can change it, and a search must
// not pay for the check every time.
func (s *Store) noteShrinkPending(ctx context.Context) string {
	s.shrinkMu.Lock()
	defer s.shrinkMu.Unlock()
	if s.shrinkPending == nil {
		pending, err := s.FTSShrinkPending(ctx)
		if err != nil {
			return "" // a notice is never worth failing a search for
		}
		s.shrinkPending = &pending
	}
	if *s.shrinkPending {
		return FTSShrinkPendingNotice
	}
	return ""
}

// tableExists reports whether the schema holds that table. The shrink
// checkpoint lives outside the migrations, so the status of a ledger the
// command never touched has to be answered without creating anything.
func (s *Store) tableExists(ctx context.Context, name string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("look up table %s: %w", name, err)
	}
	return true, nil
}
