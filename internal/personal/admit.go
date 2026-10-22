package personal

// Tool-invocation ledger (issue suga-r72b, borrowed from whip's recovery
// rule): a tool call with side effects is written down as admitted right before
// it runs, so a turn interrupted between execution and result storage is not
// replayed as if nothing happened. The next turn gets a synthetic result saying
// that the effects are unknown instead of the call running again.
//
// The row is written by the approval layer once the call is allowed
// (AdmitApprovedCall, wired through tools.Approval.SetOnApproved), not before
// the decision: a call cancelled at the approval prompt never runs, so it must
// not look like an effect whose state is unknown.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

const (
	// metaToolInterrupted marks the synthetic tool result that the end of an
	// interrupted turn writes for an admitted call whose own result never made
	// it into the transcript. The model has to treat the call's effects as
	// unknown.
	metaToolInterrupted = "suga.tool_interrupted"
	// metaInvocationID is the metadata key of the invocation id on that
	// synthetic result (the same id a host's side-effect ledger keys on).
	metaInvocationID = "suga.invocation_id"
	// eventInterrupted is the status of a tool_interrupted trace event.
	eventInterrupted = "interrupted"
)

// admitRecord is the stored body of one EntryAdmit row: the call that was
// admitted to run, its invocation id (a host ledger's idempotency key), the
// display summary of its arguments and the arguments themselves. They are kept
// because a turn that dies while the tool runs never persists the assistant
// tool-call message (the runtime writes it together with the result): the
// interrupted replay rebuilds the call from this row.
type admitRecord struct {
	InvocationID string         `json:"invocation_id"`
	CallID       string         `json:"call_id,omitempty"`
	Tool         string         `json:"tool"`
	Args         string         `json:"args,omitempty"`
	Arguments    map[string]any `json:"arguments,omitempty"`
}

// admitScope is the ledger target of one turn: the store, the session and the
// turn its admitted calls are written to. The runner puts it in the turn's
// context (withAdmitScope) and the approval layer's approved-call callback reads
// it back, so the ledger follows the turn without the approval layer knowing
// anything about sessions.
type admitScope struct {
	repo      *store.Store
	sessionID string
	turnID    string
}

// admitScopeKey is the context key of a turn's admit ledger target.
type admitScopeKey struct{}

// withAdmitScope returns ctx carrying the admit ledger target of the turn
// beginning with sessionID/turnID. Without a scope (a runner that has no store)
// AdmitApprovedCall writes nothing.
func withAdmitScope(ctx context.Context, repo *store.Store, sessionID, turnID string) context.Context {
	return context.WithValue(ctx, admitScopeKey{}, admitScope{repo: repo, sessionID: sessionID, turnID: turnID})
}

// AdmitApprovedCall writes the ledger row of one call the approval layer just
// allowed (tools.Approval.SetOnApproved, installed by wiring): it runs after the
// decision and right before the tool executes. A call that was refused, or whose
// approval prompt was cancelled or timed out, never gets here and is not
// recorded - it never ran, so the next turn must not be told its state is
// unknown. A read-only tool is not recorded either: an interrupted read has no
// effect to verify, so the replay may simply drop it, as before.
//
// The write is best-effort: a ledger that cannot be written must not stop the
// tool. An interruption then replays the call like any other incomplete one -
// no worse than before this ledger existed.
func AdmitApprovedCall(ctx context.Context, call agentcore.ToolCall, tool agentcore.Tool) {
	scope, ok := ctx.Value(admitScopeKey{}).(admitScope)
	if !ok || scope.repo == nil || scope.turnID == "" {
		return
	}
	if !toolHasSideEffects(tool) {
		return
	}
	raw, err := json.Marshal(admitRecord{
		InvocationID: tools.InvocationID(ctx),
		CallID:       call.CallID,
		Tool:         call.Name,
		Args:         toolArgSummary(call.Arguments),
		Arguments:    call.Arguments,
	})
	if err != nil {
		turnLogger(ctx).Warn("encode admitted tool call", "tool", call.Name, "err", err)
		return
	}
	if _, err := scope.repo.AppendEntries(ctx, scope.sessionID, store.NewEntry{
		TurnID: scope.turnID, Type: store.EntryAdmit, JSON: raw,
	}); err != nil {
		turnLogger(ctx).Warn("record admitted tool call", "tool", call.Name, "err", err)
	}
}

// toolHasSideEffects reports whether calls of t may change something outside
// the session: built-in tools say so through tools.Meta (ReadOnly), MCP tools
// through their server's readOnlyHint - and a server that declared nothing is
// not trusted, so its tool counts as having side effects. A tool the approval
// layer could not resolve (nil) is treated the same way (unknown, so assume it
// can change something).
func toolHasSideEffects(t agentcore.Tool) bool {
	if t == nil {
		return true
	}
	if tools.IsMCP(t) {
		annotations, ok := mcp.AnnotationsOf(t)
		return !ok || !annotations.ReadOnly()
	}
	return !tools.MetaOf(t).ReadOnly
}

// interruptedToolResult is the synthetic tool message of an admitted call that
// was interrupted before its result was stored: it tells the model that the
// effects are unknown and that it has to verify before repeating the call.
func interruptedToolResult(call *agentcore.ToolCall, invocationID string) agentcore.Message {
	name := call.Name
	if name == "" {
		name = "tool"
	}
	content := fmt.Sprintf("error: 该工具调用（%s）在回合中断时状态未知：它可能已经执行（副作用可能已生效），也可能没有。"+
		"请先核实实际状态（文件、命令输出、提交、接口返回等），再决定是否重做；不要直接重复执行。", name)
	metadata := map[string]any{metaToolInterrupted: true}
	if invocationID != "" {
		metadata[metaInvocationID] = invocationID
	}
	return agentcore.Message{
		Role:     "tool",
		Content:  content,
		ToolCall: call,
		ToolResult: &agentcore.ToolResult{
			CallID: call.CallID, Name: name, Content: content, IsError: true, Metadata: metadata,
		},
		Metadata: metadata,
	}
}

// markInterruptedToolCalls writes that synthetic result for every admitted call
// of an interrupted turn that has none, and records one tool_interrupted trace
// event per call. It runs from finish (a failed or cancelled turn) and from
// recovery (a turn a dead process left running), i.e. wherever a turn reaches
// its terminal interrupted state, so the next turn's replay (replayablePrefix)
// keeps the call together with a result instead of cutting it away, and the
// model never reruns an effect whose state is unknown. Read-only calls have no
// admit record and keep the old replay behaviour.
func (r *Runner) markInterruptedToolCalls(ctx context.Context, sessionID, turnID string) error {
	if turnID == "" {
		return nil
	}
	entries, err := r.cfg.Repository.ListEntries(ctx, sessionID, store.EntryFilter{
		TurnID: turnID, Types: []string{store.EntryAssistant, store.EntryTool, store.EntryAdmit},
	})
	if err != nil {
		return fmt.Errorf("list the turn's entries: %w", err)
	}
	results := make(map[string]bool)
	var calls []*agentcore.ToolCall
	var admitted []admitRecord
	for _, e := range entries {
		switch e.Type {
		case store.EntryTool:
			var m agentcore.Message
			if err := json.Unmarshal(e.JSON, &m); err != nil {
				return fmt.Errorf("decode tool entry %d: %w", e.Seq, err)
			}
			if m.ToolResult != nil && m.ToolResult.CallID != "" {
				results[m.ToolResult.CallID] = true
			}
		case store.EntryAssistant:
			var m agentcore.Message
			if err := json.Unmarshal(e.JSON, &m); err != nil {
				return fmt.Errorf("decode assistant entry %d: %w", e.Seq, err)
			}
			if m.ToolCall != nil {
				calls = append(calls, m.ToolCall)
			}
		case store.EntryAdmit:
			var rec admitRecord
			if err := json.Unmarshal(e.JSON, &rec); err != nil {
				return fmt.Errorf("decode admitted entry %d: %w", e.Seq, err)
			}
			admitted = append(admitted, rec)
		}
	}
	now := time.Now().UTC()
	var out []store.NewEntry
	var events []store.Event
	used := make(map[int]bool)
	marked := make(map[string]bool)
	for _, rec := range admitted {
		if rec.CallID != "" && results[rec.CallID] {
			continue // the call's own result made it into the transcript
		}
		if rec.CallID != "" && marked[rec.CallID] {
			continue // one call cannot have two results
		}
		marked[rec.CallID] = true
		call, stored := matchInterruptedCall(calls, used, rec)
		if call == nil {
			call = &agentcore.ToolCall{CallID: rec.CallID, Name: rec.Tool, Arguments: rec.Arguments}
		}
		// The runtime writes the assistant tool-call message together with its
		// result, so a turn that died while the tool ran has no assistant entry:
		// rebuild it from the ledger row, or the result would have no call.
		if !stored {
			callRaw, err := store.EncodeMessage(agentcore.Message{Role: "assistant", ToolCall: call})
			if err != nil {
				return fmt.Errorf("encode the interrupted call of %q: %w", rec.Tool, err)
			}
			out = append(out, store.NewEntry{TurnID: turnID, Type: store.EntryAssistant, JSON: callRaw})
		}
		raw, err := store.EncodeMessage(interruptedToolResult(call, rec.InvocationID))
		if err != nil {
			return fmt.Errorf("encode the interrupted result of %q: %w", rec.Tool, err)
		}
		out = append(out, store.NewEntry{TurnID: turnID, Type: store.EntryTool, JSON: raw})
		events = append(events, store.Event{Kind: store.EventToolInterrupted, Name: rec.Tool, Status: eventInterrupted,
			StartedAt: now, Attrs: map[string]any{"invocation_id": rec.InvocationID, "call_id": rec.CallID}})
	}
	if len(out) == 0 {
		return nil
	}
	if _, err := r.cfg.Repository.AppendEntries(ctx, sessionID, out...); err != nil {
		return fmt.Errorf("append the interrupted results: %w", err)
	}
	for _, e := range events {
		e.SessionID, e.TurnID = sessionID, turnID
		r.rec.record(ctx, e)
	}
	return nil
}

// matchInterruptedCall finds the already stored assistant tool call an admitted
// record belongs to: by call id, or - for a provider that sent no id - by tool
// name in order. Each call matches once. stored is false when the turn has no
// such assistant entry (the runtime persists it only with the result), so the
// caller rebuilds the call from the ledger row.
func matchInterruptedCall(calls []*agentcore.ToolCall, used map[int]bool, rec admitRecord) (call *agentcore.ToolCall, stored bool) {
	for i, c := range calls {
		if used[i] || c == nil || c.Name != rec.Tool {
			continue
		}
		if rec.CallID != "" && c.CallID != "" && c.CallID != rec.CallID {
			continue
		}
		used[i] = true
		return c, true
	}
	return nil, false
}
