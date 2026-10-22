package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

const (
	// DefaultSnapshotMaxFileBytes is the largest before-image kept for one call.
	DefaultSnapshotMaxFileBytes int64 = 16 << 20
	// DefaultSnapshotMaxSessionBytes bounds before-images in one session.
	DefaultSnapshotMaxSessionBytes int64 = 256 << 20
	// DefaultSnapshotMaxGlobalBytes bounds before-images below the sessions root.
	DefaultSnapshotMaxGlobalBytes int64 = 1 << 30

	snapshotStatusPending = "pending"
	snapshotSourceCall    = "call"

	// snapshotPartialPrefix names the in-progress directory of one call record.
	// It is renamed to its final "<seq>-<hash>" name once meta.json is on disk,
	// so a reader never sees a call directory without metadata.
	snapshotPartialPrefix = ".partial-"
	// snapshotPartialStaleAfter is how old an in-progress directory must be before
	// cleanup treats it as a leftover of a writer that crashed or was killed.
	snapshotPartialStaleAfter = 30 * time.Minute
	// snapshotPublishAttempts bounds the retries of a publish that lost a
	// sequence number to a writer in another process.
	snapshotPublishAttempts = 5
)

// SnapshotLimits controls the amount of before-image data retained by a
// SessionSnapshots. Non-positive values use the corresponding default.
type SnapshotLimits struct {
	MaxFileBytes    int64
	MaxSessionBytes int64
	MaxGlobalBytes  int64
	KeepTurns       int
	KeepDays        int
}

func defaultSnapshotLimits() SnapshotLimits {
	return SnapshotLimits{
		MaxFileBytes:    DefaultSnapshotMaxFileBytes,
		MaxSessionBytes: DefaultSnapshotMaxSessionBytes,
		MaxGlobalBytes:  DefaultSnapshotMaxGlobalBytes,
		KeepTurns:       DefaultSnapshotKeepTurns,
		KeepDays:        DefaultSnapshotKeepDays,
	}
}

func (l SnapshotLimits) withDefaults() SnapshotLimits {
	defaults := defaultSnapshotLimits()
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = defaults.MaxFileBytes
	}
	if l.MaxSessionBytes <= 0 {
		l.MaxSessionBytes = defaults.MaxSessionBytes
	}
	if l.MaxGlobalBytes <= 0 {
		l.MaxGlobalBytes = defaults.MaxGlobalBytes
	}
	if l.KeepTurns <= 0 {
		l.KeepTurns = DefaultSnapshotKeepTurns
	}
	if l.KeepDays <= 0 {
		l.KeepDays = DefaultSnapshotKeepDays
	}
	return l
}

// SnapshotMeta is the self-describing record stored beside one before-image.
// The byte contents are deliberately kept in before, not in this JSON file.
type SnapshotMeta struct {
	SessionID     string    `json:"session_id"`
	TurnID        string    `json:"turn_id"`
	CallID        string    `json:"call_id"`
	Seq           int       `json:"seq"`
	Tool          string    `json:"tool"`
	Root          string    `json:"root"`
	Rel           string    `json:"rel"`
	RealRel       string    `json:"real_rel"`
	Kind          string    `json:"kind"`
	Size          int64     `json:"size"`
	SHA256Before  string    `json:"sha256_before,omitempty"`
	SHA256After   string    `json:"sha256_after,omitempty"`
	Status        string    `json:"status"`
	Mode          string    `json:"mode,omitempty"`
	File          string    `json:"file,omitempty"`
	TrashDir      string    `json:"trash_dir,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	TrashEntries  []string  `json:"trash_entries,omitempty"`
	PurgedDirs    []string  `json:"purged_dirs,omitempty"`
	Source        string    `json:"source"`
	Unrecoverable string    `json:"unrecoverable,omitempty"`
}

// SessionSnapshots stores file-tool before-images below one sessions root. It
// implements native.Snapshotter without exposing session storage to native.
type SessionSnapshots struct {
	dir    string
	limits SnapshotLimits
	mu     sync.Mutex
	now    func() time.Time
}

var _ native.Snapshotter = (*SessionSnapshots)(nil)

// NewSessionSnapshots creates a snapshot store with the design defaults.
func NewSessionSnapshots(sessionsDir string) *SessionSnapshots {
	return NewSessionSnapshotsWithLimits(sessionsDir, SnapshotLimits{})
}

// NewSessionSnapshotsWithLimits creates a snapshot store with explicit limits.
func NewSessionSnapshotsWithLimits(sessionsDir string, limits SnapshotLimits) *SessionSnapshots {
	dir := strings.TrimSpace(sessionsDir)
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	return &SessionSnapshots{dir: dir, limits: limits.withDefaults(), now: time.Now}
}

// SessionSnapshotsDir returns the snapshot directory of a session. It performs
// no I/O and is useful to callers that need to inspect the on-disk layout.
func SessionSnapshotsDir(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, sessionID, "snapshots")
}

// Limits returns the effective limits used by the store.
func (s *SessionSnapshots) Limits() SnapshotLimits {
	if s == nil {
		return defaultSnapshotLimits()
	}
	return s.limits
}

// BeforeCall persists the before-image before the native tool changes its
// target. Oversize images get metadata but no before file, allowing the write
// to continue with an explicit non-reversible result.
func (s *SessionSnapshots) BeforeCall(ctx context.Context, req native.SnapshotRequest) (native.SnapshotBefore, error) {
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
	if strings.TrimSpace(s.dir) == "" {
		return native.SnapshotBefore{}, errors.New("snapshots: sessions directory is empty")
	}
	if strings.TrimSpace(req.CallID) == "" {
		return native.SnapshotBefore{}, errors.New("snapshots: call id is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.cleanupSessionLocked(sid, tid, int64(len(req.Before))); err != nil {
		return native.SnapshotBefore{}, fmt.Errorf("snapshots: cleanup before call: %w", err)
	}

	root, turnRel, _, err := s.openTurnRoot(sid, tid)
	if err != nil {
		return native.SnapshotBefore{}, err
	}
	defer root.Close()

	unrecoverable := ""
	if int64(len(req.Before)) > s.limits.MaxFileBytes {
		unrecoverable = native.SnapshotUnrecoverableOversize
	} else if err := s.checkCapacity(sid, int64(len(req.Before))); err != nil {
		return native.SnapshotBefore{}, err
	}
	if err := s.writeCallRecord(root, turnRel, sid, tid, req, unrecoverable); err != nil {
		return native.SnapshotBefore{}, err
	}
	if unrecoverable != "" {
		return native.SnapshotBefore{
			Unrecoverable: unrecoverable,
			Notice:        "本次写入不可撤销：快照超过单文件上限（16 MiB）",
		}, nil
	}
	return native.SnapshotBefore{}, nil
}

// writeCallRecord publishes one call record: the before image and meta.json are
// written into a hidden partial directory which is renamed to its final
// "<seq>-<hash>" name only once both files are on disk. The rename is the single
// step that makes a record visible, so a concurrent reader or cleanup never
// observes a call directory without metadata.
func (s *SessionSnapshots) writeCallRecord(root *os.Root, turnRel, sessionID, turnID string, req native.SnapshotRequest, unrecoverable string) error {
	turnDir := filepath.Join(s.dir, turnRel)
	var lastErr error
	for attempt := 0; attempt < snapshotPublishAttempts; attempt++ {
		seq, err := nextSnapshotSeq(turnDir)
		if err != nil {
			return fmt.Errorf("snapshots: choose call sequence: %w", err)
		}
		name := fmt.Sprintf("%03d-%s", seq, snapshotCallHash(req.CallID))
		meta := SnapshotMeta{
			SessionID: sessionID, TurnID: turnID, CallID: req.CallID, Seq: seq, Tool: req.Tool,
			Root: req.Root, Rel: filepath.ToSlash(req.Rel), RealRel: filepath.ToSlash(req.RealRel),
			Kind: req.Kind, Size: int64(len(req.Before)), Status: snapshotStatusPending,
			Mode: snapshotMode(req), CreatedAt: s.nowTime().UTC(), Source: snapshotSourceCall,
			Unrecoverable: unrecoverable,
		}
		if req.Kind == native.SnapshotKindFile {
			meta.SHA256Before = nativeSHA256(req.Before)
			meta.File = "before"
		}
		partialRel := filepath.Join(turnRel, snapshotPartialDirName(name))
		if err := root.Mkdir(partialRel, 0o700); err != nil {
			return fmt.Errorf("snapshots: create call directory: %w", err)
		}
		if unrecoverable == "" && req.Kind == native.SnapshotKindFile {
			if err := writeSnapshotBytes(root, filepath.Join(partialRel, "before"), req.Before); err != nil {
				_ = root.RemoveAll(partialRel)
				return fmt.Errorf("snapshots: write before: %w", err)
			}
		}
		if err := writeSnapshotMeta(root, filepath.Join(partialRel, "meta.json"), meta); err != nil {
			_ = root.RemoveAll(partialRel)
			return fmt.Errorf("snapshots: write metadata: %w", err)
		}
		if err := root.Rename(partialRel, filepath.Join(turnRel, name)); err != nil {
			_ = root.RemoveAll(partialRel)
			// A writer in another process may have published this sequence first;
			// retry with the next free one instead of failing the write.
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("snapshots: publish call record: %w", lastErr)
}

// AfterCall fills the pending record belonging to the current session/turn and
// call. It is intentionally serialized with BeforeCall so repeated calls in a
// turn retain their inverse order on disk.
func (s *SessionSnapshots) AfterCall(ctx context.Context, result native.SnapshotResult) error {
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
	if strings.TrimSpace(result.CallID) == "" {
		return errors.New("snapshots: call id is empty")
	}
	if result.Status != native.SnapshotStatusComplete && result.Status != native.SnapshotStatusIncomplete {
		return fmt.Errorf("snapshots: invalid result status %q", result.Status)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	turnDir := filepath.Join(s.dir, sid, "snapshots", tid)
	metaPath, meta, err := findPendingSnapshot(turnDir, result.CallID)
	if err != nil {
		return err
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
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return fmt.Errorf("snapshots: open sessions root: %w", err)
	}
	defer root.Close()
	metaRel, err := filepath.Rel(s.dir, metaPath)
	if err != nil {
		return fmt.Errorf("snapshots: locate metadata: %w", err)
	}
	if err := writeSnapshotMeta(root, metaRel, meta); err != nil {
		return fmt.Errorf("snapshots: update metadata: %w", err)
	}
	if result.Status == native.SnapshotStatusIncomplete {
		state, stateErr := s.LoadCheckpointState(sid, tid)
		if stateErr != nil {
			return fmt.Errorf("snapshots: load checkpoint state: %w", stateErr)
		}
		state.State = SnapshotStatePartial
		state.UpdatedAt = s.nowTime().UTC()
		if stateErr := writeCheckpointState(filepath.Join(s.dir, sid, "snapshots", tid), state); stateErr != nil {
			return fmt.Errorf("snapshots: update checkpoint state: %w", stateErr)
		}
	}
	return nil
}

// LoadTurn reads all call records in sequence order. A missing turn is the
// normal representation of an old session or a session with no writes.
func (s *SessionSnapshots) LoadTurn(sessionID, turnID string) ([]SnapshotMeta, error) {
	if s == nil {
		return nil, errors.New("snapshots: store is nil")
	}
	if err := validateSnapshotID(sessionID, "session"); err != nil {
		return nil, err
	}
	if err := validateSnapshotID(turnID, "turn"); err != nil {
		return nil, err
	}
	loaded, err := loadTurn(filepath.Join(s.dir, sessionID, "snapshots", turnID))
	if err != nil {
		return nil, err
	}
	return loaded.Metas, nil
}

func (s *SessionSnapshots) scopeIDs(ctx context.Context) (string, string, error) {
	scope := observeScopeFrom(ctx)
	if err := validateSnapshotID(scope.sessionID, "session"); err != nil {
		return "", "", fmt.Errorf("snapshots: %w", err)
	}
	if err := validateSnapshotID(scope.turnID, "turn"); err != nil {
		return "", "", fmt.Errorf("snapshots: %w", err)
	}
	return scope.sessionID, scope.turnID, nil
}

func validateSnapshotID(id, label string) error {
	if id == "" || id == "." || id == ".." || !safeSessionID.MatchString(id) {
		return fmt.Errorf("invalid %s id %q", label, id)
	}
	return nil
}

func (s *SessionSnapshots) openTurnRoot(sessionID, turnID string) (*os.Root, string, string, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, "", "", fmt.Errorf("snapshots: create sessions directory: %w", err)
	}
	if err := process.SecureDir(s.dir); err != nil {
		return nil, "", "", fmt.Errorf("snapshots: secure sessions directory: %w", err)
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, "", "", fmt.Errorf("snapshots: open sessions root: %w", err)
	}
	turnRel := filepath.Join(sessionID, "snapshots", turnID)
	if err := root.MkdirAll(turnRel, 0o700); err != nil {
		_ = root.Close()
		return nil, "", "", fmt.Errorf("snapshots: create turn directory: %w", err)
	}
	statePath := filepath.Join(s.dir, turnRel, snapshotStateFile)
	if info, err := os.Lstat(statePath); errors.Is(err, os.ErrNotExist) {
		if err := writeCheckpointState(filepath.Dir(statePath), SnapshotCheckpointState{State: SnapshotStateOpen, UpdatedAt: s.nowTime().UTC()}); err != nil {
			_ = root.Close()
			return nil, "", "", fmt.Errorf("snapshots: create checkpoint state: %w", err)
		}
	} else if err != nil {
		_ = root.Close()
		return nil, "", "", fmt.Errorf("snapshots: inspect checkpoint state: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		_ = root.Close()
		return nil, "", "", errors.New("snapshots: checkpoint state is a symlink")
	}
	return root, turnRel, filepath.Join(s.dir, turnRel), nil
}

func (s *SessionSnapshots) checkCapacity(sessionID string, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	sessionBytes, err := beforeBytes(filepath.Join(s.dir, sessionID, "snapshots"))
	if err != nil {
		return fmt.Errorf("snapshots: inspect session capacity: %w", err)
	}
	if sessionBytes+bytes > s.limits.MaxSessionBytes {
		return fmt.Errorf("snapshots: session limit exceeded (%d bytes; limit %d)", sessionBytes+bytes, s.limits.MaxSessionBytes)
	}
	globalBytes, err := beforeBytes(filepath.Join(s.dir, ""))
	if err != nil {
		return fmt.Errorf("snapshots: inspect global capacity: %w", err)
	}
	if globalBytes+bytes > s.limits.MaxGlobalBytes {
		return fmt.Errorf("snapshots: global limit exceeded (%d bytes; limit %d)", globalBytes+bytes, s.limits.MaxGlobalBytes)
	}
	return nil
}

func beforeBytes(root string) (int64, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("%s is not a directory", root)
	}
	var total int64
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "before" {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		total += fileInfo.Size()
		return nil
	})
	return total, err
}

func nextSnapshotSeq(turnDir string) (int, error) {
	entries, err := os.ReadDir(turnDir)
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == snapshotPreUndoDir {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "-")
		if !ok {
			continue
		}
		seq, err := strconv.Atoi(prefix)
		if err == nil && seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq + 1, nil
}

func snapshotCallHash(callID string) string {
	sum := sha256.Sum256([]byte(callID))
	return hex.EncodeToString(sum[:8])
}

func nativeSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func snapshotMode(req native.SnapshotRequest) string {
	if req.Kind != native.SnapshotKindFile || req.Root == "" || req.Rel == "" {
		return ""
	}
	info, err := os.Lstat(filepath.Join(req.Root, filepath.FromSlash(req.Rel)))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%04o", info.Mode().Perm())
}

func writeSnapshotBytes(root *os.Root, rel string, data []byte) error {
	file, err := root.OpenFile(rel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeSnapshotMeta(root *os.Root, rel string, meta SnapshotMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeSnapshotBytes(root, rel, append(data, '\n'))
}

func findPendingSnapshot(turnDir, callID string) (string, SnapshotMeta, error) {
	loaded, err := loadTurn(turnDir)
	if err != nil {
		return "", SnapshotMeta{}, err
	}
	metas := loaded.Metas
	for i := len(metas) - 1; i >= 0; i-- {
		meta := metas[i]
		if meta.CallID == callID && meta.Status == snapshotStatusPending {
			path := filepath.Join(turnDir, fmt.Sprintf("%03d-%s", meta.Seq, snapshotCallHash(meta.CallID)), "meta.json")
			return path, meta, nil
		}
	}
	return "", SnapshotMeta{}, fmt.Errorf("snapshots: pending call %q not found", callID)
}

// turnLoad is the outcome of reading one turn directory: the readable call
// records in sequence order plus the subdirectories skipped because their
// meta.json was missing or unreadable (a half-written record, or one removed by
// a concurrent cleanup after ReadDir).
type turnLoad struct {
	Metas   []SnapshotMeta
	Skipped []string
}

// loadTurn reads the call records of one turn. A damaged record is skipped and
// reported instead of failing the whole turn: one directory without metadata
// must not disable every later snapshot of the session.
func loadTurn(turnDir string) (turnLoad, error) {
	entries, err := os.ReadDir(turnDir)
	if errors.Is(err, os.ErrNotExist) {
		return turnLoad{}, nil
	}
	if err != nil {
		return turnLoad{}, err
	}
	var out turnLoad
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == snapshotPreUndoDir || strings.HasPrefix(entry.Name(), snapshotPartialPrefix) {
			continue
		}
		meta, err := readSnapshotMeta(filepath.Join(turnDir, entry.Name(), "meta.json"))
		if err != nil {
			out.Skipped = append(out.Skipped, entry.Name())
			slog.Warn("snapshots: skip unreadable call record", "turn_dir", turnDir, "record", entry.Name(), "err", err)
			continue
		}
		out.Metas = append(out.Metas, meta)
	}
	sort.Slice(out.Metas, func(i, j int) bool { return out.Metas[i].Seq < out.Metas[j].Seq })
	return out, nil
}

// readSnapshotMeta decodes one meta.json file.
func readSnapshotMeta(path string) (SnapshotMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SnapshotMeta{}, err
	}
	var meta SnapshotMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return SnapshotMeta{}, fmt.Errorf("read %s: %w", path, err)
	}
	return meta, nil
}

// snapshotPartialDirName returns the hidden in-progress name of one call
// record. The pid and timestamp keep two writers of the same sequence apart.
func snapshotPartialDirName(name string) string {
	return fmt.Sprintf("%s%s-%d-%d", snapshotPartialPrefix, name, os.Getpid(), time.Now().UnixNano())
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}
