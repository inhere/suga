package personal

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/inhere/suga/internal/personal/store"
)

// Reasons for a write call whose snapshot directory is missing.
const (
	rewindReasonCleared      = "cleared"       // a snapshot was built, then cleaned up
	rewindReasonNoSnapshot   = "no_snapshot"   // the call reported no snapshot
	rewindReasonSnapshotsOff = "snapshots_off" // snapshots were off (no report at all)
)

// clearedWrites finds, per turn, the successful write-tool calls (file_write,
// file_edit, file_remove with Status=ok; failed calls changed nothing) and
// returns them as unrecoverable steps keyed by turn, newest call first. The
// caller only uses the entries of turns that have no snapshot directory, so a
// turn with writes but no directory shows up as cleared. Targets are listed
// as recorded (attrs["target"]: maybe relative to cwd, maybe a directory).
// attrs["snapshot"] separates "cleared" from "never built" (plan F12); no
// report at all means snapshots were off.
func (u *UndoCore) clearedWrites(ctx context.Context, sid string) (map[string][]rewindFileStep, error) {
	events, err := u.Repository.ListEvents(ctx, store.EventFilter{SessionID: sid, Kinds: []string{store.EventTool}})
	if err != nil {
		return nil, fmt.Errorf("rewind files: list tool events: %w", err)
	}
	out := map[string][]rewindFileStep{}
	// Events are oldest first; walk backwards for newest first.
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Status != eventOK || ev.TurnID == "" || !undoTool(ev.Name) {
			continue
		}
		reason := writeEventReason(ev.Attrs)
		targets := writeEventTargets(ev.Attrs)
		if len(targets) == 0 {
			targets = []string{""}
		}
		for j := len(targets) - 1; j >= 0; j-- {
			out[ev.TurnID] = append(out[ev.TurnID], rewindFileStep{View: RewindFileItem{
				TurnID: ev.TurnID, Path: targets[j], Action: RewindFileUnrecoverable, Reason: reason,
			}})
		}
	}
	return out, nil
}

func writeEventReason(attrs map[string]any) string {
	snap, ok := attrs["snapshot"].(map[string]any)
	if !ok {
		return rewindReasonSnapshotsOff
	}
	if kind, _ := snap["kind"].(string); kind == "" {
		return rewindReasonNoSnapshot
	}
	return rewindReasonCleared
}

// writeEventTargets reads attrs["target"], a string list (decoded JSON gives []any).
func writeEventTargets(attrs map[string]any) []string {
	var out []string
	switch v := attrs["target"].(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, filepath.ToSlash(s))
			}
		}
	case string:
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
