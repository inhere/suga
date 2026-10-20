package files

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/memory"
)

// indexFile is the index name in both layouts.
const indexFile = "MEMORY.md"

// entryLinePattern matches "- [Title](link) — kind" and the newer
// "- [Title](link) — kind: summary". The title may contain backslash escapes
// (\[, \], \\); the separator is an em dash. The summary group is absent in
// the old format and empty after a bare colon; both render back identically.
var entryLinePattern = regexp.MustCompile(`^- \[((?:\\.|[^\\\]])+)\]\(([^()\s]+)\) — ([a-z]+)(?::[ \t]*(.*?))?[ \t]*$`)

// docLine is one line of MEMORY.md: either a managed entry line or a
// hand-written line that is preserved verbatim and never modified.
type docLine struct {
	raw   string // original text (without the line ending)
	entry bool
	line  memory.IndexLine
}

// indexDoc is a parsed MEMORY.md. Writes only touch entry lines; every other
// line keeps its text and position. New entries are appended at the end of
// the file.
type indexDoc struct {
	lines []docLine
	eol   string // "\n" or "\r\n", taken from the existing file
}

// parseIndex parses MEMORY.md. A line becomes an entry only when its link
// points at "<entryDir>/<id>.md" with a valid id and its kind is valid; any
// other line, including a second line for an id already seen, stays a
// read-only hand-written line.
func parseIndex(data string, entryDir string) *indexDoc {
	doc := &indexDoc{eol: "\n"}
	if strings.Contains(data, "\r\n") {
		doc.eol = "\r\n"
	}
	if data == "" {
		return doc
	}
	text := strings.TrimSuffix(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		dl := docLine{raw: raw}
		if l, ok := parseEntryLine(raw, entryDir); ok && !seen[l.ID] {
			seen[l.ID] = true
			dl.entry, dl.line = true, l
		}
		doc.lines = append(doc.lines, dl)
	}
	return doc
}

func parseEntryLine(raw, entryDir string) (memory.IndexLine, bool) {
	m := entryLinePattern.FindStringSubmatch(raw)
	if m == nil {
		return memory.IndexLine{}, false
	}
	kind := memory.Kind(m[3])
	link := strings.TrimPrefix(m[2], "./")
	prefix := ""
	if entryDir != "" {
		prefix = entryDir + "/"
	}
	id, ok := strings.CutPrefix(link, prefix)
	if !ok || !strings.HasSuffix(id, ".md") || !kind.Valid() {
		return memory.IndexLine{}, false
	}
	id = strings.TrimSuffix(id, ".md")
	if memory.ValidateID(id) != nil {
		return memory.IndexLine{}, false
	}
	return memory.IndexLine{ID: id, Title: unescapeTitle(m[1]), Kind: kind, Summary: m[4]}, true
}

// entries returns the managed entry lines in file order.
func (d *indexDoc) entries() []memory.IndexLine {
	var out []memory.IndexLine
	for _, l := range d.lines {
		if l.entry {
			out = append(out, l.line)
		}
	}
	return out
}

// find returns the position of the entry line for id, or -1.
func (d *indexDoc) find(id string) int {
	for i, l := range d.lines {
		if l.entry && l.line.ID == id {
			return i
		}
	}
	return -1
}

// set replaces the entry line for l.ID in place, or appends it at the end.
func (d *indexDoc) set(l memory.IndexLine, entryDir string) {
	dl := docLine{raw: renderEntryLine(l, entryDir), entry: true, line: l}
	if i := d.find(l.ID); i >= 0 {
		d.lines[i] = dl
		return
	}
	d.lines = append(d.lines, dl)
}

// remove drops the entry line for id.
func (d *indexDoc) remove(id string) {
	if i := d.find(id); i >= 0 {
		d.lines = append(d.lines[:i], d.lines[i+1:]...)
	}
}

func (d *indexDoc) render() []byte {
	var b strings.Builder
	for _, l := range d.lines {
		b.WriteString(l.raw)
		b.WriteString(d.eol)
	}
	return []byte(b.String())
}

// renderEntryLine formats "- [Title](relative/link.md) — kind: summary"; the
// link is relative to MEMORY.md and always uses forward slashes. An entry
// without a summary renders exactly like the old format.
func renderEntryLine(l memory.IndexLine, entryDir string) string {
	link := l.ID + ".md"
	if entryDir != "" {
		link = entryDir + "/" + link
	}
	line := "- [" + escapeTitle(l.Title) + "](" + link + ") — " + string(l.Kind)
	if l.Summary != "" {
		line += ": " + l.Summary
	}
	return line
}

var titleEscaper = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)

func escapeTitle(s string) string { return titleEscaper.Replace(s) }

func unescapeTitle(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Entry files: a small YAML frontmatter followed by the body.
//
//	---
//	kind: feedback
//	title: "Prefer table tests"
//	updated: 2026-10-11T08:00:00Z
//	applies_when: "Go 1.26"
//	source: "tool"
//	session: "s-123"
//	---
//
//	body...
//
// Only these flat keys are read or written, so a hand-written parser keeps
// the format predictable without a YAML dependency. Quoted values use Go
// string syntax (a subset of YAML double-quoted strings); unquoted and
// single-quoted values are accepted when reading hand-edited files.

func renderEntry(e memory.Entry) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("kind: " + string(e.Kind) + "\n")
	b.WriteString("title: " + strconv.Quote(e.Title) + "\n")
	b.WriteString("updated: " + e.Updated.UTC().Format(time.RFC3339) + "\n")
	if e.Prov.Source != "" {
		b.WriteString("source: " + strconv.Quote(e.Prov.Source) + "\n")
	}
	if e.Prov.SessionID != "" {
		b.WriteString("session: " + strconv.Quote(e.Prov.SessionID) + "\n")
	}
	for _, condition := range e.AppliesWhen {
		b.WriteString("applies_when: " + strconv.Quote(condition) + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.Trim(e.Body, "\r\n"))
	b.WriteString("\n")
	return []byte(b.String())
}

// parseEntry reads an entry file. A file without frontmatter is all body.
func parseEntry(data string) memory.Entry {
	var e memory.Entry
	text := strings.ReplaceAll(data, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		e.Body = strings.Trim(text, "\n")
		return e
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		// Frontmatter closed at end of file without a body.
		head, ok = strings.CutSuffix(rest, "\n---")
		if !ok {
			e.Body = strings.Trim(text, "\n")
			return e
		}
	}
	for _, line := range strings.Split(head, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		val = unquote(strings.TrimSpace(val))
		switch strings.TrimSpace(key) {
		case "kind":
			e.Kind = memory.Kind(val)
		case "title":
			e.Title = val
		case "updated":
			e.Updated, _ = time.Parse(time.RFC3339, val)
		case "source":
			e.Prov.Source = val
		case "session":
			e.Prov.SessionID = val
		case "applies_when":
			e.AppliesWhen = append(e.AppliesWhen, val)
		}
	}
	e.Body = strings.Trim(body, "\n")
	return e
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if s, err := strconv.Unquote(v); err == nil {
			return s
		}
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}
