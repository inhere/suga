package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// maxPrefetch bounds the calls of one reply run ahead at the same time.
const maxPrefetch = 8

// prefetch is one call run ahead of its turn.
type prefetch struct {
	args string // the arguments it ran with (JSON)
	done chan struct{}
	res  *api.ToolResult
	err  error
}

// prefetchState holds the runs of the current reply, keyed by call id.
type prefetchState struct {
	mu    sync.Mutex
	calls map[string]*prefetch
}

// Prefetch starts the Prefetchable calls of one model reply concurrently
// (agentcore.Config.PrefetchTools). Every call still goes through the whole
// registry stack in order - hooks, approval, taint tracking, observation,
// output wrapping - and only on reaching ExecuteTool here takes the result run
// ahead, provided its arguments are unchanged; a call refused on the way just
// leaves its result unused. Only side-effect-free local tools are prefetchable,
// so running one that is then refused changes nothing. Runs of an earlier
// reply that were never taken are forgotten. Fewer than two prefetchable calls
// start nothing.
func (r *Registry) Prefetch(ctx context.Context, calls []api.ToolCall) {
	var ahead []api.ToolCall
	for _, call := range calls {
		if t, err := r.Get(call.Name); err == nil && MetaOf(t).Prefetchable && !skipsPrefetch(t, call.Arguments) {
			ahead = append(ahead, call)
		}
	}
	r.prefetched.mu.Lock()
	defer r.prefetched.mu.Unlock()
	r.prefetched.calls = nil
	if len(ahead) < 2 {
		return
	}
	r.prefetched.calls = make(map[string]*prefetch, len(ahead))
	slots := make(chan struct{}, maxPrefetch)
	for _, call := range ahead {
		if call.CallID == "" || r.prefetched.calls[call.CallID] != nil {
			continue
		}
		p := &prefetch{args: argsKey(call.Arguments), done: make(chan struct{})}
		r.prefetched.calls[call.CallID] = p
		go func() {
			slots <- struct{}{}
			defer func() {
				if v := recover(); v != nil {
					p.res, p.err = nil, fmt.Errorf("tool %s panicked: %v", call.Name, v)
				}
				<-slots
				close(p.done)
			}()
			p.res, p.err = r.execute(ctx, call)
		}()
	}
}

// takePrefetched returns and forgets the run ahead for call, nil when there
// is none or its arguments differ (a hook changed them).
func (r *Registry) takePrefetched(call api.ToolCall) *prefetch {
	r.prefetched.mu.Lock()
	defer r.prefetched.mu.Unlock()
	p := r.prefetched.calls[call.CallID]
	if p == nil {
		return nil
	}
	delete(r.prefetched.calls, call.CallID)
	if p.args != argsKey(call.Arguments) {
		return nil
	}
	return p
}

// argsKey is a canonical form of call arguments (encoding/json sorts keys).
func argsKey(args map[string]any) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("%v", args)
	}
	return string(raw)
}
