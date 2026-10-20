package personal

import (
	"context"
	"fmt"

	"github.com/inhere/suga/internal/personal/store"
)

// maxIDAttempts is how many generated ids one insert tries. A generated id can
// only be taken by a collision in its 60 random bits, so a couple of retries is
// plenty; an id the caller supplied (--session-id) is never retried.
const maxIDAttempts = 3

// newSessionID and newTurnID generate the ids of new sessions and turns. They
// are variables so a test can inject a generator that returns a taken id on
// purpose and check the retry.
var (
	newSessionID = NewSessionID
	newTurnID    = NewTurnID
)

// createTurn inserts the turn of sessionID with the candidate id, replacing the
// id with a fresh generated one when the insert reports a primary-key conflict
// (at most maxIDAttempts ids), and returns the id actually stored. Any other
// error (an unknown session included) is returned as is.
func (r *Runner) createTurn(ctx context.Context, sessionID, candidate string) (string, error) {
	id := candidate
	var err error
	for attempt := 0; attempt < maxIDAttempts; attempt++ {
		if attempt > 0 {
			id = newTurnID()
		}
		err = r.cfg.Repository.CreateTurn(ctx, store.Turn{ID: id, SessionID: sessionID, Status: store.TurnRunning})
		if err == nil {
			return id, nil
		}
		if !store.IsDuplicateKey(err) {
			return "", fmt.Errorf("create turn: %w", err)
		}
	}
	return "", fmt.Errorf("create turn: %w", err)
}
