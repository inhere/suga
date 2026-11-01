package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppendEntries appends a batch to the session log in one transaction and
// returns the stored entries with their assigned seq (contiguous, per session).
//
// A tool or assistant entry whose body is larger than
// store.body_inline_bytes moves into the session body file (tools.jsonl or bodies.jsonl) inside this
// transaction (design 0.3): the row, the record and the swap to the stub commit
// together, and a failure of the file write rolls the transaction back after
// truncating the file to the length it had. The returned entries keep the full
// body. An assistant entry moves only when its stub saves more than the threshold
// (design 2026-10-20, revision 8): a big Content without large Reasoning/Metadata
// stays inline, since the stub has to keep the Content anyway.
func (s *Store) AppendEntries(ctx context.Context, sessionID string, in ...NewEntry) ([]Entry, error) {
	if len(in) == 0 {
		return nil, nil
	}
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// One file per session: the transaction opens it once, syncs once, and drops
	// what it wrote if anything fails.
	batch := &entryBatch{}
	committed := false
	defer func() {
		if !committed {
			_ = batch.writer.Close(false)
		}
	}()

	now := time.Now()
	// Write first: takes the write lock before MAX(seq) is read, so two
	// processes appending to one session cannot compute the same seq.
	res, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID)
	if err != nil {
		return nil, err
	}
	if err := requireAffected(res, ErrSessionNotFound); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(in))
	for _, e := range in {
		entry, err := s.insertEntryTx(ctx, tx, sessionID, batch, e, 0, now)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	if err := s.swapMovedBodiesTx(ctx, tx, sessionID, batch); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	_ = batch.writer.Close(true)
	return out, nil
}

// entryBatch is the state of the entry rows one transaction writes: the session
// body file writer (opened by the first row that moves out), the records waiting
// for the swap to their stubs, and the cached persistent switch.
type entryBatch struct {
	writer      *toolBodyWriter
	moved       []toolBodyMove
	moveChecked bool
	moveEnabled bool
	// dropFileIfNew makes an aborted batch remove a body file it created, instead
	// of leaving an empty one (a fork writes a session that does not exist yet).
	dropFileIfNew bool
}

// insertEntryTx writes one entry row, its full-text row and, for a big tool or
// assistant body, the record in the session body file (the swap to the stub is
// done by swapMovedBodiesTx once every row is in). It is the part of
// AppendEntries that runs inside the transaction, shared with
// CreateForkedSession. seq 0 assigns MAX(seq)+1; a positive seq is stored as
// given (a fork writes the seq it mapped beforehand). createdAt is stored as is.
func (s *Store) insertEntryTx(ctx context.Context, tx *sql.Tx, sessionID string, b *entryBatch, e NewEntry, seq int64, createdAt time.Time) (Entry, error) {
	if e.Type == "" {
		return Entry{}, errors.New("entry type is required")
	}
	body := e.JSON
	if len(body) == 0 {
		body = json.RawMessage("{}")
	}
	if seq > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, sessionID, seq, e.TurnID, e.Type, string(body), formatTime(createdAt)); err != nil {
			return Entry{}, fmt.Errorf("append entry: %w", err)
		}
	} else {
		err := tx.QueryRowContext(ctx, `INSERT INTO entries(session_id, seq, turn_id, type, json, created_at)
			SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ? FROM entries WHERE session_id = ?
			RETURNING seq`, sessionID, e.TurnID, e.Type, string(body), formatTime(createdAt), sessionID).Scan(&seq)
		if err != nil {
			return Entry{}, fmt.Errorf("append entry: %w", err)
		}
	}
	// Same transaction: the entry and its full-text row commit together
	// (skipped by the SQL for non-indexed types and empty text, see fts.go).
	if _, err := tx.ExecContext(ctx, ftsInsertEntrySQL, sessionID, seq); err != nil {
		return Entry{}, fmt.Errorf("index entry: %w", err)
	}
	out := Entry{SessionID: sessionID, Seq: seq, TurnID: e.TurnID, Type: e.Type, JSON: body, CreatedAt: createdAt.UTC()}
	if !s.toolBodyCandidate(e.Type, body) {
		return out, nil
	}
	if !b.moveChecked {
		// The persistent switch is read inside the transaction, so a concurrent
		// `data bodies --undo` (which holds the same write lock) is honored.
		enabled, err := toolBodyMoveEnabled(ctx, tx)
		if err != nil {
			return Entry{}, err
		}
		b.moveEnabled, b.moveChecked = enabled, true
	}
	if !b.moveEnabled {
		return out, nil
	}
	stub, ok, err := buildToolBodyStub(e.Type, body)
	if err != nil {
		return Entry{}, fmt.Errorf("build tool body stub: %w", err)
	}
	if !ok {
		return out, nil // an undecodable row keeps its body inline
	}
	if e.Type == EntryAssistant && int64(len(body)-len(stub)) <= s.toolBodyInlineBytes {
		return out, nil // the stub has to keep the Content: only real savings move the row
	}
	if b.writer == nil {
		if b.writer, err = s.openToolBodyWriter(sessionID); err != nil {
			return Entry{}, err
		}
		b.writer.dropIfNew = b.dropFileIfNew
	}
	ref, err := b.writer.WriteRecord(seq, body)
	if err != nil {
		return Entry{}, err
	}
	b.moved = append(b.moved, toolBodyMove{seq: seq, stub: stub, ref: ref})
	return out, nil
}

// swapMovedBodiesTx syncs the records of the batch and then swaps their rows to
// the stubs. The records must be durable before the transaction that points at
// them commits. It does nothing when no row moved out.
func (s *Store) swapMovedBodiesTx(ctx context.Context, tx *sql.Tx, sessionID string, b *entryBatch) error {
	if b.writer == nil {
		return nil
	}
	if err := b.writer.Synced(); err != nil {
		return err
	}
	for _, m := range b.moved {
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET json = ?, body_ref = ? WHERE session_id = ? AND seq = ?`,
			string(m.stub), m.ref.String(), sessionID, m.seq); err != nil {
			return fmt.Errorf("store tool body stub: %w", err)
		}
	}
	return nil
}

// toolBodyCandidate reports whether an entry is one the store considers moving
// out by its size alone: a tool or assistant entry whose JSON is above the
// threshold. An assistant entry is judged again by the bytes its stub saves once
// the stub is built (see AppendEntries).
func (s *Store) toolBodyCandidate(typ string, body []byte) bool {
	if s.toolBodyInlineBytes <= 0 || int64(len(body)) <= s.toolBodyInlineBytes {
		return false
	}
	return typ == EntryTool || typ == EntryAssistant
}

// toolBodyMove is one record written in a transaction, waiting for the swap of
// its row to the stub.
type toolBodyMove struct {
	seq  int64
	stub []byte
	ref  BodyRef
}

func scanEntry(r rowScanner) (Entry, error) {
	var (
		e       Entry
		body    string
		created string
		bodyRef sql.NullString
	)
	if err := r.Scan(&e.SessionID, &e.Seq, &e.TurnID, &e.Type, &body, &created, &bodyRef); err != nil {
		return Entry{}, err
	}
	e.JSON, e.CreatedAt, e.bodyRef = json.RawMessage(body), parseTime(created), bodyRef.String
	return e, nil
}

const entryCols = `session_id, seq, turn_id, type, json, created_at, COALESCE(body_ref, '')`

// ListEntries returns entries ordered by seq, narrowed by f.
func (s *Store) ListEntries(ctx context.Context, sessionID string, f EntryFilter) ([]Entry, error) {
	q := `SELECT ` + entryCols + ` FROM entries WHERE session_id = ? AND seq > ?`
	args := []any{sessionID, f.SinceSeq}
	if f.TurnID != "" {
		q += ` AND turn_id = ?`
		args = append(args, f.TurnID)
	}
	if len(f.Types) > 0 {
		q += ` AND type IN (` + placeholders(len(f.Types)) + `)`
		for _, t := range f.Types {
			args = append(args, t)
		}
	}
	q += ` ORDER BY seq ASC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Moved bodies come back from the session file, one open per call (no file is
	// touched when no row of the result carries a stub, e.g. Types without tool).
	// NoHydrate leaves the stubs in place for a caller that only reads Content or
	// metadata, or that fetches the bodies it sends on with HydrateEntries.
	if f.NoHydrate {
		return out, nil
	}
	if err := s.restoreToolBodies(sessionID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// EntriesAround returns the target entry and up to window entries on either
// side, ordered by the stable session sequence. Entry rows are append-only:
// AppendEntries assigns the next sequence, while session deletion is the only
// production path that removes entries (through the sessions foreign-key
// cascade); no path renumbers an existing sequence.
func (s *Store) EntriesAround(ctx context.Context, sessionID string, seq int64, window int) ([]Entry, error) {
	if seq <= 0 {
		return nil, errors.New("entry sequence must be positive")
	}
	if window < 0 {
		return nil, errors.New("entry window must not be negative")
	}
	lower := seq - int64(window)
	if lower < 1 || lower > seq {
		lower = 1
	}
	upper := seq + int64(window)
	if upper < seq {
		upper = int64(^uint64(0) >> 1)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryCols+` FROM entries
		WHERE session_id = ? AND seq >= ? AND seq <= ? ORDER BY seq ASC`, sessionID, lower, upper)
	if err != nil {
		return nil, fmt.Errorf("read entries around %s/%d: %w", sessionID, seq, err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.restoreToolBodies(sessionID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListEntryRefs returns the link fields of every entry of a session, oldest
// first, with the bodies left in the store (see EntryRef).
func (s *Store) ListEntryRefs(ctx context.Context, sessionID string) ([]EntryRef, error) {
	// The call id of a message lives under ToolCall (the call) or ToolResult
	// (the result); other entry types carry none. json_valid guards the
	// extraction: a foreign row must not fail the whole page.
	q := `SELECT seq, turn_id, type, created_at, CASE WHEN json_valid(json)
		THEN COALESCE(json_extract(json, '$.ToolCall.CallID'), json_extract(json, '$.ToolResult.CallID')) END
		FROM entries WHERE session_id = ? ORDER BY seq ASC`
	rows, err := s.db.QueryContext(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list entry refs: %w", err)
	}
	defer rows.Close()
	out := []EntryRef{}
	for rows.Next() {
		var (
			r       EntryRef
			callID  sql.NullString
			created string
		)
		if err := rows.Scan(&r.Seq, &r.TurnID, &r.Type, &created, &callID); err != nil {
			return nil, err
		}
		r.CreatedAt, r.CallID = parseTime(created), callID.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestEntry returns the highest-seq entry of the given type, or (nil, nil)
// when the session has none.
func (s *Store) LatestEntry(ctx context.Context, sessionID, typ string) (*Entry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+entryCols+` FROM entries WHERE session_id = ? AND type = ? ORDER BY seq DESC LIMIT 1`, sessionID, typ)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Entry{e}
	if err := s.restoreToolBodies(sessionID, out); err != nil {
		return nil, err
	}
	return &out[0], nil
}

// LastEntrySeq returns the highest entries seq of the session, 0 when it has
// none. A segment record that keeps nothing starts after it (--clear-ctx).
func (s *Store) LastEntrySeq(ctx context.Context, sessionID string) (int64, error) {
	var seq int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM entries WHERE session_id = ?`, sessionID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("read the last entry seq: %w", err)
	}
	return seq, nil
}

// RecentEntries returns the newest entries of the given types, newest first, at
// most limit of them (nil when the session has none). It walks the
// (session_id, seq) primary key backwards and stops after limit rows, so a
// session with a very long log still answers from a few index reads.
func (s *Store) RecentEntries(ctx context.Context, sessionID string, types []string, limit int) ([]Entry, error) {
	if len(types) == 0 || limit <= 0 {
		return nil, nil
	}
	q := `SELECT ` + entryCols + ` FROM entries WHERE session_id = ? AND type IN (` + placeholders(len(types)) + `)
		ORDER BY seq DESC LIMIT ?`
	args := make([]any, 0, len(types)+2)
	args = append(args, sessionID)
	for _, t := range types {
		args = append(args, t)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list recent entries: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.restoreToolBodies(sessionID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// queryEntries runs q (selecting entryCols); the caller restores moved bodies.
func (s *Store) queryEntries(ctx context.Context, q string, args []any, what string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// RecentEntriesSkipping is RecentEntries over the entries outside the masked
// intervals skip (newest first, at most limit). It walks the visible segments
// from the newest, one (session_id, seq) primary-key range query each, and stops
// when limit rows are found: a large masked interval at the tail costs one
// empty range read, not a walk over its rows. An empty skip is one query, the
// same as RecentEntries.
func (s *Store) RecentEntriesSkipping(ctx context.Context, sessionID string, types []string, skip SeqRanges, limit int) ([]Entry, error) {
	if len(types) == 0 || limit <= 0 {
		return nil, nil
	}
	segs := skip.visibleSegments()
	q := `SELECT ` + entryCols + ` FROM entries WHERE session_id = ? AND seq BETWEEN ? AND ? AND type IN (` +
		placeholders(len(types)) + `) ORDER BY seq DESC LIMIT ?`
	out := []Entry{}
	for i := len(segs) - 1; i >= 0 && len(out) < limit; i-- {
		args := make([]any, 0, len(types)+4)
		args = append(args, sessionID, segs[i][0], segs[i][1])
		for _, t := range types {
			args = append(args, t)
		}
		args = append(args, limit-len(out))
		part, err := s.queryEntries(ctx, q, args, "list recent entries")
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	if len(out) == 0 {
		return nil, nil
	}
	if err := s.restoreToolBodies(sessionID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// LatestEntrySkipping is LatestEntry over the entries outside skip, or (nil,
// nil) when none is visible.
func (s *Store) LatestEntrySkipping(ctx context.Context, sessionID, typ string, skip SeqRanges) (*Entry, error) {
	got, err := s.RecentEntriesSkipping(ctx, sessionID, []string{typ}, skip, 1)
	if err != nil || len(got) == 0 {
		return nil, err
	}
	return &got[0], nil
}

// EntriesFromSkipping returns up to limit entries of the given types with
// seq >= fromSeq outside skip, oldest first: the window read of recall. Empty
// types return nothing (as RecentEntries); the caller asks for one row more than
// it shows when it needs the seq the next window continues from.
func (s *Store) EntriesFromSkipping(ctx context.Context, sessionID string, types []string, fromSeq int64, skip SeqRanges, limit int) ([]Entry, error) {
	if len(types) == 0 || limit <= 0 {
		return nil, nil
	}
	q := `SELECT ` + entryCols + ` FROM entries WHERE session_id = ? AND seq BETWEEN ? AND ? AND type IN (` +
		placeholders(len(types)) + `) ORDER BY seq ASC LIMIT ?`
	out := []Entry{}
	for _, seg := range skip.visibleSegments() {
		if len(out) >= limit {
			break
		}
		lo := max(seg[0], fromSeq)
		if lo > seg[1] {
			continue
		}
		args := make([]any, 0, len(types)+4)
		args = append(args, sessionID, lo, seg[1])
		for _, t := range types {
			args = append(args, t)
		}
		args = append(args, limit-len(out))
		part, err := s.queryEntries(ctx, q, args, "list entries from seq")
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	if len(out) == 0 {
		return nil, nil
	}
	if err := s.restoreToolBodies(sessionID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// CountEntriesByTypeSkipping is CountEntriesByType over the entries outside
// skip: one primary-key range count per visible segment.
func (s *Store) CountEntriesByTypeSkipping(ctx context.Context, sessionID, typ string, skip SeqRanges) (int, error) {
	total := 0
	for _, seg := range skip.visibleSegments() {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM entries WHERE session_id = ? AND seq BETWEEN ? AND ? AND type = ?`,
			sessionID, seg[0], seg[1], typ).Scan(&n); err != nil {
			return 0, fmt.Errorf("count %s entries: %w", typ, err)
		}
		total += n
	}
	return total, nil
}

// CountEntriesByType counts a session's entries of one type. The query is a
// range scan of that session's primary-key rows with the type filter, never a
// walk of the whole entries table.
func (s *Store) CountEntriesByType(ctx context.Context, sessionID, typ string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM entries WHERE session_id = ? AND type = ?`,
		sessionID, typ).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s entries: %w", typ, err)
	}
	return n, nil
}

// LastEntryAt is the time of a session's newest entry (the zero time when the
// session has none). The primary key makes this a single index read plus the
// row it points at.
func (s *Store) LastEntryAt(ctx context.Context, sessionID string) (time.Time, error) {
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM entries WHERE session_id = ? ORDER BY seq DESC LIMIT 1`,
		sessionID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read last entry time: %w", err)
	}
	return parseTime(created), nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
