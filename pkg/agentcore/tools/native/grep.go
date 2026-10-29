package native

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

const (
	defaultGrepResults = 100
	maxGrepResults     = 1000
	// maxGrepLineBytes trims very long matching lines (minified files).
	maxGrepLineBytes = codeintel.MaxGrepLineBytes
)

// FileGrepTool searches file contents in the workspace with a regular
// expression, in pure Go (no external rg).
type FileGrepTool struct {
	workspace string
	readRoots []string // read-only directories outside the workspace
	addDirs   *AddDirs // the session's added directories, read at call time
	maxOut    int      // raw result cap in bytes; 0 = DefaultRawOutputBytes
}

func (t *FileGrepTool) Name() string { return "file_grep" }

// Description keeps the result shape and the skip defaults; the parameter
// restatement (glob/exclude/context/literal sentences) went, each parameter's
// own description is the one place that text lives (docs/research/
// 2026-10-23-fixed-prefix-tools-report.md 三 #12-#13).
func (t *FileGrepTool) Description() string {
	return "Search file contents in the workspace with a Go (RE2) regular expression. " +
		"Returns path:line: text (paths relative to the workspace, / separated), or only paths with output=files. " +
		"Skips .git, node_modules, vendor, dist, .gitignore matches, binary files and files over 1MB. " +
		"Prefer this over shell grep."
}

// ToolMeta marks the tool as a read-only search.
func (*FileGrepTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategorySearch, ReadOnly: true, Prefetchable: true}
}
func (t *FileGrepTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string", "description": "RE2 regular expression; lookaround/backreferences may use a backtracking fallback"},
				"path":        map[string]any{"type": "string", "description": "File or directory to search (default: workspace root); may be absolute under a read-only dependency root"},
				"glob":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Only matching files; accepts a string for compatibility or a list with ! exclusions, e.g. [\"*.go\", \"!*_test.go\"]"},
				"exclude":     map[string]any{"type": "string", "description": "Leave out matching lines (RE2 regexp, like grep -v); a pattern that is not a valid regexp but a file wildcard such as *_test.go leaves those files out instead (same as glob: [\"!*_test.go\"])"},
				"context":     map[string]any{"type": "integer", "minimum": 0, "maximum": 5, "description": "Include this many lines before and after each match"},
				"literal":     map[string]any{"type": "boolean", "description": "Match pattern as plain text"},
				"ignore_case": map[string]any{"type": "boolean", "description": "Case-insensitive match"},
				"output":      map[string]any{"type": "string", "enum": []any{"content", "files"}, "description": "content (default): matching lines; files: matching file paths"},
				"max_results": map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum lines or files (default %d, max %d)", defaultGrepResults, maxGrepResults)},
				"no_ignore":   map[string]any{"type": "boolean", "description": "Also search node_modules, vendor, dist and .gitignore'd paths (.git stays skipped)"},
			},
			"required": []any{"pattern"},
		},
	}
}

func (t *FileGrepTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	args := call.Arguments
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("file_grep: pattern is required")
	}
	ignoreCase, _ := args["ignore_case"].(bool)
	literal, _ := args["literal"].(bool)
	re, fallback, err := compileGrepPattern(pattern, ignoreCase, literal)
	if err != nil {
		return nil, err
	}
	glob := parseGrepGlobs(args["glob"])
	var exclude grepMatcher
	excludeFallback := false
	excludeAsGlob := ""
	if raw, ok := args["exclude"].(string); ok && raw != "" {
		if exclude, excludeFallback, err = compileGrepPattern(raw, ignoreCase, false); err != nil {
			// A value RE2 rejects but that reads as a file wildcard is the common
			// intent ("*_test.go" means leave the test files out). Only a value that
			// does not compile gets here, so a line filter always keeps its meaning.
			if fileGlob, isGlob := grepExcludeGlob(raw); isGlob {
				glob.exclude = append(glob.exclude, fileGlob)
				excludeAsGlob, exclude, err = raw, nil, nil
			} else {
				return nil, fmt.Errorf("file_grep exclude: %w; %s", err, grepExcludeHint)
			}
		}
	}
	start, err := searchStart(t.workspace, withAddDirs(t.readRoots, t.addDirs), args)
	if err != nil {
		return nil, err
	}
	filesOnly := args["output"] == "files"
	limit := defaultGrepResults
	if n, ok := numberArg(args["max_results"]); ok && n >= 1 {
		limit = min(int(n), maxGrepResults)
	}
	contextLines := 0
	if n, ok := numberArg(args["context"]); ok {
		contextLines = min(max(int(n), 0), 5)
	}

	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	var out []string
	hitCount := 0
	full := false
	regexp2TimedOut := false
	noIgnore, _ := args["no_ignore"].(bool)
	walk, err := walkWorkspace(ctx, start.base, start.abs, noIgnore, func(root *os.Root, rel string, d fs.DirEntry) error {
		if !glob.match(start, rel) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxSearchFileBytes {
			return nil
		}
		matches, hits, timedOut, err := grepFile(root, rel, start.show(rel), re, exclude, filesOnly, contextLines, limit-hitCount)
		if timedOut {
			regexp2TimedOut = true
		}
		if err != nil {
			return nil // unreadable file: skip
		}
		out = append(out, matches...)
		hitCount += hits
		if hitCount >= limit {
			full = true
			return errSearchFull
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("file_grep: %w", err)
	}
	content := strings.Join(out, "\n")
	if hitCount == 0 {
		content = "no matches"
	}
	if full {
		content += fmt.Sprintf("\n[stopped at %d results; narrow path/glob or raise max_results]", limit)
	}
	if walk.timedOut {
		content += fmt.Sprintf("\n[已超时（%s），结果不完整]", searchTimeout)
	}
	if walk.ignoreNote != "" {
		content += "\n" + walk.ignoreNote
	}
	if excludeAsGlob != "" {
		content += fmt.Sprintf("\n[exclude %s does not compile as a regexp, so it was read as a file wildcard: those files are skipped (same as glob: [\"!%s\"])]",
			excludeAsGlob, excludeAsGlob)
	}
	if fallback || excludeFallback {
		content += "\n[used regexp2 (lookaround/backreference) because RE2 rejected the pattern]"
	}
	if regexp2TimedOut {
		content += "\n[regexp2 match timed out; results may be incomplete]"
	}
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: TruncateHeadTail(content, rawLimit(t.maxOut))}, nil
}

type grepLine struct {
	text string
	hit  bool
	line int
}

// grepFile returns matching lines, optionally expanded with merged context
// blocks. Context lines never consume the caller's result limit.
func grepFile(root *os.Root, rel, name string, re, exclude grepMatcher, filesOnly bool, contextLines, limit int) ([]string, int, bool, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64<<10)
	head, _ := br.Peek(binarySniffBytes)
	if isBinaryHead(head) {
		return nil, 0, false, nil
	}
	var lines []grepLine
	hitCount := 0
	for lineNo := 1; ; lineNo++ {
		line, readErr := br.ReadString('\n')
		if line == "" && readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, 0, false, readErr
		}
		line = strings.TrimRight(line, "\r\n")
		matched, matchErr := re.MatchString(line)
		if matchErr != nil {
			return nil, 0, true, matchErr
		}
		if matched && exclude != nil {
			matched, matchErr = exclude.MatchString(line)
			if matchErr != nil {
				return nil, 0, true, matchErr
			}
			matched = !matched
		}
		if matched && hitCount < limit {
			hitCount++
			lines = append(lines, grepLine{text: line, hit: true, line: lineNo})
		} else {
			lines = append(lines, grepLine{text: line, line: lineNo})
		}
		if readErr == io.EOF {
			break
		}
	}
	hits := hitCount
	if hits == 0 {
		return nil, 0, false, nil
	}
	if filesOnly {
		return []string{name}, 1, false, nil
	}
	if contextLines == 0 {
		out := make([]string, 0, hits)
		for _, line := range lines {
			if line.hit {
				out = append(out, formatGrepLine(name, line, true))
			}
		}
		return out, hits, false, nil
	}
	return formatGrepContext(name, lines, contextLines), hits, false, nil
}

func formatGrepLine(name string, line grepLine, hit bool) string {
	text := line.text
	if len(text) > maxGrepLineBytes {
		text = text[:utf8Cut(text, maxGrepLineBytes)] + "…"
	}
	if hit {
		return fmt.Sprintf("%s:%d: %s", name, line.line, text)
	}
	return fmt.Sprintf("%s-%d- %s", name, line.line, text)
}

func formatGrepContext(name string, lines []grepLine, radius int) []string {
	var blocks [][2]int
	for i, line := range lines {
		if !line.hit {
			continue
		}
		start, end := max(0, i-radius), min(len(lines)-1, i+radius)
		if len(blocks) > 0 && start <= blocks[len(blocks)-1][1]+1 {
			blocks[len(blocks)-1][1] = max(blocks[len(blocks)-1][1], end)
		} else {
			blocks = append(blocks, [2]int{start, end})
		}
	}
	var out []string
	for blockIndex, block := range blocks {
		if blockIndex > 0 {
			out = append(out, "--")
		}
		for i := block[0]; i <= block[1]; i++ {
			out = append(out, formatGrepLine(name, lines[i], lines[i].hit))
		}
	}
	return out
}

// searchStart resolves the optional path argument (default: the workspace),
// which may also be an absolute path under one of the read roots.
func searchStart(workspace string, roots []string, args map[string]any) (readTarget, error) {
	p, _ := args["path"].(string)
	if p == "" {
		p = "."
	}
	return resolveRead(workspace, roots, p)
}
