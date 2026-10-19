package native

import (
	"fmt"
	"strings"
)

// The configuration formats (YAML and TOML) are listed by their keys: a
// configuration file's declarations are its keys, and the dotted key path
// (server.http.port) is the name a "where is this declared" question looks for.
// The two extractors (symbols_yaml.go, symbols_toml.go) share the scale limits,
// the signature rendering and the collector here: a config file is often a CI
// config or a k8s manifest with hundreds of keys, and an answer that lists all
// of them buries the key the model asked about.

const (
	// maxConfigKeys caps the keys one configuration file lists. A file with more
	// is answered with its first keys and a note naming file_grep: the model
	// narrows the key instead of reading a k8s manifest whole.
	maxConfigKeys = 300
	// maxConfigDepth caps the key depth one configuration file lists (a YAML
	// mapping level, or a TOML path segment). Deeper keys are counted and the
	// note says so.
	maxConfigDepth = 4
	// configPreviewBytes caps the value preview of one key's signature.
	configPreviewBytes = 60
)

// The two signatures a key whose value holds more keys gets: a mapping is not
// printed out (its own keys are listed under it by their path), and a sequence
// is only marked as one.
const (
	configMappingPreview = "{…}"
	configArrayPreview   = "[…]"
)

// configKeyPreview is the signature of one configuration key: the value as
// written for a scalar, cut to configPreviewBytes. It is always one line, even
// when the value was not (a YAML "key: |" block, an unclosed array).
func configKeyPreview(value string) string {
	text := strings.TrimSpace(value)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		// A block value: its first line and the mark that more follows.
		text = strings.TrimSpace(text[:i]) + "…"
	}
	if len(text) > configPreviewBytes {
		text = text[:utf8Cut(text, configPreviewBytes)] + "…"
	}
	return text
}

// configPathJoin appends one path segment: the segment of a mapping key
// ("server") or of a plain TOML key. A sequence segment is appended by
// configPathSeq, which adds no dot.
func configPathJoin(prefix, segment string) string {
	switch {
	case prefix == "":
		return segment
	case segment == "":
		return prefix
	default:
		return prefix + "." + segment
	}
}

// configPathSeq marks a key's path as one whose values repeat: the entries of a
// YAML sequence of mappings or of a TOML array of tables share the path segment
// "[]", so their keys come out as one path, "services[].name".
func configPathSeq(path string) string { return path + "[]" }

// configKeyName is one YAML/TOML key as a path spells it: surrounding quotes are
// dropped (a "quoted.key" is the key quoted.key) and the inner text is kept as
// written.
func configKeyName(raw string) string {
	name := strings.TrimSpace(raw)
	if len(name) >= 2 {
		if q := name[0]; (q == '"' || q == '\'') && name[len(name)-1] == q {
			return strings.TrimSpace(name[1 : len(name)-1])
		}
	}
	return name
}

// configCaps is how far one config file's listing was cut short, and how many of
// its key paths repeat.
type configCaps struct {
	deep      bool // a key deeper than maxConfigDepth was left out
	truncated bool // a key past maxConfigKeys was left out
	repeats   int  // key paths that a sequence entry or an array of tables declares again
}

// configNotes is what a configuration listing tells the model besides its keys:
// what the caps left out, and what a repeat count in a signature means. What the
// tool can say by itself (the approx marker, the count of a directory walk) is
// not repeated here.
func configNotes(format string, caps configCaps) []string {
	var notes []string
	if caps.repeats > 0 {
		notes = append(notes, fmt.Sprintf("[%s: %d key path(s) repeat - the entries of a sequence (or an array of "+
			"tables) share one path; each path is listed once, at its first line, with its count (xN) in the signature]",
			format, caps.repeats))
	}
	if caps.deep {
		notes = append(notes, fmt.Sprintf("[%s: only the first %d key levels are listed; for deeper keys use file_grep]",
			format, maxConfigDepth))
	}
	if caps.truncated {
		notes = append(notes, fmt.Sprintf("[%s: showing the first %d keys; for the rest use file_grep]",
			format, maxConfigKeys))
	}
	return notes
}

// configKeyEntry is one key path of a configuration listing: the symbol as it is
// first seen and how often the file declares that path.
type configKeyEntry struct {
	symbol Symbol
	count  int
}

// configCollector gathers one configuration file's key paths in the order they
// are found. A path the file declares again keeps its first line and counts up:
// a k8s manifest's "spec.template.spec.containers[]" is one line with its count,
// not fifty.
type configCollector struct {
	file    string
	order   []string
	entries map[string]*configKeyEntry
	caps    configCaps
}

func newConfigCollector(file string) *configCollector {
	return &configCollector{file: file, entries: map[string]*configKeyEntry{}}
}

// add records one key. kind is SymbolSection for a key whose value holds more
// keys and SymbolKey otherwise; signature is the value preview the caller built.
func (c *configCollector) add(path string, line int, kind, signature string) {
	if e, ok := c.entries[path]; ok {
		if e.count == 1 {
			c.caps.repeats++
		}
		e.count++
		return
	}
	if len(c.order) >= maxConfigKeys {
		c.caps.truncated = true
		return
	}
	c.entries[path] = &configKeyEntry{
		symbol: Symbol{File: c.file, Line: line, Kind: kind, Name: path, Signature: signature},
		count:  1,
	}
	c.order = append(c.order, path)
}

// symbols returns the collected keys in the order they were found, with the
// repeat count of a path appended to its signature. The caller sorts by line.
func (c *configCollector) symbols() []Symbol {
	out := make([]Symbol, 0, len(c.order))
	for _, path := range c.order {
		e := c.entries[path]
		s := e.symbol
		if e.count > 1 {
			s.Signature = strings.TrimSpace(fmt.Sprintf("%s ×%d", s.Signature, e.count))
		}
		out = append(out, s)
	}
	return out
}
