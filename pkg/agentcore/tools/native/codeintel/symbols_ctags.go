package codeintel

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// The optional accelerator: universal-ctags. When it is installed it finds a
// file's declarations far more precisely than the lightweight recognisers and
// covers languages the built-in extractors do not (design 0.2: "PATH 中有
// universal-ctags 时优先用它"). It is never required: ctags missing, unable to
// handle the language, failing or finding nothing all fall back to the built-in
// extractor, so an odd ctags build cannot break a tool.

// ctagsCommand is the universal-ctags binary to use, "" when there is none. A
// variable so the tests can stand in a canned ctags.
var ctagsCommand = detectCtags

// detectCtags looks ctags up in PATH and accepts it only when it is Universal
// Ctags: --output-format=json, what ctagsSymbols parses, is its flag (the older
// exuberant ctags has no JSON output). The check runs a process, so it happens
// once per process.
var detectCtags = sync.OnceValue(func() string {
	path, err := exec.LookPath("ctags")
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil || !bytes.Contains(out, []byte("Universal Ctags")) {
		return ""
	}
	return path
})

// PinBuiltinExtractors turns universal-ctags off for the rest of the process
// and returns the function that restores the previous lookup. The tests of the
// tool layer use it: which declarations a listing shows must not depend on
// whether the machine running the tests happens to have ctags installed, and
// the lookup lives here.
func PinBuiltinExtractors() (restore func()) {
	command := ctagsCommand
	ctagsCommand = func() string { return "" }
	return func() { ctagsCommand = command }
}

// ctagsRun runs ctags over one file. A variable so the tests can replace the
// process: -f - writes the JSON tags to stdout, --language-force picks the
// language from the extension instead of ctags' own guessing.
var ctagsRun = func(command, language, path string) ([]byte, error) {
	return exec.Command(command, "--output-format=json", "--language-force="+language, "-f", "-", path).Output()
}

// ctagsSymbols extracts one file's declarations with universal-ctags: file is
// how the results name it, onDisk the path ctags parses and src the content the
// signatures are read from. found is false whenever ctags is missing, does not
// know the extension, fails or finds nothing, and the caller then uses the
// built-in extractor.
func ctagsSymbols(lang symbolLanguage, file, onDisk string, src []byte) (SymbolSet, bool) {
	command := ctagsCommand()
	language := lang.ctagsLanguage[strings.ToLower(filepath.Ext(file))]
	if command == "" || language == "" || onDisk == "" {
		return SymbolSet{}, false
	}
	out, err := ctagsRun(command, language, onDisk)
	if err != nil {
		return SymbolSet{}, false
	}
	syms, err := parseCtagsTags(out, file, src)
	if err != nil || len(syms) == 0 {
		return SymbolSet{}, false
	}
	return SymbolSet{Symbols: syms, Source: sourceCtags}, true
}

// ctagsTag is the subset of one universal-ctags JSON tag the symbols use.
type ctagsTag struct {
	Type string `json:"_type"` // "tag"; anything else (a pseudo tag) is skipped
	Line int    `json:"line"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// parseCtagsTags turns ctags' JSON lines into symbols named file. One malformed
// line fails the whole parse, which makes ctagsSymbols fall back: a ctags whose
// output this is not would otherwise produce half a result.
func parseCtagsTags(out []byte, file string, src []byte) ([]Symbol, error) {
	lines := NewSourceLines(src)
	var syms []Symbol
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var tag ctagsTag
		if err := json.Unmarshal([]byte(line), &tag); err != nil {
			return nil, err
		}
		if tag.Name == "" || (tag.Type != "" && tag.Type != "tag") {
			continue
		}
		syms = append(syms, Symbol{
			File: file, Line: tag.Line, Kind: ctagsKind(tag.Kind), Name: tag.Name, Signature: lines.At(tag.Line),
		})
	}
	return syms, nil
}

// ctagsKinds maps ctags' kind spelling onto the tool's vocabulary: ctags writes
// the long name in its JSON output ("function") and its per-language letter in
// other outputs ("f" for a C function), so both are accepted. A kind the tool
// does not know is kept as ctags spelled it, which is still better than
// dropping the symbol.
var ctagsKinds = map[string]string{
	"f": SymbolFunc, "function": SymbolFunc, "func": SymbolFunc,
	"m": SymbolMethod, "method": SymbolMethod,
	"c": SymbolClass, "class": SymbolClass,
	"i": SymbolInterface, "interface": SymbolInterface,
	"t": SymbolType, "type": SymbolType, "typedef": SymbolType, "struct": SymbolType, "union": SymbolType,
	"g": SymbolEnum, "enum": SymbolEnum, "enumerator": SymbolConst,
	"r": SymbolRecord, "record": SymbolRecord,
	"v": SymbolVar, "var": SymbolVar, "variable": SymbolVar, "field": SymbolVar,
	"constant": SymbolConst, "macro": SymbolConst,
}

// ctagsKind normalises one ctags kind.
func ctagsKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	if mapped, ok := ctagsKinds[k]; ok {
		return mapped
	}
	return k
}
