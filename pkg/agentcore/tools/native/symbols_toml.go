package native

import "strings"

// tomlSymbolLanguage is TOML: its declarations are the tables and the keys a
// TOML file assigns. No parser is added for it (the file format is regular
// enough to read from its lines), so the result is marked approx: a "[table]"
// line is a section, "key = value" a key, and the dotted key path names both
// ("server.http.port"). The extractor builds its own SymbolSet (extractSet) so
// its caps travel with the result.
var tomlSymbolLanguage = symbolLanguage{
	name: "TOML", exts: []string{".toml"}, source: sourceDeclLines,
	extractSet: tomlSymbolSet,
}

// tomlSymbolSet recognises the tables and keys of one TOML file line by line and
// never fails: what it does not recognise is not an error, it is a line that is
// no declaration (a value's continuation, an inline table's inner key).
func tomlSymbolSet(file string, src []byte) (SymbolSet, error) {
	syms, caps := tomlSymbols(file, src)
	return SymbolSet{Symbols: syms, Approx: true, Source: sourceDeclLines, Notes: configNotes("toml", caps)}, nil
}

// tomlSymbols recognises the tables and keys of one TOML file line by line, with
// comments and multi-line strings blanked first (maskToml), so a key inside
// either is not listed. The table a key belongs to is remembered and prefixes
// its path.
func tomlSymbols(file string, src []byte) ([]Symbol, configCaps) {
	lines := newSourceLines(maskSpans(src, maskToml))
	collector := newConfigCollector(file)
	table := "" // the current [table] path, as the keys below it are named
	for i, raw := range lines.lines {
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.HasPrefix(text, "[[") {
			// An array of tables: its entries repeat, so its keys hang under
			// "products[]", the way a YAML sequence's mappings do. A table deeper
			// than the cap is still the current table (its keys are left out too),
			// it is just not listed itself.
			name, ok := tomlHeaderName(text, "[[", "]]")
			if !ok {
				continue
			}
			table = configPathSeq(name)
			if tomlDepthOK(collector, table) {
				collector.add(table, i+1, SymbolSection, configArrayPreview)
			}
			continue
		}
		if strings.HasPrefix(text, "[") {
			name, ok := tomlHeaderName(text, "[", "]")
			if !ok {
				continue
			}
			table = name
			if tomlDepthOK(collector, table) {
				collector.add(table, i+1, SymbolSection, configMappingPreview)
			}
			continue
		}
		key, value, ok := tomlAssignment(text)
		if !ok {
			continue
		}
		path := configPathJoin(table, key)
		if !tomlDepthOK(collector, path) {
			continue
		}
		kind, signature := SymbolKey, configKeyPreview(value)
		if strings.HasPrefix(value, "{") {
			// An inline table: only its outer key is listed, its own keys stay in
			// the one line this recogniser reads.
			signature = configMappingPreview
		} else if strings.HasPrefix(value, "[") {
			signature = configArrayPreview
		}
		collector.add(path, i+1, kind, signature)
	}
	return collector.symbols(), collector.caps
}

// tomlDepthOK reports whether a key path is inside the depth cap; a deeper one
// is counted, so the note can say so (a TOML path's segments are its levels).
func tomlDepthOK(collector *configCollector, path string) bool {
	if strings.Count(path, ".")+1 > maxConfigDepth {
		collector.caps.deep = true
		return false
	}
	return true
}

// tomlHeaderName reads the path of a "[table]" or "[[array.of.tables]]" line:
// the text between the brackets, with each segment's quotes dropped. ok is false
// for a line that does not close its brackets (a broken header is no section).
func tomlHeaderName(text, open, close string) (string, bool) {
	if !strings.HasSuffix(text, close) {
		return "", false
	}
	inner := strings.TrimSpace(text[len(open) : len(text)-len(close)])
	if inner == "" {
		return "", false
	}
	segments := strings.Split(inner, ".")
	for i, segment := range segments {
		segments[i] = configKeyName(segment)
	}
	return strings.Join(segments, "."), true
}

// tomlAssignment splits a "key = value" line into its key path (dotted and
// possibly quoted, as TOML writes it) and the value text behind the "=". ok is
// false for a continuation line of a multi-line array or a line that does not
// look like an assignment at all: the text before the first "=" outside quotes
// must be a key, which keeps "x" in ["a=b"] from reading as one.
func tomlAssignment(text string) (key, value string, ok bool) {
	eq := tomlAssignAt(text)
	if eq <= 0 {
		return "", "", false
	}
	raw := strings.TrimSpace(text[:eq])
	if raw == "" || !tomlKeyText(raw) {
		return "", "", false
	}
	segments := strings.Split(raw, ".")
	for i, segment := range segments {
		segments[i] = configKeyName(segment)
	}
	return strings.Join(segments, "."), strings.TrimSpace(text[eq+1:]), true
}

// tomlAssignAt is the index of the "=" that separates a key from its value: the
// first one outside a quoted string.
func tomlAssignAt(text string) int {
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '=':
			return i
		case c == '#' || c == '{' || c == '[':
			// A comment, or a value that already started: no assignment here.
			return -1
		}
	}
	return -1
}

// tomlKeyText reports whether the text before an "=" can be a TOML key: a bare
// or quoted dotted key (a.b, "a.b", 'a.b'), never the tail of a value (an "="
// inside a string is skipped before this, and a value's brace or bracket ends
// the search).
func tomlKeyText(raw string) bool {
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ' ' || r == '\t' || r == '"' || r == '\'':
		case r >= 0x80: // a quoted key may hold any character
		default:
			return false
		}
	}
	return strings.TrimSpace(raw) != ""
}
