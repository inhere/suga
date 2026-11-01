package personal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/tools/native"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// UndoResult is the file-level outcome of one undo operation. Files contains
// the paths successfully restored or deleted; shell commands are intentionally
// outside this result because undo never attempts to reverse shell_exec.
type UndoResult struct {
	SessionID     string   `json:"session_id"`
	Turns         int      `json:"turns"`
	Restored      int      `json:"restored"`
	Deleted       int      `json:"deleted"`
	Skipped       int      `json:"skipped"`
	Unrecoverable int      `json:"unrecoverable"`
	Files         []string `json:"files,omitempty"`
	Failures      []string `json:"failures,omitempty"`
	Forced        bool     `json:"forced"`
}

type restoreOutcome struct {
	action     CheckpointAction
	reason     string
	unresolved bool
	path       string
}

// Undo restores the latest n checkpoints in reverse turn and call order. The
// core owns the lock for the complete operation, so another writer cannot
// interleave a write between conflict detection and restoration.
func (u *UndoCore) Undo(ctx context.Context, sessionQuery string, n int, force bool) (UndoResult, error) {
	var result UndoResult
	if err := u.validate(); err != nil {
		return result, err
	}
	sessionID, err := ResolveSessionID(ctx, u.Repository, sessionQuery)
	if err != nil {
		return result, err
	}
	result.SessionID = sessionID
	result.Forced = force
	err = u.withSession(ctx, sessionID, func() error {
		session, err := u.Repository.GetSession(ctx, sessionID)
		if err != nil {
			return err
		}
		rows, err := u.checkpointsLocked(ctx, sessionID, 0)
		if err != nil {
			return err
		}
		selected := selectUndoRows(rows, n)
		// Counts and Files are per path: see pathTally.
		tally := newPathTally()
		for _, row := range selected {
			if !row.HasSnapshots() {
				continue
			}
			result.Turns++
			fullyUndone := true
			// Items are persisted in call order. Reversing them is what makes
			// two writes to one path converge on the turn's initial content.
			for i := len(row.Items) - 1; i >= 0; i-- {
				item := row.Items[i]
				outcome, restoreErr := u.restoreItem(ctx, session, sessionID, row.TurnID, item, force)
				tally.add(RewindFileItem{TurnID: row.TurnID, Path: outcome.path, Action: checkpointToFileAction(outcome.action)})
				tally.sync(&result)
				if restoreErr != nil {
					result.Failures = append(result.Failures, restoreErr.Error())
					fullyUndone = false
				}
				if outcome.unresolved {
					fullyUndone = false
				}
			}
			state := SnapshotStatePartial
			if fullyUndone {
				state = SnapshotStateUndone
			}
			if err := u.Snapshots.SetCheckpointState(sessionID, row.TurnID, state); err != nil {
				return fmt.Errorf("set checkpoint %s state: %w", row.TurnID, err)
			}
		}
		// Keep the existing session lock until the fact is queued, otherwise a
		// concurrent new turn could start in the small gap after restoration.
		if result.Turns > 0 {
			if err := u.enqueueUndoNotice(ctx, result); err != nil {
				return fmt.Errorf("queue undo notice: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func selectUndoRows(rows []Checkpoint, n int) []Checkpoint {
	if n <= 0 {
		n = 1
	}
	selected := make([]Checkpoint, 0, n)
	count := 0
	for _, row := range rows {
		if row.IsShellOnly() {
			// Keep shell-only rows visible in the selected range, but there is
			// no file operation to perform for them.
			selected = append(selected, row)
			continue
		}
		if row.State == SnapshotStateUndone {
			continue
		}
		selected = append(selected, row)
		count++
		if count == n {
			break
		}
	}
	return selected
}

func (u *UndoCore) restoreItem(ctx context.Context, session Session, sessionID, turnID string, item CheckpointItem, force bool) (restoreOutcome, error) {
	meta := item.Meta
	path := filepath.Join(meta.Root, filepath.FromSlash(meta.Rel))
	out := restoreOutcome{action: CheckpointActionSkip, path: path}
	if item.RootStatus != SnapshotRootTrusted {
		out.reason = item.Reason
		out.unresolved = true
		return out, nil
	}
	if meta.Status != native.SnapshotStatusComplete && meta.Status != native.SnapshotStatusIncomplete {
		out.reason = "快照尚未完成"
		out.unresolved = true
		return out, nil
	}
	if !undoTool(meta.Tool) {
		out.reason = "快照工具身份无效，无法恢复"
		out.unresolved = true
		return out, nil
	}
	if _, err := native.ResolveSnapshotPath(meta.Root, meta.Rel, meta.RealRel); err != nil {
		out.reason = "快照路径身份校验失败"
		out.unresolved = true
		return out, err
	}
	// The approval-rule owner supplies the canonical protected-path predicate.
	// Check both lexical and resolved spellings so a link cannot hide a control
	// plane file behind an ordinary-looking relative path.
	if u.protectedUndoPath(session, meta) {
		out.reason = "受保护路径，无法恢复"
		out.unresolved = true
		return out, nil
	}
	switch meta.Kind {
	case native.SnapshotKindFile:
		return u.restoreFile(ctx, sessionID, turnID, meta, force)
	case native.SnapshotKindAbsent:
		return u.deleteAbsent(ctx, sessionID, turnID, meta, force)
	case native.SnapshotKindTrash:
		return u.restoreTrash(ctx, sessionID, turnID, meta)
	default:
		out.action = CheckpointActionUnrecoverable
		out.reason = snapshotReason(meta, out.action)
		out.unresolved = true
		return out, nil
	}
}

func undoTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "file_write", "file_edit", "file_remove":
		return true
	default:
		return false
	}
}

func (u *UndoCore) protectedUndoPath(session Session, meta SnapshotMeta) bool {
	sugaHome := ""
	if filepath.Base(filepath.Clean(u.SessionsRoot)) == "sessions" {
		sugaHome = filepath.Dir(filepath.Clean(u.SessionsRoot))
	}
	ruleFiles, _ := RuleFiles(session.Cwd, sugaHome, false, "")
	if meta.Root != session.Cwd {
		if extra, _ := RuleFiles(meta.Root, sugaHome, false, ""); len(extra) > 0 {
			ruleFiles = append(ruleFiles, extra...)
		}
	}
	for _, rel := range []string{meta.Rel, meta.RealRel} {
		for _, workspace := range []string{session.Cwd, meta.Root} {
			target := approvalrules.Target{Tool: meta.Tool, Root: meta.Root, Path: rel}
			if approvalrules.IsProtectedPath(target, workspace, sugaHome, ruleFiles...) {
				return true
			}
		}
	}
	return false
}

func (u *UndoCore) restoreFile(ctx context.Context, sessionID, turnID string, meta SnapshotMeta, force bool) (restoreOutcome, error) {
	path := filepath.Join(meta.Root, filepath.FromSlash(meta.Rel))
	out := restoreOutcome{action: CheckpointActionSkip, path: path}
	before, err := u.readBefore(sessionID, turnID, meta)
	if err != nil {
		out.reason = "原始快照缺失或损坏"
		out.unresolved = true
		return out, err
	}
	if meta.SHA256Before == "" || nativeSHA256(before) != meta.SHA256Before {
		out.reason = "原始快照哈希校验失败"
		out.unresolved = true
		return out, nil
	}
	if meta.SHA256After == "" {
		out.reason = "快照缺少写入后哈希"
		out.unresolved = true
		return out, nil
	}

	root, rel, current, status, err := openCurrentFile(meta)
	if err != nil {
		out.reason = "无法读取当前文件"
		out.unresolved = true
		return out, err
	}
	if status == currentAbsent {
		_ = root.Close()
		out.reason = "当前文件已不存在"
		out.unresolved = true
		return out, nil
	}
	if status != currentRegular {
		_ = root.Close()
		out.reason = "当前路径不是安全的普通文件（符号链接、硬链接或特殊文件）"
		out.unresolved = true
		return out, nil
	}
	defer root.Close()
	currentHash := nativeSHA256(current)
	if currentHash == meta.SHA256Before {
		// The desired state is already present. This is a successful, idempotent
		// undo rather than a conflict and does not need another write.
		return restoreOutcome{action: CheckpointActionSkip, path: path}, nil
	}
	if currentHash != meta.SHA256After && !force {
		out.reason = "冲突：快照之后被改动过"
		out.unresolved = true
		return out, nil
	}

	finish, err := u.preparePreUndo(ctx, sessionID, turnID, meta, current)
	if err != nil {
		out.reason = "回滚前快照失败"
		out.unresolved = true
		return out, err
	}
	if err := writeRestoredFile(root, rel, before, meta.Mode); err != nil {
		_ = finish(native.SnapshotStatusIncomplete, "")
		out.reason = "恢复文件失败"
		out.unresolved = true
		return out, err
	}
	if err := finish(native.SnapshotStatusComplete, meta.SHA256Before); err != nil {
		out.reason = "记录回滚前快照失败"
		out.unresolved = true
		return out, err
	}
	out.action = CheckpointActionRestore
	return out, nil
}

func (u *UndoCore) deleteAbsent(ctx context.Context, sessionID, turnID string, meta SnapshotMeta, force bool) (restoreOutcome, error) {
	path := filepath.Join(meta.Root, filepath.FromSlash(meta.Rel))
	out := restoreOutcome{action: CheckpointActionSkip, path: path}
	root, rel, current, status, err := openCurrentFile(meta)
	if err != nil {
		out.reason = "无法读取当前文件"
		out.unresolved = true
		return out, err
	}
	if status == currentAbsent {
		_ = root.Close()
		return out, nil
	}
	if status != currentRegular {
		_ = root.Close()
		out.reason = "当前路径不是安全的普通文件（符号链接、硬链接或特殊文件）"
		out.unresolved = true
		return out, nil
	}
	defer root.Close()
	// file_write historically leaves sha256_after empty for kind=absent. If a
	// caller has a newer record with an after hash, apply the same conflict rule.
	if meta.SHA256After != "" && nativeSHA256(current) != meta.SHA256After && !force {
		out.reason = "冲突：快照之后被改动过"
		out.unresolved = true
		return out, nil
	}
	finish, err := u.preparePreUndo(ctx, sessionID, turnID, meta, current)
	if err != nil {
		out.reason = "回滚前快照失败"
		out.unresolved = true
		return out, err
	}
	if err := root.Remove(rel); err != nil {
		_ = finish(native.SnapshotStatusIncomplete, "")
		out.reason = "删除新建文件失败"
		out.unresolved = true
		return out, err
	}
	if err := finish(native.SnapshotStatusComplete, ""); err != nil {
		out.reason = "记录回滚前快照失败"
		out.unresolved = true
		return out, err
	}
	out.action = CheckpointActionDelete
	return out, nil
}

// contentConflict reports whether undoing item would hit the content conflict
// restoreFile/deleteAbsent refuse without force: the current file is neither
// what the write left (sha256_after) nor, for a restore, already the snapshot.
// It only reads the file, for the plan shown before the confirmation; the
// undo itself checks again under the session lock.
func contentConflict(item CheckpointItem) bool {
	if item.Meta.SHA256After == "" ||
		(item.Action != CheckpointActionRestore && item.Action != CheckpointActionDelete) {
		return false
	}
	root, _, current, status, err := openCurrentFile(item.Meta)
	if err != nil {
		return false
	}
	_ = root.Close()
	if status != currentRegular {
		return false
	}
	hash := nativeSHA256(current)
	if item.Action == CheckpointActionRestore && hash == item.Meta.SHA256Before {
		return false
	}
	return hash != item.Meta.SHA256After
}

const (
	currentAbsent = iota
	currentRegular
	currentOther
)

func openCurrentFile(meta SnapshotMeta) (*os.Root, string, []byte, int, error) {
	resolved, err := native.ResolveSnapshotPath(meta.Root, meta.Rel, meta.RealRel)
	if err != nil {
		return nil, "", nil, currentOther, err
	}
	root, err := os.OpenRoot(resolved.Root)
	if err != nil {
		return nil, "", nil, currentOther, err
	}
	rel := filepath.FromSlash(resolved.Rel)
	info, err := root.Lstat(rel)
	if errors.Is(err, os.ErrNotExist) {
		return root, rel, nil, currentAbsent, nil
	}
	if err != nil {
		_ = root.Close()
		return nil, "", nil, currentOther, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || native.IsHardLinked(resolved.Abs, info) {
		return root, rel, nil, currentOther, nil
	}
	data, err := root.ReadFile(rel)
	if err != nil {
		_ = root.Close()
		return nil, "", nil, currentOther, err
	}
	return root, rel, data, currentRegular, nil
}

func writeRestoredFile(root *os.Root, rel string, before []byte, mode string) error {
	if dir := filepath.Dir(rel); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	perm := os.FileMode(0o644)
	if mode != "" {
		value, err := strconv.ParseUint(mode, 8, 32)
		if err != nil {
			return fmt.Errorf("invalid snapshot mode %q: %w", mode, err)
		}
		perm = os.FileMode(value)
	}
	if err := root.WriteFile(rel, before, perm); err != nil {
		return err
	}
	return root.Chmod(rel, perm)
}

func (u *UndoCore) readBefore(sessionID, turnID string, meta SnapshotMeta) ([]byte, error) {
	if meta.File != "before" || meta.Seq <= 0 {
		return nil, errors.New("snapshot before image is not declared")
	}
	path := filepath.Join(u.Snapshots.dir, sessionID, "snapshots", turnID,
		fmt.Sprintf("%03d-%s", meta.Seq, snapshotCallHash(meta.CallID)), "before")
	return os.ReadFile(path)
}

func (u *UndoCore) preparePreUndo(ctx context.Context, sessionID, turnID string, meta SnapshotMeta, current []byte) (func(string, string) error, error) {
	callID := fmt.Sprintf("undo-%d-%s-%d", meta.Seq, meta.CallID, time.Now().UnixNano())
	scope := withObserveScope(ctx, sessionID, turnID)
	before, err := u.Snapshots.BeforeUndo(scope, native.SnapshotRequest{
		CallID: callID, Tool: "undo", Root: meta.Root, Rel: meta.Rel, RealRel: meta.RealRel,
		Kind: native.SnapshotKindFile, Before: current,
	})
	if err != nil {
		return nil, err
	}
	return func(status, after string) error {
		return u.Snapshots.AfterUndo(scope, native.SnapshotResult{
			CallID: callID, Tool: "undo", Root: meta.Root, Rel: meta.Rel, RealRel: meta.RealRel,
			Kind: native.SnapshotKindFile, AfterSHA256: after, Size: int64(len(current)), Status: status,
			Unrecoverable: before.Unrecoverable,
		})
	}, nil
}
