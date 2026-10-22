package personal

// Tests of the tool-invocation ledger and of the interrupted-call replay (issue
// suga-r72b): an admitted call without a result is answered as "state unknown"
// on replay instead of being cut or rerun, a read-only call keeps the old cut,
// and the normal path sends the model exactly the messages it sent before.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

// ctxTool is a tool that records the invocation id of every execution and
// declares its Meta; it stands for a host tool of wiring.Options.ExtraTools.
type ctxTool struct {
	name string
	meta tools.Meta
	seen []string
}

func (t *ctxTool) Name() string        { return t.name }
func (t *ctxTool) Description() string { return "ledger test tool" }
func (t *ctxTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: t.name, InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (t *ctxTool) ToolMeta() tools.Meta { return t.meta }
func (t *ctxTool) Execute(ctx context.Context, call agentcore.ToolCall) (*agentcore.ToolResult, error) {
	t.seen = append(t.seen, tools.InvocationID(ctx))
	return &agentcore.ToolResult{CallID: call.CallID, Name: call.Name, Content: "ok"}, nil
}

// mcpAnnotationsTool fakes the optional MCP annotation carrier, so the
// readOnlyHint classification is testable without a server.
type mcpAnnotationsTool struct {
	name        string
	annotations mcp.Annotations
}

func (t *mcpAnnotationsTool) Name() string        { return t.name }
func (t *mcpAnnotationsTool) Description() string { return "mcp test tool" }
func (t *mcpAnnotationsTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: t.name, InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (t *mcpAnnotationsTool) Kind() tools.Kind { return tools.KindMCP }
func (t *mcpAnnotationsTool) MCPAnnotations() mcp.Annotations {
	return t.annotations
}
func (t *mcpAnnotationsTool) Execute(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
	return &agentcore.ToolResult{Content: "ok"}, nil
}

func boolPtr(v bool) *bool { return &v }

// admitEntries returns the turn's admitted ledger rows.
func admitEntries(t *testing.T, repo *store.Store, sessionID, turnID string) []admitRecord {
	t.Helper()
	entries, err := repo.ListEntries(context.Background(), sessionID, store.EntryFilter{
		TurnID: turnID, Types: []string{store.EntryAdmit},
	})
	assert.Require(t, assert.NoErr(t, err))
	out := make([]admitRecord, 0, len(entries))
	for _, e := range entries {
		var rec admitRecord
		assert.Require(t, assert.NoErr(t, json.Unmarshal(e.JSON, &rec)))
		out = append(out, rec)
	}
	return out
}

func TestAdmitLedgerRecordsSideEffectCalls(t *testing.T) {
	repo := openObserveRepo(t)
	reg := tools.NewRegistry()
	write := &ctxTool{name: "write", meta: tools.Meta{Category: tools.CategoryEdit}}
	read := &ctxTool{name: "look", meta: tools.Meta{Category: tools.CategoryRead, ReadOnly: true}}
	reg.Register(write)
	reg.Register(read)
	provider := &captureProvider{steps: []*agentcore.GenerateResult{
		{ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "write", Arguments: map[string]any{"path": "work.txt"}}},
			FinishReason: agentcore.FinishToolCalls},
		{ToolCalls: []agentcore.ToolCall{{CallID: "c2", Name: "look", Arguments: map[string]any{"path": "work.txt"}}},
			FinishReason: agentcore.FinishToolCalls},
		{Content: "done", FinishReason: agentcore.FinishFinal},
	}}
	runner, err := New(Config{Repository: repo, Tools: reg, Provider: provider})
	assert.NoErr(t, err)

	res, err := runner.Run(context.Background(), RunRequest{Prompt: "写一下"})
	assert.NoErr(t, err)
	assert.Eq(t, RunCompleted, res.State)

	rows := admitEntries(t, repo, res.SessionID, res.TurnID)
	assert.Require(t, assert.Eq(t, 1, len(rows)))
	t.Run("only the side-effect call is admitted", func(t *testing.T) {
		assert.Eq(t, "write", rows[0].Tool)
		assert.Eq(t, "c1", rows[0].CallID)
		assert.StrContains(t, rows[0].Args, "work.txt")
	})
	t.Run("the ledger id is the id the tool saw", func(t *testing.T) {
		assert.Require(t, assert.Eq(t, 1, len(write.seen)))
		assert.Eq(t, write.seen[0], rows[0].InvocationID)
		assert.Eq(t, res.TurnID+"/c1", rows[0].InvocationID)
	})
	t.Run("a normal turn records no interruption", func(t *testing.T) {
		assert.Len(t, eventsOf(t, repo, res.SessionID, store.EventToolInterrupted), 0)
	})
	t.Run("the ledger row never reaches the model's history", func(t *testing.T) {
		history, err := runner.loadHistory(context.Background(), res.SessionID)
		assert.NoErr(t, err)
		entries, err := repo.ListEntries(context.Background(), res.SessionID, store.EntryFilter{
			TurnID: res.TurnID, Types: []string{store.EntryUser, store.EntryAssistant, store.EntryTool},
		})
		assert.NoErr(t, err)
		assert.Eq(t, len(entries), len(history.items))
		for _, item := range history.items {
			assert.Eq(t, false, strings.Contains(item.msg.Content, "invocation_id"))
		}
	})
	t.Run("the model's requests are what they were before the ledger", func(t *testing.T) {
		// The second request of the turn carries the turn's messages and nothing
		// else: the admitted rows are not in state.Messages, so the projection is
		// byte-for-byte the same as without the ledger.
		assert.Require(t, assert.Eq(t, 3, len(provider.requests)))
		roles := make([]string, 0, len(provider.requests[1].Messages))
		for _, m := range provider.requests[1].Messages {
			roles = append(roles, m.Role)
			assert.Eq(t, false, strings.Contains(m.Content, "invocation_id"))
		}
		assert.Eq(t, []string{"user", "assistant", "tool"}, roles)
	})
}

func TestMarkInterruptedToolCalls(t *testing.T) {
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(mock.Final("ok")), Tools: tools.NewRegistry()})
	assert.NoErr(t, err)
	ctx := context.Background()
	sess, err := runner.NewSession(ctx, "", "interrupted")
	assert.NoErr(t, err)
	turnID, err := runner.createTurn(ctx, sess.ID, "turn-crash")
	assert.NoErr(t, err)

	// The crash leaves the ledger row only: the runtime persists the assistant
	// tool-call message together with the tool result, so neither exists.
	admitRaw, err := json.Marshal(admitRecord{InvocationID: "turn-crash/c1", CallID: "c1", Tool: "write",
		Args: "path=work.txt", Arguments: map[string]any{"path": "work.txt"}})
	assert.NoErr(t, err)
	userRaw, err := store.EncodeMessage(agentcore.Message{Role: "user", Content: "写一下"})
	assert.NoErr(t, err)
	_, err = repo.AppendEntries(ctx, sess.ID,
		store.NewEntry{TurnID: turnID, Type: store.EntryUser, JSON: userRaw},
		store.NewEntry{TurnID: turnID, Type: store.EntryAdmit, JSON: admitRaw},
	)
	assert.NoErr(t, err)

	assert.NoErr(t, runner.markInterruptedToolCalls(ctx, sess.ID, turnID))

	entries, err := repo.ListEntries(ctx, sess.ID, store.EntryFilter{TurnID: turnID})
	assert.NoErr(t, err)
	var synthetic *agentcore.Message
	for _, e := range entries {
		if e.Type != store.EntryTool {
			continue
		}
		var m agentcore.Message
		assert.NoErr(t, json.Unmarshal(e.JSON, &m))
		synthetic = &m
	}
	t.Run("the call is rebuilt from the ledger row", func(t *testing.T) {
		var calls []*agentcore.ToolCall
		for _, e := range entries {
			if e.Type != store.EntryAssistant {
				continue
			}
			var m agentcore.Message
			assert.NoErr(t, json.Unmarshal(e.JSON, &m))
			calls = append(calls, m.ToolCall)
		}
		assert.Require(t, assert.Eq(t, 1, len(calls)))
		assert.Eq(t, "c1", calls[0].CallID)
		assert.Eq(t, "write", calls[0].Name)
		assert.Eq(t, "work.txt", calls[0].Arguments["path"])
	})
	t.Run("the synthetic result says the state is unknown", func(t *testing.T) {
		assert.Require(t, assert.NotNil(t, synthetic))
		assert.Require(t, assert.NotNil(t, synthetic.ToolResult))
		assert.Eq(t, "c1", synthetic.ToolResult.CallID)
		assert.Eq(t, true, synthetic.ToolResult.IsError)
		assert.StrContains(t, synthetic.Content, "状态未知")
		assert.StrContains(t, synthetic.Content, "请先核实")
		assert.Eq(t, true, synthetic.Metadata[metaToolInterrupted])
		assert.Eq(t, "turn-crash/c1", synthetic.Metadata[metaInvocationID])
	})
	t.Run("the trace records tool_interrupted", func(t *testing.T) {
		events := eventsOf(t, repo, sess.ID, store.EventToolInterrupted)
		assert.Require(t, assert.Eq(t, 1, len(events)))
		assert.Eq(t, "write", events[0].Name)
		assert.Eq(t, eventInterrupted, events[0].Status)
		assert.Eq(t, "turn-crash/c1", events[0].Attrs["invocation_id"])
		var text bytes.Buffer
		assert.NoErr(t, WriteTrace(&text, events, nil, false))
		assert.StrContains(t, text.String(), "tool_interrupted write interrupted")
	})
	t.Run("the replay keeps the call with its interrupted result", func(t *testing.T) {
		var turnEntries []store.Entry
		for _, e := range entries {
			if e.Type == store.EntryAdmit || e.Type == store.EntryUser ||
				e.Type == store.EntryAssistant || e.Type == store.EntryTool {
				turnEntries = append(turnEntries, e)
			}
		}
		kept, calls, dropped, err := replayablePrefix(turnEntries)
		assert.NoErr(t, err)
		assert.Eq(t, 0, dropped)
		assert.Require(t, assert.Eq(t, 3, len(kept)))
		assert.Eq(t, store.EntryTool, kept[2].Type)
		assert.Eq(t, []string{"write path=work.txt（中断时状态未知）"}, calls)
	})
	t.Run("a second run adds nothing", func(t *testing.T) {
		assert.NoErr(t, runner.markInterruptedToolCalls(ctx, sess.ID, turnID))
		again, err := repo.ListEntries(ctx, sess.ID, store.EntryFilter{TurnID: turnID, Types: []string{store.EntryTool}})
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(again))
	})
}

// TestMarkInterruptedToolCallsKeepsAStoredCall: when the assistant message did
// make it into the transcript, the ledger row only adds its result.
func TestMarkInterruptedToolCallsKeepsAStoredCall(t *testing.T) {
	repo := openObserveRepo(t)
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(mock.Final("ok")), Tools: tools.NewRegistry()})
	assert.NoErr(t, err)
	ctx := context.Background()
	sess, err := runner.NewSession(ctx, "", "partial")
	assert.NoErr(t, err)
	turnID, err := runner.createTurn(ctx, sess.ID, "turn-partial")
	assert.NoErr(t, err)
	callRaw, err := store.EncodeMessage(agentcore.Message{Role: "assistant",
		ToolCall: &agentcore.ToolCall{CallID: "c1", Name: "write", Arguments: map[string]any{"path": "work.txt"}}})
	assert.NoErr(t, err)
	admitRaw, err := json.Marshal(admitRecord{InvocationID: "turn-partial/c1", CallID: "c1", Tool: "write"})
	assert.NoErr(t, err)
	_, err = repo.AppendEntries(ctx, sess.ID,
		store.NewEntry{TurnID: turnID, Type: store.EntryAssistant, JSON: callRaw},
		store.NewEntry{TurnID: turnID, Type: store.EntryAdmit, JSON: admitRaw},
	)
	assert.NoErr(t, err)

	assert.NoErr(t, runner.markInterruptedToolCalls(ctx, sess.ID, turnID))
	entries, err := repo.ListEntries(ctx, sess.ID, store.EntryFilter{TurnID: turnID})
	assert.NoErr(t, err)
	var assistants, results int
	for _, e := range entries {
		switch e.Type {
		case store.EntryAssistant:
			assistants++
		case store.EntryTool:
			results++
		}
	}
	assert.Eq(t, 1, assistants)
	assert.Eq(t, 1, results)
}

// blockingTool has a side effect and never returns on its own: a cancelled turn
// leaves exactly the state of a process that died while the tool ran (the
// assistant message and the result were never persisted).
type blockingTool struct {
	started chan struct{}
}

func (t *blockingTool) Name() string        { return "write" }
func (t *blockingTool) Description() string { return "blocks until cancelled" }
func (t *blockingTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: t.Name(), InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (t *blockingTool) Execute(ctx context.Context, _ agentcore.ToolCall) (*agentcore.ToolResult, error) {
	close(t.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestCancelledTurnMarksTheAdmittedCall drives a turn into cancellation while a
// side-effect tool runs: finish must leave the call plus a "state unknown"
// result and the tool_interrupted trace event, and the next turn must see them.
func TestCancelledTurnMarksTheAdmittedCall(t *testing.T) {
	repo := openObserveRepo(t)
	tool := &blockingTool{started: make(chan struct{})}
	reg := tools.NewRegistry()
	reg.Register(tool)
	provider := &captureProvider{steps: []*agentcore.GenerateResult{
		{ToolCalls: []agentcore.ToolCall{{CallID: "c1", Name: "write", Arguments: map[string]any{"path": "work.txt"}}},
			FinishReason: agentcore.FinishToolCalls},
	}}
	runner, err := New(Config{Repository: repo, Provider: provider, Tools: reg})
	assert.NoErr(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan RunResult, 1)
	go func() {
		res, _ := runner.Run(ctx, RunRequest{Prompt: "写一下"})
		done <- res
	}()
	<-tool.started
	cancel()
	res := <-done
	assert.Eq(t, RunCanceled, res.State)

	t.Run("the ledger and the interrupted pair are stored", func(t *testing.T) {
		assert.Require(t, assert.Eq(t, 1, len(admitEntries(t, repo, res.SessionID, res.TurnID))))
		var assistants, results int
		entries, err := repo.ListEntries(context.Background(), res.SessionID, store.EntryFilter{TurnID: res.TurnID})
		assert.NoErr(t, err)
		for _, e := range entries {
			switch e.Type {
			case store.EntryAssistant:
				assistants++
			case store.EntryTool:
				var m agentcore.Message
				assert.NoErr(t, json.Unmarshal(e.JSON, &m))
				assert.Eq(t, true, m.Metadata[metaToolInterrupted])
				results++
			}
		}
		assert.Eq(t, 1, assistants)
		assert.Eq(t, 1, results)
	})
	t.Run("the trace records tool_interrupted", func(t *testing.T) {
		events := eventsOf(t, repo, res.SessionID, store.EventToolInterrupted)
		assert.Require(t, assert.Eq(t, 1, len(events)))
		assert.Eq(t, "write", events[0].Name)
		assert.Eq(t, res.TurnID, events[0].TurnID)
	})
	t.Run("the interruption note does not claim the call ran", func(t *testing.T) {
		entries, err := repo.ListEntries(context.Background(), res.SessionID, store.EntryFilter{
			TurnID: res.TurnID, Types: []string{store.EntryUser},
		})
		assert.NoErr(t, err)
		var note string
		for _, e := range entries {
			var m agentcore.Message
			assert.NoErr(t, json.Unmarshal(e.JSON, &m))
			if isInterruptionNote(m) {
				note = m.Content
			}
		}
		assert.StrContains(t, note, "write path=work.txt（中断时状态未知）")
	})

	t.Run("the next turn sees the unknown state", func(t *testing.T) {
		next := &captureProvider{}
		nextRunner, err := New(Config{Repository: repo, Provider: next, Tools: reg})
		assert.NoErr(t, err)
		_, err = nextRunner.Run(context.Background(), RunRequest{SessionID: res.SessionID, Prompt: "继续"})
		assert.NoErr(t, err)
		assert.Require(t, assert.Eq(t, 1, len(next.requests)))
		var call, unknown bool
		for _, m := range next.requests[0].Messages {
			if m.ToolCall != nil && m.ToolCall.CallID == "c1" {
				call = true
			}
			if m.ToolResult != nil && m.ToolResult.CallID == "c1" {
				unknown = strings.Contains(m.Content, "状态未知")
			}
		}
		assert.Eq(t, true, call)
		assert.Eq(t, true, unknown)
	})
}

// TestReadOnlyInterruptedCallIsStillCut: no admit row, so the replay keeps the
// old rule - the unanswered call cuts the prefix.
func TestReadOnlyInterruptedCallIsStillCut(t *testing.T) {
	entries := []store.Entry{
		msgEntry(t, 1, store.EntryUser, userMsg("读一下")),
		msgEntry(t, 2, store.EntryAssistant, callMsg("r1", "look", nil)),
		msgEntry(t, 3, store.EntryAssistant, agentcore.Message{Role: "assistant", Content: "半截"}),
	}
	kept, _, dropped, err := replayablePrefix(entries)
	assert.NoErr(t, err)
	assert.Eq(t, []int64{1}, keptSeqs(kept))
	assert.Eq(t, 2, dropped)
}

// captureProvider records every request and answers with the next scripted
// step (a final message once the steps are used up).
type captureProvider struct {
	steps    []*agentcore.GenerateResult
	requests []*agentcore.GenerateRequest
}

func (p *captureProvider) Name() string { return "capture" }
func (p *captureProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.requests = append(p.requests, req)
	if i := len(p.requests) - 1; i < len(p.steps) {
		return p.steps[i], nil
	}
	return &agentcore.GenerateResult{Content: "ok", FinishReason: agentcore.FinishFinal}, nil
}

// TestNextTurnSeesInterruptedResultNotRerun: after the ledger marked the call,
// the next turn's request carries the assistant call plus the "state unknown"
// result, and the tool is not executed again.
func TestNextTurnSeesInterruptedResultNotRerun(t *testing.T) {
	repo := openObserveRepo(t)
	invoked := 0
	reg := tools.NewRegistry()
	reg.Register(&ctxToolWithCounter{name: "write", invoked: &invoked})
	provider := &captureProvider{}
	runner, err := New(Config{Repository: repo, Provider: provider, Tools: reg})
	assert.NoErr(t, err)
	ctx := context.Background()
	sess, err := runner.NewSession(ctx, "", "interrupted")
	assert.NoErr(t, err)
	turnID, err := runner.createTurn(ctx, sess.ID, "turn-crash")
	assert.NoErr(t, err)

	admitRaw, err := json.Marshal(admitRecord{InvocationID: "turn-crash/c1", CallID: "c1", Tool: "write",
		Arguments: map[string]any{"path": "work.txt"}})
	assert.NoErr(t, err)
	userRaw, err := store.EncodeMessage(agentcore.Message{Role: "user", Content: "写一下"})
	assert.NoErr(t, err)
	_, err = repo.AppendEntries(ctx, sess.ID,
		store.NewEntry{TurnID: turnID, Type: store.EntryUser, JSON: userRaw},
		store.NewEntry{TurnID: turnID, Type: store.EntryAdmit, JSON: admitRaw},
	)
	assert.NoErr(t, err)
	assert.NoErr(t, runner.markInterruptedToolCalls(ctx, sess.ID, turnID))
	assert.NoErr(t, runner.cfg.Repository.UpdateTurn(ctx, store.Turn{ID: turnID, Status: store.TurnCancelled}))

	_, err = runner.Run(ctx, RunRequest{SessionID: sess.ID, Prompt: "继续"})
	assert.NoErr(t, err)

	assert.Require(t, assert.Eq(t, 1, len(provider.requests)))
	var interrupted, called bool
	for _, m := range provider.requests[0].Messages {
		if m.ToolResult != nil && m.ToolResult.CallID == "c1" {
			interrupted = strings.Contains(m.Content, "状态未知")
		}
		if m.ToolCall != nil && m.ToolCall.CallID == "c1" {
			called = true
		}
	}
	assert.Eq(t, true, called)
	assert.Eq(t, true, interrupted)
	assert.Eq(t, 0, invoked)
}

// ctxToolWithCounter counts executions without declaring side effects (the
// default of a tool without Describer is "may have side effects").
type ctxToolWithCounter struct {
	name    string
	invoked *int
}

func (t *ctxToolWithCounter) Name() string        { return t.name }
func (t *ctxToolWithCounter) Description() string { return "counter" }
func (t *ctxToolWithCounter) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: t.name, InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (t *ctxToolWithCounter) Execute(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
	(*t.invoked)++
	return &agentcore.ToolResult{Content: "ok"}, nil
}

func TestToolHasSideEffects(t *testing.T) {
	lookupOf := func(list ...agentcore.Tool) tools.Lookup {
		reg := tools.NewRegistry()
		for _, tool := range list {
			reg.Register(tool)
		}
		return reg
	}
	t.Run("unknown tool counts as side effects", func(t *testing.T) {
		assert.Eq(t, true, toolHasSideEffects(lookupOf(), "missing"))
	})
	t.Run("nil lookup counts as side effects", func(t *testing.T) {
		assert.Eq(t, true, toolHasSideEffects(nil, "write"))
	})
	t.Run("a read-only native tool does not", func(t *testing.T) {
		assert.Eq(t, false, toolHasSideEffects(lookupOf(&ctxTool{name: "look", meta: tools.Meta{ReadOnly: true}}), "look"))
	})
	t.Run("a native tool with side effects does", func(t *testing.T) {
		assert.Eq(t, true, toolHasSideEffects(lookupOf(&ctxTool{name: "write"}), "write"))
	})
	t.Run("an MCP readOnlyHint counts as read-only", func(t *testing.T) {
		tool := &mcpAnnotationsTool{name: "list", annotations: mcp.Annotations{ReadOnlyHint: boolPtr(true)}}
		assert.Eq(t, false, toolHasSideEffects(lookupOf(tool), "list"))
	})
	t.Run("an MCP tool without annotations has side effects", func(t *testing.T) {
		tool := &mcpAnnotationsTool{name: "write"}
		assert.Eq(t, true, toolHasSideEffects(lookupOf(tool), "write"))
	})
}
