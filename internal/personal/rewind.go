package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// Session rewind, preview and execution (session rewind design 0.2, plan T5).
// RewindPlan reads without a lock; ApplyRewind recomputes the plan inside
// withSessionUnlessBusy and refuses to run when anything differs from the
// preview. rewind_notice.go holds the model-facing wording.

// RewindScope is which parts of a session a rewind takes back.
type RewindScope string

const (
	RewindScopeFiles        RewindScope = "files"
	RewindScopeConversation RewindScope = "conversation"
	RewindScopeBoth         RewindScope = "both"
)

func (s RewindScope) hasFiles() bool { return s == RewindScopeFiles || s == RewindScopeBoth }
func (s RewindScope) hasConversation() bool {
	return s == RewindScopeConversation || s == RewindScopeBoth
}

// normalizeRewindScope maps the empty scope to both (the chat default) and
// rejects anything else that is not a known scope.
func normalizeRewindScope(s RewindScope) (RewindScope, error) {
	switch s {
	case "":
		return RewindScopeBoth, nil
	case RewindScopeFiles, RewindScopeConversation, RewindScopeBoth:
		return s, nil
	}
	return "", fmt.Errorf("unknown rewind scope %q: use files, conversation or both", string(s))
}

// ErrRewindStale: the session changed between the preview and the execution
// (a new entry, a changed file plan, a different cwd...), so the confirmed plan
// is not what would run. Nothing was changed; preview again.
var ErrRewindStale = errors.New("rewind plan is stale")

// RewindPlan is the preview of a rewind.
type RewindPlan struct {
	SessionID, TargetTurn string
	Scope                 RewindScope
	CutSeq                int64
	// Files is filled when the scope includes files.
	Files []RewindFileItem
	// LatestSeq is the session's newest entry seq at preview time.
	LatestSeq int64
}

// RewindResult is what a rewind actually did.
type RewindResult struct {
	// Files are the files actually restored, deleted, skipped or unrecoverable.
	Files []RewindFileItem
	// Conversation reports that a rewind record was written.
	Conversation bool
	// DroppedTasks are the ended-task notices taken out and discarded.
	DroppedTasks []tasks.Info
	// Notice is the text queued for the model; empty when nothing was queued.
	Notice string
	// Warnings are non-fatal problems the caller should show (the rewind stands):
	// the fold meta reset failed after the record was written.
	Warnings []string
}

// rewindFileStat tallies a file outcome the way UndoResult does.
type rewindFileStat struct {
	turns         int // turns with a snapshot directory in the range (UndoResult.Turns)
	restored      []string
	deleted       []string
	skipped       []string
	unrecoverable []string
	failures      []string // restore_failed items (also counted in skipped)
}

func (s rewindFileStat) undoResult(sid string) UndoResult {
	files := append(append([]string(nil), s.restored...), s.deleted...)
	return UndoResult{SessionID: sid, Turns: s.turns, Restored: len(s.restored), Deleted: len(s.deleted),
		Skipped: len(s.skipped), Unrecoverable: len(s.unrecoverable), Files: files, Failures: s.failures}
}

func tallyRewindFiles(steps []rewindFileStep, items []RewindFileItem) rewindFileStat {
	var st rewindFileStat
	seen := map[string]bool{}
	for _, s := range steps {
		if s.hasSnapshot() && !seen[s.View.TurnID] {
			seen[s.View.TurnID] = true
			st.turns++
		}
	}
	// One outcome per path (the last entry wins), see pathTally.
	tally := newPathTally()
	for _, it := range items {
		tally.add(it)
		if it.Action == RewindFileConflict && strings.HasPrefix(it.Reason, rewindReasonRestoreFailed) {
			st.failures = append(st.failures, it.Path+": "+it.Reason)
		}
	}
	st.restored = tally.paths(RewindFileRestore)
	st.deleted = tally.paths(RewindFileDelete)
	st.skipped = tally.paths(RewindFileConflict)
	st.unrecoverable = tally.paths(RewindFileUnrecoverable)
	return st
}

// computeRewind is the one place the plan is built, so the preview and the
// locked recomputation cannot drift apart.
func (r *Runner) computeRewind(ctx context.Context, core *UndoCore, sid, target string, scope RewindScope) (RewindPlan, []rewindFileStep, userTurn, error) {
	ut, err := r.resolveTarget(ctx, sid, target)
	if err != nil {
		return RewindPlan{}, nil, userTurn{}, err
	}
	plan := RewindPlan{SessionID: sid, TargetTurn: ut.Turn.ID, Scope: scope, CutSeq: cutFor(ut)}
	var steps []rewindFileStep
	if scope.hasFiles() {
		if steps, err = core.planRewindFiles(ctx, sid, ut.Turn.ID); err != nil {
			return RewindPlan{}, nil, userTurn{}, err
		}
		for _, s := range steps {
			plan.Files = append(plan.Files, s.View)
		}
	}
	if plan.LatestSeq, err = r.cfg.Repository.LastEntrySeq(ctx, sid); err != nil {
		return RewindPlan{}, nil, userTurn{}, err
	}
	return plan, steps, ut, nil
}

// planRewind is the read-only preview. It takes no gate and no lock; the
// execution is protected by its own recomputation. The refusals (archived
// session, running tasks) are checked here too so a doomed rewind is not offered
// for confirmation. Pending task notices are not listed (agentcore has no
// read-only accessor); the executor reports what it dropped.
func (r *Runner) planRewind(ctx context.Context, snapshots *SessionSnapshots, sessionsRoot, sid, target string, scope RewindScope) (RewindPlan, error) {
	scope, err := normalizeRewindScope(scope)
	if err != nil {
		return RewindPlan{}, err
	}
	sid, err = ResolveSessionID(ctx, r.cfg.Repository, sid)
	if err != nil {
		return RewindPlan{}, err
	}
	if err := r.rewindPreconditions(ctx, sid); err != nil {
		return RewindPlan{}, err
	}
	plan, _, _, err := r.computeRewind(ctx, r.UndoCore(snapshots, sessionsRoot), sid, target, scope)
	return plan, err
}

// applyRewind runs a previewed plan inside withSessionUnlessBusy. The returned
// result is meaningful even with an error (files restored before a failure).
func (r *Runner) applyRewind(ctx context.Context, snapshots *SessionSnapshots, sessionsRoot string, plan RewindPlan) (RewindResult, error) {
	var res RewindResult
	scope, err := normalizeRewindScope(plan.Scope)
	if err != nil {
		return res, err
	}
	plan.Scope = scope
	core := r.UndoCore(snapshots, sessionsRoot)
	err = core.withSessionUnlessBusy(ctx, plan.SessionID, func() error {
		if err := r.rewindPreconditions(ctx, plan.SessionID); err != nil {
			return err
		}
		// Recompute under the lock (target, file plan, latest seq) and compare
		// with the preview item by item: any difference means the person
		// confirmed something else than what would run now.
		cur, steps, ut, err := r.computeRewind(ctx, core, plan.SessionID, plan.TargetTurn, scope)
		if err != nil {
			if errors.Is(err, ErrRewindTargetNotFound) || errors.Is(err, ErrRewindTargetMasked) || errors.Is(err, ErrRewindTargetNotUserTurn) {
				return fmt.Errorf("%w: %w", ErrRewindStale, err)
			}
			return err
		}
		if !reflect.DeepEqual(cur, plan) {
			return fmt.Errorf("%w: session %s changed since the preview", ErrRewindStale, plan.SessionID)
		}
		return r.runRewind(ctx, core, plan, steps, ut, &res)
	})
	return res, err
}

// runRewind executes a verified plan; the caller holds the session. The
// failure table of the design (flow 1, step 4):
//   - the file part returns a whole-operation error (checkpoint state, lock, IO): the conversation part is not run; the
//     restored files are still reported to the model like an undo;
//   - a single item failing to restore, skipped or unrecoverable files: the
//     conversation part still runs and they are listed;
//   - the rewind record cannot be written after files were restored: the model is
//     told the files changed, the error goes to the caller (retry conversation only);
//   - the record is written but the meta reset fails: Warn only, the next turn
//     recomputes its projection from the masked history (Warn plus Result.Warnings).
func (r *Runner) runRewind(ctx context.Context, core *UndoCore, plan RewindPlan, steps []rewindFileStep, ut userTurn, res *RewindResult) error {
	wctx := context.WithoutCancel(ctx)
	sid := plan.SessionID
	var stat rewindFileStat
	if plan.Scope.hasFiles() {
		items, ferr := core.applyRewindFiles(ctx, sid, steps)
		if ferr == nil && r.rewindApplyHook != nil {
			ferr = r.rewindApplyHook("files_applied") // test-only failure injection
		}
		res.Files = items
		stat = tallyRewindFiles(steps, items)
		if r.cfg.OnFilesRestored != nil {
			if paths := stat.undoResult(sid).Files; len(paths) > 0 {
				r.cfg.OnFilesRestored(paths)
			}
		}
		if ferr != nil {
			if nerr := r.finishFilesOnly(wctx, core, plan, stat, res); nerr != nil {
				slog.Warn("rewind: record the partial file rewind", "session_id", sid, "err", nerr)
			}
			return fmt.Errorf("rewind files: %w", ferr)
		}
	}
	if !plan.Scope.hasConversation() {
		return r.finishFilesOnly(wctx, core, plan, stat, res)
	}

	raw, err := json.Marshal(map[string]any{"cut_seq": plan.CutSeq, "target_turn": plan.TargetTurn,
		"scope": string(plan.Scope), "files": rewindEntryFiles(stat)})
	if err != nil {
		return err
	}
	if _, err := r.cfg.Repository.AppendEntries(wctx, sid, store.NewEntry{Type: store.EntryRewind, JSON: raw}); err != nil {
		if plan.Scope.hasFiles() {
			// The files are already back: tell the model so it does not act on stale reads.
			if nerr := r.finishFilesOnly(wctx, core, plan, stat, res); nerr != nil {
				slog.Warn("rewind: record the file rewind after the record failed", "session_id", sid, "err", nerr)
			}
		}
		return fmt.Errorf("record rewind: %w", err)
	}
	res.Conversation = true

	if err := r.cfg.Repository.PatchSessionMeta(wctx, sid, map[string]any{metaFoldBoundary: 0, metaFoldActive: false}); err != nil {
		// The entry is written; the next turn projects from the masked history anyway.
		slog.Warn("rewind: reset fold meta", "session_id", sid, "err", err)
		res.Warnings = append(res.Warnings, fmt.Sprintf("reset fold meta: %v", err))
	}
	if r.rec != nil {
		r.rec.prefix.clearRequest(sid)
	}
	if r.cfg.Tasks != nil {
		// Ended-task notices belong to the history that is gone. The gate keeps
		// takeTaskNotices (turns only) out, so nothing races this.
		res.DroppedTasks = r.cfg.Tasks.TakeNotices(sid)
	}

	notice := r.newRewindNotice(plan, ut, stat, len(res.DroppedTasks))
	event, err := r.cfg.Repository.AppendEvent(wctx, store.Event{SessionID: sid, Kind: store.EventSession, Name: "rewind",
		Status: eventOK, StartedAt: time.Now().UTC(), Attrs: rewindEventAttrs(plan, stat, true)})
	if err != nil {
		slog.Warn("rewind: record event", "session_id", sid, "err", err)
	} else {
		notice.EventID = event.ID
	}
	final, err := pushRewindNotice(wctx, r.cfg.Repository, sid, notice, r.cfg.Translator)
	if err != nil {
		return fmt.Errorf("queue rewind notice: %w", err)
	}
	res.Notice = final.Text
	return nil
}

// finishFilesOnly records a file-only outcome with the same notice an :undo
// leaves (EventUndo plus the pending undo notice) when any turn had a snapshot
// directory; with none the disk did not change, so only a session event is
// written. Either way one operation leaves one event.
func (r *Runner) finishFilesOnly(ctx context.Context, core *UndoCore, plan RewindPlan, stat rewindFileStat, res *RewindResult) error {
	undo := stat.undoResult(plan.SessionID)
	if stat.turns > 0 {
		if err := core.enqueueUndoNotice(ctx, undo); err != nil {
			return fmt.Errorf("queue undo notice: %w", err)
		}
		res.Notice = undoNoticeText(undo)
		return nil
	}
	if _, err := r.cfg.Repository.AppendEvent(ctx, store.Event{SessionID: plan.SessionID, Kind: store.EventSession,
		Name: "rewind", Status: eventOK, StartedAt: time.Now().UTC(), Attrs: rewindEventAttrs(plan, stat, false)}); err != nil {
		slog.Warn("rewind: record event", "session_id", plan.SessionID, "err", err)
	}
	return nil
}

// rewindEventFiles caps the paths kept in event and record attrs.
const rewindEventFiles = 20

func rewindEntryFiles(st rewindFileStat) map[string]any {
	return map[string]any{"restored": len(st.restored), "deleted": len(st.deleted),
		"skipped": len(st.skipped), "unrecoverable": len(st.unrecoverable)}
}

func rewindEventAttrs(plan RewindPlan, st rewindFileStat, withEntrySeq bool) map[string]any {
	files := append(append([]string(nil), st.restored...), st.deleted...)
	if len(files) > rewindEventFiles {
		files = files[:rewindEventFiles]
	}
	if files == nil {
		files = []string{}
	}
	attrs := map[string]any{"target_turn": plan.TargetTurn, "scope": string(plan.Scope), "turns": st.turns,
		"restored": len(st.restored), "deleted": len(st.deleted), "skipped": len(st.skipped),
		"unrecoverable": len(st.unrecoverable), "files": files}
	if withEntrySeq {
		// Filled when the queued notice is injected (event_id backfill).
		attrs["entry_seq"] = int64(0)
	}
	return attrs
}
