package codeintel

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// sourceYamlAST is what SymbolSet.Source reports for a file the YAML parser read
// (the fallback keeps sourceDeclLines, like the other line recognisers).
const sourceYamlAST = "goccy/go-yaml AST"

// yamlSymbolLanguage is YAML: its declarations are the mapping keys, listed by
// their dotted key path. The repository already depends on goccy/go-yaml, whose
// AST carries the line of every key, so this is the one config format with a
// real parse (the extractor reports sourceYamlAST and no approx); a file the
// parser rejects falls back to the indentation recogniser, which is why the
// language builds its own SymbolSet (extractSet) and marks one file approx. The
// approx field stays false for the same reason: it describes the parser path,
// and a reference search uses file_refs' line matcher for YAML anyway.
var yamlSymbolLanguage = symbolLanguage{
	name: "YAML", exts: []string{".yaml", ".yml"}, source: sourceYamlAST,
	extractSet: yamlSymbolSet,
}

// yamlSymbolSet parses one YAML file and lists its keys. It never fails: a file
// the parser rejects is answered from its lines, marked approx.
func yamlSymbolSet(file string, src []byte) (SymbolSet, error) {
	parsed, err := parser.ParseBytes(src, parser.ParseComments)
	if err != nil {
		syms, caps := yamlLineSymbols(file, src)
		notes := append([]string{fmt.Sprintf("[yaml: this file did not parse (%s); the keys below come from "+
			"indentation detection and are approx - confirm a key with file_grep]", yamlParseErrText(err))},
			configNotes("yaml", caps)...)
		return SymbolSet{Symbols: syms, Approx: true, Source: sourceDeclLines, Notes: notes}, nil
	}
	w := &yamlWalker{collector: newConfigCollector(file)}
	for i, doc := range parsed.Docs {
		// One path per file stays readable; a multi-document file (a k8s manifest
		// with several --- documents) numbers its documents instead, so two
		// documents declaring "spec" do not read as one repeated key.
		prefix := ""
		if len(parsed.Docs) > 1 {
			prefix = fmt.Sprintf("doc%d", i+1)
		}
		w.walk(yamlValueNode(doc), prefix, 1)
	}
	return SymbolSet{Symbols: w.collector.symbols(), Source: sourceYamlAST, Notes: configNotes("yaml", w.collector.caps)}, nil
}

// yamlWalker lists the keys of one parsed YAML file: the top-level mapping of
// each document, the mappings under a key, and the mappings inside a sequence.
// An alias (*base) is not followed: its keys belong to the anchor, listed once
// where the anchor stands.
type yamlWalker struct {
	collector *configCollector
}

// walk lists the keys of node, whose own path is path ("" at the top of a
// document) and whose keys sit at level.
func (w *yamlWalker) walk(node ast.Node, path string, level int) {
	switch n := node.(type) {
	case *ast.DocumentNode:
		w.walk(yamlValueNode(n), path, level)
	case *ast.MappingNode:
		w.walkMapping(n, path, level)
	case *ast.MappingValueNode:
		w.walk(yamlValueNode(n), path, level)
	case *ast.SequenceNode:
		w.walkSequence(n, path, level)
	}
}

// walkMapping lists the keys of one mapping. A key whose value is a mapping is a
// section and holds the keys below it; a key whose value is a sequence is a
// section when the sequence holds mappings (its entries' keys hang under
// "path[]"). level is the level the keys of this mapping sit at: a deeper
// mapping is left out and counted, so one k8s manifest does not bury the rest.
func (w *yamlWalker) walkMapping(m *ast.MappingNode, path string, level int) {
	if level > ConfigMaxDepth {
		w.collector.caps.deep = true
		return
	}
	for _, mv := range m.Values {
		if mv.Key == nil {
			continue
		}
		name := configKeyName(mv.Key.String())
		if name == "" || strings.ContainsAny(name, "{}[]") {
			continue // a flow mapping key or a merge key is not a dotted path
		}
		child := configPathJoin(path, name)
		value := yamlValueNode(mv.Value)
		switch v := value.(type) {
		case *ast.MappingNode:
			w.collector.add(child, yamlKeyLine(mv.Key), SymbolSection, configMappingPreview)
			w.walkMapping(v, child, level+1)
		case *ast.SequenceNode:
			kind := SymbolKey
			if yamlHoldsMapping(v) {
				kind = SymbolSection
				w.walkSequence(v, child, level+1)
			}
			w.collector.add(child, yamlKeyLine(mv.Key), kind, configArrayPreview)
		default:
			// A leaf value: its text as written is the signature.
			w.collector.add(child, yamlKeyLine(mv.Key), SymbolKey, configKeyPreview(yamlNodeText(value)))
		}
	}
}

// walkSequence lists the keys of the mappings among a sequence's entries: they
// all hang under "path[]", which is what makes the entries of one array share a
// key path (and its repeat count).
func (w *yamlWalker) walkSequence(seq *ast.SequenceNode, path string, level int) {
	entry := configPathSeq(path)
	for _, item := range seq.Values {
		if m, ok := yamlValueNode(item).(*ast.MappingNode); ok {
			w.walkMapping(m, entry, level)
		}
	}
}

// yamlHoldsMapping reports whether a sequence has a mapping among its entries
// (a list of ports is not a section; a list of services is).
func yamlHoldsMapping(seq *ast.SequenceNode) bool {
	for _, item := range seq.Values {
		if _, ok := yamlValueNode(item).(*ast.MappingNode); ok {
			return true
		}
	}
	return false
}

// yamlValueNode unwraps the nodes that only carry what a value really is: a
// document's body, an anchor's value (the "&base" before a mapping) and a tag's
// value. An alias (*base) is deliberately not followed: its keys are the
// anchor's, listed once where the anchor stands.
func yamlValueNode(node ast.Node) ast.Node {
	for node != nil {
		switch n := node.(type) {
		case *ast.DocumentNode:
			node = n.Body
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		default:
			return node
		}
	}
	return nil
}

// yamlKeyLine is the line of one key, 0 when the parser gave no position (the
// collector then lists the key without a line, which is better than dropping it).
func yamlKeyLine(key ast.Node) int {
	if tok := key.GetToken(); tok != nil && tok.Position != nil {
		return tok.Position.Line
	}
	return 0
}

// yamlNodeText is one node's text as written: the empty string for a value a
// node does not carry (a nil node after unwrapping).
func yamlNodeText(node ast.Node) string {
	if node == nil {
		return ""
	}
	return node.String()
}

// yamlParseErrText is the one-line gist of the YAML parser's error: goccy's
// errors carry a code frame over several lines, and a tool result wants one line.
func yamlParseErrText(err error) string {
	text := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if len(text) > 120 {
		text = text[:utf8Cut(text, 120)] + "…"
	}
	return text
}

// yamlLineFrame is one open indentation level of the fallback recogniser: the
// indent of its line and the path its children hang under.
type yamlLineFrame struct {
	indent int
	prefix string
}

// yamlLineSymbols is the fallback for a file the YAML parser rejects: it reads
// the indentation and the "key:" of every line. That is close to the parser's
// answer without being proof (the caller marks the result approx): a key inside
// a block scalar's text or a flow mapping is not told apart from a real one, and
// a sequence entry is marked from the "- " it starts with.
func yamlLineSymbols(file string, src []byte) ([]Symbol, configCaps) {
	lines := NewSourceLines(src)
	collector := newConfigCollector(file)
	multiDoc := yamlHasDocumentMarker(lines)
	var frames []yamlLineFrame
	docNumber, started := 1, false
	for i, raw := range lines.Lines {
		line := strings.TrimRight(raw, " \t")
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if text == "---" {
			// A document marker starts the next document; the one a file begins
			// with is the first document's own header.
			if started {
				docNumber++
			}
			frames = frames[:0]
			continue
		}
		if text == "..." {
			continue
		}
		started = true
		indent, entry := indentWidth(line), false
		if text == "-" || strings.HasPrefix(text, "- ") {
			// A sequence entry: its content sits two columns in, and its key hangs
			// under the sequence's "[]" path.
			entry, text = true, strings.TrimSpace(strings.TrimPrefix(text, "-"))
			indent += 2
		}
		end := yamlLineKeyEnd(text)
		if end <= 0 {
			continue // a plain scalar entry or a flow line is no key
		}
		name := configKeyName(text[:end])
		if name == "" || strings.ContainsAny(name, "{}[]") {
			continue
		}
		value := strings.TrimSpace(text[end+1:])
		for len(frames) > 0 && frames[len(frames)-1].indent >= indent {
			frames = frames[:len(frames)-1]
		}
		// The frames carry the path without the document number: a document
		// marker resets them, and the number is added once, here.
		prefix := ""
		if len(frames) > 0 {
			prefix = frames[len(frames)-1].prefix
		}
		if entry {
			prefix = configPathSeq(prefix)
			indent-- // the entry's own keys are its siblings under "[]"
		}
		path := configPathJoin(prefix, name)
		if multiDoc {
			path = configPathJoin(fmt.Sprintf("doc%d", docNumber), path)
		}
		kind, signature := SymbolKey, configKeyPreview(value)
		switch {
		case value == "":
			kind, signature = SymbolSection, configMappingPreview
		case strings.HasPrefix(value, "["):
			signature = configArrayPreview
		}
		collector.add(path, i+1, kind, signature)
		child := configPathJoin(prefix, name)
		if entry && value != "" {
			child = prefix // the entry's other keys hang under the sequence, not under this one
		}
		frames = append(frames, yamlLineFrame{indent: indent, prefix: child})
	}
	return collector.symbols(), collector.caps
}

// yamlLineKeyEnd finds the ":" that ends the key of one line: the first colon
// outside quotes that a space, a tab or the end of the line follows, so a value
// like a URL's "http://example" does not split the line at its scheme.
func yamlLineKeyEnd(line string) int {
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case ':':
			if i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t' {
				return i
			}
		}
	}
	return -1
}

// yamlHasDocumentMarker reports whether a file has a document marker line, which
// is how the fallback knows to number its documents like the parser does (only a
// file with more than one document is prefixed).
func yamlHasDocumentMarker(lines SourceLines) bool {
	for _, raw := range lines.Lines {
		if strings.TrimSpace(raw) == "---" {
			return true
		}
	}
	return false
}
