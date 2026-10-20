package personal

import (
	"context"
	"log/slog"
)

// turnLogKey is the context key of the turn's logger (see withTurnLogger).
type turnLogKey struct{}

// withTurnLogger puts a logger carrying the turn's session_id and turn_id into
// the context, so the records written while the turn runs (tool events, budget
// notices) can be lined up with the session store and `suga trace` without
// threading a logger through every call site.
func withTurnLogger(ctx context.Context, sessionID, turnID string) context.Context {
	logger := slog.Default().With("session_id", sessionID, "turn_id", turnID)
	return context.WithValue(ctx, turnLogKey{}, logger)
}

// turnLogger is the turn's logger from the context, or the process logger for a
// record written outside a turn.
func turnLogger(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if logger, ok := ctx.Value(turnLogKey{}).(*slog.Logger); ok && logger != nil {
			return logger
		}
	}
	return slog.Default()
}
