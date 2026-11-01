package personal

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// ErrRewindBusy is returned by withSessionUnlessBusy (and the gate behind it)
// when a rewind/fork cannot take the session right now: a turn holds the
// in-process gate, or another process holds the session file lock. It never
// waits for a turn; only the preemptible idle consolidation is waited for.
var ErrRewindBusy = errors.New("session is busy")

// RewindFileAction is what a rewind does (or reports) for one file.
type RewindFileAction string

const (
	RewindFileRestore       RewindFileAction = "restore"
	RewindFileDelete        RewindFileAction = "delete"
	RewindFileConflict      RewindFileAction = "conflict"
	RewindFileUnrecoverable RewindFileAction = "unrecoverable"
)

// Reasons of conflict and unrecoverable items; the cleanup reasons come from
// rewind_files_cleared.go.
const (
	rewindReasonRootUntrusted = "root_untrusted"
	rewindReasonContent       = "content_changed" // changed after the snapshot
	rewindReasonCascade       = "cascade"         // an earlier path step conflicted
	rewindReasonUnchanged     = "unchanged"       // already in the wanted state
	// rewindReasonRestoreFailed prefixes the reason of an item whose restore
	// returned an error (followed by the error text).
	rewindReasonRestoreFailed = "restore_failed"
)

// RewindFileItem is one file line of a rewind plan or result.
type RewindFileItem struct {
	TurnID string
	// Path is the snapshot entry path; for an unrecoverable item without a
	// snapshot it is the raw attrs["target"] value (maybe relative or a dir).
	Path   string
	Action RewindFileAction
	// Reason explains a conflict or unrecoverable item.
	Reason string
}

// rewindFileStep is one file step with what restoreItem needs (the snapshot
// entry with its RootStatus). A step whose View.Action is unrecoverable is
// never sent to restoreItem.
type rewindFileStep struct {
	View RewindFileItem
	Item CheckpointItem
}

// hasSnapshot reports whether the step comes from a snapshot directory record
// (cleared / never-built steps carry a zero Item).
func (s rewindFileStep) hasSnapshot() bool { return s.Item.Meta.CallID != "" || s.Item.Meta.Seq > 0 }

// pathState is the simulated content of one path while planning: the plan
// walks newest to oldest and assumes each planned restore succeeds.
type pathState struct {
	unknown  bool   // a trash restore: content not predictable
	absent   bool   // the file is (assumed) gone
	hash     string // the content hash otherwise
	conflict bool   // an earlier step on this path conflicted (cascade)
}

// planRewindFiles lists, newest turn first, the file steps that bring the
// files back to the state before toTurnID (inclusive). It takes no lock: the
// preview reads without one; the executor recomputes it inside
// withSessionUnlessBusy. Rows come from checkpointsLocked so every item has its
// RootStatus. Masked turns count; undone turns are skipped; partial ones rerun.
func (u *UndoCore) planRewindFiles(ctx context.Context, sid, toTurnID string) ([]rewindFileStep, error) {
	if err := u.validate(); err != nil {
		return nil, err
	}
	turns, err := u.Repository.ListTurns(ctx, sid)
	if err != nil {
		return nil, fmt.Errorf("rewind files: list turns: %w", err)
	}
	from := -1
	for i, t := range turns {
		if t.ID == toTurnID {
			from = i
			break
		}
	}
	if from < 0 {
		return nil, fmt.Errorf("rewind files: turn %q not in session %s", toTurnID, sid)
	}
	session, err := u.Repository.GetSession(ctx, sid)
	if err != nil {
		return nil, err
	}
	rows, err := u.checkpointsLocked(ctx, sid, 0)
	if err != nil {
		return nil, err
	}
	rowByTurn := make(map[string]Checkpoint, len(rows))
	for _, row := range rows {
		rowByTurn[row.TurnID] = row
	}
	cleared, err := u.clearedWrites(ctx, sid)
	if err != nil {
		return nil, err
	}

	var steps []rewindFileStep
	state := map[string]*pathState{}
	for i := len(turns) - 1; i >= from; i-- {
		id := turns[i].ID
		row, ok := rowByTurn[id]
		if !ok || !row.HasSnapshots() {
			steps = append(steps, cleared[id]...)
			continue
		}
		if row.State == SnapshotStateUndone {
			continue
		}
		// Items are in call order; reverse so two writes to one path
		// converge on the turn's initial content.
		for j := len(row.Items) - 1; j >= 0; j-- {
			steps = append(steps, u.planRewindItem(session, id, row.Items[j], state))
		}
	}
	return steps, nil
}

// planRewindItem classifies one snapshot item and predicts, without writing,
// whether restoreItem would hit a content conflict (including one caused by a
// newer step on the same path that itself conflicts).
func (u *UndoCore) planRewindItem(session Session, turnID string, item CheckpointItem, state map[string]*pathState) rewindFileStep {
	meta := item.Meta
	path := filepath.Join(meta.Root, filepath.FromSlash(meta.Rel))
	step := rewindFileStep{Item: item, View: RewindFileItem{TurnID: turnID, Path: path}}
	unrecoverable := func(reason string) rewindFileStep {
		step.View.Action, step.View.Reason = RewindFileUnrecoverable, reason
		return step
	}
	switch {
	case item.Action == CheckpointActionUnrecoverable:
		reason := meta.Unrecoverable
		if reason == "" {
			reason = native.SnapshotUnrecoverablePurged
		}
		return unrecoverable(reason)
	case item.RootStatus != SnapshotRootTrusted:
		return unrecoverable(rewindReasonRootUntrusted)
	case item.Action == CheckpointActionSkip:
		// pending or malformed record: restoreItem could only skip it.
		return unrecoverable("snapshot_incomplete")
	}
	if !undoTool(meta.Tool) || u.protectedUndoPath(session, meta) {
		return unrecoverable("not_restorable")
	}

	key := meta.Root + "\x00" + meta.RealRel
	st := state[key]
	if st == nil {
		st = &pathState{}
		state[key] = st
	}
	conflict := func(reason string) rewindFileStep {
		if st.conflict {
			reason = rewindReasonCascade
		}
		st.conflict = true
		step.Item.Conflict = true
		step.View.Action, step.View.Reason = RewindFileConflict, reason
		return step
	}
	// cur is the content this step will find: the simulated one after a
	// planned newer step, otherwise what is on disk now.
	var (
		cur       string
		curAbsent bool
		curOther  bool
		known     = !st.unknown
	)
	if known {
		if st.hash != "" || st.absent {
			cur, curAbsent = st.hash, st.absent
		} else {
			root, _, data, status, err := openCurrentFile(meta)
			if err != nil {
				return conflict("unreadable")
			}
			_ = root.Close()
			curAbsent, curOther = status == currentAbsent, status == currentOther
			if status == currentRegular {
				cur = nativeSHA256(data)
			}
		}
	}

	switch meta.Kind {
	case native.SnapshotKindFile:
		step.View.Action = RewindFileRestore
		if meta.SHA256Before == "" || meta.SHA256After == "" {
			return conflict("snapshot_hash_missing")
		}
		if !known {
			st.unknown, st.hash, st.absent = false, meta.SHA256Before, false
			return step
		}
		switch {
		case curOther:
			return conflict("not_regular_file")
		case curAbsent:
			return conflict("file_missing")
		case cur != meta.SHA256Before && cur != meta.SHA256After:
			return conflict(rewindReasonContent)
		}
		st.hash, st.absent = meta.SHA256Before, false
	case native.SnapshotKindAbsent:
		step.View.Action = RewindFileDelete
		if known {
			switch {
			case curOther:
				return conflict("not_regular_file")
			case !curAbsent && meta.SHA256After != "" && cur != meta.SHA256After:
				return conflict(rewindReasonContent)
			}
		}
		st.unknown, st.hash, st.absent = false, "", true
	case native.SnapshotKindTrash:
		// restoreTrash does its own checks; just stop predicting this path.
		step.View.Action = RewindFileRestore
		st.unknown, st.hash, st.absent = true, "", false
	default:
		return unrecoverable("snapshot_incomplete")
	}
	return step
}

// applyRewindFiles runs steps (computed by the caller under the session lock
// just before) newest first and returns what actually happened. Unrecoverable
// steps are reported without calling restoreItem. A failing restoreItem does
// not stop the loop (like :undo): its error becomes that item's result (a
// conflict with reason restore_failed) and the turn is marked partial. Checkpoint
// state is written only for turns that have a snapshot directory. Only a
// whole-operation error (writing the checkpoint state) is returned. The caller
// invokes OnFilesRestored with the restored paths.
func (u *UndoCore) applyRewindFiles(ctx context.Context, sid string, steps []rewindFileStep) ([]RewindFileItem, error) {
	if err := u.validate(); err != nil {
		return nil, err
	}
	session, err := u.Repository.GetSession(ctx, sid)
	if err != nil {
		return nil, err
	}
	var (
		results []RewindFileItem
		order   []string            // turns with a snapshot dir, in step order
		done    = map[string]bool{} // turn -> every step resolved
	)
	for _, step := range steps {
		turn := step.View.TurnID
		if step.hasSnapshot() {
			if _, seen := done[turn]; !seen {
				order = append(order, turn)
				done[turn] = true
			}
		}
		if step.View.Action == RewindFileUnrecoverable {
			results = append(results, step.View)
			if step.hasSnapshot() {
				done[turn] = false
			}
			continue
		}
		outcome, restoreErr := u.restoreItem(ctx, session, sid, turn, step.Item, false)
		res := RewindFileItem{TurnID: turn, Path: outcome.path}
		switch outcome.action {
		case CheckpointActionRestore:
			res.Action = RewindFileRestore
		case CheckpointActionDelete:
			res.Action = RewindFileDelete
		case CheckpointActionUnrecoverable:
			res.Action, res.Reason = RewindFileUnrecoverable, outcome.reason
		default:
			res.Action, res.Reason = RewindFileConflict, outcome.reason
			if res.Reason == "" {
				res.Reason = rewindReasonUnchanged
			}
		}
		if restoreErr != nil {
			// A single failing item is a result, not an abort: it is reported as
			// a conflict with the error text (counted as a failure by the
			// caller) and the turn stays partial.
			res.Action, res.Reason = RewindFileConflict, rewindReasonRestoreFailed+": "+restoreErr.Error()
		}
		if restoreErr != nil || outcome.unresolved {
			done[turn] = false
		}
		results = append(results, res)
	}
	for _, turn := range order {
		state := SnapshotStatePartial
		if done[turn] {
			state = SnapshotStateUndone
		}
		if err := u.Snapshots.SetCheckpointState(sid, turn, state); err != nil {
			return results, fmt.Errorf("set checkpoint %s state: %w", turn, err)
		}
	}
	return results, nil
}
