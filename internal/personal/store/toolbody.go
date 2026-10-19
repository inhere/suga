package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// Tool body files (design 2026-10-19-tool-output-files, 0.3).
//
// A tool entry whose JSON exceeds the inline threshold moves out of the ledger
// into one append-only file per session, sessions/<id>/tools.jsonl:
//
//	{"seq":N,"sha256":"<hex>","json":<entry JSON raw bytes>}\n
//
// The entries row keeps a small stub instead of the body and the location of
// the record in the entries.body_ref column ("<off>,<len>,<sha256>", off and len
// of the raw JSON bytes, whose sha256 the header repeats). Readers get the
// original bytes back through ReadAt, so ListEntries/LatestEntry return exactly
// what they returned before a body moved out (D2).
//
// The write itself happens inside the append transaction (D3): the row, the
// record and the swap to the stub commit together, so a body never lives only in
// the file while the row still looks inline, and never the other way round. A
// record written before a crash of the commit is an orphan: harmless (a later
// seq points at its own offset) and reported by `suga data tool-bodies --check`.

const (
	// DefaultToolBodyInlineBytes is store.tool_body_inline_bytes: entry JSON
	// above this many bytes moves to the session file. 0 keeps everything inline.
	DefaultToolBodyInlineBytes int64 = 1024
	// ToolBodyFileName is the append-only file of one session's moved bodies.
	ToolBodyFileName = "tools.jsonl"
	// toolBodyPreviewRunes bounds the preview a stub keeps of a tool result, so a
	// lost file still shows what the call was and how its output began.
	toolBodyPreviewRunes = 256
	// toolBodyHeaderWindow is how far before a record's JSON a reader looks for
	// the record header (seq + sha256) when it verifies a read.
	toolBodyHeaderWindow = 192
	// storeMetaTableDDL holds the store's small switches; created by Open, never
	// by a migration.
	storeMetaTableDDL = `CREATE TABLE IF NOT EXISTS store_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
)`
	// toolBodyMoveKey is the persistent switch: "0" keeps new tool bodies inline
	// (set by `data tool-bodies --undo`), any other value moves them out.
	toolBodyMoveKey = "tool_body_move"
)

// ErrToolBodyMissing is returned when a stub's body cannot be read back: the
// file is gone, the record is shorter than the stub says, or its seq/sha256 does
// not match. The reader never falls back to the stub's preview (D4).
var ErrToolBodyMissing = errors.New("tool body missing")

// BodyRef is where one moved body lives: the raw entry JSON at Off, Len bytes
// long, with SHA256 over those bytes.
type BodyRef struct {
	Off    int64
	Len    int64
	SHA256 string
}

// String is the entries.body_ref column form.
func (r BodyRef) String() string { return fmt.Sprintf("%d,%d,%s", r.Off, r.Len, r.SHA256) }

// Empty reports whether the ref points nowhere (an inline row).
func (r BodyRef) Empty() bool { return r.Len <= 0 || r.SHA256 == "" }

// parseBodyRef reads the entries.body_ref column value.
func parseBodyRef(value string) (BodyRef, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 3 {
		return BodyRef{}, fmt.Errorf("invalid body_ref %q", value)
	}
	off, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return BodyRef{}, fmt.Errorf("invalid body_ref offset %q: %w", value, err)
	}
	length, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return BodyRef{}, fmt.Errorf("invalid body_ref length %q: %w", value, err)
	}
	return BodyRef{Off: off, Len: length, SHA256: parts[2]}, nil
}

// toolBodyStub is the stored shape of a moved tool entry: enough for the link
// extractions (ToolResult.CallID), for a trace page and for a human to see what
// the call was. It deliberately carries no ResultSameAsContent marker: a stub is
// not a deduped row and the P2 rewrites skip it (entries.body_ref non-empty).
type toolBodyStub struct {
	Role       string              `json:"Role"`
	Content    string              `json:"Content"`
	ToolCall   *api.ToolCall       `json:"ToolCall,omitempty"`
	ToolResult *toolBodyStubResult `json:"ToolResult,omitempty"`
	Reasoning  string              `json:"Reasoning,omitempty"`
}

// toolBodyStubResult is the tool result as a stub keeps it: the fields a reader
// links on plus the beginning of the output.
type toolBodyStubResult struct {
	CallID  string `json:"CallID,omitempty"`
	Name    string `json:"Name,omitempty"`
	IsError bool   `json:"IsError,omitempty"`
	Preview string `json:"Preview,omitempty"`
}

// buildToolBodyStub returns the stub of one tool entry body. ok is false when the
// body cannot be decoded as a message: such a row is never moved out, so a
// foreign row keeps exactly what it had.
func buildToolBodyStub(raw []byte) (stub []byte, ok bool, err error) {
	var m api.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, nil
	}
	out := toolBodyStub{Role: m.Role, Content: previewText(m.Content), ToolCall: m.ToolCall, Reasoning: m.Reasoning}
	if m.ToolResult != nil {
		out.ToolResult = &toolBodyStubResult{
			CallID:  m.ToolResult.CallID,
			Name:    m.ToolResult.Name,
			IsError: m.ToolResult.IsError,
			Preview: previewText(m.ToolResult.Content),
		}
	}
	stub, err = json.Marshal(out)
	if err != nil {
		return nil, false, err
	}
	return stub, true, nil
}

// previewText cuts s to the preview length in runes (never in the middle of a
// multi-byte character) and marks the cut.
func previewText(s string) string {
	runes := []rune(s)
	if len(runes) <= toolBodyPreviewRunes {
		return s
	}
	return string(runes[:toolBodyPreviewRunes]) + "…"
}

// toolBodyPath is the file of one session. A session id that sessionDir()
// rejects (separators, "..", absolute) never has a file: such a row stays inline.
func (s *Store) toolBodyPath(sessionID string) (string, bool) {
	dir, ok := sessionDir(s.sessionsDir, sessionID)
	if !ok {
		return "", false
	}
	return filepath.Join(dir, ToolBodyFileName), true
}

// sessionDir returns sessions/<id> when id is a plain single path element; an id
// that could name another place yields false, so a stored id can never make the
// store read or write outside the sessions root.
func sessionDir(sessionsDir, id string) (string, bool) {
	if id == "" || id == "." || strings.ContainsAny(id, `/\:`) || !filepath.IsLocal(id) {
		return "", false
	}
	return filepath.Join(sessionsDir, id), true
}

// toolBodyWriter appends the records of one session inside a write transaction.
// The same transaction opens the file once, syncs once and truncates back to the
// length it found on any failure, so a rolled back transaction leaves no record
// behind.
type toolBodyWriter struct {
	path   string
	file   *os.File
	size   int64 // length before this writer's first record
	pos    int64 // length after the records written so far
	dirty  bool
	closed bool
}

// openToolBodyWriter opens (creating) the session file for appending.
func (s *Store) openToolBodyWriter(sessionID string) (*toolBodyWriter, error) {
	path, ok := s.toolBodyPath(sessionID)
	if !ok {
		return nil, fmt.Errorf("session id %q cannot own a tool body file", sessionID)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open tool body file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat tool body file: %w", err)
	}
	if created {
		// The file itself is synced by Sync; the new directory entry needs its own
		// sync, or a power loss could lose the whole file while the ledger keeps
		// its stubs.
		if err := syncDir(dir); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("sync session directory: %w", err)
		}
	}
	return &toolBodyWriter{path: path, file: file, size: info.Size(), pos: info.Size()}, nil
}

// WriteRecord appends one record and returns its reference. off/len cover the
// raw entry JSON only, so a reader ReadAts exactly the bytes the row held.
func (w *toolBodyWriter) WriteRecord(seq int64, raw []byte) (BodyRef, error) {
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	header := fmt.Sprintf(`{"seq":%d,"sha256":"%s","json":`, seq, digest)
	record := make([]byte, 0, len(header)+len(raw)+2)
	record = append(record, header...)
	record = append(record, raw...)
	record = append(record, '}', '\n')
	if _, err := w.file.Write(record); err != nil {
		return BodyRef{}, fmt.Errorf("append tool body record: %w", err)
	}
	off := w.pos + int64(len(header))
	w.pos += int64(len(record))
	w.dirty = true
	return BodyRef{Off: off, Len: int64(len(raw)), SHA256: digest}, nil
}

// Synced makes the records durable before the transaction that points at them
// commits.
func (w *toolBodyWriter) Synced() error {
	if w == nil || !w.dirty {
		return nil
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sync tool body file: %w", err)
	}
	w.dirty = false
	return nil
}

// Close closes the file, truncating back to the length the writer found unless
// keep asked to keep what it wrote (a committed transaction).
func (w *toolBodyWriter) Close(keep bool) error {
	if w == nil || w.closed {
		return nil
	}
	w.closed = true
	var err error
	// The file is closed before a rollback truncates it: Windows refuses
	// SetEndOfFile on a handle opened for append ("Access is denied"), which would
	// silently leave the record of a rolled back transaction behind.
	if closeErr := w.file.Close(); closeErr != nil {
		err = closeErr
	}
	if !keep {
		if truncErr := os.Truncate(w.path, w.size); truncErr != nil && !errors.Is(truncErr, os.ErrNotExist) {
			if err == nil {
				err = fmt.Errorf("truncate tool body file: %w", truncErr)
			}
		}
	}
	return err
}

// toolBodyReader reads the records of one session. A caller opens it once per
// session and reads every stub of the batch through it, so a resumed session
// replays its file almost sequentially.
type toolBodyReader struct {
	path string
	file *os.File
}

// openToolBodyReader opens the session file for reading.
func (s *Store) openToolBodyReader(sessionID string) (*toolBodyReader, error) {
	path, ok := s.toolBodyPath(sessionID)
	if !ok {
		return nil, fmt.Errorf("%w: session %q cannot own a tool body file", ErrToolBodyMissing, sessionID)
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: session %s has no %s (restore it from a backup, or the ledger and the files are out of sync)", ErrToolBodyMissing, sessionID, ToolBodyFileName)
		}
		return nil, fmt.Errorf("%w: open %s: %v", ErrToolBodyMissing, path, err)
	}
	return &toolBodyReader{path: path, file: file}, nil
}

// Read returns the raw entry JSON of seq, verifying the record's seq and sha256.
func (r *toolBodyReader) Read(sessionID string, seq int64, ref BodyRef) ([]byte, error) {
	missing := func(format string, args ...any) error {
		return fmt.Errorf("%w: session %s seq %d: %s", ErrToolBodyMissing, sessionID, seq, fmt.Sprintf(format, args...))
	}
	if ref.Empty() || ref.Off < 0 {
		return nil, missing("invalid body_ref %q", ref.String())
	}
	start := ref.Off - toolBodyHeaderWindow
	if start < 0 {
		start = 0
	}
	window := make([]byte, ref.Off-start+ref.Len+2)
	if _, err := r.file.ReadAt(window, start); err != nil && len(window) == 0 {
		return nil, missing("read %s: %v", r.path, err)
	}
	headerLen := int(ref.Off - start)
	if int64(len(window)) < ref.Off-start+ref.Len {
		return nil, missing("%s ends before the record does", r.path)
	}
	body := window[headerLen : headerLen+int(ref.Len)]
	sum := sha256.Sum256(body)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), ref.SHA256) {
		return nil, missing("sha256 mismatch (the record changed or the stub belongs to another record)")
	}
	recordSeq, ok := parseToolBodyHeader(window[:headerLen])
	if !ok {
		return nil, missing("record header not found in %s", r.path)
	}
	if recordSeq != seq {
		return nil, missing("record holds seq %d, the row is seq %d", recordSeq, seq)
	}
	return body, nil
}

// Close releases the file.
func (r *toolBodyReader) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	return r.file.Close()
}

// parseToolBodyHeader reads seq from the record header that ends where the raw
// JSON begins. The window may also hold the tail of the previous record, so the
// last header-shaped occurrence wins.
func parseToolBodyHeader(window []byte) (int64, bool) {
	marker := []byte(`{"seq":`)
	for at := len(window); at > 0; {
		i := bytes.LastIndex(window[:at], marker)
		if i < 0 {
			return 0, false
		}
		if seq, ok := parseToolBodyHeaderAt(window[i:]); ok {
			return seq, true
		}
		at = i
	}
	return 0, false
}

// parseToolBodyHeaderAt parses `{"seq":N,"sha256":"<64 hex>","json":` exactly.
func parseToolBodyHeaderAt(head []byte) (int64, bool) {
	if !bytes.HasPrefix(head, []byte(`{"seq":`)) || !bytes.HasSuffix(head, []byte(`,"json":`)) {
		return 0, false
	}
	rest := head[len(`{"seq":`):]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || !bytes.HasPrefix(rest[digits:], []byte(`,"sha256":"`)) {
		return 0, false
	}
	seq, err := strconv.ParseInt(string(rest[:digits]), 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

// restoreToolBodies replaces the stubs of entries with the bodies their records
// hold, opening one file per session. A session without stubs costs no file
// access at all (Types without tool included).
func (s *Store) restoreToolBodies(sessionID string, entries []Entry) error {
	refs := 0
	for _, e := range entries {
		if e.bodyRef != "" {
			refs++
		}
	}
	if refs == 0 {
		return nil
	}
	reader, err := s.openToolBodyReader(sessionID)
	if err != nil {
		return err
	}
	defer reader.Close()
	for i := range entries {
		if entries[i].bodyRef == "" {
			continue
		}
		ref, err := parseBodyRef(entries[i].bodyRef)
		if err != nil {
			return fmt.Errorf("%w: session %s seq %d: %v", ErrToolBodyMissing, sessionID, entries[i].Seq, err)
		}
		body, err := reader.Read(sessionID, entries[i].Seq, ref)
		if err != nil {
			return err
		}
		entries[i].JSON = body
	}
	return nil
}

// ensureStoreMeta creates the switch table (write opens only).
func (s *Store) ensureStoreMeta(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, storeMetaTableDDL); err != nil {
		return fmt.Errorf("create store meta: %w", err)
	}
	return nil
}

// toolBodyMoveEnabled reads the persistent switch inside a write transaction, so
// a concurrent `--undo` (which holds the same lock) cannot be overrun by a
// writer that read the switch earlier.
func toolBodyMoveEnabled(ctx context.Context, tx *sql.Tx) (bool, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key = ?`, toolBodyMoveKey).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("read tool body switch: %w", err)
	}
	return value != "0", nil
}

// SetToolBodyMove writes the persistent switch: false keeps every new tool body
// inline until a later `--move` turns it on again.
func (s *Store) SetToolBodyMove(ctx context.Context, enabled bool) error {
	if err := s.ensureStoreMeta(ctx); err != nil {
		return err
	}
	value := "1"
	if !enabled {
		value = "0"
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO store_meta(key, value) VALUES(?, ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value`, toolBodyMoveKey, value); err != nil {
		return fmt.Errorf("write tool body switch: %w", err)
	}
	return nil
}
