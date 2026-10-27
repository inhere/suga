package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
)

// Run modes recorded in turn_context (Config.Mode).
const (
	ModeReadOnly   = "read_only"
	ModeAllowWrite = "allow_write"
)

// metaMemoryIndexHash is the session meta key of the frozen memory index
// snapshot hash (see memory.go; "" without a memory source).
const metaMemoryIndexHash = "memory_index_hash"

// MetaRunMode is the session meta key of the run mode the session last ran a
// turn in (ModeReadOnly / ModeAllowWrite). The mode decides which tools a turn
// declares, so it is part of the request head: a segment break between turns in
// another process (`suga session compact`) reads it to declare the same tool
// table as the turn whose request the handoff package continues (H4).
const MetaRunMode = "run_mode"

// toolSetHashLen is how many hex digits of the tool set hash are kept.
const toolSetHashLen = 12

// recordTurnContext writes the turn_context event (D9): enough to answer
// "what did the model see when this turn started" without storing the prompt
// itself. model is the session as of the turn start; seg is the segment
// snapshot (its hash identifies it) and eff the effective context after this
// turn's context update, whose rules/memory hashes are what the turn sees.
func (r *Runner) recordTurnContext(ctx context.Context, model Session, seg, eff *segmentContext) {
	var names []string
	if r.cfg.Tools != nil {
		schemas, err := r.cfg.Tools.ListTools(ctx)
		if err != nil {
			slog.Warn("list tools for turn_context", "session_id", model.ID, "err", err)
		}
		for _, s := range schemas {
			names = append(names, s.Name)
		}
	}
	mode := r.cfg.Mode
	if mode == "" {
		mode = "unknown"
	}
	_, tainted := model.Meta[metaTaintedAt]
	e := store.Event{
		Kind: store.EventTurnContext, Name: store.EventTurnContext, Status: eventOK, StartedAt: time.Now(),
		Attrs: map[string]any{
			"model":       r.cfg.Model,
			"provider":    r.cfg.ProviderName,
			"mode":        mode,
			"tools":       toolSetHash(names),
			"tools_count": len(names),
			"rules_hash":  eff.RulesHash,
			// Always present (empty when the segment has no memory index).
			"memory_hash":  eff.MemoryHash,
			"tainted":      tainted,
			"segment_hash": seg.Hash,
		},
	}
	if r.cfg.Version != "" {
		e.Attrs["version"] = r.cfg.Version
	}
	// The preferred tools this turn's environment section listed, in
	// configuration order. Names only: the declaration's scenario text,
	// command paths, MCP arguments and secrets stay out of the trace (design
	// 0.2). The source is read, not re-probed, so it matches the prompt.
	if r.cfg.PreferredToolsSource != nil {
		if names := r.cfg.PreferredToolsSource(); len(names) > 0 {
			e.Attrs["preferred_tools"] = names
		}
	}
	// The files outside the workspace this session may reach (--add-dir):
	// "path:ro"/"path:rw", the effective mode of this run.
	if r.cfg.AddDirs != nil {
		if dirs := r.cfg.AddDirs(); len(dirs) > 0 {
			e.Attrs["add_dirs"] = dirs
		}
	}
	r.rec.record(ctx, e)
	// Remember the mode for a between-turn break of another process (H4): the
	// tools of a turn depend on it, and the break rebuilds that turn's request.
	if mode := r.cfg.Mode; mode != "" && metaString(model.Meta, MetaRunMode) != mode {
		if err := r.cfg.Repository.PatchSessionMeta(ctx, model.ID, map[string]any{MetaRunMode: mode}); err != nil {
			slog.Warn("store the run mode", "session_id", model.ID, "err", err)
		}
	}
}

// toolSetHash is the first toolSetHashLen hex digits of sha256 over the
// sorted tool names (one per line), so the hash ignores listing order.
func toolSetHash(names []string) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:])[:toolSetHashLen]
}

func metaString(meta map[string]any, key string) string {
	s, _ := meta[key].(string)
	return s
}
