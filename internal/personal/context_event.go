package personal

import (
	"context"
	"encoding/json"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// SkillsStats counts the installed skills by how the skills listing
// presents them: with their description, by name only, or preloaded whole.
type SkillsStats struct {
	Listed    int `json:"listed"`
	Named     int `json:"named"`
	Preloaded int `json:"preloaded"`
	Total     int `json:"total"`
}

// recordContext writes the turn's context event once, at its first model
// request: what the context is made of, in chars per part, so trace and the
// web view can show where the window goes (the parts' text is in the
// segment snapshot and the transcript). est is the estimated request tokens.
func (b *contextBuilder) recordContext(ctx context.Context, state *agentcore.RunState, est int) {
	if b.contextRecorded {
		return
	}
	b.contextRecorded = true
	attrs := map[string]any{"est_tokens": est, "window": b.window}
	if b.prompt != nil && state != nil {
		if s, err := b.prompt.BuildSystemPrompt(ctx, state); err == nil {
			attrs["system"] = len(s)
		}
	}
	if reg := b.r.cfg.Tools; reg != nil {
		if schemas, err := reg.ListTools(ctx); err == nil {
			raw, _ := json.Marshal(schemas)
			attrs["tools"], attrs["tools_count"] = len(raw), len(schemas)
			names := make([]string, len(schemas))
			for i, s := range schemas {
				names[i] = s.Name
			}
			attrs["tool_names"] = names
			if l, ok := reg.(tools.Lookup); ok {
				mcp := 0
				for _, s := range schemas {
					if t, err := l.Get(s.Name); err == nil && tools.IsMCP(t) {
						mcp++
					}
				}
				attrs["tools_mcp"] = mcp
			}
		}
	}
	if v := b.r.cfg.Visibility; v != nil {
		// Deferred native tools are not in the table, so the counts above cannot
		// show them: report the deferred set and what the session loaded (Q7).
		native, loaded := v.DeferredStats(ctx)
		attrs["deferred_native"], attrs["deferred_loaded"] = native, loaded
	}
	if seg := b.seg; seg != nil {
		for key, text := range map[string]string{"environment": seg.Environment, "rules": seg.Rules,
			"memory_index": seg.MemoryIndex, "skills": seg.Skills, "append_prompt": seg.AppendPrompt,
			"deferred_tools": seg.DeferredTools} {
			attrs[key] = len(text)
		}
		attrs["segment_hash"] = seg.Hash
	}
	if src := b.r.cfg.SkillsStats; src != nil {
		st := src()
		attrs["skills_listed"], attrs["skills_named"], attrs["skills_preloaded"], attrs["skills_total"] = st.Listed, st.Named, st.Preloaded, st.Total
	}
	attrs["handoff"] = len(b.summary)
	history := 0
	for _, it := range b.prev {
		history += charsOfMessages([]agentcore.Message{it.msg})
	}
	attrs["history"], attrs["history_messages"] = history, len(b.prev)
	attrs["hook_context"] = len(b.hookContext)
	input := 0
	if state != nil && b.turnStart < len(state.Messages) {
		input = charsOfMessages(state.Messages[b.turnStart:])
	}
	attrs["input"] = input
	b.r.rec.record(ctx, store.Event{Kind: store.EventContext, Name: "turn", Status: eventOK,
		StartedAt: time.Now(), Attrs: attrs})
}
