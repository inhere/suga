package sugasdk

// Optional idempotent sends: a Prompt.RequestID turns a turn into a
// deduplicated request keyed by (session, request id), the way whip
// deduplicates commands by (client_id, command_id). The mapping of id to turn
// and the finished outcome are stored in the session's meta, so a host that
// reconnects (or reopens a session) can replay the first result instead of
// running the turn again, and can query the status of a send whose outcome it
// never saw.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

// Request status values returned by Session.RequestStatus.
const (
	// RequestStatusUnknown: no record for the id, or one left running by a
	// process that is not running it any more (it died mid-turn). Delivery is
	// uncertain: the host resends the prompt under a NEW request id.
	RequestStatusUnknown = "unknown"
	// RequestStatusRunning: the id's turn currently runs in this session.
	RequestStatusRunning = "running"
	// RequestStatusDone: the turn finished and its result is stored.
	RequestStatusDone = "done"
	// RequestStatusFailed: the finished turn failed (or was cancelled).
	RequestStatusFailed = "failed"
)

// Record states stored next to a claimed request id (meta value).
const (
	requestStateRunning = "running"
	requestStateDone    = "done"
	requestStateFailed  = "failed"
)

// requestMetaPrefix prefixes the session meta key of one request record: the
// session scopes the id (the same id in another session is a different
// request).
const requestMetaPrefix = "sdk_req:"

// requestRecord is what one claim of a request id stores in the session meta:
// the running marker (with the turn id once the turn created its row) and,
// after the turn, the outcome the first Send returned.
type requestRecord struct {
	TurnID string  `json:"turn_id,omitempty"`
	State  string  `json:"state"`
	Result *Result `json:"result,omitempty"`
}

// requestMetaKey is the session meta key of one request record.
func requestMetaKey(requestID string) string { return requestMetaPrefix + requestID }

// decodeRequestRecord reads a record out of a session's meta; nil means the id
// is not claimed there.
func decodeRequestRecord(meta map[string]any, requestID string) (*requestRecord, error) {
	raw, _ := meta[requestMetaKey(requestID)].(string)
	if raw == "" {
		return nil, nil
	}
	var rec requestRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("sugasdk: read request record %q: %w", requestID, err)
	}
	rec.TurnID = strings.TrimSpace(rec.TurnID)
	return &rec, nil
}

// encodeRequestRecord serialises a record for the meta patch.
func encodeRequestRecord(rec requestRecord) (string, error) {
	b, err := json.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("sugasdk: encode request record: %w", err)
	}
	return string(b), nil
}

// readRequestRecord loads one request record of this session from the store.
func (s *Session) readRequestRecord(ctx context.Context, repo *store.Store, requestID string) (*requestRecord, error) {
	model, err := repo.GetSession(ctx, s.id)
	if errors.Is(err, store.ErrSessionNotFound) {
		return nil, fmt.Errorf("sugasdk: session %q is gone", s.id)
	}
	if err != nil {
		return nil, err
	}
	return decodeRequestRecord(model.Meta, requestID)
}

// dispatchRecord answers a Send (or a claim re-check) that found an existing
// record: a finished one replays its stored result with Replayed set (the
// model is not called again); a running one reports who runs it. ErrRequestRunning
// is for a turn this session currently runs; ErrDeliveryUncertain is for a
// record left running by a process that is not running it any more (it died
// mid-turn): delivery is uncertain, the host resends under a NEW request id.
func (s *Session) dispatchRecord(requestID string, rec *requestRecord) (res Result, err error) {
	switch strings.TrimSpace(rec.State) {
	case requestStateDone, requestStateFailed:
		if rec.Result == nil {
			// A finish that never stored the result counts as uncertain: the
			// caller resends under a new id instead of replaying nothing.
			return Result{}, ErrDeliveryUncertain
		}
		res = *rec.Result
		res.Replayed = true
		return res, nil
	case requestStateRunning:
		s.mu.Lock()
		mine := s.running && s.runningRequest == requestID
		s.mu.Unlock()
		if mine {
			return Result{}, ErrRequestRunning
		}
		return Result{}, ErrDeliveryUncertain
	default:
		// A state this build does not know is uncertain, never a replay.
		return Result{}, ErrDeliveryUncertain
	}
}

// claimRequestRecord persists the claim of a fresh id (state running, no turn
// yet) as a write-once meta key. false means another claimer took the key
// between the read and this patch.
func (s *Session) claimRequestRecord(ctx context.Context, repo *store.Store, requestID string) (bool, error) {
	value, err := encodeRequestRecord(requestRecord{State: requestStateRunning})
	if err != nil {
		return false, err
	}
	key := requestMetaKey(requestID)
	return repo.PatchSessionMetaIfAbsent(ctx, s.id, key, map[string]any{key: value})
}

// requestTurnStarted records the turn id on the claim as best effort as soon
// as the turn created its row, so the id-to-turn mapping survives a process
// that dies during the turn. A failure is logged, never fatal: the turn runs.
func (s *Session) requestTurnStarted(ctx context.Context, repo *store.Store, requestID, turnID string) {
	value, err := encodeRequestRecord(requestRecord{TurnID: turnID, State: requestStateRunning})
	if err == nil {
		var perr error
		// The key is ours (claimed before the turn), so the plain patch only
		// rewrites it.
		if perr = repo.PatchSessionMeta(ctx, s.id, map[string]any{requestMetaKey(requestID): value}); perr == nil {
			return
		}
		err = perr
	}
	s.engine.logger.Warn("sugasdk: record the request turn id", "session", s.id, "request", requestID, "err", err)
}

// requestTurnFinished rewrites the claim with the turn's outcome: the result
// the caller received (replayable from then on), named done or failed.
func (s *Session) requestTurnFinished(ctx context.Context, repo *store.Store, requestID, turnID string, res Result, runErr error) error {
	state := requestStateDone
	if runErr != nil {
		state = requestStateFailed
		if strings.TrimSpace(res.State) == "" {
			// A turn that failed before the runtime wrote a state of its own
			// still stores a reportable state.
			res.State = string(personal.RunFailed)
		}
	}
	value, err := encodeRequestRecord(requestRecord{TurnID: turnID, State: state, Result: &res})
	if err != nil {
		return err
	}
	return repo.PatchSessionMeta(ctx, s.id, map[string]any{requestMetaKey(requestID): value})
}
