package native

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

const (
	// RemoveMaxFiles is the most files one file_remove call may move to the
	// trash. Ignored directories (see FileRemoveTool) are neither counted nor
	// moved.
	RemoveMaxFiles = 1000
	// TrashRetention is how long removed paths are kept in the trash.
	TrashRetention = 7 * 24 * time.Hour
	// trashStampLayout prefixes every trash entry name (UTC removal time).
	trashStampLayout = "20060102-150405"
	// maxNotedPurged is how many permanently deleted directories a result names.
	maxNotedPurged = 5
)

// FileRemoveTool removes a file or directory inside the workspace, or under a
// directory the session added as read-write (--add-dir ...:rw). Regular
// content is moved into the trash (kept TrashRetention, then purged) so it can
// be restored; ignored directories (node_modules, vendor, dist and directories
// matched by the .gitignore files, e.g. tmp/) are regenerable and deleted for
// good without being walked, so large caches neither slow the call down nor
// fill the trash. Ignored single files (.env, local config) still go to the
// trash: they are often not regenerable. It refuses the workspace root, the
// workspace's own .git, the user's home directory and anything outside the
// workspace, never expands wildcards, needs recursive for a directory and
// moves at most RemoveMaxFiles files to the trash per call.
type FileRemoveTool struct {
	workspace string
	trashDir  string
	addDirs   *AddDirs
	snapshots Snapshotter
	now       func() time.Time
}

// NewFileRemoveTool builds file_remove; trashDir must be an absolute directory
// outside the workspace (e.g. <SUGA_HOME>/trash).
func NewFileRemoveTool(workspace, trashDir string) *FileRemoveTool {
	return &FileRemoveTool{workspace: filepath.Clean(workspace), trashDir: trashDir, now: time.Now}
}

func (t *FileRemoveTool) Name() string { return "file_remove" }

// Description skips the "instead of shell rm" sentence: the stable system
// prompt already names file_remove for every delete (docs/research/
// 2026-10-23-fixed-prefix-tools-report.md 三 #19).
func (t *FileRemoveTool) Description() string {
	return fmt.Sprintf("Remove a file or directory in the workspace. It is moved to the trash and kept for %d days, "+
		"so it can be restored; directories ignored by .gitignore (and node_modules, vendor, dist) are deleted for good "+
		"instead, and the result lists them. A directory needs recursive=true; at most %d files go to the trash per call. "+
		"Wildcards are not expanded.", int(TrashRetention.Hours()/24), RemoveMaxFiles)
}

// ToolMeta marks the tool as a deletion: it changes workspace files.
func (*FileRemoveTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryDelete, ReadOnly: false}
}

func (t *FileRemoveTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File or directory in the workspace (relative, or absolute inside it), or an absolute path under a directory added as read-write",
				},
				"recursive": map[string]any{
					"type":        "boolean",
					"description": "Required to remove a directory and everything in it",
				},
			},
			"required": []any{"path"},
		},
	}
}

// RemovePlan is what file_remove would do: the absolute path and its path
// relative to Base, the files and bytes going to the trash, and the ignored
// directories deleted for good (Purge: all of the path is). Exported for
// approval prompts.
type RemovePlan struct {
	Abs, Rel string
	// Base is the directory Abs was resolved in: the workspace, or an added
	// read-write directory.
	Base         string
	Dir          bool
	Files, Bytes int64
	// Purge: the path is (inside) an ignored directory and is deleted for
	// good as a whole; nothing goes to the trash.
	Purge bool
	// PurgeDirs are the ignored directories below the path deleted for good
	// (workspace-relative, slash-separated) before the rest goes to the trash.
	PurgeDirs []string
}

// PlanRemove checks a file_remove call against the workspace.
func PlanRemove(workspace string, args map[string]any) (RemovePlan, error) {
	return PlanRemoveWithDirs(workspace, nil, args)
}

// PlanRemoveWithDirs is PlanRemove against the workspace and the session's
// read-write directories (--add-dir ...:rw), which are treated the same way:
// the same delete guards apply inside them. writeRoots are absolute.
func PlanRemoveWithDirs(workspace string, writeRoots []string, args map[string]any) (RemovePlan, error) {
	workspace = filepath.Clean(workspace)
	arg, _ := args["path"].(string)
	if strings.TrimSpace(arg) == "" {
		return RemovePlan{}, errors.New(`"path" arg is required`)
	}
	if strings.ContainsAny(arg, "*?[") {
		return RemovePlan{}, fmt.Errorf("wildcards are not expanded: name one file or directory, got %q", arg)
	}
	target, err := resolveWrite(workspace, writeRoots, arg)
	if err != nil {
		return RemovePlan{}, err
	}
	rel, err := relInWorkspace(target.base, target.abs)
	if err != nil {
		return RemovePlan{}, err
	}
	switch {
	case rel == "." && !target.outside:
		return RemovePlan{}, errors.New("refusing to remove the workspace root")
	case rel == ".":
		return RemovePlan{}, errors.New("refusing to remove the root of an added directory")
	case rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)):
		return RemovePlan{}, errors.New("refusing to remove the workspace's own .git")
	}
	if home, herr := os.UserHomeDir(); herr == nil && home != "" {
		if _, inside := relUnderRoot(target.abs, filepath.Clean(home), foldPathCase); inside {
			return RemovePlan{}, fmt.Errorf("refusing to remove %q: it is or contains the home directory", arg)
		}
	}
	// The parent's real location must be inside the same directory the path was
	// resolved in: a symlinked parent directory pointing elsewhere is an escape.
	// The target itself is never followed (a symlink is removed as a link).
	if _, err := realRelInRoot(target.base, filepath.Dir(target.abs)); err != nil {
		return RemovePlan{}, fmt.Errorf("path escape: %q is outside workspace", arg)
	}
	info, err := os.Lstat(target.abs)
	if errors.Is(err, fs.ErrNotExist) {
		return RemovePlan{}, fmt.Errorf("%s does not exist", filepath.ToSlash(rel))
	} else if err != nil {
		return RemovePlan{}, err
	}
	plan := RemovePlan{Base: target.base, Abs: target.abs, Rel: filepath.ToSlash(rel), Dir: info.IsDir()}
	if plan.Dir {
		if recursive, _ := args["recursive"].(bool); !recursive {
			return RemovePlan{}, fmt.Errorf("%s is a directory; pass recursive=true to remove it and everything in it", plan.Rel)
		}
	}
	root, err := os.OpenRoot(target.base)
	if err != nil {
		return RemovePlan{}, err
	}
	defer root.Close()
	ign := newIgnoreMatcher(root, false)
	ignoredDir := func(rel, name string) bool { return ignoredDirs[name] || ign.ignored(rel, true) }
	// The path is regenerable as a whole when it, or a directory above it,
	// is an ignored directory.
	segs := strings.Split(plan.Rel, "/")
	for i := 1; i <= len(segs); i++ {
		if i == len(segs) && !plan.Dir {
			break
		}
		if ignoredDir(strings.Join(segs[:i], "/"), segs[i-1]) {
			plan.Purge = true
			return plan, nil
		}
	}
	if !plan.Dir {
		plan.Files, plan.Bytes = 1, info.Size()
		return plan, nil
	}
	// Walk without following symlinks; ignored directories are not entered.
	var walk func(rel string) error
	walk = func(rel string) error {
		entries, err := fs.ReadDir(root.FS(), rel)
		if err != nil {
			return err
		}
		for _, e := range entries {
			child := rel + "/" + e.Name()
			if e.IsDir() {
				if ignoredDir(child, e.Name()) {
					plan.PurgeDirs = append(plan.PurgeDirs, child)
					continue
				}
				if err := walk(child); err != nil {
					return err
				}
				continue
			}
			plan.Files++
			if plan.Files > RemoveMaxFiles {
				return fmt.Errorf("%s has more than %d files that would go to the trash; remove smaller parts", plan.Rel, RemoveMaxFiles)
			}
			if fi, ierr := e.Info(); ierr == nil && fi.Mode().IsRegular() {
				plan.Bytes += fi.Size()
			}
		}
		return nil
	}
	return plan, walk(plan.Rel)
}

func (t *FileRemoveTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	plan, err := PlanRemoveWithDirs(t.workspace, t.addDirs.WritePaths(), call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("file_remove: %w", err)
	}

	var snapshotReq SnapshotRequest
	var beforeResult SnapshotBefore
	if t.snapshots != nil {
		snapshotReq, err = t.removeSnapshotRequest(plan, call.CallID)
		if err != nil {
			ReportSnapshot(ctx, SnapshotReport{Status: SnapshotStatusIncomplete, Bytes: plan.Bytes})
			return nil, fmt.Errorf("file_remove: %w", err)
		}
		beforeResult, err = t.snapshots.BeforeCall(ctx, snapshotReq)
		if err != nil {
			ReportSnapshot(ctx, SnapshotReport{Kind: snapshotReq.Kind, Status: SnapshotStatusIncomplete,
				Bytes: plan.Bytes, Unrecoverable: snapshotUnrecoverable(snapshotReq.Kind, beforeResult.Unrecoverable)})
			return nil, fmt.Errorf("file_remove: snapshot before: %w", err)
		}
	}
	unrecoverable := snapshotUnrecoverable(snapshotReq.Kind, beforeResult.Unrecoverable)
	finishSnapshot := func(status string, trashEntries, purgedDirs []string) error {
		if t.snapshots == nil {
			return nil
		}
		result := SnapshotResult{
			CallID:        call.CallID,
			Tool:          t.Name(),
			Root:          snapshotReq.Root,
			Rel:           snapshotReq.Rel,
			RealRel:       snapshotReq.RealRel,
			Kind:          snapshotReq.Kind,
			Size:          plan.Bytes,
			Status:        status,
			Unrecoverable: unrecoverable,
			TrashEntries:  trashEntries,
			PurgedDirs:    purgedDirs,
		}
		if err := t.snapshots.AfterCall(ctx, result); err != nil {
			ReportSnapshot(ctx, SnapshotReport{Kind: result.Kind, Status: SnapshotStatusIncomplete,
				Bytes: result.Size, Unrecoverable: result.Unrecoverable})
			return err
		}
		ReportSnapshot(ctx, SnapshotReport{Kind: result.Kind, Status: result.Status,
			Bytes: result.Size, Unrecoverable: result.Unrecoverable})
		return nil
	}
	incomplete := func(opErr error, trashEntries, purgedDirs []string) (*api.ToolResult, error) {
		if snapshotErr := finishSnapshot(SnapshotStatusIncomplete, trashEntries, purgedDirs); snapshotErr != nil {
			return nil, fmt.Errorf("file_remove: %w; snapshot incomplete: %v", opErr, snapshotErr)
		}
		return nil, fmt.Errorf("file_remove: %w", opErr)
	}

	if plan.Purge {
		if err := os.RemoveAll(plan.Abs); err != nil {
			return incomplete(err, nil, nil)
		}
		if err := finishSnapshot(SnapshotStatusComplete, nil, []string{plan.Rel}); err != nil {
			return nil, fmt.Errorf("file_remove: snapshot after failed after removing %s: %w", plan.Abs, err)
		}
		out := fmt.Sprintf("removed %s permanently: it is ignored (regenerable) content, which does not go to the trash", plan.Rel)
		if notice := snapshotNotice(beforeResult); notice != "" {
			out += "\n" + notice
		}
		return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: out}, nil
	}
	// Regenerable directories first: they are never kept, so the move to the
	// trash (possibly a copy to another volume) only handles the rest.
	var purgedDirs []string
	for _, dir := range plan.PurgeDirs {
		if err := os.RemoveAll(filepath.Join(plan.Base, filepath.FromSlash(dir))); err != nil {
			return incomplete(err, nil, purgedDirs)
		}
		purgedDirs = append(purgedDirs, dir)
	}
	t.purgeTrash()
	entry, err := t.newTrashEntry()
	if err != nil {
		return incomplete(fmt.Errorf("trash: %w", err), nil, purgedDirs)
	}
	trashName := filepath.Base(plan.Abs)
	// origin.json is reserved for provenance. Keep a removed file with that
	// basename under a separate payload name so its bytes are not overwritten.
	if strings.EqualFold(trashName, "origin.json") {
		trashName = ".suga-content"
	}
	if err := moveToTrash(plan.Abs, filepath.Join(entry, trashName)); err != nil {
		_ = os.RemoveAll(entry)
		return incomplete(err, nil, purgedDirs)
	}
	origin, _ := json.Marshal(map[string]any{
		"path": plan.Abs, "name": trashName, "removed_at": t.now().UTC().Format(time.RFC3339), "files": plan.Files, "bytes": plan.Bytes,
		"purged_dirs": plan.PurgeDirs,
	})
	if err := os.WriteFile(filepath.Join(entry, "origin.json"), origin, 0o644); err != nil {
		return incomplete(fmt.Errorf("write trash origin: %w", err), []string{entry}, purgedDirs)
	}
	trashEntries := []string{entry}
	if err := finishSnapshot(SnapshotStatusComplete, trashEntries, purgedDirs); err != nil {
		return nil, fmt.Errorf("file_remove: snapshot after failed after removing %s: %w", plan.Abs, err)
	}
	what := "file"
	if plan.Dir {
		what = fmt.Sprintf("directory (%d files)", plan.Files)
	}
	out := fmt.Sprintf("removed %s %s, %d bytes; kept in the trash for %d days: %s",
		what, plan.Rel, plan.Bytes, int(TrashRetention.Hours()/24), entry)
	if n := len(plan.PurgeDirs); n > 0 {
		out += fmt.Sprintf("\n%d ignored (regenerable) director%s deleted permanently: %s",
			n, map[bool]string{true: "y", false: "ies"}[n == 1], NotedDirs(plan.PurgeDirs))
	}
	if notice := snapshotNotice(beforeResult); notice != "" {
		out += "\n" + notice
	}
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: out}, nil
}

// removeSnapshotRequest records a pending deletion before any trash or purge
// I/O. The target itself is not followed when it is a symlink, so its lexical
// path remains the safe real path fallback for that case.
func (t *FileRemoveTool) removeSnapshotRequest(plan RemovePlan, callID string) (SnapshotRequest, error) {
	realRel, err := realRelInRoot(plan.Base, plan.Abs)
	if err != nil {
		realRel = plan.Rel
	}
	kind := SnapshotKindTrash
	if plan.Purge {
		kind = SnapshotKindPurged
	}
	return SnapshotRequest{
		CallID:  callID,
		Tool:    t.Name(),
		Root:    plan.Base,
		Rel:     plan.Rel,
		RealRel: filepath.ToSlash(realRel),
		Kind:    kind,
	}, nil
}

// NotedDirs names the first few directories of dirs and how many more.
func NotedDirs(dirs []string) string {
	shown := dirs[:min(len(dirs), maxNotedPurged)]
	out := strings.Join(shown, "/, ") + "/"
	if more := len(dirs) - len(shown); more > 0 {
		out += fmt.Sprintf(" and %d more", more)
	}
	return out
}

// newTrashEntry creates <trash>/<UTC stamp>-<random>/.
func (t *FileRemoveTool) newTrashEntry() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(t.trashDir, t.now().UTC().Format(trashStampLayout)+"-"+hex.EncodeToString(b[:]))
	return dir, os.MkdirAll(dir, 0o700)
}

// purgeTrash deletes trash entries older than TrashRetention (best-effort).
// Entries are recognised by their name stamp; anything else is left alone.
func (t *FileRemoveTool) purgeTrash() {
	entries, err := os.ReadDir(t.trashDir)
	if err != nil {
		return
	}
	cutoff := t.now().Add(-TrashRetention)
	for _, e := range entries {
		if !e.IsDir() || len(e.Name()) < len(trashStampLayout) {
			continue
		}
		at, err := time.Parse(trashStampLayout, e.Name()[:len(trashStampLayout)])
		if err == nil && at.Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(t.trashDir, e.Name()))
		}
	}
}

// renamePath is os.Rename; tests replace it to simulate a trash on another
// volume (where a rename always fails).
var renamePath = os.Rename

// MovePath moves src to dst with the same-volume rename and cross-volume
// copy-then-remove fallback used by file_remove. Undo uses this helper to
// restore a trash entry without maintaining a second copy of the algorithm.
func MovePath(src, dst string) error {
	return moveToTrash(src, dst)
}

// moveToTrash renames src to dst, or copies and then deletes when the trash is
// on another volume (at most RemoveMaxFiles files reach this point). A
// failed copy leaves src untouched.
func moveToTrash(src, dst string) error {
	if err := renamePath(src, dst); err == nil {
		return nil
	}
	if err := copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return fmt.Errorf("move to trash: %w", err)
	}
	return os.RemoveAll(src)
}

// copyTree copies src (file, directory or symlink, never followed) to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFile(p, target, info.Mode().Perm())
		}
		return nil // sockets, devices: not copied
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
