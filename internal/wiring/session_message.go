package wiring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"

	axconfig "github.com/inhere/suga/internal/config"
)

// sessionMessenger is the host side of the session_message tool (design
// 2026-10-21 session_message): it reuses the local session socket directory of
// `suga send` for both the list and the delivery, so the tool and the command
// share one protocol, one short-name resolution and one set of records.
//
// The runner is deliberately not part of it: the process records name every
// chat (this one included, by PID), so the caller's own short name and session
// come from the records and ctx, and the tools can be built before the runtime
// exists (the same reason native.Env.ExtraEnv is a function).
type sessionMessenger struct {
	// root is the local process directory (SUGA_HOME/run).
	root string
}

// newSessionMessenger returns the messenger tools.session_message needs, or nil
// when the tool is turned off (tools.session_message.enabled,
// SUGA_SESSION_MESSAGE=off) or SUGA_HOME cannot be resolved.
func newSessionMessenger() native.SessionMessenger {
	if !axconfig.SessionMessageEnabled() {
		return nil
	}
	home, err := axconfig.ResolveSugaHome()
	if err != nil || !filepath.IsAbs(home) {
		return nil
	}
	return &sessionMessenger{root: filepath.Join(home, "run")}
}

// currentTurnID is native.Env.TurnID: the turn ctx belongs to ("" outside a
// turn), so the per-turn send limit of session_message resets with every turn.
func currentTurnID(ctx context.Context) string {
	_, turnID := personal.CurrentTurn(ctx)
	return turnID
}

// currentSessionID is the session ctx belongs to ("" outside a turn of a known
// session).
func currentSessionID(ctx context.Context) string {
	if sessionID, _ := personal.CurrentTurn(ctx); sessionID != "" {
		return sessionID
	}
	return api.SessionIDFrom(ctx)
}

// Peers lists the running chat sessions of this SUGA_HOME, the caller's own
// process excluded (a chat can list itself only by PID: the session ID in ctx is
// not registered for a run or ACP session).
func (m *sessionMessenger) Peers(ctx context.Context) ([]native.SessionPeer, error) {
	records, err := personalprocess.List(m.root)
	if err != nil {
		return nil, err
	}
	self := currentSessionID(ctx)
	peers := make([]native.SessionPeer, 0, len(records))
	for _, record := range records {
		if record.PID == os.Getpid() || (self != "" && record.SessionID == self) {
			continue
		}
		peers = append(peers, native.SessionPeer{
			Name: record.Name, SessionID: record.SessionID, CWD: record.CWD, State: record.State,
			HopsCapable: record.HasFeature(personalprocess.FeatureHops),
		})
	}
	return peers, nil
}

// Send delivers one message to the session to names, with the hop depth of this
// turn plus one and this session as the sender.
func (m *sessionMessenger) Send(ctx context.Context, to, text, mode string) (native.SessionSendResult, error) {
	records, err := personalprocess.List(m.root)
	if err != nil {
		return native.SessionSendResult{}, err
	}
	record, err := personalprocess.Resolve(records, to)
	switch {
	case errors.Is(err, personalprocess.ErrNotOnline):
		return native.SessionSendResult{}, fmt.Errorf("对方会话 %q 不存在或已退出（可用 action=list 查看正在运行的会话）", to)
	case errors.Is(err, personalprocess.ErrAmbiguous):
		return native.SessionSendResult{}, fmt.Errorf("选择器 %q 匹配多个会话，请改用完整会话 ID 或短名称", to)
	case err != nil:
		return native.SessionSendResult{}, err
	}
	sender := senderOf(records)
	self := currentSessionID(ctx)
	if record.PID == os.Getpid() || (self != "" && record.SessionID == self) {
		return native.SessionSendResult{}, fmt.Errorf("不能把消息发给自己（%s）", record.Name)
	}
	if sender.SessionID == "" && self == "" {
		return native.SessionSendResult{}, fmt.Errorf("当前会话未登记为本地进程，无法标明消息来源")
	}
	if mode == "" {
		mode = personalprocess.ModeFollowUp
	}
	request := sessionMessageRequest(ctx, sender, self, text, mode)
	// The invocation id names the message: a receiver that sees it twice does not
	// queue it twice, and a receipt or a status query names it (design
	// session-message-delivery D1/D2).
	request.ID = tools.InvocationID(ctx)
	response, err := personalprocess.Send(record, request)
	switch {
	case errors.Is(err, personalprocess.ErrNotOnline):
		return native.SessionSendResult{}, fmt.Errorf("对方会话 %q 已退出，消息未投递", to)
	case errors.Is(err, personalprocess.ErrQueueFull):
		return native.SessionSendResult{}, fmt.Errorf("对方会话 %q 的投递队列已满，稍后再试", to)
	case errors.Is(err, personalprocess.ErrImagesUnsupported):
		return native.SessionSendResult{}, fmt.Errorf("对方会话 %q 的 suga 版本过旧，无法投递", to)
	case err != nil:
		return native.SessionSendResult{}, err
	}
	return native.SessionSendResult{
		Queued: response.Queued, Steered: response.Steered, Warning: response.Warning,
		// The ID the receiver dedupes on and a receipt names (design
		// session-message-delivery D1): the invocation id of this tool call, so a
		// retried call of the same invocation is not queued twice.
		MessageID: response.MessageID,
		// An older receiver ignores Request.Hops and starts a turn every time.
		Legacy: !record.HasFeature(personalprocess.FeatureHops),
	}, nil
}

// Status asks the receiving session for the state of one message ID
// (`session_message action=status`). An ID that process never saw is reported as
// unknown instead of guessed: another process, or a restarted chat.
func (m *sessionMessenger) Status(ctx context.Context, to, id string) (string, error) {
	records, err := personalprocess.List(m.root)
	if err != nil {
		return "", err
	}
	record, err := personalprocess.Resolve(records, to)
	switch {
	case errors.Is(err, personalprocess.ErrNotOnline):
		return "", fmt.Errorf("对方会话 %q 不存在或已退出（可用 action=list 查看正在运行的会话）", to)
	case errors.Is(err, personalprocess.ErrAmbiguous):
		return "", fmt.Errorf("选择器 %q 匹配多个会话，请改用完整会话 ID 或短名称", to)
	case err != nil:
		return "", err
	}
	state, err := personalprocess.Status(record, id)
	if errors.Is(err, personalprocess.ErrUnknownMessage) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return state, nil
}

// senderOf is the record of this process, the empty one when it is not
// registered (a run or ACP session, which never registers).
func senderOf(records []personalprocess.Record) personalprocess.Record {
	for _, record := range records {
		if record.PID == os.Getpid() {
			return record
		}
	}
	return personalprocess.Record{}
}

// senderID names the sender: the full session ID of this process's record, else
// the one ctx carries.
func senderID(record personalprocess.Record, fromCtx string) string {
	if strings.TrimSpace(record.SessionID) != "" {
		return record.SessionID
	}
	return fromCtx
}

// sessionMessageRequest is the request one session message becomes: the sender's
// full session ID and short name (design session_message "来源标记"), and the hop
// depth of this turn plus one, so a message a hops-h turn sends counts as h+1 and
// the receiver stops the chain at tools.session_message.max_hops.
func sessionMessageRequest(ctx context.Context, sender personalprocess.Record, selfSessionID, text, mode string) personalprocess.Request {
	return personalprocess.Request{
		Text:     text,
		From:     personalprocess.SessionFromPrefix + senderID(sender, selfSessionID),
		FromName: sender.Name,
		Hops:     personal.TurnHops(ctx) + 1,
		Mode:     mode,
	}
}
