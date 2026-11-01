package personal

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inhere/suga/internal/adddir"
	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// CheckpointAction is the work an undo implementation may perform for one
// snapshot. T4a only classifies records; T5 supplies the conflict checks and
// the actual file operations.
type CheckpointAction string

const (
	CheckpointActionRestore       CheckpointAction = "restore"
	CheckpointActionDelete        CheckpointAction = "delete"
	CheckpointActionSkip          CheckpointAction = "skip"
	CheckpointActionUnrecoverable CheckpointAction = "unrecoverable"
)

// CheckpointItem is the user-visible classification of one write-tool call.
type CheckpointItem struct {
	Meta       SnapshotMeta       `json:"meta"`
	Action     CheckpointAction   `json:"action"`
	RootStatus SnapshotRootStatus `json:"root_status,omitempty"`
	Reason     string             `json:"reason,omitempty"`
	// Conflict is set by Select when the target changed after the snapshot
	// (contentConflict): undo skips it unless forced, so the plan shown before
	// the confirmation lists it as skipped (design 3.4).
	Conflict bool `json:"conflict,omitempty"`
}

// ShellCommand is a redacted shell_exec event associated with a checkpoint.
// shell_exec is intentionally reported, not included in the undo operation.
type ShellCommand struct {
	Command   string    `json:"command"`
	TaskID    string    `json:"task_id,omitempty"`
	Status    string    `json:"status,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// Checkpoint describes one turn with file snapshots or shell activity. A
// shell-only turn is retained in the list so selecting the latest snapshot
// cannot silently hide an intervening command that may have changed files.
type Checkpoint struct {
	SessionID string           `json:"session_id"`
	TurnID    string           `json:"turn_id"`
	StartedAt time.Time        `json:"started_at"`
	State     SnapshotState    `json:"state,omitempty"`
	Items     []CheckpointItem `json:"items,omitempty"`
	Shell     []ShellCommand   `json:"shell,omitempty"`

	// Summary counts are deliberately derived from the directory and event
	// sources on every read; no checkpoint index is added to the database.
	Calls         int   `json:"calls"`
	Files         int   `json:"files"`
	Bytes         int64 `json:"bytes"`
	Restorable    int   `json:"restorable"`
	Deletable     int   `json:"deletable"`
	Skipped       int   `json:"skipped"`
	Unrecoverable int   `json:"unrecoverable"`
	ShellOnly     bool  `json:"shell_only"`
}

// HasSnapshots reports whether this turn can be selected as an undo target.
func (c Checkpoint) HasSnapshots() bool { return len(c.Items) > 0 }

// ShellOnly reports whether this row has shell activity but no file snapshots.
func (c Checkpoint) IsShellOnly() bool { return c.ShellOnly }

// ErrSessionPrefixAmbiguous means a session query matched more than one
// persisted session. The caller must use a longer prefix or the full ID.
var ErrSessionPrefixAmbiguous = errors.New("session prefix is ambiguous")

// ErrSessionBusy is returned when an undo/checkpoint operation cannot obtain
// the existing cross-process session lock without waiting.
var ErrSessionBusy = process.ErrSessionLocked

// SnapshotRootStatus describes whether a snapshot root is still an allowed,
// exact session root. Undo must not treat an ancestor as an allowed root.
type SnapshotRootStatus string

const (
	SnapshotRootTrusted         SnapshotRootStatus = "trusted"
	SnapshotRootCwdChanged      SnapshotRootStatus = "cwd_changed"
	SnapshotRootAddedDirRemoved SnapshotRootStatus = "added_dir_removed"
	SnapshotRootUntrusted       SnapshotRootStatus = "untrusted"
)

// UndoGate is the in-process session gate owned by a Runner. The selected
// session ID is carried in the context because the existing gate is per-session.
type UndoGate interface {
	Acquire(context.Context) (func(), error)
	// AcquireUnlessBusy is Acquire that fails with ErrRewindBusy while a
	// non-preemptible holder (a turn) has the gate; see sessionGate.
	AcquireUnlessBusy(context.Context) (func(), error)
}

// UndoCore owns the provider-independent checkpoint boundary. It only reads
// the store/snapshot directories and serializes access with the existing gate
// and run/locks session lock; file restoration is added by later undo tasks.
type UndoCore struct {
	Repository   *store.Store
	Snapshots    *SessionSnapshots
	SessionsRoot string
	LockRoot     string
	Gate         UndoGate
	HeldLock     func(sessionID string) bool
}

// NewUndoCore constructs the provider-independent checkpoint core.
func NewUndoCore(repo *store.Store, snapshots *SessionSnapshots, sessionsRoot, lockRoot string) *UndoCore {
	return &UndoCore{Repository: repo, Snapshots: snapshots, SessionsRoot: sessionsRoot, LockRoot: lockRoot}
}

func (u *UndoCore) validate() error {
	if u == nil || u.Repository == nil || u.Snapshots == nil {
		return errors.New("undo core is not configured")
	}
	return nil
}

// Checkpoints lists checkpoints while holding the session gate first and the
// existing cross-process lock second. A held chat lock is reused rather than
// acquired a second time, which is important on Windows as well as Unix.
func (u *UndoCore) Checkpoints(ctx context.Context, sessionQuery string, limit int) ([]Checkpoint, error) {
	if err := u.validate(); err != nil {
		return nil, err
	}
	sessionID, err := ResolveSessionID(ctx, u.Repository, sessionQuery)
	if err != nil {
		return nil, err
	}
	var out []Checkpoint
	err = u.withSession(ctx, sessionID, func() error {
		out, err = u.checkpointsLocked(ctx, sessionID, limit)
		return err
	})
	return out, err
}

// Select returns the latest n undo candidates with shell-only turns retained.
func (u *UndoCore) Select(ctx context.Context, sessionQuery string, n int) ([]Checkpoint, error) {
	if err := u.validate(); err != nil {
		return nil, err
	}
	sessionID, err := ResolveSessionID(ctx, u.Repository, sessionQuery)
	if err != nil {
		return nil, err
	}
	var out []Checkpoint
	err = u.withSession(ctx, sessionID, func() error {
		all, listErr := u.checkpointsLocked(ctx, sessionID, 0)
		if listErr != nil {
			return listErr
		}
		if n <= 0 {
			n = 1
		}
		selected := 0
		for _, row := range all {
			if row.IsShellOnly() {
				out = append(out, row)
				continue
			}
			if row.State == SnapshotStateUndone {
				continue
			}
			for i := range row.Items {
				row.Items[i].Conflict = contentConflict(row.Items[i])
			}
			out = append(out, row)
			selected++
			if selected == n {
				break
			}
		}
		return nil
	})
	return out, err
}

func (u *UndoCore) checkpointsLocked(ctx context.Context, sessionID string, limit int) ([]Checkpoint, error) {
	rows, err := ListCheckpoints(ctx, u.Repository, u.Snapshots, sessionID, limit)
	if err != nil {
		return nil, err
	}
	session, err := u.Repository.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if err := u.annotateRoots(ctx, session, &rows[i]); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (u *UndoCore) withSession(ctx context.Context, sessionID string, fn func() error) error {
	if u.Gate != nil {
		gateCtx := context.WithValue(ctx, undoSessionIDKey{}, sessionID)
		release, err := u.Gate.Acquire(gateCtx)
		if err != nil {
			return err
		}
		defer release()
	}
	if u.LockRoot != "" && (u.HeldLock == nil || !u.HeldLock(sessionID)) {
		lock, err := process.AcquireSessionLockContext(ctx, u.LockRoot, sessionID, 0)
		if err != nil {
			if errors.Is(err, process.ErrSessionLocked) {
				return fmt.Errorf("%w: %s", ErrSessionBusy, sessionID)
			}
			return err
		}
		defer lock.Close()
	}
	return fn()
}

// withSessionUnlessBusy is withSession for rewind/fork: same gate-then-file-lock
// order, but the gate is taken with AcquireUnlessBusy and a held cross-process
// lock is reported as ErrRewindBusy. Never call withSession inside fn: the gate
// is not reentrant. A nil Gate (CLI) skips the in-process gate.
func (u *UndoCore) withSessionUnlessBusy(ctx context.Context, sessionID string, fn func() error) error {
	if u.Gate != nil {
		gateCtx := context.WithValue(ctx, undoSessionIDKey{}, sessionID)
		release, err := u.Gate.AcquireUnlessBusy(gateCtx)
		if err != nil {
			return err
		}
		defer release()
	}
	if u.LockRoot != "" && (u.HeldLock == nil || !u.HeldLock(sessionID)) {
		lock, err := process.AcquireSessionLockContext(ctx, u.LockRoot, sessionID, 0)
		if err != nil {
			if errors.Is(err, process.ErrSessionLocked) {
				return fmt.Errorf("%w: %s", ErrRewindBusy, sessionID)
			}
			return err
		}
		defer lock.Close()
	}
	return fn()
}

func (u *UndoCore) annotateRoots(ctx context.Context, session store.Session, row *Checkpoint) error {
	events, err := u.Repository.ListEvents(ctx, store.EventFilter{SessionID: session.ID})
	if err != nil {
		return fmt.Errorf("list root history: %w", err)
	}
	for i := range row.Items {
		status := snapshotRootStatus(session, row.Items[i].Meta, events)
		row.Items[i].RootStatus = status
		if status == SnapshotRootTrusted || row.Items[i].Action == CheckpointActionUnrecoverable {
			continue
		}
		previous := row.Items[i].Action
		row.Items[i].Action = CheckpointActionSkip
		row.Items[i].Reason = snapshotRootReason(status)
		if previous == CheckpointActionRestore || previous == CheckpointActionDelete {
			row.Skipped++
			if previous == CheckpointActionDelete {
				row.Deletable--
			} else {
				row.Restorable--
			}
		}
	}
	return nil
}

// snapshotRootStatus accepts only exact roots currently recorded as the session
// cwd or as a persistent writable additional directory. A current process cwd
// or a lexical ancestor is never sufficient.
func snapshotRootStatus(session store.Session, meta SnapshotMeta, events []store.Event) SnapshotRootStatus {
	root := strings.TrimSpace(meta.Root)
	if root == "" || !filepath.IsAbs(root) {
		return SnapshotRootUntrusted
	}
	if SameDir(root, session.Cwd) && session.Cwd != "" {
		return SnapshotRootTrusted
	}
	if dirs, err := adddir.FromMeta(session.Meta[adddir.MetaKey]); err == nil {
		for _, dir := range dirs {
			if dir.Writable && SameDir(root, dir.Path) {
				return SnapshotRootTrusted
			}
		}
	}
	for _, event := range events {
		if event.Kind != store.EventAddDir || event.Name != "remove" {
			continue
		}
		if path, _ := event.Attrs["path"].(string); path != "" && SameDir(root, path) {
			return SnapshotRootAddedDirRemoved
		}
	}
	if session.Cwd == "" {
		return SnapshotRootUntrusted
	}
	return SnapshotRootCwdChanged
}

func snapshotRootReason(status SnapshotRootStatus) string {
	switch status {
	case SnapshotRootCwdChanged:
		return "会话目录已变更，无法恢复"
	case SnapshotRootAddedDirRemoved:
		return "附加目录已移除，无法恢复"
	default:
		return "快照根目录不可信，无法恢复"
	}
}

// ResolveSessionID accepts a complete ID or a unique prefix. Exact matches win
// so a full ID remains usable even if another session starts with the same text.
func ResolveSessionID(ctx context.Context, repo *store.Store, query string) (string, error) {
	if repo == nil {
		return "", errors.New("personal store is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("%w: empty session id", store.ErrSessionNotFound)
	}
	if session, err := repo.GetSession(ctx, query); err == nil {
		return session.ID, nil
	} else if !errors.Is(err, store.ErrSessionNotFound) {
		return "", err
	}

	sessions, err := repo.ListSessions(ctx)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, session := range sessions {
		if strings.HasPrefix(session.ID, query) {
			matches = append(matches, session.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: %s", store.ErrSessionNotFound, query)
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("%w %q: %s", ErrSessionPrefixAmbiguous, query, strings.Join(matches, ", "))
	}
}

// ListCheckpoints returns all turns that have either a file snapshot or a
// shell_exec event, newest first. limit applies to displayed rows; zero means
// no limit. The turn order is the database's started_at,id order, reversed
// here rather than inferred from snapshot directory names.
func ListCheckpoints(ctx context.Context, repo *store.Store, snapshots *SessionSnapshots, sessionQuery string, limit int) ([]Checkpoint, error) {
	if repo == nil || snapshots == nil {
		return nil, errors.New("checkpoint store is nil")
	}
	sessionID, err := ResolveSessionID(ctx, repo, sessionQuery)
	if err != nil {
		return nil, err
	}
	turns, err := repo.ListTurns(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list checkpoint turns: %w", err)
	}
	events, err := repo.ListEvents(ctx, store.EventFilter{SessionID: sessionID})
	if err != nil {
		return nil, fmt.Errorf("list checkpoint events: %w", err)
	}
	shellByTurn := shellEventsByTurn(events)

	rows := make([]Checkpoint, 0, len(turns))
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		metas, err := snapshots.LoadTurn(sessionID, turn.ID)
		if err != nil {
			return nil, fmt.Errorf("load checkpoint %s: %w", turn.ID, err)
		}
		shell := shellByTurn[turn.ID]
		if len(metas) == 0 && len(shell) == 0 {
			continue
		}
		row, err := checkpointFromTurn(sessionID, turn, metas, shell, snapshots)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		if limit > 0 && len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

// SelectUndoCheckpoints selects the latest n turns that have snapshots and are
// not already undone. Shell-only rows encountered while scanning are included
// before the selected rows, making the omission visible to chat and CLI.
func SelectUndoCheckpoints(ctx context.Context, repo *store.Store, snapshots *SessionSnapshots, sessionQuery string, n int) ([]Checkpoint, error) {
	if n <= 0 {
		n = 1
	}
	all, err := ListCheckpoints(ctx, repo, snapshots, sessionQuery, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Checkpoint, 0, n)
	selected := 0
	for _, row := range all {
		if row.IsShellOnly() {
			out = append(out, row)
			continue
		}
		if row.State == SnapshotStateUndone {
			continue
		}
		out = append(out, row)
		selected++
		if selected == n {
			break
		}
	}
	return out, nil
}

// checkpointFromTurn turns the two authoritative stores into a display model.
// Snapshot directories are the source of file facts; events are only the source
// of shell facts, so a future change to event argument rendering cannot invent
// a synthetic shell snapshot.
func checkpointFromTurn(sessionID string, turn store.Turn, metas []SnapshotMeta, shell []ShellCommand, snapshots *SessionSnapshots) (Checkpoint, error) {
	state := SnapshotStateOpen
	if len(metas) > 0 {
		loaded, err := snapshots.LoadCheckpointState(sessionID, turn.ID)
		if err != nil {
			return Checkpoint{}, fmt.Errorf("load checkpoint state %s: %w", turn.ID, err)
		}
		state = loaded.State
	}
	row := Checkpoint{SessionID: sessionID, TurnID: turn.ID, StartedAt: turn.StartedAt, State: state, Shell: shell}
	for _, meta := range metas {
		item := CheckpointItem{Meta: meta, Action: classifySnapshot(meta)}
		item.Reason = snapshotReason(meta, item.Action)
		row.Items = append(row.Items, item)
		row.Calls++
		row.Bytes += meta.Size
		if meta.Kind != native.SnapshotKindPurged {
			row.Files++
		}
		switch item.Action {
		case CheckpointActionRestore:
			row.Restorable++
		case CheckpointActionDelete:
			row.Deletable++
		case CheckpointActionUnrecoverable:
			row.Unrecoverable++
		default:
			row.Skipped++
		}
	}
	row.ShellOnly = len(row.Items) == 0 && len(row.Shell) > 0
	return row, nil
}

func classifySnapshot(meta SnapshotMeta) CheckpointAction {
	if meta.Unrecoverable != "" || meta.Kind == native.SnapshotKindPurged {
		return CheckpointActionUnrecoverable
	}
	if meta.Status == "pending" {
		return CheckpointActionSkip
	}
	switch meta.Kind {
	case native.SnapshotKindFile, native.SnapshotKindTrash:
		return CheckpointActionRestore
	case native.SnapshotKindAbsent:
		return CheckpointActionDelete
	default:
		return CheckpointActionSkip
	}
}

func snapshotReason(meta SnapshotMeta, action CheckpointAction) string {
	if meta.Unrecoverable != "" {
		return meta.Unrecoverable
	}
	switch action {
	case CheckpointActionDelete:
		return "本回合新建的文件"
	case CheckpointActionUnrecoverable:
		return "文件已永久删除或快照不可恢复"
	case CheckpointActionSkip:
		if meta.Status == "pending" {
			return "快照尚未完成"
		}
		return "快照记录不完整"
	}
	return ""
}

func shellEventsByTurn(events []store.Event) map[string][]ShellCommand {
	byTurn := map[string][]ShellCommand{}
	for _, event := range events {
		if event.Kind != store.EventTool || event.Name != "shell_exec" || event.TurnID == "" {
			continue
		}
		command := shellEventCommand(event.Attrs)
		if command == "" {
			continue
		}
		byTurn[event.TurnID] = append(byTurn[event.TurnID], ShellCommand{
			Command: redact.Secrets(command), TaskID: shellEventString(event.Attrs, "task_id"),
			Status: event.Status, StartedAt: event.StartedAt,
		})
	}
	for turnID, commands := range byTurn {
		if len(commands) > 5 {
			byTurn[turnID] = commands[:5]
		}
	}
	return byTurn
}

func shellEventCommand(attrs map[string]any) string {
	for _, key := range []string{"command", "arg"} {
		if command := shellEventString(attrs, key); command != "" {
			return command
		}
	}
	return ""
}

func shellEventString(attrs map[string]any, key string) string {
	value, _ := attrs[key].(string)
	return strings.TrimSpace(value)
}
