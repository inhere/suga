package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

// `suga data bodies --check` (design 2026-10-19, 0.3; design 2026-10-20, revisions
// 5 and 8): verify every stub of either type against its record and count the
// records no stub points at, per file.

// ToolBodyCheckReport is what --check found. It only reports: cleaning an orphan
// record would rewrite the file and move every offset after it, which has to
// happen under the write lock with the stubs, and is left out of this version.
type ToolBodyCheckReport struct {
	// Checked is the number of stub rows verified, Missing those whose body could
	// not be read back (the ledger points at nothing valid).
	Checked int64
	Missing int64
	// OrphanRecords are records no stub points at (a crash before the commit of a
	// moved body leaves one), OrphanBytes their size.
	OrphanRecords int64
	OrphanBytes   int64
	// Problems lists the first few failures, for the operator to see.
	Problems []string
}

// OK reports whether the ledger and its files agree.
func (r ToolBodyCheckReport) OK() bool { return r.Missing == 0 }

// ToolBodyCheck verifies every stub and reports what does not line up. It only
// reads: a missing body or an orphan record is left for the operator (restore
// from a backup, or --undo when the file is still complete).
func (s *Store) ToolBodyCheck(ctx context.Context) (ToolBodyCheckReport, error) {
	var report ToolBodyCheckReport
	used := map[string]map[string]bool{} // session -> "off,len" the stubs point at
	var cursor cursorState
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		rows, err := toolBodyStubWindow(ctx, s.db, cursor, toolBodyCheckBatch)
		if err != nil {
			return report, err
		}
		if len(rows) == 0 {
			break
		}
		readers := map[string]*toolBodyReader{}
		for i := range rows {
			row := &rows[i]
			ref, err := parseBodyRef(row.Ref)
			if err != nil {
				report.Missing++
				report.addProblem(fmt.Sprintf("session %s seq %d: %v", row.SessionID, row.Seq, err))
				continue
			}
			if used[row.SessionID] == nil {
				used[row.SessionID] = map[string]bool{}
			}
			reader, ok := readers[row.SessionID]
			if !ok {
				reader, err = s.openToolBodyReader(row.SessionID)
				if err != nil {
					report.Missing++
					report.addProblem(fmt.Sprintf("session %s seq %d: %v", row.SessionID, row.Seq, err))
					continue
				}
				readers[row.SessionID] = reader
			}
			if _, err := reader.Read(row.SessionID, row.Seq, ref); err != nil {
				report.Missing++
				report.addProblem(err.Error())
				continue
			}
			// The record counts as used whatever file it was read from: an exact copy
			// under the other name refers to the same bytes.
			used[row.SessionID][toolBodyCheckUsedKey(ref)] = true
			report.Checked++
		}
		for _, r := range readers {
			_ = r.Close()
		}
		last := rows[len(rows)-1]
		cursor.SessionID, cursor.Seq = last.SessionID, last.Seq
		if len(rows) < toolBodyCheckBatch {
			break
		}
	}
	// Orphans: every record of every session file that no stub points at.
	files, err := s.toolBodyFiles()
	if err != nil {
		return report, err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		orphans, bytes, err := countToolBodyOrphans(file.path, used[file.sessionID])
		if err != nil {
			report.addProblem(err.Error())
			continue
		}
		report.OrphanRecords += orphans
		report.OrphanBytes += bytes
	}
	return report, nil
}

// toolBodyCheckUsedKey is the orphan key of one stub: the record span and its
// digest. Two files of one session may hold the same offset; a record under the
// other name that does not hash to the same bytes is still an orphan, an exact
// copy of a referenced record is not.
func toolBodyCheckUsedKey(ref BodyRef) string {
	return fmt.Sprintf("%d,%d,%s", ref.Off, ref.Len, strings.ToLower(ref.SHA256))
}

// addProblem keeps the first few failures (a broken ledger can have thousands).
func (r *ToolBodyCheckReport) addProblem(msg string) {
	if len(r.Problems) < 10 {
		r.Problems = append(r.Problems, msg)
	}
}

// countToolBodyOrphans walks the records of one file and counts those the stubs
// of the session do not reference (by span and digest).
func countToolBodyOrphans(path string, used map[string]bool) (int64, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read %s: %w", path, err)
	}
	var orphans, orphanBytes int64
	for at := 0; at < len(data); {
		next := bytes.IndexByte(data[at:], '\n')
		if next < 0 {
			break // a torn last line: the writer was interrupted mid-record
		}
		lineStart := int64(at)
		line := data[at : at+next]
		at += next + 1
		off, length, ok := toolBodyRecordSpan(line, lineStart)
		if !ok {
			continue
		}
		// The record is matched by span and digest, so an exact copy under the other
		// file name is not an orphan and a different record at the same offset is.
		bodyStart := bytes.Index(line, []byte(`,"json":`)) + len(`,"json":`)
		ref := BodyRef{Off: off, Len: length, SHA256: fmt.Sprintf("%x", sha256.Sum256(line[bodyStart:len(line)-1]))}
		if used == nil || !used[toolBodyCheckUsedKey(ref)] {
			orphans++
			orphanBytes += length
		}
	}
	return orphans, orphanBytes, nil
}

// toolBodyRecordSpan returns the offset and length of the raw JSON of one record
// line whose first byte is at lineStart in the file.
func toolBodyRecordSpan(line []byte, lineStart int64) (int64, int64, bool) {
	marker := []byte(`,"json":`)
	i := bytes.Index(line, marker)
	if i < 0 || len(line) == 0 || line[len(line)-1] != '}' {
		return 0, 0, false
	}
	bodyStart := i + len(marker)
	if bodyStart > len(line)-1 {
		return 0, 0, false
	}
	return lineStart + int64(bodyStart), int64(len(line) - bodyStart - 1), true
}
