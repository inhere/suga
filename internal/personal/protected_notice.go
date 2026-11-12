package personal

import (
	"context"
	"time"

	"github.com/inhere/suga/internal/personal/store"
)

// protectedConfigEvent is the name of the config event a turn writes when the
// protected settings changed since the user last confirmed them.
const protectedConfigEvent = "protected_config_changed"

// recordProtectedNotice writes the config event of a turn that starts while the
// protected settings of the config file differ from what the user last
// confirmed: it is how a trace shows that a session ran on a changed security
// configuration. The notice is per session (the source decides), so the event
// appears once per session, at its first turn; attrs carry the changed key
// names only - a protected key can be a secret.
func (r *Runner) recordProtectedNotice(ctx context.Context, sessionID string) {
	if r.cfg.ProtectedNotice == nil {
		return
	}
	keys, wholeFile, unreadable, ok := r.cfg.ProtectedNotice(sessionID)
	if !ok {
		return
	}
	attrs := map[string]any{"keys": keys}
	switch {
	case wholeFile:
		// The file cannot be compared key by key: there are no names to show.
		attrs = map[string]any{"whole_file": true}
	case unreadable:
		attrs["snapshot_unreadable"] = true
	}
	r.rec.record(ctx, store.Event{SessionID: sessionID, Kind: store.EventConfig, Name: protectedConfigEvent,
		Status: eventOK, StartedAt: time.Now(), Attrs: attrs})
}
