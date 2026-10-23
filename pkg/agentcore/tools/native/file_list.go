package native

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

const (
	defaultListEntries = 500
	maxListDepth       = 5
)

// FileListTool lists files in a workspace directory, optionally recursively.
type FileListTool struct {
	workspace string
	readRoots []string // read-only directories outside the workspace
	addDirs   *AddDirs // the session's added directories, read at call time
	maxOut    int      // raw result cap in bytes; 0 = DefaultRawOutputBytes
}

func (t *FileListTool) Name() string { return "file_list" }

// Description skips the "the result says which" parenthetical: the no_ignore
// parameter and the result's own note carry it (docs/research/
// 2026-10-23-fixed-prefix-tools-report.md 三 #22).
func (t *FileListTool) Description() string {
	return "List a workspace directory. Directories end with /, symlinks with @ (not followed). " +
		"depth>1 lists recursively as relative paths; .git, node_modules, vendor, dist and .gitignore'd directories are " +
		"shown but not entered."
}

// ToolMeta marks the tool as a read-only read.
func (*FileListTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryRead, ReadOnly: true, Prefetchable: true}
}
func (t *FileListTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir": map[string]any{
					"type":        "string",
					"description": "Relative directory path (empty = workspace root), or an absolute one under a read-only dependency root",
				},
				"depth": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Levels to list (default 1, max %d)", maxListDepth),
				},
				"max_entries": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum entries to return (default %d)", defaultListEntries),
				},
				"no_ignore": map[string]any{
					"type":        "boolean",
					"description": "Also enter node_modules, vendor, dist and .gitignore'd directories (.git stays out)",
				},
			},
		},
	}
}

func (t *FileListTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	dirArg, _ := call.Arguments["dir"].(string)
	if dirArg == "" {
		dirArg = "."
	}
	target, err := resolveRead(t.workspace, withAddDirs(t.readRoots, t.addDirs), dirArg)
	if err != nil {
		return nil, err
	}
	depth, maxEntries := 1, defaultListEntries
	if n, ok := numberArg(call.Arguments["depth"]); ok && n >= 1 {
		depth = min(int(n), maxListDepth)
	}
	if n, ok := numberArg(call.Arguments["max_entries"]); ok && n >= 1 {
		maxEntries = int(n)
	}

	noIgnore, _ := call.Arguments["no_ignore"].(bool)
	list, err := withRoot(target.base, target.abs, func(root *os.Root, rel string) (listing, error) {
		var out listing
		ign := newIgnoreMatcher(root, noIgnore)
		err := listDir(root, ign, rel, "", depth, maxEntries, &out)
		if depth > 1 {
			out.note = ign.note() // depth 1 enters nothing, so there is nothing to report
		}
		return out, err
	})
	if err != nil {
		return nil, fmt.Errorf("file_list: %w", err)
	}
	content := strings.Join(list.entries, "\n")
	if list.truncated {
		content += fmt.Sprintf("\n[truncated at %d entries; narrow dir or depth]", maxEntries)
	}
	if list.note != "" {
		content += "\n" + list.note
	}
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    t.Name(),
		Content: TruncateHeadTail(content, rawLimit(t.maxOut)),
	}, nil
}

type listing struct {
	entries   []string
	truncated bool
	note      string // what was not entered as ignored
}

// listDir appends the entries of dir (relative to root) to out, prefixed by
// prefix, descending while depth > 1 into directories ign does not skip.
// Symlinks are never followed and os.Root keeps every open inside the root.
func listDir(root *os.Root, ign *ignoreMatcher, dir, prefix string, depth, maxEntries int, out *listing) error {
	d, err := root.Open(dir)
	if err != nil {
		return err
	}
	entries, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if len(out.entries) >= maxEntries {
			out.truncated = true
			return nil
		}
		name := path.Join(prefix, e.Name())
		switch {
		case e.Type()&os.ModeSymlink != 0:
			out.entries = append(out.entries, name+"@")
		case e.IsDir():
			out.entries = append(out.entries, name+"/")
			child := filepath.Join(dir, e.Name())
			if depth > 1 && !ign.skipDir(filepath.ToSlash(child), e.Name()) {
				if err := listDir(root, ign, child, name, depth-1, maxEntries, out); err != nil {
					return err
				}
			}
		default:
			out.entries = append(out.entries, name)
		}
	}
	return nil
}
