package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

const (
	DefaultSnapshotKeepTurns = 20
	DefaultSnapshotKeepDays  = 7
	snapshotSourcePreUndo    = "pre-undo"
	snapshotStateFile        = "state.json"
	snapshotPreUndoDir       = "pre-undo"
)

// SnapshotState is the lifecycle state of one turn checkpoint.
type SnapshotState string

const (
	SnapshotStateOpen    SnapshotState = "open"
	SnapshotStateUndone  SnapshotState = "undone"
	SnapshotStatePartial SnapshotState = "partial"
)

// SnapshotCheckpointState is stored as state.json beside a turn's call records.
type SnapshotCheckpointState struct {
	State     SnapshotState `json:"state"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// SnapshotCleanupReport describes checkpoint directories removed by cleanup.
type SnapshotCleanupReport struct {
	RemovedTurns int
	RemovedBytes int64
	// SkippedRecords counts call records left unreadable because their
	// meta.json was missing or corrupt.
	SkippedRecords int
	// RepairedPartials counts stale half-written call directories removed by
	// cleanup.
	RepairedPartials int
}

type snapshotCheckpoint struct {
	sessionID string
	turnID    string
	dir       string
	state     SnapshotState
	createdAt time.Time
	bytes     int64
	eligible  bool
}

func snapshotLimitsWithRetention(l SnapshotLimits) SnapshotLimits {
	if l.KeepTurns <= 0 {
		l.KeepTurns = DefaultSnapshotKeepTurns
	}
	if l.KeepDays <= 0 {
		l.KeepDays = DefaultSnapshotKeepDays
	}
	return l
}

// LoadCheckpointState reads a turn's state. Old snapshot directories without a
// state file are treated as open, which keeps pre-T3b sessions readable.
func (s *SessionSnapshots) LoadCheckpointState(sessionID, turnID string) (SnapshotCheckpointState, error) {
	if s == nil {
		return SnapshotCheckpointState{}, errors.New("snapshots: store is nil")
	}
	if err := validateSnapshotID(sessionID, "session"); err != nil {
		return SnapshotCheckpointState{}, err
	}
	if err := validateSnapshotID(turnID, "turn"); err != nil {
		return SnapshotCheckpointState{}, err
	}
	turnDir := filepath.Join(s.dir, sessionID, "snapshots", turnID)
	if info, err := os.Lstat(turnDir); err != nil {
		return SnapshotCheckpointState{}, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return SnapshotCheckpointState{}, errors.New("snapshots: checkpoint is not a directory")
	}
	path := filepath.Join(turnDir, snapshotStateFile)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return SnapshotCheckpointState{}, errors.New("snapshots: checkpoint state is a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return SnapshotCheckpointState{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(filepath.Dir(path)); statErr != nil {
			return SnapshotCheckpointState{}, statErr
		}
		return SnapshotCheckpointState{State: SnapshotStateOpen}, nil
	}
	if err != nil {
		return SnapshotCheckpointState{}, err
	}
	var state SnapshotCheckpointState
	if err := json.Unmarshal(data, &state); err != nil {
		return SnapshotCheckpointState{}, fmt.Errorf("snapshots: read state: %w", err)
	}
	if !validSnapshotState(state.State) {
		return SnapshotCheckpointState{}, fmt.Errorf("snapshots: invalid checkpoint state %q", state.State)
	}
	return state, nil
}

// SetCheckpointState changes the lifecycle state without touching call records.
func (s *SessionSnapshots) SetCheckpointState(sessionID, turnID string, state SnapshotState) error {
	if s == nil {
		return errors.New("snapshots: store is nil")
	}
	if err := validateSnapshotID(sessionID, "session"); err != nil {
		return err
	}
	if err := validateSnapshotID(turnID, "turn"); err != nil {
		return err
	}
	if !validSnapshotState(state) {
		return fmt.Errorf("snapshots: invalid checkpoint state %q", state)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.now == nil {
		s.now = time.Now
	}
	path := filepath.Join(s.dir, sessionID, "snapshots", turnID)
	if info, err := os.Lstat(path); err != nil {
		return err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("snapshots: checkpoint is not a directory")
	}
	return writeCheckpointState(path, SnapshotCheckpointState{State: state, UpdatedAt: s.now().UTC()})
}

func validSnapshotState(state SnapshotState) bool {
	return state == SnapshotStateOpen || state == SnapshotStateUndone || state == SnapshotStatePartial
}

func writeCheckpointState(dir string, state SnapshotCheckpointState) error {
	path := filepath.Join(dir, snapshotStateFile)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("snapshots: checkpoint state is a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// BeforeUndo stores the state immediately before a future undo changes it.
// Pre-undo records are deliberately outside LoadTurn's normal call list.
func (s *SessionSnapshots) BeforeUndo(ctx context.Context, req native.SnapshotRequest) (native.SnapshotBefore, error) {
	if s == nil {
		return native.SnapshotBefore{}, errors.New("snapshots: store is nil")
	}
	if err := ctx.Err(); err != nil {
		return native.SnapshotBefore{}, err
	}
	sid, tid, err := s.scopeIDs(ctx)
	if err != nil {
		return native.SnapshotBefore{}, err
	}
	if req.CallID == "" {
		return native.SnapshotBefore{}, errors.New("snapshots: call id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turnDir := filepath.Join(s.dir, sid, "snapshots", tid)
	if info, err := os.Lstat(turnDir); err != nil {
		return native.SnapshotBefore{}, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return native.SnapshotBefore{}, errors.New("snapshots: checkpoint is not a directory")
	}
	preDir := filepath.Join(turnDir, snapshotPreUndoDir)
	if info, err := os.Lstat(preDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return native.SnapshotBefore{}, errors.New("snapshots: pre-undo path is not a directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return native.SnapshotBefore{}, err
	} else if err := os.MkdirAll(preDir, 0o700); err != nil {
		return native.SnapshotBefore{}, fmt.Errorf("snapshots: create pre-undo directory: %w", err)
	}
	seq, err := nextSnapshotSeq(preDir)
	if err != nil {
		return native.SnapshotBefore{}, fmt.Errorf("snapshots: choose pre-undo sequence: %w", err)
	}
	name := fmt.Sprintf("%03d-%s", seq, snapshotCallHash(req.CallID))
	callDir := filepath.Join(preDir, name)
	if _, err := os.Lstat(callDir); err == nil {
		return native.SnapshotBefore{}, errors.New("snapshots: pre-undo record already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return native.SnapshotBefore{}, err
	}
	meta := SnapshotMeta{
		SessionID: sid, TurnID: tid, CallID: req.CallID, Seq: seq, Tool: req.Tool,
		Root: req.Root, Rel: filepath.ToSlash(req.Rel), RealRel: filepath.ToSlash(req.RealRel),
		Kind: req.Kind, Size: int64(len(req.Before)), Status: snapshotStatusPending,
		Mode: snapshotMode(req), CreatedAt: s.nowTime().UTC(), Source: snapshotSourcePreUndo,
	}
	if req.Kind == native.SnapshotKindFile {
		meta.SHA256Before = nativeSHA256(req.Before)
		meta.File = "before"
	}
	if int64(len(req.Before)) > s.limits.MaxFileBytes {
		meta.Unrecoverable = native.SnapshotUnrecoverableOversize
	} else if err := s.checkCapacity(sid, int64(len(req.Before))); err != nil {
		return native.SnapshotBefore{}, err
	}
	// Publish the record the same way BeforeCall does: the files go into a hidden
	// partial directory and the rename makes it visible.
	partialDir := filepath.Join(preDir, snapshotPartialDirName(name))
	if err := os.MkdirAll(partialDir, 0o700); err != nil {
		return native.SnapshotBefore{}, fmt.Errorf("snapshots: create pre-undo record: %w", err)
	}
	if meta.Unrecoverable == "" && req.Kind == native.SnapshotKindFile {
		if err := os.WriteFile(filepath.Join(partialDir, "before"), req.Before, 0o600); err != nil {
			_ = os.RemoveAll(partialDir)
			return native.SnapshotBefore{}, fmt.Errorf("snapshots: write pre-undo before: %w", err)
		}
	}
	if err := writeSnapshotMetaFile(filepath.Join(partialDir, "meta.json"), meta); err != nil {
		_ = os.RemoveAll(partialDir)
		return native.SnapshotBefore{}, fmt.Errorf("snapshots: write pre-undo metadata: %w", err)
	}
	if err := os.Rename(partialDir, callDir); err != nil {
		_ = os.RemoveAll(partialDir)
		return native.SnapshotBefore{}, fmt.Errorf("snapshots: publish pre-undo record: %w", err)
	}
	if meta.Unrecoverable != "" {
		return native.SnapshotBefore{Unrecoverable: meta.Unrecoverable, Notice: "本次回滚前状态不可保存：快照超过单文件上限（16 MiB）"}, nil
	}
	return native.SnapshotBefore{}, nil
}

// AfterUndo completes the pending pre-undo record.
func (s *SessionSnapshots) AfterUndo(ctx context.Context, result native.SnapshotResult) error {
	if s == nil {
		return errors.New("snapshots: store is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sid, tid, err := s.scopeIDs(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	turnDir := filepath.Join(s.dir, sid, "snapshots", tid, snapshotPreUndoDir)
	metaPath, meta, err := findPendingSnapshot(turnDir, result.CallID)
	if err != nil {
		return err
	}
	if result.Status != native.SnapshotStatusComplete && result.Status != native.SnapshotStatusIncomplete {
		return fmt.Errorf("snapshots: invalid result status %q", result.Status)
	}
	meta.Status = result.Status
	if result.Root != "" {
		meta.Root = result.Root
	}
	if result.Rel != "" {
		meta.Rel = filepath.ToSlash(result.Rel)
	}
	if result.RealRel != "" {
		meta.RealRel = filepath.ToSlash(result.RealRel)
	}
	if result.Kind != "" {
		meta.Kind = result.Kind
	}
	if result.Size > 0 || meta.Size == 0 {
		meta.Size = result.Size
	}
	meta.SHA256After = result.AfterSHA256
	if result.Unrecoverable != "" {
		meta.Unrecoverable = result.Unrecoverable
	}
	meta.TrashEntries = cloneStrings(result.TrashEntries)
	meta.PurgedDirs = cloneStrings(result.PurgedDirs)
	if err := writeSnapshotMetaFile(metaPath, meta); err != nil {
		return fmt.Errorf("snapshots: update pre-undo metadata: %w", err)
	}
	return nil
}

func writeSnapshotMetaFile(path string, meta SnapshotMeta) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("snapshots: metadata path is a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func (s *SessionSnapshots) nowTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// CleanupSession applies keep_turns, keep_days and the session byte limit.
// Pending or incomplete call records are never removed as cleanup cannot prove them safe.
func (s *SessionSnapshots) CleanupSession(sessionID string) (SnapshotCleanupReport, error) {
	if s == nil {
		return SnapshotCleanupReport{}, errors.New("snapshots: store is nil")
	}
	if err := validateSnapshotID(sessionID, "session"); err != nil {
		return SnapshotCleanupReport{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupSessionLocked(sessionID, "", 0)
}

// CleanupAll applies per-session retention and then the global byte limit.
// It is intended for startup and explicit GC, where no active turn is known.
func (s *SessionSnapshots) CleanupAll() (SnapshotCleanupReport, error) {
	if s == nil {
		return SnapshotCleanupReport{}, errors.New("snapshots: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var report SnapshotCleanupReport
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return report, fmt.Errorf("snapshots: session %q is a symlink", entry.Name())
		}
		if !entry.IsDir() {
			continue
		}
		if err := validateSnapshotID(entry.Name(), "session"); err != nil {
			return report, err
		}
		part, err := s.cleanupSessionLocked(entry.Name(), "", 0)
		if err != nil {
			return report, err
		}
		report = addCleanupReports(report, part)
	}
	for {
		bytes, err := beforeBytes(s.dir)
		if err != nil {
			return report, err
		}
		if bytes <= s.limits.MaxGlobalBytes {
			return report, nil
		}
		candidates, err := s.collectEligibleCheckpoints()
		if err != nil {
			return report, err
		}
		if len(candidates) == 0 {
			return report, nil
		}
		removed, err := s.removeCheckpoint(candidates[0])
		if err != nil {
			return report, err
		}
		report = addCleanupReports(report, removed)
	}
}

func (s *SessionSnapshots) cleanupSessionLocked(sessionID, preserveTurn string, required int64) (SnapshotCleanupReport, error) {
	limits := snapshotLimitsWithRetention(s.limits)
	checkpoints, skippedRecords, err := s.collectSessionCheckpoints(sessionID)
	if err != nil {
		return SnapshotCleanupReport{}, err
	}
	var report SnapshotCleanupReport
	report.SkippedRecords = skippedRecords
	// Repair half-written records of this session first: a stale partial
	// directory is a writer that died, so removing it does not race a live call.
	turnDirs := make([]string, 0, len(checkpoints))
	for _, cp := range checkpoints {
		turnDirs = append(turnDirs, cp.dir)
	}
	if preserveTurn != "" {
		turnDirs = append(turnDirs, filepath.Join(s.dir, sessionID, "snapshots", preserveTurn))
	}
	for _, dir := range turnDirs {
		repaired, err := s.prunePartialDirs(dir)
		if err != nil {
			return report, err
		}
		report.RepairedPartials += repaired
	}
	cutoff := s.nowTime().Add(-time.Duration(limits.KeepDays) * 24 * time.Hour)
	completed := make([]snapshotCheckpoint, 0, len(checkpoints))
	keepTurns := limits.KeepTurns
	// Before a new turn is opened, reserve one retention slot for it.
	if preserveTurn != "" && keepTurns > 0 {
		keepTurns--
	}
	for _, cp := range checkpoints {
		if cp.turnID == preserveTurn || !cp.eligible {
			continue
		}
		completed = append(completed, cp)
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].createdAt.Before(completed[j].createdAt) })
	removeBefore := len(completed) - keepTurns
	if removeBefore < 0 {
		removeBefore = 0
	}
	for i, cp := range completed {
		if i < removeBefore || cp.createdAt.Before(cutoff) {
			removed, err := s.removeCheckpoint(cp)
			if err != nil {
				return report, err
			}
			report = addCleanupReports(report, removed)
		}
	}
	for {
		bytes, err := beforeBytes(filepath.Join(s.dir, sessionID, "snapshots"))
		if err != nil {
			return report, err
		}
		if bytes+required <= limits.MaxSessionBytes {
			return report, nil
		}
		candidates, skipped, err := s.collectSessionCheckpoints(sessionID)
		if err != nil {
			return report, err
		}
		report.SkippedRecords += skipped
		var oldest *snapshotCheckpoint
		for i := range candidates {
			cp := &candidates[i]
			if cp.turnID != preserveTurn && cp.eligible && (oldest == nil || cp.createdAt.Before(oldest.createdAt)) {
				oldest = cp
			}
		}
		if oldest == nil {
			return report, nil
		}
		removed, err := s.removeCheckpoint(*oldest)
		if err != nil {
			return report, err
		}
		report = addCleanupReports(report, removed)
	}
}

func (s *SessionSnapshots) collectSessionCheckpoints(sessionID string) ([]snapshotCheckpoint, int, error) {
	root := filepath.Join(s.dir, sessionID, "snapshots")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var result []snapshotCheckpoint
	skipped := 0
	for _, entry := range entries {
		if entry.Name() == snapshotPreUndoDir {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, 0, fmt.Errorf("snapshots: checkpoint %q is a symlink", entry.Name())
		}
		if !entry.IsDir() {
			continue
		}
		if err := validateSnapshotID(entry.Name(), "turn"); err != nil {
			return nil, 0, err
		}
		dir := filepath.Join(root, entry.Name())
		info, err := os.Lstat(dir)
		if err != nil {
			return nil, 0, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, 0, fmt.Errorf("snapshots: checkpoint %q is a symlink", entry.Name())
		}
		loaded, err := loadTurn(dir)
		if err != nil {
			return nil, 0, err
		}
		metas := loaded.Metas
		skipped += len(loaded.Skipped)
		state, err := s.LoadCheckpointState(sessionID, entry.Name())
		if err != nil {
			return nil, 0, err
		}
		created := info.ModTime()
		complete := len(metas) > 0
		for _, meta := range metas {
			if !meta.CreatedAt.IsZero() && (created.IsZero() || meta.CreatedAt.Before(created)) {
				created = meta.CreatedAt
			}
			if meta.Status != native.SnapshotStatusComplete {
				complete = false
			}
		}
		bytes, err := snapshotBytesForCheckpoint(dir)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, snapshotCheckpoint{
			sessionID: sessionID, turnID: entry.Name(), dir: dir, state: state.State,
			createdAt: created, bytes: bytes,
			eligible: complete && (state.State == SnapshotStateOpen || state.State == SnapshotStateUndone),
		})
	}
	return result, skipped, nil
}

// prunePartialDirs repairs the half-written call records of one turn: hidden
// ".partial-" directories of a writer that died, and published records whose
// meta.json never made it to disk. Both are removed only once they are older than
// snapshotPartialStaleAfter, so a writer that is still running (possibly in
// another process) is never disturbed.
func (s *SessionSnapshots) prunePartialDirs(turnDir string) (int, error) {
	removed := 0
	cutoff := s.nowTime().Add(-snapshotPartialStaleAfter)
	for _, dir := range []string{turnDir, filepath.Join(turnDir, snapshotPreUndoDir)} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == snapshotPreUndoDir {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			partial := strings.HasPrefix(entry.Name(), snapshotPartialPrefix)
			if !partial && !snapshotRecordIncomplete(path) {
				continue
			}
			info, err := entry.Info()
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return removed, err
			}
			if info.ModTime().After(cutoff) {
				continue // the writer is probably still running
			}
			if err := os.RemoveAll(path); err != nil {
				return removed, err
			}
			removed++
			slog.Warn("snapshots: removed stale half-written call record", "dir", path)
		}
	}
	return removed, nil
}

// snapshotRecordIncomplete reports whether a published call directory has no
// meta.json, the file that makes a record readable. A record without it cannot
// be restored from, so cleanup treats it as the leftover of a crash.
func snapshotRecordIncomplete(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "meta.json"))
	return errors.Is(err, os.ErrNotExist)
}

func (s *SessionSnapshots) collectEligibleCheckpoints() ([]snapshotCheckpoint, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []snapshotCheckpoint
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := validateSnapshotID(entry.Name(), "session"); err != nil {
			return nil, err
		}
		checkpoints, _, err := s.collectSessionCheckpoints(entry.Name())
		if err != nil {
			return nil, err
		}
		for _, cp := range checkpoints {
			if cp.eligible {
				result = append(result, cp)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].createdAt.Before(result[j].createdAt) })
	return result, nil
}

func (s *SessionSnapshots) removeCheckpoint(cp snapshotCheckpoint) (SnapshotCleanupReport, error) {
	if cp.dir == "" {
		return SnapshotCleanupReport{}, errors.New("snapshots: empty checkpoint path")
	}
	if err := os.RemoveAll(cp.dir); err != nil {
		return SnapshotCleanupReport{}, err
	}
	return SnapshotCleanupReport{RemovedTurns: 1, RemovedBytes: cp.bytes}, nil
}

func snapshotBytesForCheckpoint(dir string) (int64, error) {
	return beforeBytes(dir)
}

func addCleanupReports(a, b SnapshotCleanupReport) SnapshotCleanupReport {
	a.RemovedTurns += b.RemovedTurns
	a.RemovedBytes += b.RemovedBytes
	a.SkippedRecords += b.SkippedRecords
	a.RepairedPartials += b.RepairedPartials
	return a
}
