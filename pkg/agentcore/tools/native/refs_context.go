package native

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The context lines of one file_refs result (design
// docs/design/2026-10-19-batch-tool-calls-design.md, 0.2): a call site is often
// only readable with the lines around it, and requesting them here saves the
// file_read the model would otherwise issue per match. The format is the one
// file_grep's context uses: a match keeps "path:line class text", a context line
// is "path-line- text" and "--" separates the blocks of a file.

// maxRefContext caps the context radius, like file_grep's.
const maxRefContext = 5

// refSourceCache reads each file of one result once for its context lines. The
// lines are the raw source: the masked line the approximate matcher sees is not
// readable and never shown.
type refSourceCache struct {
	target readTarget
	lines  map[string][]string // by the file name the result shows
}

func newRefSourceCache(target readTarget) *refSourceCache {
	return &refSourceCache{target: target, lines: make(map[string][]string)}
}

// linesOf returns the raw lines of file, 1-based by index-1, reading the file
// once; a file that cannot be read again yields nil, and its matches are shown
// without context instead of failing the whole result.
func (c *refSourceCache) linesOf(file string) []string {
	if lines, ok := c.lines[file]; ok {
		return lines
	}
	lines := c.read(file)
	c.lines[file] = lines
	return lines
}

// read opens file through os.Root, so the re-read stays inside the workspace or
// the read root the search ran under.
func (c *refSourceCache) read(file string) []string {
	abs := file
	if !c.target.outside {
		abs = filepath.Join(c.target.base, filepath.FromSlash(file))
	}
	data, err := withRoot(c.target.base, abs, func(root *os.Root, rel string) ([]byte, error) {
		return root.ReadFile(rel)
	})
	if err != nil {
		return nil
	}
	lines := newSourceLines(data).lines
	// A file ending in a newline has one empty element left over; it is not a
	// line and must not widen a block at the end of the file.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// refGroupLines renders one group of matches - the shown matches of a result, or
// its maybe-also group - in path and line order. radius 0 keeps the plain
// one-line-per-match output the tool always had; otherwise a block of source
// around each match is shown, adjacent or overlapping blocks are merged, and the
// context lines do not consume the caller's match limit (the caller has already
// applied it).
func refGroupLines(refs []Ref, radius int, src *refSourceCache) []string {
	if radius == 0 || src == nil {
		out := make([]string, 0, len(refs))
		for _, r := range refs {
			out = append(out, formatRefLine(r))
		}
		return out
	}
	var out []string
	for start := 0; start < len(refs); {
		end := start
		for end < len(refs) && refs[end].File == refs[start].File {
			end++
		}
		out = append(out, refFileContextLines(refs[start:end], radius, src)...)
		start = end
	}
	return out
}

// refFileContextLines formats the matches of one file with radius lines of
// source around each: blocks of close matches merge, and a file that is not
// readable any more falls back to the match lines alone.
func refFileContextLines(refs []Ref, radius int, src *refSourceCache) []string {
	name := refs[0].File
	source := src.linesOf(name)
	if len(source) == 0 {
		out := make([]string, 0, len(refs))
		for _, r := range refs {
			out = append(out, formatRefLine(r))
		}
		return out
	}
	hits := make(map[int]Ref, len(refs))
	for _, r := range refs {
		hits[r.Line] = r
	}
	// blocks holds the shown line spans, in order: the same merging file_grep
	// does with its context.
	var blocks [][2]int
	for _, r := range refs {
		start, end := max(1, r.Line-radius), min(len(source), r.Line+radius)
		end = max(end, r.Line) // a line the file no longer holds still shows its match
		if len(blocks) > 0 && start <= blocks[len(blocks)-1][1]+1 {
			blocks[len(blocks)-1][1] = max(blocks[len(blocks)-1][1], end)
			continue
		}
		blocks = append(blocks, [2]int{start, end})
	}
	var out []string
	for i, block := range blocks {
		if i > 0 {
			out = append(out, "--")
		}
		for line := block[0]; line <= block[1]; line++ {
			if r, ok := hits[line]; ok {
				out = append(out, formatRefLine(r))
				continue
			}
			if line < 1 || line > len(source) {
				continue
			}
			out = append(out, fmt.Sprintf("%s-%d- %s", name, line, refLineText(strings.TrimSpace(source[line-1]))))
		}
	}
	return out
}

// formatRefLine is one match line of the result.
func formatRefLine(r Ref) string {
	return fmt.Sprintf("%s:%d %s %s", r.File, r.Line, refClass(r), r.Text)
}
