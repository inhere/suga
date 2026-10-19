package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// metaLoadedTools is the session meta key holding the names of the deferred
// tools loaded so far (written by tool_search; a handoff inherits it).
const metaLoadedTools = "loaded_tools"

// handoffRecord is the JSON body of a handoff entry: the first entry of a
// session created by --from.
type handoffRecord struct {
	Source      string          `json:"source"`
	Summary     string          `json:"summary"`
	Plan        json.RawMessage `json:"plan,omitempty"` // source's latest plan entry, verbatim
	LoadedTools []string        `json:"loaded_tools,omitempty"`
}

// HandoffRequest creates a new session that continues from SourceID.
type HandoffRequest struct {
	SourceID string
	// NewID is the id of the new session ("" = generate). It must not exist.
	NewID string
	Title string
	// Origin names the entry point creating the new session (personal.OriginChat
	// from chat, personal.OriginRun from run); it is stored as the new session's
	// meta.origin.
	Origin string
}

// handoffMessage renders the record as the first user message of the new
// session. It is an ordinary context item (seq = the handoff entry), so a later
// compaction naturally folds it into its summary.
func handoffMessage(rec handoffRecord) agentcore.Message {
	var sb strings.Builder
	fmt.Fprintf(&sb, "以下是从会话 %s 交接过来的上下文（摘要与当前计划），请据此继续工作：\n\n%s", rec.Source, rec.Summary)
	if len(rec.Plan) > 0 {
		sb.WriteString("\n\n## 交接时的计划\n" + string(rec.Plan))
	}
	return agentcore.Message{Role: "user", Content: sb.String()}
}

// Handoff creates a new session whose first entry is a handoff record built
// from the source session. The source is only read: nothing is written to it
// and its session lock is not taken. The latest adopted summary is reused when
// it already covers the whole source view; otherwise a summary is generated in
// memory (model event recorded under the new session id).
//
// The record is built before the session exists, so a failed handoff leaves no
// session behind and the same NewID can be retried. The summary call's events
// carry the new id anyway (events have no foreign key), so a later successful
// retry also accounts for the failed attempt's tokens.
func (r *Runner) Handoff(ctx context.Context, req HandoffRequest) (Session, error) {
	src, err := r.LoadSession(ctx, req.SourceID)
	if err != nil {
		return Session{}, err
	}
	newID := req.NewID
	if newID == "" {
		newID = NewSessionID()
	}
	if err := validateSessionID(newID); err != nil {
		return Session{}, err
	}
	if _, err := r.cfg.Repository.GetSession(ctx, newID); err == nil {
		return Session{}, fmt.Errorf("session %q already exists; --from creates a new session", newID)
	} else if !errors.Is(err, ErrSessionNotFound) {
		return Session{}, err
	}
	rec, err := r.buildHandoff(withObserveScope(ctx, newID, ""), src)
	if err != nil {
		return Session{}, fmt.Errorf("handoff from %q: %w", src.ID, err)
	}
	title := req.Title
	if strings.TrimSpace(title) == "" {
		title = "Handoff from " + src.ID
	}
	model, err := r.NewSessionFor(ctx, newID, title, req.Origin)
	if err != nil {
		return Session{}, err
	}
	if err := r.writeHandoff(ctx, model, rec); err != nil {
		// Release the id: a session without its handoff entry must never be resumed.
		_ = r.cfg.Repository.DeleteSession(context.WithoutCancel(ctx), model.ID)
		return Session{}, fmt.Errorf("handoff from %q: %w", src.ID, err)
	}
	return r.LoadSession(ctx, model.ID)
}

// buildHandoff reads the source (read-only) and produces the record.
func (r *Runner) buildHandoff(ctx context.Context, src Session) (handoffRecord, error) {
	view, err := r.loadHistory(ctx, src.ID)
	if err != nil {
		return handoffRecord{}, err
	}
	rec := handoffRecord{Source: src.ID, Summary: view.summary, LoadedTools: loadedToolNames(src.Meta)}
	// A summary only stands for itself when no message is kept after it (e.g.
	// a manual :compact); a kept tail must be folded in, so build a package
	// the same way a segment break does (the plan stays in rec.Plan).
	if len(view.items) > 0 {
		requests, err := r.recentRequests(ctx, src.ID, "", recentRequestCount)
		if err != nil {
			return handoffRecord{}, err
		}
		body, err := r.summarize(ctx, packageInput{previous: view.summary, dropped: view.items, plan: r.latestPlan(ctx, src.ID),
			requests: requests, candidates: refCandidates(src.ID, view.items, len(view.items))}, r.threshold())
		if err != nil {
			return handoffRecord{}, err
		}
		rec.Summary = renderPackage(body, requests, nil, "")
	}
	if strings.TrimSpace(rec.Summary) == "" {
		return handoffRecord{}, errors.New("source session has no content to hand off")
	}
	if e, err := r.cfg.Repository.LatestEntry(ctx, src.ID, store.EntryPlan); err != nil {
		return handoffRecord{}, err
	} else if e != nil && len(e.JSON) > 0 {
		rec.Plan = e.JSON
	}
	return rec, nil
}

// threshold mirrors contextBuilder.threshold for the summary size cap.
func (r *Runner) threshold() int {
	window, reserve := r.compactLimits()
	return thresholdOf(window, reserve)
}

// ContextThreshold is the context size (tokens) at which a turn starts a new
// segment. It depends only on the runner config, not on the session; chat
// compares the last request's input tokens with it before an idle
// consolidation (context.idle_min_ratio).
func (r *Runner) ContextThreshold() int { return r.threshold() }

// writeHandoff appends the handoff entry and inherits loaded tools into meta.
func (r *Runner) writeHandoff(ctx context.Context, model Session, rec handoffRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := r.cfg.Repository.AppendEntries(ctx, model.ID, store.NewEntry{Type: store.EntryHandoff, JSON: raw}); err != nil {
		return err
	}
	if len(rec.LoadedTools) > 0 {
		return r.cfg.Repository.PatchSessionMeta(ctx, model.ID, map[string]any{metaLoadedTools: rec.LoadedTools})
	}
	return nil
}

// loadedToolNames reads the loaded deferred tool names from session meta.
func loadedToolNames(meta map[string]any) []string {
	var out []string
	switch v := meta[metaLoadedTools].(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
