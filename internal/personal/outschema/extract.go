package outschema

import (
	"strings"
)

// Sources of a checked JSON value; they are reported to the caller and in the
// trace event, so the caller never has to guess (design 3.3).
const (
	// SourceWhole: the whole answer is the JSON value.
	SourceWhole = "whole"
	// SourceFenced: a fenced code block (```json or unlabelled) holds it.
	SourceFenced = "fenced"
	// SourceSubstring: a balanced {...}/[...] inside the prose holds it.
	SourceSubstring = "substring"
	// SourceNone: the answer holds no JSON value (reported as no_json).
	SourceNone = "no_json"
)

// candidate is one JSON text found in an answer, with where it came from.
type candidate struct {
	Source string
	Raw    string
}

// extract lists the JSON candidates of answer in the order they are checked
// (design 3.3), from the most to the least literal reading of the answer:
//
//  1. the whole answer, when it parses as JSON on its own;
//  2. fenced code blocks, ```json ones first, then unlabelled ones, each from
//     the last block to the first;
//  3. balanced {...}/[...] pieces of the prose, from the last to the first.
//
// The order is fixed and explainable: the same answer always yields the same
// result, and a model that ends with its final block has that block tried
// first.
func extract(answer string) []candidate {
	trimmed := strings.TrimSpace(answer)
	var out []candidate
	if trimmed == "" {
		return nil
	}
	if isJSONValue(trimmed) {
		out = append(out, candidate{Source: SourceWhole, Raw: trimmed})
	}
	blocks := fencedBlocks(answer)
	for _, wantJSON := range []bool{true, false} {
		for i := len(blocks) - 1; i >= 0; i-- {
			if blocks[i].json == wantJSON {
				out = append(out, candidate{Source: SourceFenced, Raw: blocks[i].body})
			}
		}
	}
	pieces := substrings(answer)
	for i := len(pieces) - 1; i >= 0; i-- {
		out = append(out, candidate{Source: SourceSubstring, Raw: pieces[i]})
	}
	return out
}

// isJSONValue reports whether text is one complete JSON value.
func isJSONValue(text string) bool {
	if text == "" {
		return false
	}
	switch text[0] {
	case '{', '[', '"', 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		_, err := decodeInstance(text)
		return err == nil
	}
	return false
}

// fenceBlock is one ``` fenced block: its body and whether the info string
// asked for JSON.
type fenceBlock struct {
	json bool
	body string
}

// fencedBlocks collects the fenced code blocks of answer. An unterminated last
// block still counts (a cut-off answer often loses its closing fence).
func fencedBlocks(answer string) []fenceBlock {
	var blocks []fenceBlock
	lines := strings.Split(answer, "\n")
	var body []string
	var lang string
	inBlock := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if !inBlock {
				lang = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "```")))
				inBlock, body = true, body[:0]
				continue
			}
			blocks = append(blocks, fenceBlock{json: strings.HasPrefix(lang, "json"), body: strings.TrimSpace(strings.Join(body, "\n"))})
			inBlock = false
			continue
		}
		if inBlock {
			body = append(body, line)
		}
	}
	if inBlock {
		blocks = append(blocks, fenceBlock{json: strings.HasPrefix(lang, "json"), body: strings.TrimSpace(strings.Join(body, "\n"))})
	}
	return blocks
}

// substrings returns the balanced {...}/[...] pieces of text in the order they
// appear. Braces inside string literals are skipped, so a JSON value quoted in
// prose does not split a candidate, and a mismatched closer restarts the scan
// instead of producing a broken piece.
func substrings(text string) []string {
	var out []string
	var stack []byte
	start := -1
	inString, escaped := false, false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			// A quote only opens a string inside a candidate; outside one it is prose.
			if len(stack) > 0 {
				inString = true
			}
		case '{', '[':
			if len(stack) == 0 {
				start = i
			}
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) == 0 {
				continue
			}
			open := stack[len(stack)-1]
			if (open == '{' && ch != '}') || (open == '[' && ch != ']') {
				stack, start = stack[:0], -1
				continue
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 && start >= 0 {
				out = append(out, strings.TrimSpace(text[start:i+1]))
				start = -1
			}
		}
	}
	return out
}
