package native

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

const (
	// defaultSymbolLimit caps one file_symbols result: the model narrows name
	// or kind when it is reached instead of paging through thousands of lines.
	defaultSymbolLimit = 200
	// maxSymbolErrors is how many unreadable or unparsable files a result names
	// before it just counts the rest.
	maxSymbolErrors = 5
)

// FileSymbolsTool lists the declarations of a source file, or of every source
// file under a directory (design docs/design/2026-10-18-explore-efficiency-design.md,
// CAP-02): "which declarations are in this package, and on which line" is what
// the model answered by guessing file_grep patterns before. It is read-only and
// parses in memory only.
type FileSymbolsTool struct {
	workspace string
	readRoots []string // read-only directories outside the workspace
	addDirs   *AddDirs // the session's added directories, read at call time
	maxOut    int      // raw result cap in bytes; 0 = DefaultRawOutputBytes
}

func (t *FileSymbolsTool) Name() string { return "file_symbols" }
func (t *FileSymbolsTool) Description() string {
	return "List the declarations of a source file, or of every source file under a directory (recursive): " +
		"one `path:line kind name signature` line each, sorted by path and line. Use it instead of guessing " +
		"file_grep patterns to find where something is declared. Go is parsed exactly (func/method/type/const/var); " +
		"JavaScript/TypeScript, Python, Java and PHP are recognised from their declaration lines and marked approx " +
		"(confirm a name with file_grep); universal-ctags is used first when it is installed and covers more " +
		"languages. It lists declarations only, never usages: for who calls X or where X is used, use file_refs. " +
		"Any other file type is refused with the supported list. Paths under a read-only dependency " +
		"root (module cache, SDK sources) work too: use it there to list a dependency's API instead of reading its " +
		"source in pieces. " +
		fmt.Sprintf("Directories skip test files unless include_tests is set, and a result stops at %d declarations.",
			defaultSymbolLimit)
}

// ToolMeta marks the tool as a read-only read of the sources. It pages its own
// result (limit), so it is not Rereadable: the output layer may wrap it.
func (*FileSymbolsTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryRead, ReadOnly: true, Prefetchable: true}
}
func (t *FileSymbolsTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File or directory to list (relative to the workspace, or absolute under a read-only dependency root)",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Only declarations whose name contains this text (case-insensitive)",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Only these kinds: " + strings.Join(SymbolKinds, ", ") + " (comma-separated; empty = all)",
				},
				"include_tests": map[string]any{
					"type":        "boolean",
					"description": "Also list *_test.go, *.test.ts, test_*.py and *Test.java (default false)",
				},
				"no_ignore": map[string]any{
					"type":        "boolean",
					"description": "Also search .gitignore matches and node_modules/vendor/dist (.git is always skipped)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum declarations to list (default %d)", defaultSymbolLimit),
				},
			},
			"required": []any{"path"},
		},
	}
}

func (t *FileSymbolsTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	pathArg, _ := call.Arguments["path"].(string)
	if pathArg == "" {
		return nil, fmt.Errorf("file_symbols: %q arg is required", "path")
	}
	filter, err := newSymbolFilter(call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("file_symbols: %w", err)
	}
	limit := defaultSymbolLimit
	if n, ok := numberArg(call.Arguments["limit"]); ok && n >= 1 {
		limit = int(n)
	}
	includeTests, _ := call.Arguments["include_tests"].(bool)
	noIgnore, _ := call.Arguments["no_ignore"].(bool)
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), pathArg)
	if err != nil {
		return nil, fmt.Errorf("file_symbols: %w", err)
	}

	out := &symbolOut{}
	// A file argument is parsed as that file: a type no extractor covers is
	// refused with the supported list, not answered with an empty result.
	if info, statErr := os.Lstat(target.abs); statErr == nil && info.Mode().IsRegular() {
		set, err := t.oneFile(target)
		if err != nil {
			return nil, fmt.Errorf("file_symbols: %w", err)
		}
		out.files, out.approx = 1, set.Approx
		out.syms = filter.apply(set.Symbols)
	} else if err := t.walkDir(ctx, target, filter, limit, includeTests, noIgnore, out); err != nil {
		return nil, fmt.Errorf("file_symbols: %w", err)
	}
	if out.files == 0 {
		if out.tests > 0 && out.unsupported == 0 {
			return nil, fmt.Errorf("file_symbols: only test files under %s; pass include_tests: true to list them", pathArg)
		}
		return nil, fmt.Errorf("file_symbols: no supported source files under %s; supported: %s",
			pathArg, SupportedSymbolLanguages())
	}
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    t.Name(),
		Content: TruncateHeadTail(out.render(filter, limit), rawLimit(t.maxOut)),
	}, nil
}

// oneFile reads one file through os.Root and extracts its declarations.
func (t *FileSymbolsTool) oneFile(target readTarget) (SymbolSet, error) {
	file, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) (symbolFile, error) {
		return readSymbolFile(root, target, rel)
	})
	if err != nil {
		return SymbolSet{}, err
	}
	return ExtractSymbolsAt(file.name, file.onDisk, file.data)
}

// walkDir collects the declarations of every supported source file under the
// directory target, stopping one declaration past limit (the result then says
// more matched instead of walking a whole module cache).
func (t *FileSymbolsTool) walkDir(ctx context.Context, target readTarget, filter symbolFilter, limit int, includeTests bool, noIgnore bool, out *symbolOut) error {
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
		file, err := readSymbolFile(root, target, rel)
		if err != nil {
			out.noteError(fmt.Sprintf("%s: %v", target.show(rel), err))
			return nil
		}
		if len(file.data) > maxSearchFileBytes {
			out.tooLarge++
			return nil
		}
		set, err := ExtractSymbolsAt(file.name, file.onDisk, file.data)
		if err != nil {
			// A parser error names the file and the line itself.
			out.noteError(err.Error())
			return nil
		}
		out.files++
		out.approx = out.approx || set.Approx
		out.syms = append(out.syms, filter.apply(set.Symbols)...)
		if len(out.syms) > limit {
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

// symbolFile is one source file the tool hands to the extractor: name is how
// the results show it, onDisk where an external tool can read it again, data
// what os.Root read.
type symbolFile struct {
	name   string
	onDisk string
	data   []byte
}

// readSymbolFile reads one file through root: root keeps the read inside the
// workspace or the read root, whatever the path says. The walk and the paths
// are slash-separated, as every file tool reports them.
func readSymbolFile(root *os.Root, target readTarget, rel string) (symbolFile, error) {
	rel = filepath.ToSlash(rel)
	data, err := root.ReadFile(rel)
	if err != nil {
		return symbolFile{}, err
	}
	return symbolFile{
		name:   target.show(rel),
		onDisk: filepath.Join(target.base, filepath.FromSlash(rel)),
		data:   data,
	}, nil
}

// symbolOut collects one run: the matching declarations and everything the
// model should know about what was left out.
type symbolOut struct {
	syms        []Symbol
	files       int // source files that were parsed
	tests       int // test files left out
	unsupported int // files of a type no extractor covers
	tooLarge    int // files over maxSearchFileBytes
	approx      bool
	more        bool // the walk stopped one declaration past the limit
	errors      []string
	ignoreNote  string
	timedOut    bool
}

// noteError records an unreadable or unparsable file; the result names the
// first few and counts the rest.
func (o *symbolOut) noteError(msg string) { o.errors = append(o.errors, msg) }

// render formats the result: the declaration lines, a line per thing that was
// left out, and the notes the walk produced.
func (o *symbolOut) render(filter symbolFilter, limit int) string {
	if len(o.syms) > limit {
		o.more = true // one file alone can hold more declarations than the limit
	}
	sort.SliceStable(o.syms, func(i, j int) bool {
		if o.syms[i].File != o.syms[j].File {
			return o.syms[i].File < o.syms[j].File
		}
		return o.syms[i].Line < o.syms[j].Line
	})
	shown := o.syms
	if len(shown) > limit {
		shown = shown[:limit]
	}
	lines := make([]string, 0, len(shown)+1)
	if len(shown) == 0 {
		note := fmt.Sprintf("no symbols matched (%d source file(s) scanned", o.files)
		if desc := filter.describe(); desc != "" {
			note += "; " + desc
		}
		lines = append(lines, note+")")
	} else {
		for _, s := range shown {
			lines = append(lines, fmt.Sprintf("%s:%d %s %s %s", s.File, s.Line, s.Kind, s.Name, s.Signature))
		}
	}
	return strings.Join(append(lines, o.notes(limit)...), "\n")
}

// notes are what the result says besides the declarations themselves.
func (o *symbolOut) notes(limit int) []string {
	var notes []string
	if len(o.errors) > 0 {
		shown := o.errors
		if len(shown) > maxSymbolErrors {
			shown = shown[:maxSymbolErrors]
		}
		note := fmt.Sprintf("[%d file(s) could not be parsed: %s", len(o.errors), strings.Join(shown, "; "))
		if more := len(o.errors) - len(shown); more > 0 {
			note += fmt.Sprintf("; and %d more", more)
		}
		notes = append(notes, note+"]")
	}
	if o.approx {
		notes = append(notes, "[approx: JavaScript/TypeScript, Python, Java and PHP declarations come from declaration-line "+
			"detection, not a parser; confirm a name with file_grep]")
	}
	if o.more {
		notes = append(notes, fmt.Sprintf("[showing %d declarations; more matched - narrow name or kind, or raise limit]", limit))
	}
	if o.tests > 0 {
		notes = append(notes, fmt.Sprintf("[skipped %d test file(s); pass include_tests: true to include them]", o.tests))
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

// symbolFilter is file_symbols' name and kind filter: name is a
// case-insensitive substring of a declaration's name, kinds the accepted kind
// names (empty = all of them).
type symbolFilter struct {
	name  string
	kinds map[string]bool
}

// kindAliases are the spellings a model may pass for a kind; the vocabulary
// itself is SymbolKinds.
var kindAliases = map[string]string{
	"fn": SymbolFunc, "funcs": SymbolFunc, "function": SymbolFunc, "functions": SymbolFunc, "def": SymbolFunc,
	"methods": SymbolMethod, "types": SymbolType, "typedef": SymbolType, "struct": SymbolType,
	"constants": SymbolConst, "vars": SymbolVar, "variables": SymbolVar,
	"classes": SymbolClass, "interfaces": SymbolInterface, "enums": SymbolEnum, "records": SymbolRecord,
}

// newSymbolFilter reads the name and kind arguments. An unknown kind is an
// error that lists the vocabulary: a typo must not look like "nothing found".
func newSymbolFilter(args map[string]any) (symbolFilter, error) {
	filter := symbolFilter{}
	filter.name, _ = args["name"].(string)
	filter.name = strings.TrimSpace(filter.name)
	raw, _ := args["kind"].(string)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		kind := strings.ToLower(strings.TrimSpace(part))
		if alias, ok := kindAliases[kind]; ok {
			kind = alias
		}
		if !slices.Contains(SymbolKinds, kind) {
			return symbolFilter{}, fmt.Errorf("unknown kind %q; kinds: %s", part, strings.Join(SymbolKinds, ", "))
		}
		if filter.kinds == nil {
			filter.kinds = map[string]bool{}
		}
		filter.kinds[kind] = true
	}
	return filter, nil
}

// apply returns the symbols that pass the filter.
func (f symbolFilter) apply(syms []Symbol) []Symbol {
	if f.name == "" && len(f.kinds) == 0 {
		return syms
	}
	needle := strings.ToLower(f.name)
	out := make([]Symbol, 0, len(syms))
	for _, s := range syms {
		if len(f.kinds) > 0 && !f.kinds[s.Kind] {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(s.Name), needle) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// describe names the filter for the "no symbols matched" line; "" without one.
func (f symbolFilter) describe() string {
	var parts []string
	if f.name != "" {
		parts = append(parts, fmt.Sprintf("name=%q", f.name))
	}
	if len(f.kinds) > 0 {
		kinds := make([]string, 0, len(f.kinds))
		for kind := range f.kinds {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		parts = append(parts, "kind="+strings.Join(kinds, ","))
	}
	return strings.Join(parts, " ")
}

// testFilePatterns are the names the supported languages give their test files.
// file_symbols skips them unless include_tests is set: the report's third waste
// is test declarations drowning the real ones.
var testFilePatterns = []*regexp.Regexp{
	regexp.MustCompile(`_test\.go$`),
	regexp.MustCompile(`(?:\.|-)(?:test|spec)\.(?:ts|tsx|mts|cts|js|jsx|mjs|cjs)$`),
	regexp.MustCompile(`^(?:test_.*|.*_test)\.pyi?$`),
	regexp.MustCompile(`(?:Test|Tests|IT)\.java$`),
}

// isTestFileName reports whether name looks like a test file of one of the
// supported languages.
func isTestFileName(name string) bool {
	for _, re := range testFilePatterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
