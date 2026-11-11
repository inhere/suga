package personal

import (
	"context"
	"log/slog"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// withTurnRequest marks ctx with the person's request of the turn
// (api.WithTurnPrompt) and, for an injected turn, with its own text as untrusted
// material (api.WithInjectedTurnText, suga-xsvv).
func (r *Runner) withTurnRequest(ctx context.Context, sessionID string, req RunRequest) context.Context {
	ctx = api.WithTurnPrompt(ctx, r.turnPrompt(ctx, sessionID, req))
	if req.Injected {
		ctx = api.WithInjectedTurnText(ctx, req.Prompt)
	}
	return ctx
}

// turnPrompt returns the user request a turn acts on. A user turn is its own
// prompt. An injected turn (RunRequest.Injected: a Stop hook reply, a delivery)
// was not typed by a person, so it takes the latest earlier user request of the
// session, read from the store so it also holds after a process restart. Entries
// follow recentRequests' notion of a request (userRequestMessage, rewound
// entries dropped) and additionally exclude messages tagged metaInjected, so a
// chain of injected turns never takes the previous injected text as the
// request. A session without a typed request, or a failed read, gives "".
func (r *Runner) turnPrompt(ctx context.Context, sessionID string, req RunRequest) string {
	if !req.Injected {
		return req.Prompt
	}
	entries, err := r.cfg.Repository.ListEntries(ctx, sessionID, store.EntryFilter{Types: []string{store.EntryUser}})
	if err != nil {
		slog.Warn("turn prompt: list user entries", "session_id", sessionID, "err", err)
		return ""
	}
	masked, err := r.maskedRanges(ctx, sessionID)
	if err != nil {
		slog.Warn("turn prompt: read rewind records", "session_id", sessionID, "err", err)
		return ""
	}
	entries = dropMasked(entries, masked)
	for i := len(entries) - 1; i >= 0; i-- { // newest first
		m, ok := userRequestMessage(entries[i])
		if !ok {
			continue
		}
		if v, _ := m.Metadata[metaInjected].(bool); v {
			continue
		}
		return m.Content
	}
	return ""
}
