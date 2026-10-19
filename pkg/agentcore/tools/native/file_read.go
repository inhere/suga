package native

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

const (
	// defaultReadLines is how many lines file_read returns without a limit.
	defaultReadLines = 2000
	// binarySniffBytes is how much of a file is checked for NUL bytes.
	binarySniffBytes = 8 << 10
	// maxReadPaths is how many files one file_read call may read via paths.
	maxReadPaths = 10
	// DefaultReadPageBytes is the default page cap of one file_read
	// (tools.read_page_bytes): most source files fit in one read, so the model
	// does not page through files it just wrote (suag-jz0l review).
	DefaultReadPageBytes = 32 << 10
	// readPathsPages bounds a paths call: at most this many pages in all.
	readPathsPages = 4
	// outlineLines is how many lines a file needs before file_read's outline=true
	// answers with the declaration outline instead of the text window (design Q1):
	// a shorter file fits in one read anyway.
	outlineLines = 300
	// maxOutlineBytes caps the file an outline is built for: a bigger file is
	// answered with the window instead, so one outline call cannot pull a huge
	// file in and parse it.
	maxOutlineBytes = 1 << 20
)

// FileReadTool reads files within the workspace (or a read root), a window of
// lines at a time.
type FileReadTool struct {
	workspace string
	readRoots []string // read-only directories outside the workspace
	addDirs   *AddDirs // the session's added directories, read at call time
	maxOut    int      // page cap in bytes per file; 0 = DefaultReadPageBytes
}

func (t *FileReadTool) Name() string { return "file_read" }
func (t *FileReadTool) Description() string {
	return fmt.Sprintf("Read a text file in the workspace. Lines are prefixed with their number and a tab "+
		"(the prefix is not part of the file). Returns at most %d lines per call; for large files pass "+
		"offset (1-based start line) and limit to read further. To look at several places at once, prefer "+
		"paths (up to %d) over path: offset and limit then apply to each file, and an entry may ask for "+
		"line spans of its own as path:start-end[,start-end], e.g. paths: [\"a.go:10-30\", "+
		"\"b.go:40-60,90-110\"]. Use outline=true only to see which declarations a source file over %d "+
		"lines holds: it answers with the declaration outline (line numbers and declaration lines) instead "+
		"of the text. Binary files are reported, not dumped.", defaultReadLines, maxReadPaths, outlineLines)
}

// ToolMeta marks the tool as a read-only read.
func (*FileReadTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryRead, ReadOnly: true, Rereadable: true, Prefetchable: true}
}
func (t *FileReadTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path within workspace (relative, or absolute inside it or under a read-only dependency root)",
				},
				"paths": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
					"description": fmt.Sprintf("Several files to read in one call (up to %d), instead of path; "+
						"an entry may ask for line spans of its own: path:start-end[,start-end]", maxReadPaths),
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "1-based line to start from (default 1)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum lines to return (default %d)", defaultReadLines),
				},
				"outline": map[string]any{
					"type": "boolean",
					"description": fmt.Sprintf("Return the declaration outline (line number and declaration line) "+
						"of a source file over %d lines instead of the text window; a shorter file, an unknown "+
						"file type and an entry with line spans return the window and say why (default false)", outlineLines),
				},
			},
		},
	}
}

func (t *FileReadTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	items, err := readPathItems(call.Arguments)
	if err != nil {
		return nil, err
	}
	offset, limit := 1, defaultReadLines
	if n, ok := numberArg(call.Arguments["offset"]); ok && n >= 1 {
		offset = int(n)
	}
	if n, ok := numberArg(call.Arguments["limit"]); ok && n >= 1 {
		limit = int(n)
	}
	outline, _ := call.Arguments["outline"].(bool)
	var content string
	if len(items) == 1 {
		if content, err = t.readItem(items[0], offset, limit, outline); err != nil {
			return nil, err
		}
	} else {
		// Several files: each gets its own page cap, as if read one by one, and
		// a failing path is reported in place so the others still come back.
		var sb strings.Builder
		for i, item := range items {
			if sb.Len() >= readPathsPages*t.pageBytes() {
				fmt.Fprintf(&sb, "\n\n[output cap reached; not read: %s - read them in another call]", itemArgs(items[i:]))
				break
			}
			if i > 0 {
				sb.WriteString("\n\n")
			}
			text, err := t.readItem(item, offset, limit, outline)
			if err != nil {
				text = "[error: " + err.Error() + "]"
			}
			fmt.Fprintf(&sb, "==> %s <==\n%s", item.arg, text)
		}
		content = sb.String()
	}
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    t.Name(),
		Content: content,
	}, nil
}

// pageBytes is the page cap of one file.
func (t *FileReadTool) pageBytes() int {
	if t.maxOut > 0 {
		return t.maxOut
	}
	return DefaultReadPageBytes
}

// readOne reads the line window of one path argument.
func (t *FileReadTool) readOne(arg string, offset, limit int) (string, error) {
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), arg)
	if err != nil {
		return "", err
	}
	content, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) (string, error) {
		f, err := root.Open(rel)
		if err != nil {
			return "", err
		}
		defer f.Close()
		return readLineWindow(f, offset, limit, t.pageBytes(), readHintFor(target, rel))
	})
	if err != nil {
		return "", fmt.Errorf("file_read: %w", err)
	}
	return content, nil
}

// readHint is what a truncated read needs to point at the rest of the file: the
// name the model used for it (the guidance spells a line span with that name)
// and the same file as an absolute path (the extraction layer reads it again). A
// nil hint turns the guidance off, so a window read without a file name behind
// it answers exactly as before.
type readHint struct {
	name   string
	onDisk string
}

// readHintFor names one file the way a truncated read's guidance has to name it.
func readHintFor(target readTarget, rel string) *readHint {
	return &readHint{name: target.show(rel), onDisk: filepath.Join(target.base, filepath.FromSlash(rel))}
}

// readItem returns what file_read answers for one path entry: the entry's own
// line spans when it carried a ":start-end" suffix, else the declaration outline
// when outline is set, else the offset/limit window.
func (t *FileReadTool) readItem(item readItem, offset, limit int, outline bool) (string, error) {
	switch {
	case len(item.ranges) > 0:
		return t.readRanges(item)
	case outline:
		return t.readOutline(item.path, offset, limit)
	default:
		return t.readOne(item.path, offset, limit)
	}
}

// readRanges reads the line spans one path entry asked for: a window per span,
// a marker for the lines in between, and an error when no span of the entry
// holds a line, so a range past the end is a mistake reported back, not a short
// answer.
func (t *FileReadTool) readRanges(item readItem) (string, error) {
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), item.path)
	if err != nil {
		return "", err
	}
	window, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) (rangeWindow, error) {
		f, err := root.Open(rel)
		if err != nil {
			return rangeWindow{}, err
		}
		defer f.Close()
		return readLineRanges(f, item.ranges, t.pageBytes(), readHintFor(target, rel))
	})
	if err != nil {
		return "", fmt.Errorf("file_read: %w", err)
	}
	if len(window.missing) == len(item.ranges) {
		return "", fmt.Errorf("file_read: %s: no lines read: %s", item.arg, strings.Join(window.missing, "; "))
	}
	return window.text, nil
}

// rangeWindow is what readLineRanges read: the text of the requested spans and
// a note for every span that holds no line (a span past the end of the file).
type rangeWindow struct {
	text    string
	missing []string
}

// readLineRanges returns the requested line spans of f, one window each, in the
// order asked, with a marker naming the lines between two spans and a note in
// place of a span that holds no line. Spans are read through the window reader,
// so the numbering, the byte cap and the notices for a binary or empty file are
// the same as a plain read's.
func readLineRanges(f *os.File, ranges []readRange, maxBytes int, hint *readHint) (rangeWindow, error) {
	var out rangeWindow
	var blocks []string
	size, prevEnd := 0, 0 // bytes gathered, last line of the last span that held lines
	for i, r := range ranges {
		if i > 0 && size >= readPathsPages*maxBytes {
			blocks = append(blocks, fmt.Sprintf("[output cap reached; not read: %s - read them in another call]",
				rangesText(ranges[i:])))
			break
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil { // every span starts at the file's start
			return rangeWindow{}, err
		}
		text, err := readLineWindowAt(f, r.start, r.end-r.start+1, maxBytes, true, hint)
		if err != nil {
			return rangeWindow{}, err
		}
		switch {
		// A file the window reader refuses (binary, empty) reads the same for
		// every span, so it is answered once, as a plain read answers it.
		case i == 0 && isWindowNotice(text):
			return rangeWindow{text: text}, nil
		case isPastEndWindow(text):
			note := fmt.Sprintf("span %s: %s", r, strings.TrimSuffix(strings.TrimPrefix(text, "["), "]"))
			out.missing = append(out.missing, note)
			blocks = append(blocks, "["+note+"]")
			continue
		}
		if i > 0 {
			if gap := r.start - prevEnd - 1; gap > 0 {
				blocks = append(blocks, fmt.Sprintf("[skipped %d-%d: not in the requested ranges]", prevEnd+1, r.start-1))
			}
		}
		blocks = append(blocks, text)
		size += len(text)
		prevEnd = r.end
	}
	out.text = strings.Join(blocks, "\n")
	return out, nil
}

// isWindowNotice reports a window answer that is a notice about the file itself
// rather than lines (readLineWindow's "[binary file: ..." and "[empty file]"):
// it is the same for every span asked for.
func isWindowNotice(text string) bool {
	return strings.HasPrefix(text, "[binary file:") || strings.HasPrefix(text, "[empty file]")
}

// isPastEndWindow reports readLineWindow's answer for a span that starts past the
// end of the file ("[offset N is past the end: ...]"): such a span holds no
// line, whatever the file is.
func isPastEndWindow(text string) bool { return strings.HasPrefix(text, "[offset ") }

// readOutline answers outline=true for one path: the declaration outline of a
// source file of a language the extraction layer covers, when the file is long
// enough to need one. Every other file - a shorter file, a type no extractor
// knows, a binary, a file too big to parse - is answered with the offset/limit
// window and a first line saying why: an outline is a convenience, never an
// error (design 2026-10-18-explore-efficiency, means A).
func (t *FileReadTool) readOutline(arg string, offset, limit int) (string, error) {
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), arg)
	if err != nil {
		return "", err
	}
	content, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) (string, error) {
		rel = filepath.ToSlash(rel)
		f, err := root.Open(rel)
		if err != nil {
			return "", err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return "", err
		}
		// window is the fallback answer: this file's plain offset/limit window,
		// with a first line that says why there is no outline.
		window := func(note string) (string, error) {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return "", err
			}
			text, err := readLineWindow(f, offset, limit, t.pageBytes(), readHintFor(target, rel))
			if err != nil {
				return "", err
			}
			if note == "" {
				return text, nil
			}
			return note + "\n" + text, nil
		}
		if !info.Mode().IsRegular() {
			return window("") // a directory: the window reader names the tool that lists one
		}
		name := filepath.Base(rel)
		if !SupportsSymbols(name) {
			return window(fmt.Sprintf("[no outline: no declaration extractor for %q; supported: %s]",
				filepath.Ext(name), SupportedSymbolLanguages()))
		}
		if info.Size() > maxOutlineBytes {
			return window(fmt.Sprintf("[no outline: %d bytes is over the %d byte outline cap]",
				info.Size(), maxOutlineBytes))
		}
		data, err := io.ReadAll(io.LimitReader(f, maxOutlineBytes+1))
		if err != nil {
			return "", err
		}
		if len(data) > maxOutlineBytes {
			return window(fmt.Sprintf("[no outline: over the %d byte outline cap]", maxOutlineBytes))
		}
		head := data
		if len(head) > binarySniffBytes {
			head = head[:binarySniffBytes]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return window("") // a binary file: the window reader reports it as one
		}
		lines := newSourceLines(data)
		lineCount := fileLineCount(lines)
		if lineCount <= outlineLines {
			return window(fmt.Sprintf("[no outline: %d lines is under the %d line outline threshold]",
				lineCount, outlineLines))
		}
		// The outline names the file as the model named it and gives ctags a path
		// it can read again, exactly like file_symbols does.
		set, err := ExtractSymbolsAt(target.show(rel), filepath.Join(target.base, filepath.FromSlash(rel)), data)
		if err != nil {
			return window(fmt.Sprintf("[no outline: %s]", err))
		}
		if len(set.Symbols) == 0 {
			return window("[no outline: no declarations found]")
		}
		return renderOutline(target.show(rel), lines, lineCount, set, t.pageBytes()), nil
	})
	if err != nil {
		return "", fmt.Errorf("file_read: %w", err)
	}
	return content, nil
}

// renderOutline formats the declaration outline of one file: a first line saying
// what it is, one line per declaration ("<line>: <declaration line as written>"),
// and a marker for every line span between two of them, so the model knows the
// exact ranges it has not read yet and can ask for one. The outline is capped at
// maxBytes like a window is.
func renderOutline(name string, lines sourceLines, lineCount int, set SymbolSet, maxBytes int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[outline of %s: %d lines, %d declarations; this is a declaration outline, not the whole "+
		"file - read a span as %s:start-end for the text]\n", name, lineCount, len(set.Symbols), name)
	prevEnd := 0 // the last line already in the outline
	for _, s := range set.Symbols {
		if s.Line > prevEnd+1 {
			sb.WriteString(omittedSpan(prevEnd+1, s.Line-1))
		}
		signature := lines.at(s.Line) // the line as written, not the masked one
		if signature == "" {
			signature = s.Signature
		}
		fmt.Fprintf(&sb, "%d: %s\n", s.Line, signature)
		prevEnd = s.Line
	}
	if lineCount > prevEnd {
		sb.WriteString(omittedSpan(prevEnd+1, lineCount))
	}
	if set.Approx {
		sb.WriteString("[approx: the declarations come from declaration-line detection, not a parser; " +
			"confirm a name with file_grep]\n")
	}
	// The extractor's own notes (a configuration file's caps, why a YAML file fell
	// back to its lines) belong to this outline: they explain what is listed.
	for _, note := range set.Notes {
		sb.WriteString(note + "\n")
	}
	text := strings.TrimRight(sb.String(), "\n")
	if len(text) > maxBytes {
		text = text[:utf8Cut(text, maxBytes)]
		if i := strings.LastIndexByte(text, '\n'); i > 0 {
			text = text[:i]
		}
		text += fmt.Sprintf("\n[truncated at output cap %d bytes; read a span as %s:start-end]", maxBytes, name)
	}
	return text
}

// omittedSpan is the marker for the lines an outline left out: the exact range,
// so the model can ask for it next, and how long it is. A declaration is never
// inside one - every declaration is a line of the outline itself - so the marker
// carries a line count, not a count of declarations.
func omittedSpan(start, end int) string {
	return fmt.Sprintf("[omitted %d-%d: %d lines]\n", start, end, end-start+1)
}

// fileLineCount counts the lines the window reader numbers: a trailing newline
// does not open an empty last line.
func fileLineCount(lines sourceLines) int {
	if n := len(lines.lines); n > 0 && lines.lines[n-1] == "" {
		return n - 1
	}
	return len(lines.lines)
}

// readRange is one line span a paths entry asks for: 1-based, both ends
// included, as in "path:120-180".
type readRange struct{ start, end int }

// String spells the span the way the model wrote it, for notes and errors.
func (r readRange) String() string { return fmt.Sprintf("%d-%d", r.start, r.end) }

// rangesText lists the spans of the entries from i on one by one, for the
// output cap note.
func rangesText(ranges []readRange) string {
	parts := make([]string, len(ranges))
	for i, r := range ranges {
		parts[i] = r.String()
	}
	return strings.Join(parts, ", ")
}

// readItem is one entry of file_read's path/paths: path is the file to read and
// ranges the line spans the entry itself asked for. An entry without a suffix
// leaves ranges nil and reads the offset/limit window, exactly as before.
type readItem struct {
	arg    string // the entry as the model wrote it; the result header shows it
	path   string // the entry without its range suffix
	ranges []readRange
}

// splitReadItem splits the optional ":start-end[,start-end]" suffix off one path
// argument. Only a suffix that starts with a digit is read as line spans, so a
// plain name, a path holding a colon and a Windows drive ("D:\src\a.go") stay
// whole; a suffix that starts like a span must be a valid one.
func splitReadItem(arg string) (readItem, error) {
	item := readItem{arg: arg, path: arg}
	colon := strings.LastIndexByte(arg, ':')
	if colon < 0 || colon+1 == len(arg) || arg[colon+1] < '0' || arg[colon+1] > '9' {
		return item, nil
	}
	ranges, err := parseRanges(arg[colon+1:])
	if err != nil {
		return readItem{}, fmt.Errorf("%w in %q: write line spans as path:start-end[,start-end]", err, arg)
	}
	item.path, item.ranges = arg[:colon], ranges
	return item, nil
}

// lineSpanWindow is how many lines a bare "path:N" reads from line N: the
// file:line a search result points at (rg, file_grep, a stack trace) is
// written that way, and the batch-19 re-measure had the model send
// src/dent.rs:199 and get an error for it.
const lineSpanWindow = 40

// parseRanges reads "start-end[,start-end...]": start is at least 1 and start is
// not past end; a bare line number N reads lineSpanWindow lines from N. A
// malformed span is an error - a span the model got wrong must not look like a
// read that happens to be short.
func parseRanges(text string) ([]readRange, error) {
	parts := strings.Split(text, ",")
	out := make([]readRange, 0, len(parts))
	for _, part := range parts {
		startText, endText, ok := strings.Cut(strings.TrimSpace(part), "-")
		if !ok {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || n < 1 {
				return nil, fmt.Errorf("invalid line span %q", part)
			}
			out = append(out, readRange{start: n, end: n + lineSpanWindow - 1})
			continue
		}
		start, startErr := strconv.Atoi(strings.TrimSpace(startText))
		end, endErr := strconv.Atoi(strings.TrimSpace(endText))
		if startErr != nil || endErr != nil || start < 1 || end < start {
			return nil, fmt.Errorf("invalid line span %q: want start-end with start >= 1 and start <= end", part)
		}
		out = append(out, readRange{start: start, end: end})
	}
	return out, nil
}

// itemArgs lists the path arguments of the entries from i on as the model wrote
// them, for the output cap note.
func itemArgs(items []readItem) string {
	args := make([]string, len(items))
	for i, item := range items {
		args[i] = item.arg
	}
	return strings.Join(args, ", ")
}

// ReadPathList returns file_read's paths: path, then the entries of paths
// (duplicates dropped), each without its line-span suffix. paths may arrive as a
// JSON-encoded string, as some models encode arrays that way, or as a single
// plain string. The callers beyond file_read (the outside-directory check and
// the ACP location) get the file itself, never a "file:120-180" argument.
func ReadPathList(args map[string]any) ([]string, error) {
	items, err := readPathItems(args)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.path
	}
	return out, nil
}

// readPathItems returns file_read's path arguments with the line spans each entry
// asked for.
func readPathItems(args map[string]any) ([]readItem, error) {
	list, err := readPathArgs(args)
	if err != nil {
		return nil, fmt.Errorf("file_read: %w", err)
	}
	items := make([]readItem, 0, len(list))
	for _, arg := range list {
		item, err := splitReadItem(arg)
		if err != nil {
			return nil, fmt.Errorf("file_read: %w", err)
		}
		items = append(items, item)
	}
	return items, nil
}

// readPathArgs gathers file_read's path arguments as the model wrote them.
func readPathArgs(args map[string]any) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if p, _ := args["path"].(string); p != "" {
		add(p)
	}
	raw := args["paths"]
	if text, isText := raw.(string); isText {
		if strings.HasPrefix(strings.TrimSpace(text), "[") {
			if err := json.Unmarshal([]byte(text), &raw); err != nil {
				return nil, fmt.Errorf("paths must be an array of strings: %w", err)
			}
		} else {
			raw = []any{text}
		}
	}
	if raw != nil {
		items, isList := raw.([]any)
		if !isList {
			return nil, fmt.Errorf("paths must be an array of strings")
		}
		for i, item := range items {
			p, isText := item.(string)
			if !isText {
				return nil, fmt.Errorf("paths[%d] must be a string", i)
			}
			add(p)
		}
	}
	switch {
	case len(out) == 0:
		return nil, fmt.Errorf("%q arg is required", "path")
	case len(out) > maxReadPaths:
		return nil, fmt.Errorf("at most %d paths per call, got %d", maxReadPaths, len(out))
	}
	return out, nil
}

// readLineWindow returns lines [offset, offset+limit) of f with line numbers,
// stopping early on a line boundary when maxBytes would be exceeded. When more
// lines follow it says which offset continues the read, and hint (when set)
// tells how long the file is and how to read on without paging.
func readLineWindow(f *os.File, offset, limit, maxBytes int, hint *readHint) (string, error) {
	return readLineWindowAt(f, offset, limit, maxBytes, false, hint)
}

// readLineWindowAt is readLineWindow with the continuation note under control:
// span marks a window that is a line span the model asked for by its own numbers
// (a "path:start-end" entry), where reaching the limit is the end of the span and
// not a reason to read on. A window cut short by the byte cap still says so.
func readLineWindowAt(f *os.File, offset, limit, maxBytes int, span bool, hint *readHint) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use file_list", info.Name())
	}
	br := bufio.NewReaderSize(f, 64<<10)
	head, _ := br.Peek(binarySniffBytes)
	if bytes.IndexByte(head, 0) >= 0 {
		return fmt.Sprintf("[binary file: %s, %d bytes, type %s; not shown]",
			info.Name(), info.Size(), http.DetectContentType(head)), nil
	}
	if info.Size() == 0 {
		return "[empty file]", nil
	}
	// The text budget: the page cap. The note that ends a cut window (and the
	// guidance on the file in it, truncationDetail) is added on top of it and
	// stays within truncationNoteBytes.
	bodyCap := maxBytes

	var sb strings.Builder
	lineNo, shown := 0, 0
	more, cutBySize := false, false
	for {
		line, err := br.ReadString('\n')
		if line == "" && err != nil {
			if err != io.EOF {
				return "", err
			}
			break
		}
		lineNo++
		if lineNo < offset {
			continue
		}
		if shown == limit {
			more = true
			break
		}
		entry := fmt.Sprintf("%d\t%s", lineNo, strings.TrimRight(line, "\r\n")) + "\n"
		if sb.Len()+len(entry) > bodyCap {
			if shown == 0 { // a single huge line: return part of it
				entry = entry[:utf8Cut(entry, bodyCap)] + "\n"
				sb.WriteString(entry)
				shown = 1
			}
			more, cutBySize = true, true
			break
		}
		sb.WriteString(entry)
		shown++
		if err == io.EOF {
			break
		}
	}
	if shown == 0 {
		return fmt.Sprintf("[offset %d is past the end: the file has %d lines]", offset, lineNo), nil
	}
	if more && (!span || cutBySize) {
		next := offset + shown
		reason := fmt.Sprintf("limit %d lines", limit)
		if cutBySize {
			reason = fmt.Sprintf("output cap %d bytes", maxBytes)
		}
		// The guidance on the file sits inside the note, in front of the offset
		// the model already continues with, so a truncated read stays one line.
		detail := truncationDetail(f, hint)
		if detail != "" {
			detail = "; " + detail
		}
		fmt.Fprintf(&sb, "[truncated at %s%s; continue with offset=%d]", reason, detail, next)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// truncationNoteBytes is the room the note at the end of a cut window takes: the
// continuation, the file's length, its declaration count and the two ways to read
// on (truncationDetail). A page is therefore at most read_page_bytes plus this;
// file_read declares itself Rereadable, so the output layer passes its pages
// through whatever their size (see output_wrap_test.go).
const truncationNoteBytes = 512

// truncationDetail is what a truncated read tells the model about the file besides
// the offset to continue with: how long the file is, how many declarations it has
// when the extraction layer covers it and succeeds, and how to read on without
// paging (research 2026-10-18-explore-efficiency section 7.4, suggestion 2 - the
// model never used outline=true because a read before the first truncation gives
// it no reason to). It is empty when hint is nil or the file cannot be looked at
// again within the outline cap: counting a huge file would read it whole, so
// paging one stays as cheap as it was.
func truncationDetail(f *os.File, hint *readHint) string {
	if hint == nil || hint.name == "" {
		return ""
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxOutlineBytes {
		return ""
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, maxOutlineBytes+1))
	if err != nil || len(data) > maxOutlineBytes {
		return ""
	}
	lines := fileLineCount(newSourceLines(data))
	switch count, hasCount := declarationCount(hint, data); {
	// The outline is worth naming only when the file would really get one:
	// outline=true answers a file under the outline threshold with the window
	// and says why.
	case hasCount && lines > outlineLines:
		return fmt.Sprintf("the file has %d lines and %d declarations; read a span as %s:start-end or ask "+
			"for the declaration outline with outline=true", lines, count, hint.name)
	case hasCount:
		return fmt.Sprintf("the file has %d lines and %d declarations; read a span as %s:start-end",
			lines, count, hint.name)
	default:
		return fmt.Sprintf("the file has %d lines; read a span as %s:start-end", lines, hint.name)
	}
}

// declarationCount counts the declarations of the file the hint names, through
// the same extraction layer file_symbols and outline=true use. A file type no
// extractor covers and an extraction that failed both leave ok false: the count
// is a convenience, never an error (suag-nxwg).
func declarationCount(hint *readHint, data []byte) (count int, ok bool) {
	if !SupportsSymbols(hint.name) {
		return 0, false
	}
	set, err := ExtractSymbolsAt(hint.name, hint.onDisk, data)
	if err != nil {
		return 0, false
	}
	return len(set.Symbols), true
}
