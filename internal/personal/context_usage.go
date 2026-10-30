package personal

import (
	"context"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// ContextOccupancy is the turn's context size as stream-json reports it on its
// turn_end and result lines: the estimated input tokens of the turn's last
// request and the window they are measured against. Both come from where the
// context trace event reads them (the builder's estimate, compactLimits), so a
// caller such as gofer sees the same figure as `suga trace`.
type ContextOccupancy struct {
	EstTokens int `json:"est_tokens"`
	Window    int `json:"window"`
}

// ContextUsage is the persisted half of a session's context composition, for
// chat's :context command. The handoff and history figures come from the same
// view a turn assembles (loadHistory) and are measured by the same helpers the
// context trace event uses (charsOfMessages, tokensOfMessages), so the command
// and `suga trace` never disagree about what the context holds.
type ContextUsage struct {
	// Handoff is the handoff package of the current segment (0 without one).
	Handoff       int `json:"handoff"`
	HandoffTokens int `json:"handoff_tokens"`
	// History is the kept history of the segment: its chars, its estimated
	// tokens and how many messages it holds.
	History         int `json:"history"`
	HistoryTokens   int `json:"history_tokens"`
	HistoryMessages int `json:"history_messages"`
	// Input is the size of the last turn's own messages, EstTokens the trace's
	// estimate of that turn's request and Window its context window: all three
	// are read from the session's last context event and stay zero before the
	// first turn.
	Input     int `json:"input"`
	EstTokens int `json:"est_tokens"`
	Window    int `json:"window"`
}

// ContextUsage returns what the current context holds besides the resident
// parts. Reading the persisted history fails only when the store cannot be
// read; a missing context event is not an error (the session has run no turn).
func (r *Runner) ContextUsage(ctx context.Context, sessionID string) (ContextUsage, error) {
	view, err := r.loadHistory(ctx, sessionID)
	if err != nil {
		return ContextUsage{}, err
	}
	msgs := make([]agentcore.Message, 0, len(view.items))
	for _, it := range view.items {
		msgs = append(msgs, it.msg)
	}
	usage := ContextUsage{
		Handoff: len(view.summary), HandoffTokens: EstimateTokens(view.summary),
		History: charsOfMessages(msgs), HistoryTokens: tokensOfMessages(msgs), HistoryMessages: len(msgs),
	}
	if last := r.lastContextEvent(ctx, sessionID); last != nil {
		usage.Input, usage.EstTokens, usage.Window = attrInt(last.Attrs["input"]), attrInt(last.Attrs["est_tokens"]), attrInt(last.Attrs["window"])
	}
	return usage, nil
}

// lastContextEvent is the newest context event of the session (nil when no turn
// has recorded one). ListEvents returns oldest first, so the last match wins.
func (r *Runner) lastContextEvent(ctx context.Context, sessionID string) *store.Event {
	if r == nil || r.cfg.Repository == nil || sessionID == "" {
		return nil
	}
	events, err := r.cfg.Repository.ListEvents(ctx, store.EventFilter{SessionID: sessionID, Kinds: []string{store.EventContext}})
	if err != nil || len(events) == 0 {
		return nil
	}
	return &events[len(events)-1]
}

// LoadedFile is one file a session's tool put into its context: the path, the
// tool that touched it, the turn it happened in and what the result cost.
type LoadedFile struct {
	Path string `json:"path"`
	Tool string `json:"tool"`
	// Turn is the 1-based turn this use belongs to (the order the turns appear
	// in the session's events, not a stored id).
	Turn  int `json:"turn"`
	Bytes int `json:"bytes"`
}

// fileContextTools are the tools whose result is a file's content, so a target
// of theirs is a file the context carries.
var fileContextTools = map[string]bool{"file_read": true, "file_edit": true, "file_write": true, "view_image": true}

// LoadedFiles lists the files this session's file tools touched, most recent
// use first, at most limit of them (0 = all). It reads the recorded tool events
// only — the paths from addCallAttrs' "target" and the size from
// addOutputAttrs' "inline_bytes" — so no file is read or measured again, and a
// path is listed once, under its latest use.
func (r *Runner) LoadedFiles(ctx context.Context, sessionID string, limit int) []LoadedFile {
	if r == nil || r.cfg.Repository == nil || sessionID == "" {
		return nil
	}
	events, err := r.cfg.Repository.ListEvents(ctx, store.EventFilter{SessionID: sessionID, Kinds: []string{store.EventTool}})
	if err != nil {
		return nil
	}
	// Number the turns by the order they first appear, so a listing can say
	// which turn read a file without resolving turn ids.
	turns := map[string]int{}
	var uses []LoadedFile
	for _, event := range events {
		if !fileContextTools[event.Name] {
			continue
		}
		turn := 0
		if event.TurnID != "" {
			if n, ok := turns[event.TurnID]; ok {
				turn = n
			} else {
				turn = len(turns) + 1
				turns[event.TurnID] = turn
			}
		}
		for _, target := range attrStrings(event.Attrs["target"]) {
			uses = append(uses, LoadedFile{Path: target, Tool: event.Name, Turn: turn, Bytes: attrInt(event.Attrs["inline_bytes"])})
		}
	}
	// The events are oldest first: walk them backwards for the newest use, and
	// keep one line per path.
	out := make([]LoadedFile, 0, len(uses))
	seen := make(map[string]bool, len(uses))
	for i := len(uses) - 1; i >= 0; i-- {
		if seen[uses[i].Path] {
			continue
		}
		seen[uses[i].Path] = true
		out = append(out, uses[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
