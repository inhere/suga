package sugasdk

// Optional idempotent sends: a Prompt.RequestID turns a turn into a
// deduplicated request keyed by (session, request id), the way whip
// deduplicates commands by (client_id, command_id). Each claim keeps a small
// record (turn id, state, a short outcome summary, no answer text) in the
// session meta, so a host that reconnects (or reopens a session) can replay the
// first result instead of running the turn again by rebuilding the output from
// the turn's own entries, and can query the status of a send whose outcome it
// never saw. Meta is rewritten whole on every write, so the records stay small
// and are capped per session (requestRecordLimit).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// Request status values returned by Session.RequestStatus.
const (
	// RequestStatusUnknown: no record for the id, or one left running by a
	// process that is not running it any more (it died mid-turn). Delivery is
	// uncertain: the host resends the prompt under a NEW request id.
	RequestStatusUnknown = "unknown"
	// RequestStatusRunning: the id's turn currently runs in this session.
	RequestStatusRunning = "running"
	// RequestStatusDone: the turn finished and its outcome is stored.
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
// after the turn, a small outcome summary. The answer text is not stored: meta
// is rewritten as a whole on every write and a session's meta must not grow
// with the length of the answers, so the output is rebuilt from the turn's
// assistant entry at replay time.
type requestRecord struct {
	TurnID string `json:"turn_id,omitempty"`
	State  string `json:"state"`
	// At is when the record was last written (claim or finish), used to order
	// the records of one session oldest first for the size cap.
	At      time.Time       `json:"at,omitempty"`
	Summary *requestSummary `json:"summary,omitempty"`
}

// requestSummary is the small outcome a replay needs besides the output: the
// state, the usage numbers and why the answer stopped early.
type requestSummary struct {
	// State is the turn's result state (COMPLETED, FAILED, ...).
	State        string   `json:"state,omitempty"`
	Usage        Usage    `json:"usage"`
	SessionUsage Usage    `json:"session_usage"`
	DeniedTools  []string `json:"denied_tools,omitempty"`
	WrapUp       string   `json:"wrap_up,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
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
// record: a finished one replays the stored outcome (the output rebuilt from
// the turn's entries, the model is not called again); a running one reports
// who runs it. ErrRequestRunning is for a turn this session currently runs;
// ErrDeliveryUncertain is for a record left running by a process that is not
// running it any more (it died mid-turn): delivery is uncertain, the host
// resends under a NEW request id.
func (s *Session) dispatchRecord(ctx context.Context, repo *store.Store, requestID string, rec *requestRecord) (res Result, err error) {
	switch strings.TrimSpace(rec.State) {
	case requestStateDone, requestStateFailed:
		if rec.Summary == nil {
			// A finish that never stored the outcome counts as uncertain: the
			// caller resends under a new id instead of replaying nothing.
			return Result{}, ErrDeliveryUncertain
		}
		return s.replayRequest(ctx, repo, requestID, rec)
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

// replayRequest rebuilds the first send's Result from storage: the small
// summary from the record, the output text from the turn's last assistant
// entry (assembled the way the runner's result is: the last assistant message
// of the turn that carries text).
func (s *Session) replayRequest(ctx context.Context, repo *store.Store, requestID string, rec *requestRecord) (Result, error) {
	output, err := s.turnOutput(ctx, repo, requestID, rec.TurnID)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		SessionID: s.id, TurnID: rec.TurnID, State: rec.Summary.State, Output: output,
		Usage: rec.Summary.Usage, SessionUsage: rec.Summary.SessionUsage,
		DeniedTools: rec.Summary.DeniedTools, WrapUp: rec.Summary.WrapUp, Truncated: rec.Summary.Truncated,
	}
	res.Replayed = true
	return res, nil
}

// turnOutput rebuilds a turn's answer text from its assistant entries: the
// newest one that carries content, scanning backwards like the runner's
// resume-reading of a reply (an assistant message that only asked for tools
// carries none). A turn whose entries are gone from the session log (an old
// record past the window of a trimmed log) is a replay the storage cannot
// answer: ErrRequestOutputGone.
func (s *Session) turnOutput(ctx context.Context, repo *store.Store, requestID, turnID string) (string, error) {
	entries, err := repo.ListEntries(ctx, s.id, store.EntryFilter{
		TurnID: turnID,
		Types:  []string{store.EntryAssistant},
		// Only the Content is read here, and an assistant stub keeps it whole:
		// the session file is not opened just to replay an answer.
		NoHydrate: true,
	})
	if err != nil {
		return "", fmt.Errorf("sugasdk: read the entries of request %q (turn %q): %w", requestID, turnID, err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		var m agentcore.Message
		if json.Unmarshal(entries[i].JSON, &m) != nil || strings.TrimSpace(m.Content) == "" {
			continue
		}
		return m.Content, nil
	}
	return "", fmt.Errorf("%w: request %q (turn %q) has no assistant entry with content in session %q",
		ErrRequestOutputGone, requestID, turnID, s.id)
}

// claimRequestRecord persists the claim of a fresh id (state running, no turn
// yet) as a write-once meta key. false means another claimer took the key
// between the read and this patch.
func (s *Session) claimRequestRecord(ctx context.Context, repo *store.Store, requestID string) (bool, error) {
	value, err := encodeRequestRecord(requestRecord{State: requestStateRunning, At: time.Now().UTC()})
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
	value, err := encodeRequestRecord(requestRecord{TurnID: turnID, State: requestStateRunning, At: time.Now().UTC()})
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

// requestTurnFinished rewrites the claim with the turn's outcome: the small
// summary the caller received (its Usage, state and stop reason; never the
// output text), named done or failed. Replay rebuilds the output from the
// turn's entries.
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
	rec := requestRecord{TurnID: turnID, State: state, At: time.Now().UTC(), Summary: &requestSummary{
		State: res.State, Usage: res.Usage, SessionUsage: res.SessionUsage,
		DeniedTools: res.DeniedTools, WrapUp: res.WrapUp, Truncated: res.Truncated,
	}}
	value, err := encodeRequestRecord(rec)
	if err != nil {
		return err
	}
	return repo.PatchSessionMeta(ctx, s.id, map[string]any{requestMetaKey(requestID): value})
}
