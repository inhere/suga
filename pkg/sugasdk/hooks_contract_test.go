package sugasdk

// Contract tests for host hooks around tool calls: the SDK hands the host's
// agentcore.Hook to the runtime unchanged, so the three hard rules hold here
// too — a rewritten call is approved on its new arguments, a hook allow never
// replaces the approval decision, and a panicking hook fails closed.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// rewritingHook rewrites the text argument of every tool call (BeforeTool) or
// panics there, standing in for a malformed host hook.
type rewritingHook struct {
	agentcore.NopHook
	text  string
	panic bool
}

func (h *rewritingHook) BeforeTool(_ context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if h.panic {
		panic("host hook boom")
	}
	if event.Call != nil {
		event.Call.Arguments["text"] = h.text
	}
	return nil
}

// gateApprover records the arguments of every decision and refuses the
// protected text, like an approval rule would.
type gateApprover struct {
	mu   sync.Mutex
	seen []string
}

func (a *gateApprover) Decide(_ context.Context, call api.ToolCall, _ api.Tool) error {
	text, _ := call.Arguments["text"].(string)
	a.mu.Lock()
	a.seen = append(a.seen, text)
	a.mu.Unlock()
	if text == "protected" {
		return tools.Denied("受保护参数")
	}
	return nil
}

func (a *gateApprover) asks() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

// TestHostHookRewrittenCallIsApprovedAgain proves that an SDK host hook cannot
// use the approval for one argument set and run another.
func TestHostHookRewrittenCallIsApprovedAgain(t *testing.T) {
	ctx := context.Background()
	server := chatServer(t, "sdk_echo", "all done", nil)
	home := sdkTestHome(t, server.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })

	var calls int32
	approver := &gateApprover{}
	session, err := engine.OpenSession(ctx, "", SessionOptions{
		Workspace:  t.TempDir(),
		ExtraTools: []api.Tool{echoTool{calls: &calls}},
		Hooks:      []agentcore.Hook{&rewritingHook{text: "protected"}},
		Approver:   approver,
	})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.Send(ctx, Prompt{Text: "use the echo tool"}, func(Event) {})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "COMPLETED", result.State)
	// The tool was refused on the rewritten arguments: it never ran.
	assert.Eq(t, int32(0), calls)
	assert.Eq(t, []string{"protected"}, approver.asks())
}

// TestHostHookPanicFailsClosed proves that a panicking host hook refuses the
// call instead of running it with the arguments the model sent.
func TestHostHookPanicFailsClosed(t *testing.T) {
	ctx := context.Background()
	server := chatServer(t, "sdk_echo", "all done", nil)
	home := sdkTestHome(t, server.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })

	var calls int32
	session, err := engine.OpenSession(ctx, "", SessionOptions{
		Workspace:  t.TempDir(),
		ExtraTools: []api.Tool{echoTool{calls: &calls}},
		Hooks:      []agentcore.Hook{&rewritingHook{panic: true}},
		Approver:   allowAll{},
	})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = session.Close() })

	_, err = session.Send(ctx, Prompt{Text: "use the echo tool"}, func(Event) {})
	assert.Require(t, assert.Err(t, err))
	assert.Eq(t, true, strings.Contains(err.Error(), "host hook boom"))
	assert.Eq(t, int32(0), calls)
}
