package personal

import (
	"context"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// loadedToolsStore keeps the session's loaded deferred tool names in session
// meta (key loaded_tools), so a resumed session or a handoff target sees the
// same tool set. The session comes from the observe scope in ctx.
type loadedToolsStore struct{ repo *store.Store }

func (s loadedToolsStore) LoadedTools(ctx context.Context) ([]string, error) {
	id := observeScopeFrom(ctx).sessionID
	if id == "" {
		return nil, nil
	}
	sess, err := s.repo.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return loadedToolNames(sess.Meta), nil
}

func (s loadedToolsStore) SaveLoadedTools(ctx context.Context, names []string) error {
	id := observeScopeFrom(ctx).sessionID
	if id == "" {
		return nil
	}
	return s.repo.PatchSessionMeta(ctx, id, map[string]any{metaLoadedTools: names})
}

// bindVisibility wires the visibility layer to this runner's store and
// recorder: loaded tools persist in session meta, and every load action is
// written as a tool_search event.
func (r *Runner) bindVisibility() {
	v := r.cfg.Visibility
	if v == nil {
		return
	}
	v.Bind(loadedToolsStore{repo: r.cfg.Repository}, func(ctx context.Context, ev tools.SearchEvent) {
		name := ev.Kind
		hits, newly := ev.Hits, ev.NewlyLoaded
		if hits == nil {
			hits = []string{}
		}
		if newly == nil {
			newly = []string{}
		}
		// newly_loaded_kinds splits the newly loaded names by source, so a trace
		// shows whether the model found a built-in or an MCP tool (design Q7).
		kinds := map[string]int{}
		for _, n := range newly {
			if k := r.cfg.Visibility.KindOfDeferred(n); k != "" {
				kinds[k]++
			}
		}
		r.rec.record(ctx, store.Event{
			Kind: store.EventToolSearch, Name: name, Status: eventOK,
			Attrs: map[string]any{
				"query": ev.Query, "hits": hits, "newly_loaded": newly,
				"newly_loaded_kinds": kinds,
				"cache_invalidated":  ev.CacheInvalidated,
			},
		})
	})
}

// deferredToolsPrompt is the segment context section listing deferred tools.
func (r *Runner) deferredToolsPrompt() string {
	if r.cfg.Visibility == nil {
		return ""
	}
	return r.cfg.Visibility.PromptSection()
}
