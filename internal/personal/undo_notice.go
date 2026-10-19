package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// metaUndo marks the fact message that a completed undo operation injects into
// the next explicit turn. It is metadata rather than a user request, so history
// replay, handoff and compaction can keep it without treating it as input.
const metaUndo = "suga.undo"

// undoNoticeQueueKey stores pending facts in session metadata. The queue is
// consumed while Run holds the existing session gate and cross-process lock.
const undoNoticeQueueKey = "suga.undo.pending"

const (
	undoNoticeTextFiles = 8
	undoEventFiles      = 20
)

// pendingUndoNotice carries both the model-facing text and the facts needed by
// EventUndo. event_id lets the deferred entry fill in entry_seq after it exists.
type pendingUndoNotice struct {
	Text          string   `json:"text"`
	Turns         int      `json:"turns"`
	Restored      int      `json:"restored"`
	Deleted       int      `json:"deleted"`
	Skipped       int      `json:"skipped"`
	Unrecoverable int      `json:"unrecoverable"`
	Files         []string `json:"files,omitempty"`
	Forced        bool     `json:"forced"`
	EventID       int64    `json:"event_id,omitempty"`
}

func undoNoticeMessage(text string) agentcore.Message {
	return agentcore.Message{Role: "user", Content: text, Metadata: map[string]any{metaUndo: true}}
}

func newUndoNotice(result UndoResult) pendingUndoNotice {
	return pendingUndoNotice{
		Text:          undoNoticeText(result),
		Turns:         result.Turns,
		Restored:      result.Restored,
		Deleted:       result.Deleted,
		Skipped:       result.Skipped,
		Unrecoverable: result.Unrecoverable,
		Files:         append([]string(nil), result.Files...),
		Forced:        result.Forced,
	}
}

func undoNoticeText(result UndoResult) string {
	var parts []string
	if result.Restored > 0 {
		parts = append(parts, fmt.Sprintf("已恢复 %d 个文件", result.Restored))
	}
	if result.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("已删除 %d 个新建文件", result.Deleted))
	}
	if result.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("跳过 %d 个冲突", result.Skipped))
	}
	if result.Unrecoverable > 0 {
		parts = append(parts, fmt.Sprintf("%d 个文件无法恢复", result.Unrecoverable))
	}
	if len(parts) == 0 {
		parts = append(parts, "没有文件被恢复")
	}
	if files := result.Files; len(files) > 0 {
		shown := files
		if len(shown) > undoNoticeTextFiles {
			shown = shown[:undoNoticeTextFiles]
		}
		parts = append(parts, "文件："+strings.Join(shown, "、"))
		if len(files) > len(shown) {
			parts = append(parts, fmt.Sprintf("另有 %d 个文件", len(files)-len(shown)))
		}
	}
	return "[用户回滚了文件改动（这不是新请求）：" + strings.Join(parts, "；") +
		"。shell_exec 等命令造成的改动不在快照范围内；不要重做这些改动，除非用户明确要求；需要时先重新读取文件确认当前内容。]"
}

func undoEventAttrs(notice pendingUndoNotice) map[string]any {
	files := append([]string(nil), notice.Files...)
	if len(files) > undoEventFiles {
		files = files[:undoEventFiles]
	}
	if files == nil {
		files = []string{}
	}
	return map[string]any{
		"turns": notice.Turns, "restored": notice.Restored, "deleted": notice.Deleted,
		"skipped": notice.Skipped, "unrecoverable": notice.Unrecoverable,
		"files": files, "forced": notice.Forced, "entry_seq": int64(0),
	}
}

func appendUndoNotice(ctx context.Context, repo *store.Store, sessionID string, notice pendingUndoNotice) error {
	session, err := repo.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	notices, err := decodeUndoNotices(session.Meta[undoNoticeQueueKey])
	if err != nil {
		return err
	}
	notices = append(notices, notice)
	return repo.PatchSessionMeta(ctx, sessionID, map[string]any{undoNoticeQueueKey: notices})
}

func takeUndoNotice(ctx context.Context, repo *store.Store, sessionID string) (pendingUndoNotice, bool, error) {
	session, err := repo.GetSession(ctx, sessionID)
	if err != nil {
		return pendingUndoNotice{}, false, err
	}
	notices, err := decodeUndoNotices(session.Meta[undoNoticeQueueKey])
	if err != nil {
		return pendingUndoNotice{}, false, err
	}
	if len(notices) == 0 {
		return pendingUndoNotice{}, false, nil
	}
	first := notices[0]
	var remaining any
	if len(notices) > 1 {
		remaining = notices[1:]
	}
	if err := repo.PatchSessionMeta(ctx, sessionID, map[string]any{undoNoticeQueueKey: remaining}); err != nil {
		return pendingUndoNotice{}, false, err
	}
	return first, true, nil
}

func decodeUndoNotices(raw any) ([]pendingUndoNotice, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode undo notice queue: %w", err)
	}
	var notices []pendingUndoNotice
	if err := json.Unmarshal(data, &notices); err != nil {
		return nil, fmt.Errorf("decode undo notice queue: %w", err)
	}
	return notices, nil
}

func (r *Runner) injectUndoNotice(ctx context.Context, sessionID, turnID string) (ctxItem, bool, error) {
	notice, ok, err := takeUndoNotice(context.WithoutCancel(ctx), r.cfg.Repository, sessionID)
	if err != nil || !ok {
		return ctxItem{}, false, err
	}
	message := undoNoticeMessage(notice.Text)
	raw, err := store.EncodeMessage(message)
	if err != nil {
		return ctxItem{}, false, err
	}
	entries, err := r.cfg.Repository.AppendEntries(context.WithoutCancel(ctx), sessionID,
		store.NewEntry{TurnID: turnID, Type: store.EntryUser, JSON: raw})
	if err != nil {
		if qerr := appendUndoNotice(context.WithoutCancel(ctx), r.cfg.Repository, sessionID, notice); qerr != nil {
			slog.Warn("return undo notice", "session_id", sessionID, "err", qerr)
		}
		return ctxItem{}, false, fmt.Errorf("store undo notice: %w", err)
	}
	if len(entries) == 0 {
		if qerr := appendUndoNotice(context.WithoutCancel(ctx), r.cfg.Repository, sessionID, notice); qerr != nil {
			slog.Warn("return undo notice", "session_id", sessionID, "err", qerr)
		}
		return ctxItem{}, false, fmt.Errorf("store undo notice: no entry returned")
	}
	if notice.EventID > 0 {
		if err := r.cfg.Repository.UpdateEventEntrySeq(context.WithoutCancel(ctx), notice.EventID, entries[0].Seq); err != nil {
			slog.Warn("update undo event entry sequence", "session_id", sessionID, "event_id", notice.EventID, "err", err)
		}
	}
	return ctxItem{msg: message, seq: entries[0].Seq}, true, nil
}

func (u *UndoCore) enqueueUndoNotice(ctx context.Context, result UndoResult) error {
	notice := newUndoNotice(result)
	wctx := context.WithoutCancel(ctx)
	event, err := u.Repository.AppendEvent(wctx, store.Event{
		SessionID: result.SessionID, Kind: store.EventUndo, Name: "undo", Status: eventOK,
		StartedAt: time.Now().UTC(), Attrs: undoEventAttrs(notice),
	})
	if err != nil {
		slog.Warn("record undo event", "session_id", result.SessionID, "err", err)
	} else {
		notice.EventID = event.ID
	}
	return appendUndoNotice(wctx, u.Repository, result.SessionID, notice)
}
