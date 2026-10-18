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
		"A qualified name narrows the result to the uses of that type or package: name: \"Runner.Run\" keeps the " +
		"definitions of type Runner and the Runner.x / pkg.Run uses, and a longer path such as s.runner.Run keeps " +
		"its last segment. " +
		"Go is parsed exactly (class def, call, sel or ref; sel/call carry the X of X.name, with pkg:X when X is an " +
		"imported package) but without type checking, so a match may be another local, field or another package's " +
		"symbol - the shown line decides. JavaScript/TypeScript, Python, Java and PHP match word-bounded text with " +
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
					"type": "string",
					"description": "The identifier to look for: a function, method, type, field, variable or import name (case-sensitive); " +
						"a qualified name such as Runner.Run keeps only the uses qualified by Runner",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Where to look: a file or directory to search (default: workspace root); may be absolute under a read-only dependency root",
				},
				"include_tests": map[string]any{
					"type":        "boolean",
					"description": "Also search *_test.go, *.test.ts, test_*.py, *Test.java and *Test.php (default false)",
				},
				"no_ignore": map[string]any{
					"type":        "boolean",
					"description": "Also search .gitignore matches and node_modules/vendor/dist (.git is always skipped)",
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
	// A "Q.Name" argument searches Name and keeps its Q uses only; the display
	// name stays as it was written, so a zero-match result echoes the query.
	displayName := name
	name, qual := splitRefName(name)
	pathArg, _ := call.Arguments["path"].(string)
	if pathArg == "" {
		pathArg = "."
	}
	limit := defaultRefLimit
	if n, ok := numberArg(call.Arguments["limit"]); ok && n >= 1 {
		limit = int(n)
	}
	includeTests, _ := call.Arguments["include_tests"].(bool)
	noIgnore, _ := call.Arguments["no_ignore"].(bool)
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), pathArg)
	if err != nil {
		return nil, fmt.Errorf("file_refs: %w", err)
	}
	re := refWordRe(name)

	out := &refOut{name: name, qual: qual, dropped: newRefDrops()}
	// A file argument is searched as that file: a type no extractor covers is
	// refused with the supported list, not answered with an empty result.
	if info, statErr := os.Lstat(target.abs); statErr == nil && info.Mode().IsRegular() {
		refs, approx, dropped, err := t.oneFile(target, name, qual, re)
		if err != nil {
			return nil, fmt.Errorf("file_refs: %w", err)
		}
		out.files, out.approx, out.refs, out.dropped = 1, approx, refs, dropped
		out.goRefs = !approx && len(refs) > 0
	} else if err := t.walkDir(ctx, target, name, qual, re, limit, includeTests, noIgnore, out); err != nil {
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
		Content: TruncateHeadTail(out.render(displayName, pathArg, limit), rawLimit(t.maxOut)),
	}, nil
}

// oneFile searches one file through os.Root, the way file_symbols reads one.
func (t *FileRefsTool) oneFile(target readTarget, name, qual string, re *regexp.Regexp) ([]Ref, bool, *refDrops, error) {
	if !SupportsSymbols(target.abs) {
		return nil, false, nil, fmt.Errorf("unsupported file type %q; supported: %s",
			filepath.Ext(target.abs), SupportedSymbolLanguages())
	}
	file, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) (symbolFile, error) {
		return readSymbolFile(root, target, rel)
	})
	if err != nil {
		return nil, false, nil, err
	}
	return scanRefs(file.name, file.data, name, qual, re)
}

// walkDir searches every supported source file under the directory target,
// stopping one match past limit (the result then says more matched instead of
// walking a whole module cache).
func (t *FileRefsTool) walkDir(ctx context.Context, target readTarget, name, qual string, re *regexp.Regexp, limit int, includeTests bool, noIgnore bool, out *refOut) error {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	walk, err := walkWorkspace(ctx, target.base, target.abs, noIgnore, func(root *os.Root, rel string, d fs.DirEntry) error {
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
		refs, approx, dropped, err := scanRefs(file.name, file.data, name, qual, re)
		if err != nil {
			// A parser error names the file and the line itself.
			out.noteError(err.Error())
			return nil
		}
		out.approx = out.approx || approx
		out.goRefs = out.goRefs || (!approx && len(refs) > 0)
		out.dropped.merge(dropped)
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
// first. qual narrows the result to the Q of a qualified name and dropped is how
// many occurrences of the name qualified something else.
func scanRefs(file string, src []byte, name, qual string, re *regexp.Regexp) ([]Ref, bool, *refDrops, error) {
	lang, ok := symbolLanguageFor(file)
	if !ok {
		return nil, false, nil, fmt.Errorf("unsupported file type %q; supported: %s",
			filepath.Ext(file), SupportedSymbolLanguages())
	}
	if lang.approx { // Go is the only language with an exact extractor
		refs, dropped := scanLineRefs(file, src, name, qual)
		return refs, true, dropped, nil
	}
	if !re.Match(src) {
		return nil, false, nil, nil
	}
	refs, dropped, err := scanGoRefs(file, src, name, qual)
	return refs, false, dropped, err
}

// splitRefName splits a possibly qualified name argument: the last dot segment
// is the name to search and the segment before it the qualifier Q that narrows
// the result, so "a.b.Run" searches Run with Q=b and "Run" searches it
// unqualified. A trailing or doubled dot falls back to the text as it is.
func splitRefName(raw string) (name, qual string) {
	i := strings.LastIndexByte(raw, '.')
	if i <= 0 || i == len(raw)-1 {
		return raw, ""
	}
	name, qual = raw[i+1:], raw[:i]
	if j := strings.LastIndexByte(qual, '.'); j >= 0 {
		qual = qual[j+1:]
	}
	if qual == "" {
		return name, ""
	}
	return name, qual
}

// refOut collects one run: the matches and everything the model should know
// about what was left out.
type refOut struct {
	refs    []Ref
	files   int       // supported source files that were read
	name    string    // the searched identifier, without its qualifier
	qual    string    // the Q of a qualified query; empty when the name had no dot
	dropped *refDrops // occurrences of the name that qualify another object, by qualifier
	tests   int       // test files left out

	unsupported int // files of a type no extractor covers
	tooLarge    int // files over maxSearchFileBytes
	approx      bool
	goRefs      bool // at least one shown match came from the Go parser
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
	if o.qual != "" {
		// Rare variable qualifiers are shown as lines of their own group: the
		// filter cannot tell their type, and one of them may hold a o.qual.
		if rare, more := o.dropped.rare(); len(rare) > 0 {
			lines = append(lines, fmt.Sprintf("[maybe also %s: unqualified uses (a call inside the defining package "+
				"needs no qualifier) and uses through variables whose type the filter cannot tell (names used at "+
				"most %d times) - check each line]", o.qual, rareDropUses))
			for _, r := range rare {
				lines = append(lines, fmt.Sprintf("%s:%d %s %s", r.File, r.Line, refClass(r), r.Text))
			}
			if more > 0 {
				lines = append(lines, fmt.Sprintf("[and %d more such line(s)]", more))
			}
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
		// search only looks inside path. A qualified search that only dropped other
		// qualifiers is told apart by the note below, not by this hint.
		if o.dropped.total() == 0 {
			notes = append(notes, "[no references in this path; the name may be used from another package/directory - "+
				"try the workspace root (path=.) or the defining package's parent]")
		}
	}
	if o.qual != "" {
		// The filter is textual, and the model should know what it left out: the
		// same name is used on other objects often enough to look like a miss,
		// and a variable whose name says nothing (child.Run on a *Runner) is
		// dropped too - so the dropped qualifiers are named, not only counted.
		notes = append(notes, fmt.Sprintf("[qualified by %q: kept the definitions of type/package %s and the "+
			"sel/call uses whose qualifier text is %s or ends with %s (case-insensitive); dropped %d use(s) of "+
			"%q with another qualifier%s - a variable among them may still refer to %s, so check the names you "+
			"do not recognise, or search without the qualifier for all of them]",
			o.qual, o.qual, o.qual, o.qual, o.dropped.total(), o.name, o.dropped.describe(), o.qual))
	}
	if o.goRefs {
		// The conclusion the model needs to stop re-checking with file_grep: a Go
		// result is complete for the name, only its other objects are in question.
		if o.qual == "" {
			notes = append(notes, "[Go results come from a syntax parse and list every occurrence; only confirm "+
				"same-named other objects - no file_grep needed to fill in]")
		} else {
			notes = append(notes, "[Go results come from a syntax parse: together with the dropped qualifiers "+
				"above they account for every occurrence - no file_grep needed to fill in]")
		}
	}
	// Fixed line: the tool does not resolve types, so a match may be another
	// object with that name and the shown line decides.
	notes = append(notes, "[no type resolution: a match may be another object with the same name "+
		"(a local, a field, another package's symbol); the shown line decides]")
	if o.approx {
		notes = append(notes, "[approx: JavaScript/TypeScript, Python, Java and PHP matches come from word-boundary "+
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
