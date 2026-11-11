package sugasdk

// SessionOptions.ApprovalMode: trust in the SDK only lifts suga's own outer
// guards. The host Approver keeps deciding and MemoryConfirm keeps being asked.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

func TestSessionApprovalMode(t *testing.T) {
	// run replays the taint scenario (external read, memory write, exec call that
	// copies the read text) under tier and reports the three counters.
	run := func(t *testing.T, tier tools.ApprovalMode) (confirms, execRuns, approverCalls int32) {
		t.Helper()
		ctx := context.Background()
		server := scriptedProvider(t, []struct {
			name string
			args map[string]any
		}{
			{"host_read", map[string]any{"text": "go"}},
			{"memory", map[string]any{"action": "add", "scope": "project", "kind": "user", "title": "Tier probe",
				"summary": "probe entry", "body": "plain body"}},
			{"host_exec", map[string]any{"text": taintFeature}},
		})
		engine, err := Open(ctx, EngineOptions{SugaHome: sdkTestHome(t, server.URL)})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = engine.Close() })
		var readRuns int32
		session, err := engine.OpenSession(ctx, "", SessionOptions{
			Workspace: t.TempDir(), ProjectMemoryKey: "tier-probe", ApprovalMode: tier,
			ExtraTools: []api.Tool{
				hostTool{name: "host_read", content: taintFeature, runs: &readRuns,
					meta: tools.Meta{Category: tools.CategoryRead, ReadOnly: true, External: true}},
				hostTool{name: "host_exec", content: "executed", runs: &execRuns,
					meta: tools.Meta{Category: tools.CategoryExecute}},
			},
			Approver: tools.ApproverFunc(func(_ context.Context, call agentcore.ToolCall, _ agentcore.Tool) error {
				if call.Name == "host_exec" {
					atomic.AddInt32(&approverCalls, 1)
				}
				return nil
			}),
			MemoryConfirm: func(context.Context, MemoryConfirmRequest) bool {
				atomic.AddInt32(&confirms, 1)
				return true
			},
		})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = session.Close() })
		result, err := session.Send(ctx, Prompt{Text: "run the probe"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "COMPLETED", result.State)
		return atomic.LoadInt32(&confirms), atomic.LoadInt32(&execRuns), atomic.LoadInt32(&approverCalls)
	}

	t.Run("default: the origin guard refuses the copied text", func(t *testing.T) {
		confirms, execRuns, approver := run(t, "")
		assert.Eq(t, int32(1), confirms)
		assert.Eq(t, int32(0), execRuns)
		assert.Eq(t, int32(0), approver)
	})
	t.Run("trust skips the origin guard but the host still decides and confirms memory", func(t *testing.T) {
		confirms, execRuns, approver := run(t, tools.ApprovalModeTrust)
		assert.Eq(t, int32(1), confirms) // MemoryConfirm is not wrapped by the SDK
		assert.Eq(t, int32(1), execRuns)
		assert.Eq(t, int32(1), approver) // the host Approver was still asked
	})
	t.Run("an unknown tier is refused when the session opens", func(t *testing.T) {
		server := chatServer(t, "sdk_echo", "ok", nil)
		engine, err := Open(context.Background(), EngineOptions{SugaHome: sdkTestHome(t, server.URL)})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = engine.Close() })
		_, err = engine.OpenSession(context.Background(), "", SessionOptions{Workspace: t.TempDir(), ApprovalMode: "yolo"})
		assert.ErrMsgContains(t, err, "invalid approval mode")
	})
}
