package acpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coder/acp-go-sdk"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// ErrMemoryWriteRejected is the refusal of a memory write confirmation.
var ErrMemoryWriteRejected = errors.New("用户拒绝写入记忆")

// memoryBodyPreview bounds the body shown in a confirmation, in runes.
const memoryBodyPreview = 300

// ConfirmMemory implements TurnGate: a memory write from a tainted session is
// confirmed through session/request_permission on the memory tool call.
// Allowed → nil; rejected or a failed request → ErrMemoryWriteRejected;
// cancelled (outcome or turn) → context.Canceled, which ends the turn.
func (h *permissionHook) ConfirmMemory(ctx context.Context, s memory.Scope, op memory.Op) error {
	if h.conn == nil {
		return ErrMemoryWriteRejected
	}
	id := native.MemoryCallID(ctx)
	if id == "" {
		h.sequence++
		id = fmt.Sprintf("memory-confirm-%d", h.sequence)
	}
	decision, err := h.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: h.sessionID,
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId(id),
			Title:      acp.Ptr(h.tr().T("host.memory.title")),
			Kind:       acp.Ptr(acp.ToolKindEdit),
			Content:    []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(memoryConfirmText(h.tr(), s, op)))},
		},
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once", OptionId: "allow"},
			{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "reject"},
		},
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: %v", ErrMemoryWriteRejected, err)
	}
	if decision.Outcome.Cancelled != nil {
		return context.Canceled
	}
	if decision.Outcome.Selected == nil || decision.Outcome.Selected.OptionId != "allow" {
		return ErrMemoryWriteRejected
	}
	return nil
}

// memoryConfirmText describes the write. Confirm receives the op before the
// governance layer sanitizes it, so every field is cleaned here first. The keys
// are the ones of the chat confirmation (internal/wiring/memory_runtime.go), so
// both front ends describe the same write in the same words.
func memoryConfirmText(tr i18n.Translator, s memory.Scope, op memory.Op) string {
	t := trOrFallback(tr)
	lines := []string{
		t.T("host.memory.label.scope", map[string]any{"scope": s.String()}),
		t.T("host.memory.label.action", map[string]any{"action": string(op.Action)}),
	}
	if op.Kind != "" {
		lines = append(lines, t.T("host.memory.label.kind", map[string]any{"kind": string(op.Kind)}))
	}
	if op.ID != "" {
		lines = append(lines, t.T("host.memory.label.id", map[string]any{"id": op.ID}))
	}
	if op.Title != "" {
		lines = append(lines, t.T("host.memory.label.title", map[string]any{"title": redact.Clean(op.Title)}))
	}
	if op.Body != "" {
		body := []rune(redact.Clean(op.Body))
		if len(body) > memoryBodyPreview {
			body = append(body[:memoryBodyPreview], '…')
		}
		lines = append(lines, t.T("host.memory.label.body", map[string]any{"body": string(body)}))
	}
	return strings.Join(lines, "\n")
}
