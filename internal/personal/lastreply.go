package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// LastReply returns the text of the last assistant message of the session that
// has any ("" when the session holds no model reply yet) and whether the
// session's last turn ended normally. An interactive chat that continues an
// existing session shows that text above its first prompt so the person can go
// on from it (internal/chat).
//
// It only reads: no model call, no entry written, no hook. A session without
// a reply is not an error (a new session, or a session handed off with
// --from, has nothing to show yet).
func (r *Runner) LastReply(ctx context.Context, sessionID string) (string, bool, error) {
	sess, err := r.LoadSession(ctx, sessionID)
	if err != nil {
		return "", false, err
	}
	entries, err := r.cfg.Repository.ListEntries(ctx, sessionID, store.EntryFilter{
		Types: []string{store.EntryAssistant},
		// Only the Content is read here, and an assistant stub keeps it whole: the
		// session file is not opened just to show a reply.
		NoHydrate: true,
	})
	if err != nil {
		return "", false, fmt.Errorf("read session replies: %w", err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		var m agentcore.Message
		// An assistant message that only asked for tools carries no content, and
		// neither has one whose JSON cannot be read: not a reply, keep looking.
		if json.Unmarshal(entries[i].JSON, &m) != nil || strings.TrimSpace(m.Content) == "" {
			continue
		}
		// Entries after the reply (entries[i+1:]) are assistant messages without
		// text, i.e. the session stopped right after a tool call.
		return m.Content, r.turnUnfinished(ctx, sess, i < len(entries)-1), nil
	}
	return "", false, nil
}

// turnUnfinished reports whether the last turn of sess did not end normally,
// which is what makes chat add "上一回合未正常结束" to the reply it shows. Three
// signals, most reliable first:
//
//   - Store.SessionRunning: Runner.markRunning sets it when a turn starts and
//     only Runner.finish (a normal, failed or cancelled turn end) sets it back
//     to open, so a session still marked running means the process running it
//     died mid-turn. It is the strongest signal: it survives a kill -9, which
//     leaves neither a turn row update nor a final answer.
//   - The turn row of Session.LastTurnID is recorded failed or cancelled (a
//     model error, a Ctrl-C at an approval question): the reply shown is from
//     before that turn.
//   - laterAssistant: the last assistant entry holds no text (only its tool
//     calls), so the turn ended without answering. The scan for the reply text
//     yields this, hence it is passed in.
//
// A session left running by another live chat cannot be told apart from a dead
// one here; chat refuses a new turn on such a session anyway (Runner.Run).
func (r *Runner) turnUnfinished(ctx context.Context, sess Session, laterAssistant bool) bool {
	if sess.Status == store.SessionRunning {
		return true
	}
	if sess.LastTurnID != "" {
		turn, err := r.cfg.Repository.GetTurn(ctx, sess.LastTurnID)
		switch {
		case err != nil:
			// Best effort: without the turn row the hint is simply not added.
			slog.Warn("read last turn for the resume hint", "session_id", sess.ID, "turn_id", sess.LastTurnID, "err", err)
		case turn.Status == store.TurnFailed || turn.Status == store.TurnCancelled:
			return true
		}
	}
	return laterAssistant
}
