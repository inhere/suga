package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// segmentReasonClear is the reason of a --clear-ctx segment record: a break
// that keeps nothing from the closing segment - no handoff package, no history,
// no plan and no loaded deferred tools. The old entries stay in the log (they
// are still searchable and visible in `suga trace`); the new record's
// first_kept_seq simply sits after all of them, so loadHistory stops keeping
// them.
const segmentReasonClear = "clear"

// ClearedContext reports what a --clear-ctx break took out of the turn's
// context: how many message entries left it and their estimated tokens.
type ClearedContext struct {
	// Messages is the number of history messages that left the context.
	Messages int `json:"messages"`
	// Tokens is their estimated size (tokensOfMessages plus the handoff package
	// of the closed segment): what the next turn no longer sends.
	Tokens int `json:"tokens"`
	// Segment is the ordinal of the new segment record (1-based; 0 unknown).
	Segment int `json:"segment"`
}

// ClearContext starts a fresh segment of the session before the next turn: the
// turn then begins with the resident parts only (system prompt, project rules,
// segment context snapshot, append prompt) and none of the previous messages.
//
// It is a segment break of the same machinery a compaction uses (the record,
// loadHistory and the snapshot are shared), with two differences: no handoff
// package is built - nothing is summarised, so no model call happens - and the
// session's plan and loaded deferred tools are cleared with it. The closed
// segment's entries are not deleted.
//
// The session must exist (the caller decides what a missing one means) and it
// holds the session gate like Compact, so a running turn finishes first.
func (r *Runner) ClearContext(ctx context.Context, sessionID string) (ClearedContext, error) {
	release, err := r.gate(sessionID).Acquire(ctx)
	if err != nil {
		return ClearedContext{}, err
	}
	defer release()
	model, err := r.LoadSession(ctx, sessionID)
	if err != nil {
		return ClearedContext{}, err
	}
	if model.Status == store.SessionArchived || model.Status == store.SessionRunning {
		return ClearedContext{}, fmt.Errorf("session %q is not ready for a context clear: %s", model.ID, model.Status)
	}
	r.locksMu.Lock()
	_, held := r.heldLocks[model.ID]
	r.locksMu.Unlock()
	if r.cfg.LockRoot != "" && !held {
		lock, err := process.AcquireSessionLockContext(ctx, r.cfg.LockRoot, model.ID, 30*time.Second)
		if err != nil {
			return ClearedContext{}, err
		}
		defer lock.Close()
	}
	ctx = withObserveScope(ctx, model.ID, "")
	hist, err := r.loadHistory(ctx, model.ID)
	if err != nil {
		return ClearedContext{}, err
	}
	r.resolveSegment(&hist, model.Meta)
	msgs := make([]agentcore.Message, 0, len(hist.items))
	for _, it := range hist.items {
		msgs = append(msgs, it.msg)
	}
	dropped := len(msgs)
	// The package of the closed segment goes with its messages, so it counts in
	// what the clear took away.
	tokensBefore := tokensOfMessages(msgs) + EstimateTokens(hist.summary)
	// The loaded deferred tools of the closed segment go with it (before the
	// snapshot is taken, so its listing shows the whole deferred set again).
	if r.cfg.Visibility != nil {
		if err := r.cfg.Visibility.ClearLoaded(ctx); err != nil {
			slog.Warn("clear loaded deferred tools", "session_id", model.ID, "err", err)
		}
	}
	// Everything the new segment carries besides the messages is re-read now:
	// rules, memory index, environment, skills, append prompt, deferred listing.
	seg, serr := r.freshSnapshot(ctx, model.ID, hist.eff)
	if serr != nil {
		slog.Warn("re-read rules for the new segment; keeping the previous rules", "session_id", model.ID, "err", serr)
	}
	// The record is written after the session's last entry, so no earlier entry
	// is kept by the view (loadHistory drops everything below first_kept_seq).
	lastSeq, err := r.cfg.Repository.LastEntrySeq(ctx, model.ID)
	if err != nil {
		return ClearedContext{}, err
	}
	wctx := context.WithoutCancel(ctx)
	rec := compactionRecord{Summary: "", FirstKeptSeq: lastSeq + 1, TokensBefore: tokensBefore, TokensAfter: 0,
		Reason: segmentReasonClear, Context: seg}
	raw, err := json.Marshal(rec)
	if err != nil {
		return ClearedContext{}, err
	}
	es, err := r.cfg.Repository.AppendEntries(wctx, model.ID, store.NewEntry{
		// The break belongs to no turn, so the next turn adopts it immediately.
		Type: store.EntryCompaction, JSON: raw,
	})
	if err != nil {
		return ClearedContext{}, fmt.Errorf("write context clear entry: %w", err)
	}
	// The plan is part of the cleared context: an empty plan entry becomes the
	// session's current plan, so a later handoff package or session card does not
	// carry the closed segment's task list.
	emptyPlan, _ := json.Marshal(native.Plan{Steps: []native.PlanStep{}})
	if _, err := r.cfg.Repository.AppendEntries(wctx, model.ID, store.NewEntry{Type: store.EntryPlan, JSON: emptyPlan}); err != nil {
		slog.Warn("clear the session plan", "session_id", model.ID, "err", err)
	}
	segment := r.segmentOrdinal(wctx, model.ID, es)
	r.cacheSegmentMeta(wctx, model.ID, seg) // also resets the fold baseline
	// The closed segment's last request is spent: a later break rebuilds the
	// prefix instead of continuing a request no one sent.
	r.rec.prefix.clearRequest(model.ID)
	cleared := ClearedContext{Messages: dropped, Tokens: tokensBefore, Segment: segment}
	r.rec.record(wctx, store.Event{
		Kind: store.EventContextCleared, Name: "clear_ctx", Status: eventOK, StartedAt: time.Now(),
		Attrs: map[string]any{"dropped_messages": dropped, "est_tokens": tokensBefore,
			"first_kept_seq": rec.FirstKeptSeq, "segment": segment},
	})
	return cleared, nil
}
