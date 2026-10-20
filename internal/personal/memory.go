package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// metaMemoryIndexContent is the session meta key of the frozen memory index
// section (its hash is metaMemoryIndexHash).
const metaMemoryIndexContent = "memory_index_content"

// memoryIndexNote is the fixed explanation put above the index entries.
const memoryIndexNote = "## 记忆索引\n\n" +
	"以下是跨会话记忆的索引快照：会话开始时冻结，只列标题，本会话内的写入不会更新它（压缩后才刷新）。" +
	"需要正文时用 memory 工具 action=get（id 为行首标识，scope 为 project 或 user）；" +
	"新增、改写、删除用 add / replace / remove。" +
	"条目四类：user（用户本人）、feedback（对工作方式的纠正与确认）、project（代码和 git 看不出的目标、约束、进展）、" +
	"reference（外部资源的位置）。不要记：能从代码或 git 查到的、规则文件里已有的、本次会话的临时信息、密钥。"

// memoryIndexEmpty is shown when no scope has entries yet.
const memoryIndexEmpty = "（暂无记忆条目）"

// renderMemorySection wraps the rendered scope indexes into the prompt section.
func renderMemorySection(index string) string {
	index = strings.TrimSpace(index)
	if index == "" {
		index = memoryIndexEmpty
	}
	return memoryIndexNote + "\n\n" + index
}

// RenderMemorySection is the exported form of renderMemorySection: the
// resident context report measures the same section the segment context sends.
func RenderMemorySection(index string) string { return renderMemorySection(index) }

// memorySnapshot reads the memory index through Config.MemoryIndex and
// renders the segment context section with its sha256. ok is false when there
// is no memory source or nothing could be read; a failure is only logged and
// never affects the turn (the segment then has no memory section until a
// later turn's context update fills it in, see contextUpdate).
func (r *Runner) memorySnapshot(ctx context.Context, sessionID string) (content, hash string, ok bool) {
	if r.cfg.MemoryIndex == nil {
		return "", "", false
	}
	index, err := r.cfg.MemoryIndex(ctx)
	if err != nil {
		slog.Warn("read memory index", "session_id", sessionID, "err", err)
		if strings.TrimSpace(index) == "" {
			return "", "", false
		}
		// Partial index (one scope failed): keep what could be read.
	}
	content = renderMemorySection(index)
	sum := sha256.Sum256([]byte(content))
	return content, hex.EncodeToString(sum[:]), true
}

// SessionIDFromContext returns the session of the turn running in ctx ("" outside a turn).
func SessionIDFromContext(ctx context.Context) string { return observeScopeFrom(ctx).sessionID }

// MemoryProvenance is the provenance of a memory write made by a tool in
// the turn running in ctx.
func MemoryProvenance(ctx context.Context) memory.Provenance {
	scope := observeScopeFrom(ctx)
	return memory.Provenance{SessionID: scope.sessionID, TurnID: scope.turnID, Source: "tool"}
}

// SessionTainted reports whether the session of the turn in ctx has seen
// external content (tainted_at in its meta, design D7). Outside a turn or
// when the session cannot be read it reports true (fail closed: the write
// then needs a confirmation).
func (r *Runner) SessionTainted(ctx context.Context) bool {
	sessionID := observeScopeFrom(ctx).sessionID
	if sessionID == "" {
		return true
	}
	if m := r.rec.taint; m != nil {
		if _, done := m.marked.Load(sessionID); done {
			return true
		}
	}
	model, err := r.cfg.Repository.GetSession(context.WithoutCancel(ctx), sessionID)
	if err != nil {
		slog.Warn("read session taint", "session_id", sessionID, "err", err)
		return true
	}
	_, tainted := model.Meta[metaTaintedAt]
	return tainted
}

// RecordMemoryEvent writes a memory_write / memory_denied event for the turn
// in ctx (memory.Policy.Observe); best-effort like every event.
func (r *Runner) RecordMemoryEvent(ctx context.Context, ev memory.Event) {
	status := eventOK
	if ev.Type == memory.EventDenied {
		status = eventDenied
	}
	attrs := map[string]any{"scope": ev.Scope.String(), "backend": ev.Backend, "action": string(ev.Action),
		"tainted": ev.Tainted}
	for key, value := range map[string]string{"id": ev.ID, "kind": string(ev.Kind), "source": ev.Source, "reason": ev.Reason} {
		if value != "" {
			attrs[key] = value
		}
	}
	r.rec.record(ctx, store.Event{Kind: ev.Type, Name: string(ev.Action), Status: status, Error: ev.Detail,
		StartedAt: time.Now(), Attrs: attrs})
}
