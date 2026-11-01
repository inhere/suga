package personal

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/inhere/suga/internal/personal/store"
)

// Masked readers (session rewind design 0.2, plan T2).
//
// A conversation rewind appends an EntryRewind record; the entries strictly
// between its cut_seq and its own seq are masked (store.SeqRanges). Every
// reader that feeds the model request, a handoff package or the state a person
// sees must skip them, and gets the intervals from maskedRanges below.
//
// Readers wired to maskedRanges: loadHistory (and so SegmentHistory), latestPlan
// (handoff, segment package, nudge), recentRequests, buildHandoff's plan, LastReply,
// the four session card fields, and the Ledger (session_search excludes the
// current session's masked rows, recall skips them and rewrites a continuation
// ref that would land inside one).
//
// Readers that stay as they are, and why:
//   - compact.go:176, replay.go:233 (markInterruptedToolCalls), admit.go:165,
//     steer.go:418 (turnSteers): they read the entries of one turn by id, and a
//     masked turn is never adopted as the current turn again;
//   - :checkpoints / :undo (undo.go ListTurns): they count what is on disk, since
//     snapshots are not masked;
//   - segment_clear.go:100 LastEntrySeq: reads a seq, never content;
//   - sugasdk Entries and engine.go:296 search, the CLI search and trace: the
//     masked rows stay auditable (R9), so they keep the zero ExcludeSession;
//   - HasSearchableEntries: it only picks the wording of the "no history" notice
//     and takes no ExcludeSession on purpose.
//
// There is deliberately no cache of the intervals (plan S20, F29): another
// Store instance or process may append a rewind record at any time, and the
// readers must see it on their next call. RewindRecords is a point lookup on
// idx_entries_type, so one call per reader is cheap (BenchmarkSessionCard).

// maskedRanges returns the union of the masked intervals of a session (nil when
// it was never rewound).
func (r *Runner) maskedRanges(ctx context.Context, sessionID string) (store.SeqRanges, error) {
	return maskedRangesOf(ctx, r.cfg.Repository, sessionID)
}

func maskedRangesOf(ctx context.Context, repo *store.Store, sessionID string) (store.SeqRanges, error) {
	recs, err := repo.RewindRecords(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read rewind records: %w", err)
	}
	return store.RangesOf(recs), nil
}

// dropMasked removes the entries inside a masked interval, in place. With no
// intervals it returns entries untouched.
func dropMasked(entries []store.Entry, masked store.SeqRanges) []store.Entry {
	if len(masked) == 0 {
		return entries
	}
	out := entries[:0]
	for _, e := range entries {
		if !masked.Hidden(e.Seq) {
			out = append(out, e)
		}
	}
	return out
}

// maskedOrWarn is maskedRanges for the readers whose signature cannot return an
// error (latestPlan). A failed read hides nothing it should show: the reader
// answers "none", which is the fail-closed direction, and the cause is logged.
func (r *Runner) maskedOrWarn(ctx context.Context, sessionID string) (store.SeqRanges, bool) {
	masked, err := r.maskedRanges(ctx, sessionID)
	if err != nil {
		slog.Warn("read masked ranges; reader answers none", "session_id", sessionID, "err", err)
		return nil, false
	}
	return masked, true
}
