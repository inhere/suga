package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
)

// Session card: a read-only snapshot of where one session stands. A host that
// keeps one session per topic needs the latest handoff or compaction summary, the
// open steps of the plan, the cumulative usage and the activity times without opening
// - let alone continuing - the session.

// The kinds of summary SessionCard.LatestSummary can come from.
const (
	SessionCardSummaryHandoff    = "handoff"    // a --from handoff entry
	SessionCardSummaryCompaction = "compaction" // a segment break's handoff package
)

// sessionCardSummaryCandidates is how many of the newest summary entries are
// inspected. The session start record is a compaction entry without a summary
// body, so the newest record alone is not always a summary; a handful of rows
// covers it without reading the log.
const sessionCardSummaryCandidates = 5

// SessionCard is where a session stands right now. Building it writes nothing:
// no entry, no meta, no migration (Runner.SessionCard).
type SessionCard struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title,omitempty"`
	// ShortName is the name a person sees in `suga ps` and the address
	// `suga send --session` uses; sessions that never registered a local socket
	// (run, ACP) have none.
	ShortName string `json:"short_name,omitempty"`
	// State is the session's stored status: open, running (a turn is in
	// progress, possibly left behind by a dead process) or archived.
	State string `json:"state"`
	// Origin is the entry point that created the session (chat/run/acp/agent);
	// empty for a session from before the key existed.
	Origin string `json:"origin,omitempty"`

	// LatestSummary is the body of the newest handoff or compaction summary, ""
	// when the session has none yet. SummaryKind names which of the two it is,
	// SummaryReason the segment break's reason (threshold/manual/idle; empty for
	// a handoff entry and for a record written by an older binary).
	LatestSummary string `json:"latest_summary,omitempty"`
	SummaryKind   string `json:"summary_kind,omitempty"`
	SummaryReason string `json:"summary_reason,omitempty"`
	// SummaryAt is when that summary was written (zero: no summary).
	SummaryAt time.Time `json:"summary_at"`

	// OpenSteps are the pending and in_progress steps of the latest plan, in
	// plan order; PlanUpdatedAt is when that plan was recorded (zero: no plan).
	// A plan whose steps are all done has no open steps but keeps its time.
	OpenSteps     []string  `json:"open_steps,omitempty"`
	PlanUpdatedAt time.Time `json:"plan_updated_at"`

	Usage SessionCardUsage `json:"usage"`

	CreatedAt time.Time `json:"created_at"`
	// LastActiveAt is the time of the newest entry of the session, or the
	// session's update time when it holds no entry at all.
	LastActiveAt time.Time `json:"last_active_at"`
	// SegmentCount is how many segment records the session has: the start
	// record plus one per segment break, so 1 is a session that never compacted.
	SegmentCount int `json:"segment_count"`
	// Turns is how many turns were started in the session.
	Turns int `json:"turns"`
}

// SessionCardUsage is the session's cumulative usage, the sub-agent sessions it
// started included (store.SumUsageTree: the same numbers the session total of
// chat and ACP shows). CostUSD is the sum of the provider-reported per-call cost
// of the model events.
type SessionCardUsage struct {
	Calls            int64   `json:"calls"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64   `json:"cache_write_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd"`
}

// SessionCard reads where one session stands. It is strictly read-only: no
// entry and no meta is written and no migration is triggered (the repository is
// already open when the runner runs). Every query is scoped to the session -
// store.RecentEntries and store.LastEntryAt walk the (session_id, seq) primary
// key backwards and stop at the first rows, store.CountTurns reads
// idx_turns_session, store.SumUsageTree aggregates the session's model events -
// so the cost of a card does not grow with the store, only with the session
// itself (BenchmarkSessionCard in session_card_test.go measures it).
//
// A session that does not exist returns SessionNotFoundError, which unwraps to
// ErrSessionNotFound.
func (r *Runner) SessionCard(ctx context.Context, sessionID string) (SessionCard, error) {
	model, err := r.LoadSession(ctx, sessionID)
	if err != nil {
		return SessionCard{}, SessionNotFound(err, sessionID)
	}
	card := SessionCard{
		SessionID: model.ID,
		Title:     model.Title,
		ShortName: strings.TrimSpace(metaString(model.Meta, metaShortName)),
		State:     model.Status,
		Origin:    store.SessionOrigin(model),
		CreatedAt: model.CreatedAt,
	}
	if err := r.fillCardUsage(ctx, &card); err != nil {
		return SessionCard{}, err
	}
	// A handoff entry and a segment record both hold a summary; the newest one
	// with a body is the session's latest summary, whichever type it is.
	recents, err := r.cfg.Repository.RecentEntries(ctx, sessionID,
		[]string{store.EntryHandoff, store.EntryCompaction}, sessionCardSummaryCandidates)
	if err != nil {
		return SessionCard{}, fmt.Errorf("session card summary: %w", err)
	}
	fillCardSummary(recents, &card)
	if err := r.fillCardPlan(ctx, &card); err != nil {
		return SessionCard{}, err
	}
	if card.SegmentCount, err = r.cfg.Repository.CountEntriesByType(ctx, sessionID, store.EntryCompaction); err != nil {
		return SessionCard{}, fmt.Errorf("session card segments: %w", err)
	}
	if card.Turns, err = r.cfg.Repository.CountTurns(ctx, sessionID); err != nil {
		return SessionCard{}, fmt.Errorf("session card turns: %w", err)
	}
	// Last activity is the newest entry of the log; a session that never stored
	// one (an empty session) falls back to the update time the store keeps.
	last, err := r.cfg.Repository.LastEntryAt(ctx, sessionID)
	if err != nil {
		return SessionCard{}, fmt.Errorf("session card last activity: %w", err)
	}
	if last.IsZero() {
		last = model.UpdatedAt
	}
	card.LastActiveAt = last
	return card, nil
}

// fillCardUsage copies the session's cumulative usage into the card (the
// statistics stay store.SumUsageTree's; nothing is recomputed here).
func (r *Runner) fillCardUsage(ctx context.Context, card *SessionCard) error {
	sum, err := r.cfg.Repository.SumUsageTree(ctx, card.SessionID)
	if err != nil {
		return fmt.Errorf("session card usage: %w", err)
	}
	card.Usage = SessionCardUsage{
		Calls:            sum.Calls,
		InputTokens:      sum.InputTokens,
		OutputTokens:     sum.OutputTokens,
		CacheReadTokens:  sum.CacheReadTokens,
		CacheWriteTokens: sum.CacheWriteTokens,
		CostUSD:          sum.CostUSD,
	}
	return nil
}

// fillCardSummary sets the summary fields from the newest summary entry of
// entries (newest first) that carries a body. A start record, an undecodable
// entry or a summary that ended up empty is skipped, exactly as loadHistory
// treats an empty package.
func fillCardSummary(entries []store.Entry, card *SessionCard) {
	for _, e := range entries {
		switch e.Type {
		case store.EntryHandoff:
			var rec handoffRecord
			if json.Unmarshal(e.JSON, &rec) != nil || strings.TrimSpace(rec.Summary) == "" {
				continue
			}
			card.LatestSummary, card.SummaryKind, card.SummaryReason = rec.Summary, SessionCardSummaryHandoff, ""
		case store.EntryCompaction:
			var rec compactionRecord
			if json.Unmarshal(e.JSON, &rec) != nil || strings.TrimSpace(rec.Summary) == "" {
				continue
			}
			card.LatestSummary, card.SummaryKind, card.SummaryReason = rec.Summary, SessionCardSummaryCompaction, rec.Reason
		default:
			continue
		}
		card.SummaryAt = e.CreatedAt
		return
	}
}

// fillCardPlan sets the open steps of the session's latest plan and when it was
// recorded. The plan is decoded by latestPlanModel and its open steps are the
// nudge rules' (openPlanSteps), so a card and a nudge finding never disagree.
func (r *Runner) fillCardPlan(ctx context.Context, card *SessionCard) error {
	entry, err := r.cfg.Repository.LatestEntry(ctx, card.SessionID, store.EntryPlan)
	if err != nil {
		return fmt.Errorf("session card plan: %w", err)
	}
	if entry == nil || len(entry.JSON) == 0 {
		return nil
	}
	card.PlanUpdatedAt = entry.CreatedAt
	card.OpenSteps = openPlanSteps(latestPlanModel(card.SessionID, string(entry.JSON)))
	return nil
}
