package personal

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/inhere/suga/internal/personal/store"
)

// Continuing an existing session in another working directory is not silent:
// the directory recorded on the session (sessions.cwd) is what its recorded
// history and its file boundaries belong to, while the tools read the
// directory this process was started in. The front ends compare the two before
// anything runs: chat asks, run and chat without a terminal fail (both may
// still be told to continue here with --allow-cwd-change), and ACP's
// session/load refuses the other directory. When the person or the flag does
// continue here, AdoptSessionCwd moves the recorded directory with it, so the
// session listing and every later resume agree.
const (
	// sessionCwdChangedEvent is the name of the trace event (kind
	// store.EventSession) that records such a move: attrs "from" and "to".
	sessionCwdChangedEvent = "session_cwd_changed"
)

// NormalizeDir renders a working directory in the form two directories are
// compared in: absolute, cleaned, with symbolic links resolved. A path that
// cannot be resolved - the directory is gone, or a link does not lead anywhere
// - falls back to its cleaned absolute form, so a session whose directory was
// deleted still compares by name instead of never matching. "" stays "".
func NormalizeDir(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = filepath.Clean(resolved)
	}
	return p
}

// SameDir reports whether two working directories are the same directory: both
// are normalized (NormalizeDir) and, on Windows (a case-insensitive file
// system), compared ignoring case.
func SameDir(a, b string) bool {
	return sameDir(a, b, runtime.GOOS == "windows")
}

// sameDir is SameDir with the case folding as a parameter, so the Windows rule
// is testable on every platform.
func sameDir(a, b string, fold bool) bool {
	x, y := NormalizeDir(a), NormalizeDir(b)
	if fold {
		return strings.EqualFold(x, y)
	}
	return x == y
}

// SessionCwd returns the working directory recorded on a session ("" for a
// session created before cwd was recorded; the next turn fills it in then).
func (r *Runner) SessionCwd(ctx context.Context, sessionID string) (string, error) {
	model, err := r.LoadSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return model.Cwd, nil
}

// AdoptSessionCwd records that sessionID is now worked on in dir: sessions.cwd
// is updated and the move is written to the trace as a session_cwd_changed
// event (attrs from/to), so `suga inspect trace` shows where the session moved and
// `suga sessions` follows it. It returns the directory the session had before.
//
// The callers only reach it after comparing the directories themselves
// (SameDir), so a session already on dir is left untouched, as is an empty dir.
func (r *Runner) AdoptSessionCwd(ctx context.Context, sessionID, dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", nil
	}
	model, err := r.LoadSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	from := model.Cwd
	if from == dir {
		return from, nil
	}
	model.Cwd = dir
	if err := r.cfg.Repository.UpdateSessionFields(ctx, model); err != nil {
		return from, fmt.Errorf("update session cwd: %w", err)
	}
	r.RecordEvent(ctx, sessionID, HostEvent{
		Kind: store.EventSession, Name: sessionCwdChangedEvent, Status: eventOK,
		Attrs: map[string]any{"from": from, "to": dir},
	})
	return from, nil
}
