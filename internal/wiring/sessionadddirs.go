package wiring

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/inhere/suga/internal/adddir"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// ApplyAddDirs publishes the session's additional directories (--add-dir): the
// ones its metadata already carries plus the ones this invocation requested (a
// path named again takes this invocation's mode). The metadata records the
// requested mode and source, so resuming the session keeps the directories,
// their permissions and where they came from; the tools see the effective view,
// which a read-only runtime downgrades (it has no write tool at all).
//
// The front end calls it once the session is open (run: after openRequestedSession,
// chat: through chat.Options.SetupSession) and before the first turn.
func (rt *Runtime) ApplyAddDirs(ctx context.Context, sessionID string) error {
	if rt == nil || rt.sessionDirs == nil {
		return nil
	}
	return rt.sessionDirs.apply(ctx, sessionID)
}

// SessionAddDirs is the live handle on the added directories of one session: the
// CLI's ApplyAddDirs fills it when the session opens, chat's :add-dir/:dirs/
// :rm-dir commands go through it, and the outside-directory authorization
// (chat.ask_outside_dirs) is answered with it. Both the session metadata
// (add_dirs) and the set the tools read (native.AddDirs) change in this one
// place, so a command takes effect on the next tool call and survives a resume.
type SessionAddDirs struct {
	runner *personal.Runner
	// live is the set the file tools read at every call; the runtime's
	// environment prompt reads it too, so a change shows up in the next turn's
	// context update.
	live *native.AddDirs
	// requested is this invocation's --add-dir set (already parsed by the CLI)
	// and readOnly is its --read-only mode, which downgrades the effective view.
	requested []adddir.Dir
	readOnly  bool
	// workspace is the workspace root, listed first by List.
	workspace string
	// sessionsDir is the per-session file root ($SUGA_HOME/sessions); ImagesDir
	// derives the served session's images directory from it (design 0.3).
	sessionsDir string

	mu sync.Mutex
	// sessionID is the session being served, filled by apply; the handle itself
	// is built while the runtime is assembled, before the session is known, so
	// every method refuses to work until then.
	sessionID string
	// dirs is the session's set as of the last write: the stored order and mode,
	// Source saying where each entry came from.
	dirs []adddir.Dir
}

// newSessionAddDirs builds the handle of one runtime.
func newSessionAddDirs(runner *personal.Runner, live *native.AddDirs, requested []adddir.Dir,
	readOnly bool, workspace string, sessionsDir string) *SessionAddDirs {
	return &SessionAddDirs{runner: runner, live: live, requested: requested, readOnly: readOnly,
		workspace: workspace, sessionsDir: sessionsDir}
}

// SessionID is the session the handle currently serves ("" until one opens,
// through apply or a :add-dir publish).
func (h *SessionAddDirs) SessionID() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionID
}

// ImagesDir is the images directory of the session being served ("" until one
// opens): view_image resolves a session image ref (images/<sha256>.<ext>,
// design 0.3) against it.
func (h *SessionAddDirs) ImagesDir() string {
	if h == nil || h.runner == nil {
		return ""
	}
	sid := h.SessionID()
	sessionsDir := h.sessionsDir
	if sid == "" || sessionsDir == "" {
		return ""
	}
	return store.SessionImagesDir(sessionsDir, sid)
}

// Workspace is the workspace root the session's tools are confined to.
func (h *SessionAddDirs) Workspace() string { return h.workspace }

// apply records the session's own directories merged with this invocation's
// --add-dir values (see ApplyAddDirs). It is the only place the stored set is
// read back from the store, so the handle and the metadata cannot drift apart.
func (h *SessionAddDirs) apply(ctx context.Context, sessionID string) error {
	if h == nil {
		return nil
	}
	stored, err := h.stored(ctx, sessionID)
	if err != nil {
		return err
	}
	return h.publish(ctx, sessionID, stored, adddir.Merge(stored, h.requested), nil)
}

// stored reads the session's recorded directories; a hand-edited or older value
// must not stop the session, so a malformed one is reported and ignored.
func (h *SessionAddDirs) stored(ctx context.Context, sessionID string) ([]adddir.Dir, error) {
	model, err := h.runner.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	dirs, err := adddir.FromMeta(model.Meta[adddir.MetaKey])
	if err != nil {
		slog.Warn("ignore stored session add_dirs", "session_id", sessionID, "err", err)
		return nil, nil
	}
	return dirs, nil
}

// publish writes merged to the session metadata when it changed and always
// republishes the live set the tools read (the effective view: a read-only
// session reads everywhere and writes nowhere). event, when set, records the
// change in the trace.
func (h *SessionAddDirs) publish(ctx context.Context, sessionID string, before, merged []adddir.Dir,
	event func() personal.HostEvent) error {
	if sessionID == "" {
		return errors.New("会话尚未打开，请稍后再试")
	}
	if !adddir.Equal(before, merged) {
		if err := h.runner.SaveSessionMeta(ctx, sessionID, map[string]any{adddir.MetaKey: adddir.ToMeta(merged)}); err != nil {
			return err
		}
	}
	h.live.Set(nativeAddDirs(adddir.Effective(merged, h.readOnly)))
	h.mu.Lock()
	h.sessionID, h.dirs = sessionID, merged
	h.mu.Unlock()
	if event != nil {
		h.runner.RecordEvent(ctx, sessionID, event())
	}
	return nil
}

// List returns the directories the session may reach: the workspace first (the
// caller tells it apart by Workspace), then the added ones in order, each with
// the mode in effect for this process and where it came from (an adddir.Source*
// value; "" for the workspace and for a directory stored by an older version).
func (h *SessionAddDirs) List(ctx context.Context) ([]adddir.Dir, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessionID == "" {
		return nil, errors.New("会话尚未打开，请稍后再试")
	}
	out := make([]adddir.Dir, 0, len(h.dirs)+1)
	if h.workspace != "" {
		out = append(out, adddir.Dir{Path: h.workspace, Writable: !h.readOnly})
	}
	return append(out, adddir.Effective(h.dirs, h.readOnly)...), nil
}

// Add authorizes one directory for this session: value is
// "<absolute directory>[:ro|:rw]" (default :ro), as typed after chat's
// :add-dir, and source is where it came from (adddir.SourceSession for a typed
// command, adddir.SourceAsk for an outside-directory authorization). The path
// must be an existing directory; in a --read-only session only the read-only
// mode is accepted. It returns the entry that took effect.
func (h *SessionAddDirs) Add(ctx context.Context, value, source string) (adddir.Dir, error) {
	dir, err := adddir.Entry(value)
	if err != nil {
		return adddir.Dir{}, err
	}
	return h.add(ctx, dir, source)
}

// add is Add on an already parsed and checked directory. A write authorization
// is refused in a read-only session: such a session has no write tool at all,
// so recording :rw would only promise something it cannot do.
func (h *SessionAddDirs) add(ctx context.Context, dir adddir.Dir, source string) (adddir.Dir, error) {
	if dir.Writable && h.readOnly {
		return adddir.Dir{}, errors.New("本会话为只读（--read-only），只能授权只读目录")
	}
	dir.Source = source
	current, sessionID, err := h.snapshot()
	if err != nil {
		return adddir.Dir{}, err
	}
	merged := adddir.Merge(current, []adddir.Dir{dir})
	err = h.publish(ctx, sessionID, current, merged, func() personal.HostEvent {
		return addDirEvent("add", dir, source)
	})
	if err != nil {
		return adddir.Dir{}, err
	}
	return dir, nil
}

// Remove revokes a directory: value is "<directory>" (a :ro/:rw suffix and "~"
// are accepted and ignored, since a directory may be gone from disk by now). It
// reports whether the directory was part of the session's set at all.
func (h *SessionAddDirs) Remove(ctx context.Context, value string) (bool, error) {
	path := adddir.StripMode(value)
	if path == "" || !adddir.IsAbsPath(path) {
		return false, errors.New("需要一个绝对路径")
	}
	path = adddir.Expand(path)
	current, sessionID, err := h.snapshot()
	if err != nil {
		return false, err
	}
	// The event names the entry that went away: its mode is what the session
	// loses.
	gone, ok := adddir.Find(current, path)
	if !ok {
		return false, nil
	}
	merged := adddir.Without(current, path)
	err = h.publish(ctx, sessionID, current, merged, func() personal.HostEvent {
		return addDirEvent("remove", gone, adddir.SourceSession)
	})
	return err == nil, err
}

// snapshot returns the session's current set and its ID, refusing to work before
// the session is open.
func (h *SessionAddDirs) snapshot() ([]adddir.Dir, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessionID == "" {
		return nil, "", errors.New("会话尚未打开，请稍后再试")
	}
	return h.dirs, h.sessionID, nil
}

// addDirEvent is the trace event of one authorization change outside a turn
// (store.EventAddDir): what changed, its mode and who asked for it.
func addDirEvent(name string, dir adddir.Dir, source string) personal.HostEvent {
	from := "keyboard"
	if source == adddir.SourceAsk {
		from = "ask"
	}
	return personal.HostEvent{
		Kind: store.EventAddDir, Name: name, Status: "ok",
		Attrs: map[string]any{"path": dir.Path, "mode": modeName(dir.Writable), "from": from},
	}
}

// modeName renders a directory mode as the stored short form.
func modeName(writable bool) string {
	if writable {
		return "rw"
	}
	return "ro"
}

// nativeAddDirs converts the session's directories into the native tool set.
func nativeAddDirs(dirs []adddir.Dir) []native.AddDir {
	out := make([]native.AddDir, len(dirs))
	for i, dir := range dirs {
		out[i] = native.AddDir{Path: dir.Path, Writable: dir.Writable}
	}
	return out
}
