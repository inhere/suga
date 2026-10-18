package native

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

const (
	// defaultRefLimit caps one file_refs result, like file_symbols: the model
	// narrows path instead of paging through thousands of lines.
	defaultRefLimit = 200
	// maxRefErrors is how many unreadable or unparsable files a result names
	// before it just counts the rest.
	maxRefErrors = 5
)

// FileRefsTool answers "who calls X / where is X used" for one name (design
// docs/design/2026-10-18-symbol-refs-design.md): one "path:line class text"
// line per use, across a file, a directory or the workspace. Go is classified
// by go/parser without type checking; the other supported languages are matched
// word-bounded with comments and strings masked and marked approx. It is
// read-only, parses in memory only, and no index or cache is involved: every
// call reads the files again.
type FileRefsTool struct {
	workspace string
	readRoots []string // read-only directories outside the workspace
	addDirs   *AddDirs // the session's added directories, read at call time
	maxOut    int      // raw result cap in bytes; 0 = DefaultRawOutputBytes
}

func (t *FileRefsTool) Name() string { return "file_refs" }
func (t *FileRefsTool) Description() string {
	return "Find where a name is used: who calls X, who uses X, where is X referenced. " +
		"Searches a file, a directory (recursive) or the workspace and answers one `path:line class[(qualifier)] text` " +
		"line each, sorted by path and line. Use it instead of guessing file_grep patterns for a call chain. " +
		"Go is parsed exactly (class def, call, sel or ref; sel/call carry the X of X.name, with pkg:X when X is an " +
		"imported package) but without type checking, so a match may be another local, field or another package's " +
		"symbol - the shown line decides. JavaScript/TypeScript, Python and Java match word-bounded text with " +
		"comments and strings masked and are marked approx; universal-ctags offers declarations, not references, so " +
		"it is not used here. Any other file type is refused with the supported list. " +
		fmt.Sprintf("Directories skip test files unless include_tests is set, and a result stops at %d matches.", defaultRefLimit)
}

// ToolMeta marks the tool as a read-only search by name, like file_grep: it
// reports positions, it does not read a target's whole content.
func (*FileRefsTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategorySearch, ReadOnly: true, Prefetchable: true}
}
func (t *FileRefsTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The identifier to look for: a function, method, type, field, variable or import name (case-sensitive)",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Where to look: a file or directory to search (default: workspace root); may be absolute under a read-only dependency root",
				},
				"include_tests": map[string]any{
					"type":        "boolean",
					"description": "Also search *_test.go, *.test.ts, test_*.py and *Test.java (default false)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum matches to list (default %d)", defaultRefLimit),
				},
			},
			"required": []any{"name"},
		},
	}
}

func (t *FileRefsTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	name, _ := call.Arguments["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("file_refs: %q arg is required", "name")
	}
	pathArg, _ := call.Arguments["path"].(string)
	if pathArg == "" {
		pathArg = "."
	}
	limit := defaultRefLimit
	if n, ok := numberArg(call.Arguments["limit"]); ok && n >= 1 {
		limit = int(n)
	}
	includeTests, _ := call.Arguments["include_tests"].(bool)
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), pathArg)
	if err != nil {
		return nil, fmt.Errorf("file_refs: %w", err)
	}
	re := refWordRe(name)

	out := &refOut{}
	// A file argument is searched as that file: a type no extractor covers is
	// refused with the supported list, not answered with an empty result.
	if info, statErr := os.Lstat(target.abs); statErr == nil && info.Mode().IsRegular() {
		refs, approx, err := t.oneFile(target, name, re)
		if err != nil {
			return nil, fmt.Errorf("file_refs: %w", err)
		}
		out.files, out.approx, out.refs = 1, approx, refs
	} else if err := t.walkDir(ctx, target, name, re, limit, includeTests, out); err != nil {
		return nil, fmt.Errorf("file_refs: %w", err)
	}
	if out.files == 0 {
		if out.tests > 0 && out.unsupported == 0 {
			return nil, fmt.Errorf("file_refs: only test files under %s; pass include_tests: true to search them", pathArg)
		}
		return nil, fmt.Errorf("file_refs: no supported source files under %s; supported: %s",
			pathArg, SupportedSymbolLanguages())
	}
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    t.Name(),
		Content: TruncateHeadTail(out.render(name, pathArg, limit), rawLimit(t.maxOut)),
	}, nil
}

// oneFile searches one file through os.Root, the way file_symbols reads one.
func (t *FileRefsTool) oneFile(target readTarget, name string, re *regexp.Regexp) ([]Ref, bool, error) {
	if !SupportsSymbols(target.abs) {
		return nil, false, fmt.Errorf("unsupported file type %q; supported: %s",
			filepath.Ext(target.abs), SupportedSymbolLanguages())
	}
	file, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) (symbolFile, error) {
		return readSymbolFile(root, target, rel)
	})
	if err != nil {
		return nil, false, err
	}
	return scanRefs(file.name, file.data, name, re)
}

// walkDir searches every supported source file under the directory target,
// stopping one match past limit (the result then says more matched instead of
// walking a whole module cache).
func (t *FileRefsTool) walkDir(ctx context.Context, target readTarget, name string, re *regexp.Regexp, limit int, includeTests bool, out *refOut) error {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	walk, err := walkWorkspace(ctx, target.base, target.abs, false, func(root *os.Root, rel string, d fs.DirEntry) error {
		if !includeTests && isTestFileName(d.Name()) {
			out.tests++
			return nil
		}
		if !SupportsSymbols(d.Name()) {
			out.unsupported++
			return nil
		}
		// The size is checked before reading: a huge file must not be read only
		// to be skipped afterwards.
		if info, err := d.Info(); err != nil || info.Size() > maxSearchFileBytes {
			out.tooLarge++
			return nil
		}
		file, err := readSymbolFile(root, target, rel)
		if err != nil {
			out.noteError(fmt.Sprintf("%s: %v", target.show(rel), err))
			return nil
		}
		out.files++
		// Byte prefilter: a file whose text has no word-bounded occurrence of
		// the name is never parsed nor matched. It cannot hide one either: a Go
		// identifier is exactly the delimited text the filter looks for.
		if !re.Match(file.data) {
			return nil
		}
		refs, approx, err := scanRefs(file.name, file.data, name, re)
		if err != nil {
			// A parser error names the file and the line itself.
			out.noteError(err.Error())
			return nil
		}
		out.approx = out.approx || approx
		out.refs = append(out.refs, refs...)
		if len(out.refs) > limit {
			out.more = true
			return errSearchFull
		}
		return nil
	})
	if err != nil {
		return err
	}
	out.ignoreNote, out.timedOut = walk.ignoreNote, walk.timedOut
	return nil
}

// scanRefs extracts the references of one file: Go by parser (exact), the other
// languages of the registry by word-bounded text match (approx). A file type
// the registry does not know is an error; a file without the name yields
// nothing, which is why the caller filters on the same word-bounded pattern
// first.
func scanRefs(file string, src []byte, name string, re *regexp.Regexp) ([]Ref, bool, error) {
	lang, ok := symbolLanguageFor(file)
	if !ok {
		return nil, false, fmt.Errorf("unsupported file type %q; supported: %s",
			filepath.Ext(file), SupportedSymbolLanguages())
	}
	if lang.approx { // Go is the only language with an exact extractor
		return ScanLineRefs(file, src, name), true, nil
	}
	if !re.Match(src) {
		return nil, false, nil
	}
	refs, err := ScanGoRefs(file, src, name)
	return refs, false, err
}

// refOut collects one run: the matches and everything the model should know
// about what was left out.
type refOut struct {
	refs        []Ref
	files       int // supported source files that were read
	tests       int // test files left out
	unsupported int // files of a type no extractor covers
	tooLarge    int // files over maxSearchFileBytes
	approx      bool
	more        bool // the walk stopped one match past the limit
	errors      []string
	ignoreNote  string
	timedOut    bool
}

// noteError records an unreadable or unparsable file; the result names the
// first few and counts the rest.
func (o *refOut) noteError(msg string) { o.errors = append(o.errors, msg) }

// render formats the result: the matches, a line per thing that was left out,
// and the notes the walk produced.
func (o *refOut) render(name, pathArg string, limit int) string {
	if len(o.refs) > limit {
		o.more = true // one file alone can hold more matches than the limit
	}
	sort.SliceStable(o.refs, func(i, j int) bool {
		if o.refs[i].File != o.refs[j].File {
			return o.refs[i].File < o.refs[j].File
		}
		return o.refs[i].Line < o.refs[j].Line
	})
	shown := o.refs
	if len(shown) > limit {
		shown = shown[:limit]
	}
	lines := make([]string, 0, len(shown)+1)
	if len(shown) == 0 {
		lines = append(lines, fmt.Sprintf("no references to %q in %s (%d source file(s) searched)", name, pathArg, o.files))
	} else {
		for _, r := range shown {
			lines = append(lines, fmt.Sprintf("%s:%d %s %s", r.File, r.Line, refClass(r), r.Text))
		}
	}
	return strings.Join(append(lines, o.notes(limit)...), "\n")
}

// refClass is how one match is labelled: the class, plus the qualifier of a
// selector or call, with the pkg: prefix when the qualifier is an imported
// package name.
func refClass(r Ref) string {
	switch {
	case r.Qualifier == "":
		return r.Class
	case r.Pkg:
		return r.Class + "(pkg:" + r.Qualifier + ")"
	default:
		return r.Class + "(" + r.Qualifier + ")"
	}
}

// notes are what the result says besides the matches themselves.
func (o *refOut) notes(limit int) []string {
	var notes []string
	if len(o.errors) > 0 {
		shown := o.errors
		if len(shown) > maxRefErrors {
			shown = shown[:maxRefErrors]
		}
		note := fmt.Sprintf("[%d file(s) could not be parsed: %s", len(o.errors), strings.Join(shown, "; "))
		if more := len(o.errors) - len(shown); more > 0 {
			note += fmt.Sprintf("; and %d more", more)
		}
		notes = append(notes, note+"]")
	}
	if len(o.refs) == 0 {
		// A name defined here but used elsewhere is the common false empty: the
		// search only looks inside path.
		notes = append(notes, "[no references in this path; the name may be used from another package/directory - "+
			"try the workspace root (path=.) or the defining package's parent]")
	}
	// Fixed line: the tool does not resolve types, so a match may be another
	// object with that name and the shown line decides.
	notes = append(notes, "[no type resolution: a match may be another object with the same name "+
		"(a local, a field, another package's symbol); the shown line decides]")
	if o.approx {
		notes = append(notes, "[approx: JavaScript/TypeScript, Python and Java matches come from word-boundary "+
			"text matching with comments and strings masked, not a parser; confirm with file_grep]")
	}
	if o.more {
		notes = append(notes, fmt.Sprintf("[showing %d matches; more matched - narrow path, or use file_grep "+
			"with a word-boundary pattern for the full list]", limit))
	}
	if o.tests > 0 {
		notes = append(notes, fmt.Sprintf("[skipped %d test file(s); pass include_tests: true to search them]", o.tests))
	}
	if o.unsupported > 0 {
		notes = append(notes, fmt.Sprintf("[skipped %d file(s) of a type no extractor covers; supported: %s]",
			o.unsupported, SupportedSymbolLanguages()))
	}
	if o.tooLarge > 0 {
		notes = append(notes, fmt.Sprintf("[skipped %d file(s) over %d bytes]", o.tooLarge, maxSearchFileBytes))
	}
	if o.ignoreNote != "" {
		notes = append(notes, o.ignoreNote)
	}
	if o.timedOut {
		notes = append(notes, fmt.Sprintf("[已超时（%s），结果不完整]", searchTimeout))
	}
	return notes
}
