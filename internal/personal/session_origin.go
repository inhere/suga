package personal

import (
	"context"
	"fmt"

	"github.com/inhere/suga/internal/personal/store"
)

// Session origins (design 0.3, 会话来源). An entry point names itself when it
// creates a session, so `suga gc --retention` can keep interactive sessions
// longer than the ones a batch job or an editor integration started. The
// constants are re-exported here so the chat, run and ACP entry points do not
// have to reach into the store package for them.
const (
	OriginChat  = store.OriginChat
	OriginRun   = store.OriginRun
	OriginACP   = store.OriginACP
	OriginAgent = store.OriginAgent
)

// NewSessionFor is NewSession for a caller that knows the entry point it runs
// as: origin is stored as meta.origin (an empty origin stores nothing, for a
// caller with no origin to declare).
func (r *Runner) NewSessionFor(ctx context.Context, id, title, origin string) (Session, error) {
	if origin == "" {
		return r.NewSession(ctx, id, title)
	}
	return r.NewSessionWithMeta(ctx, id, title, map[string]any{store.MetaOrigin: origin})
}

// MarkSessionUsedInteractively records that an interactive chat continued a
// session a non-interactive entry point created (run, ACP): the session counts
// as interactive for `suga gc --retention` from then on. A chat session, a
// sub-agent session and a session from before meta.origin existed (no origin,
// treated as interactive anyway) have nothing to record.
func (r *Runner) MarkSessionUsedInteractively(ctx context.Context, sessionID string) error {
	model, err := r.cfg.Repository.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	switch store.SessionOrigin(model) {
	case store.OriginRun, store.OriginACP:
	default:
		return nil
	}
	if store.SessionUsedInteractively(model) {
		return nil
	}
	if err := r.cfg.Repository.PatchSessionMeta(ctx, sessionID, map[string]any{store.MetaUsedInteractively: true}); err != nil {
		return fmt.Errorf("mark session %q used interactively: %w", sessionID, err)
	}
	return nil
}
