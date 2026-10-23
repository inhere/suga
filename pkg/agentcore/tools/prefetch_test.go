package tools

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// barrierTool counts its runs; with a barrier set, each run waits until
// barrier.n runs have started (or a timeout), so only concurrent runs finish.
type barrierTool struct {
	name    string
	meta    Meta
	runs    atomic.Int32
	barrier *barrier
}

type barrier struct {
	mu      sync.Mutex
	n, seen int
	all     chan struct{}
}

func newBarrier(n int) *barrier { return &barrier{n: n, all: make(chan struct{})} }

func (b *barrier) wait() error {
	b.mu.Lock()
	b.seen++
	if b.seen == b.n {
		close(b.all)
	}
	b.mu.Unlock()
	select {
	case <-b.all:
		return nil
	case <-time.After(300 * time.Millisecond):
		return errors.New("ran alone")
	}
}

func (t *barrierTool) Name() string        { return t.name }
func (t *barrierTool) Description() string { return t.name }
func (t *barrierTool) Schema() api.ToolSchema {
	return api.ToolSchema{Name: t.name, InputSchema: map[string]any{"type": "object"}}
}
func (t *barrierTool) ToolMeta() Meta { return t.meta }
func (t *barrierTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	t.runs.Add(1)
	if t.barrier != nil {
		if err := t.barrier.wait(); err != nil {
			return nil, err
		}
	}
	return &api.ToolResult{Content: t.name + ":" + argsKey(call.Arguments)}, nil
}

func readCall(id, name, path string) api.ToolCall {
	return api.ToolCall{CallID: id, Name: name, Arguments: map[string]any{"path": path}}
}

// exemptTool is a Prefetchable tool that exempts single calls: one whose path
// names a picture, like file_read does.
type exemptTool struct{ *barrierTool }

// SkipPrefetch implements PrefetchExempt.
func (*exemptTool) SkipPrefetch(args map[string]any) bool {
	path, _ := args["path"].(string)
	return strings.HasSuffix(path, ".png")
}

func TestRegistryPrefetchExempt(t *testing.T) {
	ctx := context.Background()
	prefetchable := Meta{Category: CategoryRead, ReadOnly: true, Prefetchable: true}

	t.Run("an exempt call is not run ahead, its siblings still are", func(t *testing.T) {
		read := &exemptTool{&barrierTool{name: "read", meta: prefetchable}}
		reg := NewRegistry()
		reg.Register(read)
		calls := []api.ToolCall{readCall("c1", "read", "a.txt"), readCall("c2", "read", "b.txt"),
			readCall("c3", "read", "c.png")}
		reg.Prefetch(ctx, calls)
		deadline := time.Now().Add(time.Second)
		for read.runs.Load() < 2 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)
		assert.Eq(t, int32(2), read.runs.Load(), "only the text calls ran ahead")
		// The picture call runs in turn, once.
		_, err := reg.ExecuteTool(ctx, calls[2])
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int32(3), read.runs.Load())
	})
	t.Run("a tool without the interface keeps prefetching every call", func(t *testing.T) {
		read := &barrierTool{name: "read", meta: prefetchable}
		reg := NewRegistry()
		reg.Register(read)
		reg.Prefetch(ctx, []api.ToolCall{readCall("c1", "read", "a.png"), readCall("c2", "read", "b.png")})
		deadline := time.Now().Add(time.Second)
		for read.runs.Load() < 2 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		assert.Eq(t, int32(2), read.runs.Load())
	})
}

func TestRegistryPrefetch(t *testing.T) {
	ctx := context.Background()
	prefetchable := Meta{Category: CategoryRead, ReadOnly: true, Prefetchable: true}

	t.Run("a reply's reads run concurrently, results taken in order", func(t *testing.T) {
		b := newBarrier(3)
		read := &barrierTool{name: "read", meta: prefetchable, barrier: b}
		reg := NewRegistry()
		reg.Register(read)
		calls := []api.ToolCall{readCall("c1", "read", "a"), readCall("c2", "read", "b"), readCall("c3", "read", "c")}
		reg.Prefetch(ctx, calls)
		for _, call := range calls {
			res, err := reg.ExecuteTool(ctx, call) // run one by one, as the runtime does
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, `read:{"path":"`+call.Arguments["path"].(string)+`"}`, res.Content)
			assert.Eq(t, call.CallID, res.CallID)
		}
		assert.Eq(t, int32(3), read.runs.Load()) // each ran once
	})
	t.Run("without prefetch the same calls run alone", func(t *testing.T) {
		read := &barrierTool{name: "read", meta: prefetchable, barrier: newBarrier(2)}
		reg := NewRegistry()
		reg.Register(read)
		_, err := reg.ExecuteTool(ctx, readCall("c1", "read", "a"))
		assert.ErrMsg(t, err, "ran alone")
	})
	t.Run("changed arguments run again", func(t *testing.T) {
		read := &barrierTool{name: "read", meta: prefetchable}
		reg := NewRegistry()
		reg.Register(read)
		reg.Prefetch(ctx, []api.ToolCall{readCall("c1", "read", "a"), readCall("c2", "read", "b")})
		res, err := reg.ExecuteTool(ctx, readCall("c1", "read", "changed")) // a hook rewrote it
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, `read:{"path":"changed"}`, res.Content)
		_, err = reg.ExecuteTool(ctx, readCall("c2", "read", "b"))
		assert.NoErr(t, err)
		// a and b ran ahead (the unused run of a may still be finishing), "changed" in turn
		deadline := time.Now().Add(time.Second)
		for read.runs.Load() < 3 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		assert.Eq(t, int32(3), read.runs.Load())
	})
	t.Run("only prefetchable tools, and only two or more", func(t *testing.T) {
		read := &barrierTool{name: "read", meta: prefetchable}
		write := &barrierTool{name: "write", meta: Meta{Category: CategoryEdit}}
		plan := &barrierTool{name: "plan", meta: Meta{Category: CategoryThink, ReadOnly: true}}
		reg := NewRegistry()
		reg.Register(read)
		reg.Register(write)
		reg.Register(plan)
		reg.Prefetch(ctx, []api.ToolCall{readCall("c1", "read", "a"), readCall("c2", "write", "a"), readCall("c3", "plan", "a")})
		time.Sleep(20 * time.Millisecond)
		assert.Eq(t, int32(0), read.runs.Load()) // a single prefetchable call: nothing ahead
		assert.Eq(t, int32(0), write.runs.Load())
		assert.Eq(t, int32(0), plan.runs.Load())
	})
	t.Run("results of an earlier reply are forgotten", func(t *testing.T) {
		read := &barrierTool{name: "read", meta: prefetchable}
		reg := NewRegistry()
		reg.Register(read)
		reg.Prefetch(ctx, []api.ToolCall{readCall("c1", "read", "a"), readCall("c2", "read", "b")})
		reg.Prefetch(ctx, nil) // the next reply
		assert.Nil(t, reg.takePrefetched(readCall("c1", "read", "a")))
	})
}
