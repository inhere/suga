package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Storage usage report (`suga data usage`, design 2026-10-20).
//
// The report answers "where is the space": the database file and its free
// pages, the entries and events that fill it, the full-text index, and the
// per-session files below the sessions root. It is a pure reader: every number
// comes from a SQL aggregate (sum(length(...)), json_extract, json_each) or
// from a directory walk, so no entry body is ever read into Go. Open it with
// OpenReadOnly; it never takes the maintenance lock and never writes.

// UsageOptions narrows the usage report. Top bounds every "N largest" list.
type UsageOptions struct {
	Top int
}

// SizeStat is a row or file count with the bytes it occupies. Rows counts
// ledger records, Files counts files on disk; a section fills the one that
// applies.
type SizeStat struct {
	Rows  int64 `json:"rows,omitempty"`
	Files int64 `json:"files,omitempty"`
	Bytes int64 `json:"bytes"`
}

// NamedStat is one row of a breakdown (an entry type, an event kind, a JSON
// field, a tool name).
type NamedStat struct {
	Name  string `json:"name"`
	Rows  int64  `json:"rows,omitempty"`
	Bytes int64  `json:"bytes"`
}

// UsageFile is the database file itself.
type UsageFile struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	PageSize  int64  `json:"page_size"`
	Pages     int64  `json:"pages"`
	FreePages int64  `json:"free_pages"`
	FreeBytes int64  `json:"free_bytes"`
	WALBytes  int64  `json:"wal_bytes"`
	// Suggestion names the command that acts on this section ("" if none).
	Suggestion string `json:"suggestion,omitempty"`
}

// UsageAssistant splits the assistant entries, which carry the bulk of the
// ledger: their JSON fields, the metadata keys and the tool call arguments. The
// numbers describe what is still inside the ledger; a row whose body moved to a
// session file only counts the stub (see UsageEntries.MovedAssistant).
type UsageAssistant struct {
	Rows         int64       `json:"rows"`
	Bytes        int64       `json:"bytes"`
	Fields       []NamedStat `json:"fields"`
	MetadataKeys []NamedStat `json:"metadata_keys"`
	Tools        []NamedStat `json:"tool_arguments"`
}

// UsageToolEntries separates tool results that moved to the session file
// (entries.body_ref set, only a stub in the ledger) from the ones stored
// inline.
type UsageToolEntries struct {
	Stub   SizeStat `json:"stub"`
	Inline SizeStat `json:"inline"`
}

// UsageEntries is the entries table by type and by large JSON field.
type UsageEntries struct {
	Rows      int64            `json:"rows"`
	Bytes     int64            `json:"bytes"`
	Types     []NamedStat      `json:"types"`
	Assistant UsageAssistant   `json:"assistant"`
	Tool      UsageToolEntries `json:"tool"`
	// MovedAssistant are the assistant rows whose body lives in a session file
	// (entries.body_ref set, only a stub in the ledger). Their Reasoning,
	// Metadata and call arguments are in the file, so the Values above no longer
	// count them.
	MovedAssistant SizeStat `json:"moved_assistant"`
	Suggestion     string   `json:"suggestion,omitempty"`
}

// UsageEvents is the events table by kind. Bytes counts attrs_json and error,
// the two free-form columns.
type UsageEvents struct {
	Rows       int64       `json:"rows"`
	Bytes      int64       `json:"bytes"`
	Kinds      []NamedStat `json:"kinds"`
	Suggestion string      `json:"suggestion,omitempty"`
}

// UsageFTS is the entries_fts index: its rows (one per indexed entry) and the
// bytes of its shadow tables.
type UsageFTS struct {
	Rows         int64       `json:"rows"`
	Roles        []NamedStat `json:"roles"`
	DataBytes    int64       `json:"data_bytes"`
	ContentBytes int64       `json:"content_bytes"`
	Bytes        int64       `json:"bytes"`
	Suggestion   string      `json:"suggestion,omitempty"`
}

// UsageSessions is the sessions root outside the database: the per-session
// files and the sessions that hold the most.
type UsageSessions struct {
	InStore int64 `json:"in_store"`
	Dirs    int64 `json:"dirs"`
	// Tools is every body file of a session, under both names (Tools carries
	// tools.jsonl of version 0.36, BodyFiles says how it splits).
	Tools      SizeStat       `json:"tools"`
	BodyFiles  []NamedStat    `json:"body_files"`
	Outputs    SizeStat       `json:"outputs"`
	OutputsGz  SizeStat       `json:"outputs_gz"`
	Images     SizeStat       `json:"images"`
	Snapshots  SizeStat       `json:"snapshots"`
	Other      SizeStat       `json:"other"`
	Top        []UsageSession `json:"top"`
	Suggestion string         `json:"suggestion,omitempty"`
}

// UsageSession is one session in the "largest" list: its ledger bytes and its
// directory bytes, summed into Bytes.
type UsageSession struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	DBBytes  int64  `json:"db_bytes"`
	DirBytes int64  `json:"dir_bytes"`
	Bytes    int64  `json:"bytes"`
}

// UsageTable is the real page usage of one table or index, from SQLite's dbstat
// virtual table.
type UsageTable struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// UsageReport is the result of `suga data usage`.
type UsageReport struct {
	File     UsageFile     `json:"file"`
	Entries  UsageEntries  `json:"entries"`
	Events   UsageEvents   `json:"events"`
	FTS      UsageFTS      `json:"fts"`
	Sessions UsageSessions `json:"sessions"`
	// Tables holds the dbstat page usage (largest first) and DBStat says
	// whether this build of SQLite has the virtual table.
	Tables []UsageTable `json:"tables,omitempty"`
	DBStat bool         `json:"dbstat"`
	// Top is the N every list was cut at.
	Top int `json:"top"`
	// TotalBytes is the database file plus the files below the sessions root.
	TotalBytes int64 `json:"total_bytes"`
}

// usageDefaultTop is the list length without --top.
const usageDefaultTop = 10

// Usage builds the storage report. It reads through s, so it must be a
// read-only store; no statement writes.
func (s *Store) Usage(ctx context.Context, opts UsageOptions) (UsageReport, error) {
	top := opts.Top
	if top <= 0 {
		top = usageDefaultTop
	}
	rep := UsageReport{Top: top, File: UsageFile{Path: s.path}}
	if err := s.readUsageFile(ctx, &rep.File); err != nil {
		return UsageReport{}, err
	}
	perSession := map[string]int64{}
	if err := s.readUsageEntries(ctx, top, &rep.Entries, perSession); err != nil {
		return UsageReport{}, err
	}
	if err := s.readUsageEvents(ctx, top, &rep.Events, perSession); err != nil {
		return UsageReport{}, err
	}
	if err := s.readUsageFTS(ctx, &rep.FTS); err != nil {
		return UsageReport{}, err
	}
	if err := s.readUsageTables(ctx, top, &rep); err != nil {
		return UsageReport{}, err
	}
	dirs, err := scanSessionDirs(s.sessionsDir)
	if err != nil {
		return UsageReport{}, err
	}
	rep.Sessions = dirs.sessions
	if err := s.readUsageSessionTops(ctx, top, perSession, dirs.perSession, &rep.Sessions); err != nil {
		return UsageReport{}, err
	}
	rep.TotalBytes = rep.File.Bytes + rep.Sessions.Tools.Bytes + rep.Sessions.Outputs.Bytes +
		rep.Sessions.Images.Bytes + rep.Sessions.Snapshots.Bytes + rep.Sessions.Other.Bytes
	usageSuggestions(&rep)
	return rep, nil
}

// readUsageFile reports the database file size, its page geometry and the
// freelist: the pages a VACUUM would return to the disk.
func (s *Store) readUsageFile(ctx context.Context, f *UsageFile) error {
	info, err := os.Stat(f.Path)
	if err != nil {
		return fmt.Errorf("usage: stat store: %w", err)
	}
	f.Bytes = info.Size()
	for _, probe := range []struct {
		pragma string
		dst    *int64
	}{
		{"page_size", &f.PageSize},
		{"page_count", &f.Pages},
		{"freelist_count", &f.FreePages},
	} {
		if err := s.db.QueryRowContext(ctx, `PRAGMA `+probe.pragma).Scan(probe.dst); err != nil {
			return fmt.Errorf("usage: read pragma %s: %w", probe.pragma, err)
		}
	}
	f.FreeBytes = f.FreePages * f.PageSize
	// A WAL beside the ledger holds the pages written but not yet checkpointed.
	if info, err := os.Stat(f.Path + "-wal"); err == nil {
		f.WALBytes = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("usage: stat wal: %w", err)
	}
	return nil
}

// readUsageEntries fills the entries section in one pass per question: the
// GROUP BY type scan carries the assistant field sums, and two more passes
// break the assistant JSON down by metadata key and by tool name. Every size is
// the byte length of the stored JSON (cast to BLOB, so UTF-8 counts).
// body_ref may be missing on a store that was not opened writable since the
// column was added, so the stub split falls back to "all inline".
func (s *Store) readUsageEntries(ctx context.Context, top int, e *UsageEntries, perSession map[string]int64) error {
	hasBodyRef, err := s.usageHasColumn(ctx, "entries", "body_ref")
	if err != nil {
		return err
	}
	bodyRefExpr := `''`
	if hasBodyRef {
		bodyRefExpr = `COALESCE(body_ref, '')`
	}
	// One scan: the byte total of every row, the assistant field sums, the
	// type totals and the tool stub split all come from this grouping.
	q := `SELECT session_id, type, length(CAST(json AS BLOB)),
		CASE WHEN type = 'assistant' AND json_valid(json) THEN COALESCE(length(CAST(json_extract(json, '$.Content') AS BLOB)), 0) ELSE 0 END,
		CASE WHEN type = 'assistant' AND json_valid(json) THEN COALESCE(length(CAST(json_extract(json, '$.Reasoning') AS BLOB)), 0) ELSE 0 END,
		CASE WHEN type = 'assistant' AND json_valid(json) THEN COALESCE(length(CAST(json_extract(json, '$.Metadata') AS BLOB)), 0) ELSE 0 END,
		CASE WHEN type = 'assistant' AND json_valid(json) THEN COALESCE(length(CAST(json_extract(json, '$.ToolCall.Arguments') AS BLOB)), 0) ELSE 0 END,
		CASE WHEN ` + bodyRefExpr + ` <> '' THEN 1 ELSE 0 END
		FROM entries`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("usage: scan entries: %w", err)
	}
	defer rows.Close()
	byType := map[string]*NamedStat{}
	for rows.Next() {
		var (
			sessionID, typ                        string
			bytes, content, reasoning, meta, args int64
			stub                                  int
		)
		if err := rows.Scan(&sessionID, &typ, &bytes, &content, &reasoning, &meta, &args, &stub); err != nil {
			return fmt.Errorf("usage: scan entries: %w", err)
		}
		e.Rows++
		e.Bytes += bytes
		perSession[sessionID] += bytes
		stat := byType[typ]
		if stat == nil {
			stat = &NamedStat{Name: typ}
			byType[typ] = stat
		}
		stat.Rows++
		stat.Bytes += bytes
		if typ == EntryAssistant {
			e.Assistant.Rows++
			e.Assistant.Bytes += bytes
			addUsageField(&e.Assistant.Fields, "Content", content)
			addUsageField(&e.Assistant.Fields, "Reasoning", reasoning)
			addUsageField(&e.Assistant.Fields, "Metadata", meta)
			addUsageField(&e.Assistant.Fields, "ToolCall.Arguments", args)
		}
		if typ == EntryTool {
			if stub == 1 {
				e.Tool.Stub.Rows++
				e.Tool.Stub.Bytes += bytes
			} else {
				e.Tool.Inline.Rows++
				e.Tool.Inline.Bytes += bytes
			}
		}
		if typ == EntryAssistant && stub == 1 {
			e.MovedAssistant.Rows++
			e.MovedAssistant.Bytes += bytes
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("usage: scan entries: %w", err)
	}
	e.Types = sortUsageStats(byType)

	if e.Assistant.MetadataKeys, err = s.usageMetadataKeys(ctx, top); err != nil {
		return err
	}
	if e.Assistant.Tools, err = s.usageToolArguments(ctx, top); err != nil {
		return err
	}
	return nil
}

// addUsageField accumulates one JSON field of the assistant split.
func addUsageField(fields *[]NamedStat, name string, bytes int64) {
	for i := range *fields {
		if (*fields)[i].Name == name {
			(*fields)[i].Bytes += bytes
			return
		}
	}
	*fields = append(*fields, NamedStat{Name: name, Bytes: bytes})
}

// usageMetadataKeys ranks the keys of the assistant metadata maps by the bytes
// their values take (suga.responses_reasoning is the usual first one).
func (s *Store) usageMetadataKeys(ctx context.Context, top int) ([]NamedStat, error) {
	q := `SELECT je.key, COALESCE(SUM(length(CAST(je.value AS BLOB))), 0)
		FROM entries, json_each(json_extract(entries.json, '$.Metadata')) AS je
		WHERE entries.type = ? AND json_valid(entries.json)
		GROUP BY je.key ORDER BY 2 DESC, 1 LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, EntryAssistant, top)
	if err != nil {
		return nil, fmt.Errorf("usage: metadata keys: %w", err)
	}
	defer rows.Close()
	var out []NamedStat
	for rows.Next() {
		var stat NamedStat
		if err := rows.Scan(&stat.Name, &stat.Bytes); err != nil {
			return nil, fmt.Errorf("usage: metadata keys: %w", err)
		}
		out = append(out, stat)
	}
	return out, rows.Err()
}

// usageToolArguments ranks the tools by the bytes of their call arguments.
func (s *Store) usageToolArguments(ctx context.Context, top int) ([]NamedStat, error) {
	q := `SELECT COALESCE(json_extract(json, '$.ToolCall.Name'), ''), COUNT(1),
		COALESCE(SUM(length(CAST(json_extract(json, '$.ToolCall.Arguments') AS BLOB))), 0)
		FROM entries WHERE type = ? AND json_valid(json)
		AND json_extract(json, '$.ToolCall.Name') IS NOT NULL
		GROUP BY 1 ORDER BY 3 DESC, 1 LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, EntryAssistant, top)
	if err != nil {
		return nil, fmt.Errorf("usage: tool arguments: %w", err)
	}
	defer rows.Close()
	var out []NamedStat
	for rows.Next() {
		var stat NamedStat
		if err := rows.Scan(&stat.Name, &stat.Rows, &stat.Bytes); err != nil {
			return nil, fmt.Errorf("usage: tool arguments: %w", err)
		}
		out = append(out, stat)
	}
	return out, rows.Err()
}

// readUsageEvents groups the events by kind and adds each session's event bytes
// to perSession.
func (s *Store) readUsageEvents(ctx context.Context, top int, e *UsageEvents, perSession map[string]int64) error {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, kind, COUNT(1),
		SUM(length(CAST(attrs_json AS BLOB)) + length(CAST(error AS BLOB)))
		FROM events GROUP BY session_id, kind`)
	if err != nil {
		return fmt.Errorf("usage: scan events: %w", err)
	}
	defer rows.Close()
	byKind := map[string]*NamedStat{}
	for rows.Next() {
		var sessionID, kind string
		var n, bytes int64
		if err := rows.Scan(&sessionID, &kind, &n, &bytes); err != nil {
			return fmt.Errorf("usage: scan events: %w", err)
		}
		e.Rows += n
		e.Bytes += bytes
		perSession[sessionID] += bytes
		stat := byKind[kind]
		if stat == nil {
			stat = &NamedStat{Name: kind}
			byKind[kind] = stat
		}
		stat.Rows += n
		stat.Bytes += bytes
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("usage: scan events: %w", err)
	}
	e.Kinds = sortUsageStats(byKind)
	if len(e.Kinds) > top {
		e.Kinds = e.Kinds[:top]
	}
	return nil
}

// readUsageFTS reads the index: its row count per role (one full scan of the
// virtual table) and the size of its shadow tables. role is UNINDEXED, so the
// per-role query is the only scan this report does on entries_fts.
func (s *Store) readUsageFTS(ctx context.Context, f *UsageFTS) error {
	exists, err := s.usageHasTable(ctx, "entries_fts")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT role, COUNT(1) FROM entries_fts GROUP BY role`)
	if err != nil {
		return fmt.Errorf("usage: scan fts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var stat NamedStat
		if err := rows.Scan(&stat.Name, &stat.Rows); err != nil {
			return fmt.Errorf("usage: scan fts: %w", err)
		}
		f.Rows += stat.Rows
		f.Roles = append(f.Roles, stat)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("usage: scan fts: %w", err)
	}
	sort.Slice(f.Roles, func(i, j int) bool {
		if f.Roles[i].Rows != f.Roles[j].Rows {
			return f.Roles[i].Rows > f.Roles[j].Rows
		}
		return f.Roles[i].Name < f.Roles[j].Name
	})
	// entries_fts_data holds the inverted index blocks, entries_fts_content the
	// indexed text (fts5 keeps a copy of it). Either may be absent on an exotic
	// build, so each is probed.
	if ok, err := s.usageHasTable(ctx, "entries_fts_data"); err != nil {
		return err
	} else if ok {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(block)), 0) FROM entries_fts_data`).Scan(&f.DataBytes); err != nil {
			return fmt.Errorf("usage: fts data bytes: %w", err)
		}
	}
	if ok, err := s.usageHasTable(ctx, "entries_fts_content"); err != nil {
		return err
	} else if ok {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(CAST(c0 AS BLOB)) + length(CAST(c1 AS BLOB)) +
			length(CAST(c2 AS BLOB)) + length(CAST(c3 AS BLOB))), 0) FROM entries_fts_content`).Scan(&f.ContentBytes); err != nil {
			return fmt.Errorf("usage: fts content bytes: %w", err)
		}
	}
	f.Bytes = f.DataBytes + f.ContentBytes
	return nil
}

// readUsageTables fills the dbstat page usage when this SQLite build has the
// virtual table; the report then says whether it does.
func (s *Store) readUsageTables(ctx context.Context, top int, rep *UsageReport) error {
	rows, err := s.db.QueryContext(ctx, `SELECT name, SUM(pgsize) FROM dbstat GROUP BY name ORDER BY 2 DESC, 1`)
	if err != nil {
		// No dbstat in this build: the byte statistics above stand, without the
		// B-tree overhead they do not measure.
		rep.DBStat = false
		return nil
	}
	defer rows.Close()
	rep.DBStat = true
	for rows.Next() {
		var table UsageTable
		if err := rows.Scan(&table.Name, &table.Bytes); err != nil {
			return fmt.Errorf("usage: dbstat: %w", err)
		}
		rep.Tables = append(rep.Tables, table)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("usage: dbstat: %w", err)
	}
	if len(rep.Tables) > top {
		rep.Tables = rep.Tables[:top]
	}
	return nil
}

// readUsageSessionTops ranks the sessions by ledger bytes plus directory
// bytes, so the list shows which conversations to retire.
func (s *Store) readUsageSessionTops(ctx context.Context, top int, perSession, perDir map[string]int64, out *UsageSessions) error {
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sessions`).Scan(&out.InStore); err != nil {
		return fmt.Errorf("usage: count sessions: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, title FROM sessions`)
	if err != nil {
		return fmt.Errorf("usage: list sessions: %w", err)
	}
	defer rows.Close()
	titles := map[string]string{}
	ids := map[string]bool{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return fmt.Errorf("usage: list sessions: %w", err)
		}
		ids[id] = true
		titles[id] = title
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("usage: list sessions: %w", err)
	}
	for id, bytes := range perSession {
		if bytes > 0 {
			ids[id] = true
		}
	}
	for id := range perDir {
		ids[id] = true
	}
	list := make([]UsageSession, 0, len(ids))
	for id := range ids {
		entry := UsageSession{ID: id, Title: titles[id], DBBytes: perSession[id], DirBytes: perDir[id]}
		entry.Bytes = entry.DBBytes + entry.DirBytes
		if entry.Bytes == 0 {
			continue
		}
		list = append(list, entry)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Bytes != list[j].Bytes {
			return list[i].Bytes > list[j].Bytes
		}
		return list[i].ID < list[j].ID
	})
	if len(list) > top {
		list = list[:top]
	}
	out.Top = list
	return nil
}

// usageHasColumn reports whether a table has a column; it is how the report
// adapts to a store older than a migration it must not run.
func (s *Store) usageHasColumn(ctx context.Context, table, column string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM pragma_table_info(?) WHERE name = ?`,
		table, column).Scan(&n); err != nil {
		return false, fmt.Errorf("usage: inspect %s: %w", table, err)
	}
	return n > 0, nil
}

// usageHasTable reports whether a table or virtual table exists.
func (s *Store) usageHasTable(ctx context.Context, name string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil {
		return false, fmt.Errorf("usage: inspect %s: %w", name, err)
	}
	return n > 0, nil
}

// sortUsageStats turns a name-keyed accumulator into the report order: largest
// first, then by name.
func sortUsageStats(stats map[string]*NamedStat) []NamedStat {
	out := make([]NamedStat, 0, len(stats))
	for _, stat := range stats {
		out = append(out, *stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// sessionDirUsage is what the walk of one sessions root found.
type sessionDirUsage struct {
	sessions   UsageSessions
	perSession map[string]int64
}

// scanSessionDirs measures sessions/<id>/: the tool body file, the spilled
// outputs (plain and .gz), the images and the snapshots. A missing root means a
// home without session files yet. WalkDir does not follow symlinks, so the walk
// stays inside the sessions root.
func scanSessionDirs(root string) (sessionDirUsage, error) {
	out := sessionDirUsage{perSession: map[string]int64{}}
	if strings.TrimSpace(root) == "" {
		return out, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, fmt.Errorf("usage: read sessions root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		out.sessions.Dirs++
		dir := filepath.Join(root, entry.Name())
		var total int64
		walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			total += info.Size()
			out.sessions.addFile(rel, info.Size())
			return nil
		})
		if errors.Is(walkErr, os.ErrNotExist) {
			// The session was removed while walking: its bytes are gone too.
			continue
		}
		if walkErr != nil {
			return out, fmt.Errorf("usage: walk %s: %w", dir, walkErr)
		}
		out.perSession[entry.Name()] = total
	}
	return out, nil
}

// addFile sorts one file of a session directory into the section it belongs to:
// the tool body file, outputs/ (spill), images/ or snapshots/; anything else
// counts as other.
func (u *UsageSessions) addFile(rel string, size int64) {
	part := rel
	if i := strings.Index(rel, "/"); i >= 0 {
		part = rel[:i]
	}
	switch part {
	case BodyFileName, LegacyBodyFileName:
		u.Tools.Files++
		u.Tools.Bytes += size
		// The breakdown keeps the name it found, so the report never labels a
		// bodies.jsonl file with the old name.
		for i := range u.BodyFiles {
			if u.BodyFiles[i].Name == part {
				u.BodyFiles[i].Rows++
				u.BodyFiles[i].Bytes += size
				return
			}
		}
		u.BodyFiles = append(u.BodyFiles, NamedStat{Name: part, Rows: 1, Bytes: size})
	case "outputs":
		u.Outputs.Files++
		u.Outputs.Bytes += size
		if strings.HasSuffix(rel, ".gz") {
			u.OutputsGz.Files++
			u.OutputsGz.Bytes += size
		}
	case ImageDirName:
		u.Images.Files++
		u.Images.Bytes += size
	case "snapshots":
		u.Snapshots.Files++
		u.Snapshots.Bytes += size
	default:
		u.Other.Files++
		u.Other.Bytes += size
	}
}

// usageSuggestions attaches the maintenance command that acts on each finding.
// A section without a way to act on it (the assistant bodies are the model's
// own output) says which retention job shrinks it instead.
func usageSuggestions(rep *UsageReport) {
	if rep.File.FreePages > 0 {
		rep.File.Suggestion = fmt.Sprintf("有 %d 个空闲页（%s），`suga data vacuum` 可回收",
			rep.File.FreePages, humanUsageBytes(rep.File.FreeBytes))
	}
	// The entries section can carry two findings: tool results still inline (a
	// command moves them) and the assistant bodies (no command trims them).
	var entryHints []string
	if rep.Entries.Tool.Inline.Bytes > 0 {
		entryHints = append(entryHints, fmt.Sprintf("库内 tool 结果 %s，`suga data bodies --move` 可移到会话文件",
			humanUsageBytes(rep.Entries.Tool.Inline.Bytes)))
	}
	if rep.Entries.Assistant.Bytes-rep.Entries.MovedAssistant.Bytes > 0 {
		entryHints = append(entryHints, fmt.Sprintf("库内 assistant 的推理内容与工具调用参数 %s，`suga data bodies --move` 可移到会话文件",
			humanUsageBytes(rep.Entries.Assistant.Bytes-rep.Entries.MovedAssistant.Bytes)))
	}
	if len(entryHints) > 0 {
		rep.Entries.Suggestion = strings.Join(entryHints, "；")
	}
	if rep.Events.Rows > 0 {
		rep.Events.Suggestion = "事件随会话一起删除：`suga data gc --retention`（或 --older-than）"
	}
	toolRows := int64(0)
	for _, role := range rep.FTS.Roles {
		if role.Name == EntryTool {
			toolRows = role.Rows
		}
	}
	if toolRows > 0 {
		rep.FTS.Suggestion = fmt.Sprintf("索引里还有 %d 行 tool 结果，旧库可用 v0.40 的 `suga data shrink-fts` 删除它们", toolRows)
	} else if rep.FTS.Bytes > 0 {
		rep.FTS.Suggestion = "索引已无 tool 行；删除会话后执行 `suga data vacuum` 让文件回落"
	}
	if rep.Sessions.Outputs.Files > 0 {
		rep.Sessions.Suggestion = "spill 输出可用 `suga data compress-spill --apply` 压缩空闲文件；旧会话用 `suga data gc --retention` 删除"
	} else if rep.Sessions.Dirs > 0 {
		rep.Sessions.Suggestion = "会话目录随会话删除：`suga data gc --retention`（或 --older-than）"
	}
}

// humanUsageBytes formats a byte count for the suggestions.
func humanUsageBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}
