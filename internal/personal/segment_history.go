package personal

import (
	"context"

	"github.com/inhere/suga/pkg/agentcore"
)

// SegmentHistory is the read-only projection of one session's current segment
// for a front end that replays persisted history to its client (ACP
// session/load, design 2026-10-20): the segment's handoff package and the kept
// messages in order. It is exactly the view loadHistory assembles — the same
// boundaries and adoption rules, no second implementation of them — so what a
// client sees is what the next turn's model sees.
type SegmentHistory struct {
	// Summary is the handoff package of the current segment: the latest adopted
	// compaction summary ("" when there is none). The client replay shows it as
	// one thought note outside the conversation (replay.go in acpserver).
	Summary string
	// Items are the kept message entries of the segment in their stored order,
	// hydrated (full bodies, no stubs).
	Items []SegmentItem
}

// SegmentItem is one kept message with the entries seq it was persisted under.
type SegmentItem struct {
	Seq int64
	Msg agentcore.Message
}

// SegmentHistory returns the persisted view of the session's current segment.
// It fails when the store cannot be read; the caller decides what a replay
// failure means (ACP session/load refuses).
func (r *Runner) SegmentHistory(ctx context.Context, sessionID string) (SegmentHistory, error) {
	view, err := r.loadHistory(ctx, sessionID)
	if err != nil {
		return SegmentHistory{}, err
	}
	out := SegmentHistory{Summary: view.summary}
	for _, it := range view.items {
		out.Items = append(out.Items, SegmentItem{Seq: it.seq, Msg: it.msg})
	}
	return out, nil
}
