package native

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// FileEditTool replaces exact strings in a workspace file (or a file under a
// directory the session added as read-write): one replacement (old_string/new_string) or several in one call (edits).
type FileEditTool struct {
	workspace string
	addDirs   *AddDirs
	snapshots Snapshotter
}

func (t *FileEditTool) Name() string { return "file_edit" }

// Description skips the "prefer this over file_write" sentence: the stable
// system prompt already says it (docs/research/
// 2026-10-23-fixed-prefix-tools-report.md 三 #20).
func (t *FileEditTool) Description() string {
	return "Edit a workspace file by replacing an exact string. Read the file first: old_string must match the " +
		"file text exactly (without file_read's line-number prefix) and be unique unless replace_all is set; " +
		"include surrounding lines to make it unique. For several changes to the same file pass them all in " +
		"edits (one call, applied in order, all or nothing) instead of old_string/new_string. " +
		"The file's line endings (LF/CRLF) are kept."
}

// ToolMeta marks the tool as an edit: it changes workspace files.
func (*FileEditTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryEdit, ReadOnly: false}
}
func (t *FileEditTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "Path within the workspace (relative, or absolute inside it), or an absolute path under a directory added as read-write"},
				"old_string":  map[string]any{"type": "string", "description": "Exact text to replace"},
				"new_string":  map[string]any{"type": "string", "description": "Replacement text"},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence (default false)"},
				"edits": map[string]any{
					"type":        "array",
					"description": "Several replacements, each applied to the result of the previous one; if any fails the file is left unchanged. Use instead of old_string/new_string",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string":  map[string]any{"type": "string"},
							"new_string":  map[string]any{"type": "string"},
							"replace_all": map[string]any{"type": "boolean"},
						},
						"required": []any{"old_string", "new_string"},
					},
				},
			},
			"required": []any{"path"},
		},
	}
}

func (t *FileEditTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	target, err := writeTargetPath(t.workspace, t.addDirs, call.Arguments)
	if err != nil {
		return nil, err
	}
	path := target.abs
	data, err := readFileInRoot(target.base, path)
	if err != nil {
		return nil, fmt.Errorf("file_edit: %w", err)
	}
	updated, spans, err := applyEditSpans(string(data), call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("file_edit %s: %w", path, err)
	}

	var snapshotReq SnapshotRequest
	var beforeResult SnapshotBefore
	if t.snapshots != nil {
		snapshotReq, err = editSnapshotRequest(target, call.CallID, data)
		if err != nil {
			ReportSnapshot(ctx, SnapshotReport{Kind: SnapshotKindFile, Status: SnapshotStatusIncomplete,
				Bytes: int64(len(data))})
			return nil, fmt.Errorf("file_edit: %w", err)
		}
		beforeResult, err = t.snapshots.BeforeCall(ctx, snapshotReq)
		if err != nil {
			ReportSnapshot(ctx, SnapshotReport{Kind: snapshotReq.Kind, Status: SnapshotStatusIncomplete,
				Bytes: int64(len(data)), Unrecoverable: beforeResult.Unrecoverable})
			return nil, fmt.Errorf("file_edit: snapshot before: %w", err)
		}
	}

	if err := writeFileInRoot(target.base, path, []byte(updated)); err != nil {
		if t.snapshots != nil {
			incomplete := editSnapshotResult(call, snapshotReq, beforeResult, data, "", SnapshotStatusIncomplete)
			if snapshotErr := t.snapshots.AfterCall(ctx, incomplete); snapshotErr != nil {
				ReportSnapshot(ctx, SnapshotReport{Kind: incomplete.Kind, Status: SnapshotStatusIncomplete,
					Bytes: incomplete.Size, Unrecoverable: incomplete.Unrecoverable})
				return nil, fmt.Errorf("file_edit: %w; snapshot incomplete: %v", err, snapshotErr)
			}
			ReportSnapshot(ctx, SnapshotReport{Kind: incomplete.Kind, Status: incomplete.Status,
				Bytes: incomplete.Size, Unrecoverable: incomplete.Unrecoverable})
		}
		return nil, fmt.Errorf("file_edit: %w", err)
	}
	if t.snapshots != nil {
		complete := editSnapshotResult(call, snapshotReq, beforeResult, data, updated, SnapshotStatusComplete)
		if err := t.snapshots.AfterCall(ctx, complete); err != nil {
			ReportSnapshot(ctx, SnapshotReport{Kind: complete.Kind, Status: SnapshotStatusIncomplete,
				Bytes: complete.Size, Unrecoverable: complete.Unrecoverable})
			return nil, fmt.Errorf("file_edit: snapshot after failed after writing %s: %w", target.abs, err)
		}
		ReportSnapshot(ctx, SnapshotReport{Kind: complete.Kind, Status: complete.Status,
			Bytes: complete.Size, Unrecoverable: complete.Unrecoverable})
	}
	// The edited lines are the verification: the model sees the result and
	// needs no re-read (suag-jz0l review; omp, pigo and Cline do the same).
	out := fmt.Sprintf("edited %s: %d replacement(s)", path, len(spans))
	if notice := snapshotNotice(beforeResult); notice != "" {
		out += "\n" + notice
	}
	if warn := syntaxRegression(path, string(data), updated); warn != "" {
		out += "\n" + warn
	}
	if snippet := editSnippet(updated, spans); snippet != "" {
		out += "\n" + snippet
	}
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: out}, nil
}

// editSnapshotRequest reuses file_edit's already-read bytes. A second read here
// would make the snapshot race with an approval hook and violate edit atomicity.
func editSnapshotRequest(target readTarget, callID string, before []byte) (SnapshotRequest, error) {
	rel, err := relInWorkspace(target.base, target.abs)
	if err != nil {
		return SnapshotRequest{}, err
	}
	realRel, err := realRelInRoot(target.base, target.abs)
	if err != nil {
		return SnapshotRequest{}, err
	}
	return SnapshotRequest{
		CallID:  callID,
		Tool:    "file_edit",
		Root:    target.base,
		Rel:     filepath.ToSlash(rel),
		RealRel: filepath.ToSlash(realRel),
		Kind:    SnapshotKindFile,
		Before:  before,
	}, nil
}

func editSnapshotResult(call api.ToolCall, req SnapshotRequest, before SnapshotBefore, original []byte, updated, status string) SnapshotResult {
	result := SnapshotResult{
		CallID:        call.CallID,
		Tool:          "file_edit",
		Root:          req.Root,
		Rel:           req.Rel,
		RealRel:       req.RealRel,
		Kind:          req.Kind,
		Size:          int64(len(original)),
		Status:        status,
		Unrecoverable: before.Unrecoverable,
	}
	if status == SnapshotStatusComplete {
		result.AfterSHA256 = sha256Hex([]byte(updated))
	}
	return result
}

// ApplyEdit applies file_edit's arguments (old_string, new_string,
// replace_all, or the edits list) to content and returns the new content and
// the number of replacements. Edits apply in order to the previous result and
// any failure aborts the whole call. When the file uses CRLF and the strings
// use bare LF, the strings are converted so the file keeps CRLF. Exported so
// ACP can preview the exact result as a diff.
func ApplyEdit(content string, args map[string]any) (string, int, error) {
	updated, spans, err := applyEditSpans(content, args)
	return updated, len(spans), err
}

// applyEditSpans is ApplyEdit that also returns where every replacement
// ended up in the final content (byte spans of the inserted text), so the
// result can show the edited lines.
func applyEditSpans(content string, args map[string]any) (string, []span, error) {
	edits, err := EditList(args)
	if err != nil {
		return "", nil, err
	}
	var spans []span
	for i, edit := range edits {
		var placed []replacement
		content, placed, err = applyOne(content, edit)
		if err != nil {
			if len(edits) > 1 {
				err = fmt.Errorf("edits[%d]: %w (no edit was applied)", i, err)
			}
			return "", nil, err
		}
		spans = shiftSpans(spans, placed)
		for _, r := range placed {
			spans = append(spans, span{r.newStart, r.newStart + r.newLen})
		}
	}
	return content, spans, nil
}

// span is a byte range [start, end) of the edited content.
type span struct{ start, end int }

// replacement is one replaced occurrence: where it was in the content before
// the edit (oldStart, oldLen) and where its new text is after it.
type replacement struct{ oldStart, oldLen, newStart, newLen int }

// shiftSpans maps spans recorded by earlier edits through the replacements
// of a later edit (placed, in increasing order).
func shiftSpans(spans []span, placed []replacement) []span {
	for i, sp := range spans {
		spans[i] = span{mapPos(sp.start, placed, false), mapPos(sp.end, placed, true)}
	}
	return spans
}

// mapPos maps a byte position of the content before an edit to the content
// after it. A position inside a replaced range moves to the start of the new
// text, or to its end when isEnd is set, so a span stays over the new text.
func mapPos(pos int, placed []replacement, isEnd bool) int {
	delta := 0
	for _, r := range placed {
		oldEnd := r.oldStart + r.oldLen
		switch {
		case oldEnd <= pos:
			delta += r.newLen - r.oldLen
		case r.oldStart >= pos:
			return pos + delta
		default: // inside the replaced range
			if isEnd {
				return r.newStart + r.newLen
			}
			return r.newStart
		}
	}
	return pos + delta
}

// EditList returns file_edit's replacements as a list: the edits argument,
// or the single old_string/new_string pair. edits may arrive as a JSON string
// (some models encode nested arrays that way); mixing both forms is an error.
// An empty value of the form not used counts as absent: models with strict
// tool schemas fill every optional field, sending edits: [] next to an
// old_string/new_string pair, or old_string: "" next to edits.
func EditList(args map[string]any) ([]map[string]any, error) {
	raw, ok := args["edits"]
	if !ok || emptyEdits(raw) {
		return []map[string]any{args}, nil
	}
	if oldStr, single := args["old_string"]; single && oldStr != nil && oldStr != "" {
		return nil, fmt.Errorf("pass either edits or old_string/new_string, not both")
	}
	if text, isText := raw.(string); isText {
		if err := json.Unmarshal([]byte(text), &raw); err != nil {
			return nil, fmt.Errorf("edits must be an array of {old_string, new_string, replace_all}: %w", err)
		}
	}
	items, _ := raw.([]any)
	if len(items) == 0 {
		return nil, fmt.Errorf("edits must be a non-empty array of {old_string, new_string, replace_all}")
	}
	edits := make([]map[string]any, len(items))
	for i, item := range items {
		edit, isObj := item.(map[string]any)
		if !isObj {
			return nil, fmt.Errorf("edits[%d] must be an object with old_string and new_string", i)
		}
		edits[i] = edit
	}
	return edits, nil
}

// emptyEdits reports whether the edits argument carries no replacement: null,
// an empty array, or an empty / "[]" JSON string.
func emptyEdits(raw any) bool {
	switch v := raw.(type) {
	case nil:
		return true
	case []any:
		return len(v) == 0
	case string:
		text := strings.TrimSpace(v)
		return text == "" || text == "[]" || text == "null"
	}
	return false
}

// applyOne applies a single replacement (old_string, new_string,
// replace_all) and reports where each replaced occurrence went.
func applyOne(content string, args map[string]any) (string, []replacement, error) {
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	replaceAll, _ := args["replace_all"].(bool)
	if oldStr == "" {
		return "", nil, fmt.Errorf("old_string is required (use file_write to create a file)")
	}
	if oldStr == newStr {
		return "", nil, fmt.Errorf("old_string and new_string are identical")
	}
	if strings.Contains(content, "\r\n") && !strings.Contains(oldStr, "\r\n") {
		oldStr = strings.ReplaceAll(oldStr, "\n", "\r\n")
		newStr = strings.ReplaceAll(strings.ReplaceAll(newStr, "\r\n", "\n"), "\n", "\r\n")
	}
	n := strings.Count(content, oldStr)
	switch {
	case n == 0:
		return "", nil, fmt.Errorf("old_string not found%s", closestMatchHint(content, oldStr))
	case n > 1 && !replaceAll:
		return "", nil, fmt.Errorf("old_string matches %d places (%s); add surrounding lines to make it unique or set replace_all",
			n, matchLines(content, oldStr, 10))
	}
	limit := 1
	if replaceAll {
		limit = n
	}
	var b strings.Builder
	var placed []replacement
	rest, offset := content, 0
	for i := 0; i < limit; i++ {
		idx := strings.Index(rest, oldStr)
		b.WriteString(rest[:idx])
		placed = append(placed, replacement{oldStart: offset + idx, oldLen: len(oldStr), newStart: b.Len(), newLen: len(newStr)})
		b.WriteString(newStr)
		rest = rest[idx+len(oldStr):]
		offset += idx + len(oldStr)
	}
	b.WriteString(rest)
	return b.String(), placed, nil
}
