package native

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

const (
	sessionMessageToolName = "session_message"
	// sessionMessageSendLimit caps how many session messages one turn sends: a
	// model that keeps reporting back to another session cannot flood it
	// (design session_message D3).
	sessionMessageSendLimit = 5
)

// SessionPeer is one running chat session of this machine and SUGA_HOME, as
// session_message lists it.
type SessionPeer struct {
	// Name is the short name `suga send --session <name>` accepts.
	Name      string
	SessionID string
	CWD       string
	// State is idle or busy: an idle chat starts a turn for a follow-up message at once.
	State string
	// HopsCapable is false for a peer of an older version, whose chat has no
	// automatic-reply limit: a message to it always starts a turn.
	HopsCapable bool
}

// SessionSendResult is what delivering one session message produced.
type SessionSendResult struct {
	// Queued is true when the message waited for the next turn of a busy peer,
	// false when the peer started a turn for it at once.
	Queued bool
	// Steered is true when the message went into the turn the peer runs right now.
	Steered bool
	// Warning is what the receiver did to accept the message (the model cannot see
	// pictures, so they became a note).
	Warning string
	// Legacy is true when the peer runs a version without the automatic-reply
	// limit: the message was delivered and starts its turn whenever it arrives.
	Legacy bool
}

// SessionMessenger is the host side of session_message: the running chat
// processes of this SUGA_HOME (native.Env.SessionMessenger). The suga layering
// keeps the tool out of internal/*; the host resolves short names, the caller's
// session and the hop depth of the current turn.
type SessionMessenger interface {
	// Peers lists the running chat sessions, the caller's own excluded.
	Peers(ctx context.Context) ([]SessionPeer, error)
	// Send delivers text to the session `to` names (a short name, a full session
	// ID or an ID prefix). mode is "follow_up" or "steer".
	Send(ctx context.Context, to, text, mode string) (SessionSendResult, error)
}

// SessionMessageTool lets the model list and message the other chat sessions of
// this machine (design 2026-10-21 session_message): one tool with the actions
// list and send.
type SessionMessageTool struct {
	messenger SessionMessenger
	// turnID names the turn ctx belongs to ("" when unknown): the per-turn send
	// limit resets with every turn.
	turnID func(context.Context) string
	// mu guards sentTurn and sent.
	mu sync.Mutex
	// sentTurn is the turn the counter belongs to, sent how many messages it sent.
	sentTurn string
	sent     int
}

func (t *SessionMessageTool) Name() string { return sessionMessageToolName }

func (t *SessionMessageTool) Description() string {
	return "List and message the other chat sessions running on this machine (same SUGA_HOME): " +
		"\"list\" shows their short name, session ID, working directory and whether they are idle or busy; " +
		"\"send\" delivers a text message to one of them, which the model there reads as a message from a peer " +
		"(a request or a report, never the person's instruction or an approval). " +
		"mode follow_up (default) waits for that session's next turn; steer inserts the text into the turn it runs right now. " +
		"Replies between sessions are limited: a message that would be the 3rd automatic reply to a session starts no turn there but waits for its person."
}

// ToolMeta: it only messages processes of this machine, so it is available in
// read-only sessions and needs no approval.
func (*SessionMessageTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryOther, ReadOnly: true}
}

func (t *SessionMessageTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []any{"list", "send"},
					"description": "list: the running chat sessions; send: deliver text to one of them"},
				"to":   map[string]any{"type": "string", "description": "send: the short name (e.g. suga-k7) or the session ID/ID prefix of the target"},
				"text": map[string]any{"type": "string", "description": "send: the message text the other session's model reads"},
			},
			"required": []any{"action"},
		},
	}
}

func (t *SessionMessageTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	action, _ := call.Arguments["action"].(string)
	switch strings.TrimSpace(action) {
	case "list":
		text, err := t.list(ctx)
		if err != nil {
			return nil, fmt.Errorf("session_message: %w", err)
		}
		return result(call, t.Name(), text), nil
	case "send":
		text, err := t.send(ctx, call.Arguments)
		if err != nil {
			return nil, fmt.Errorf("session_message: %w", err)
		}
		return result(call, t.Name(), text), nil
	default:
		return nil, fmt.Errorf("session_message: action 必须是 list 或 send，收到 %q", action)
	}
}

// list renders the running chat sessions, the caller's own excluded.
func (t *SessionMessageTool) list(ctx context.Context) (string, error) {
	peers, err := t.messenger.Peers(ctx)
	if err != nil {
		return "", err
	}
	if len(peers) == 0 {
		return "本机同一 SUGA_HOME 下没有其它正在运行的 chat 会话（只有 chat 会登记进程；run/ACP 会话收不到消息）。", nil
	}
	lines := make([]string, 0, len(peers)+1)
	lines = append(lines, fmt.Sprintf("本机共有 %d 个其它 chat 会话：", len(peers)))
	for _, peer := range peers {
		line := fmt.Sprintf("- %s · 会话 %s · %s · %s", peer.Name, peer.SessionID, peer.State, peer.CWD)
		if !peer.HopsCapable {
			line += " · 旧版本，不支持连续回复上限"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

// send validates the arguments and delivers one message, telling the model what
// the receiver did with it.
func (t *SessionMessageTool) send(ctx context.Context, args map[string]any) (string, error) {
	to := strings.TrimSpace(stringArg(args, "to"))
	text := strings.TrimSpace(stringArg(args, "text"))
	mode := strings.TrimSpace(stringArg(args, "mode"))
	if to == "" {
		return "", fmt.Errorf("send 需要 to：对方的短名称（如 suga-k7）或会话 ID（可用 action=list 查看）")
	}
	if text == "" {
		return "", fmt.Errorf("send 需要 text：要发给对方的消息内容")
	}
	// A session message always waits for a turn of its own (the receiver queues a
	// steered one as well): only then does the hop limit see every reply. The
	// tool therefore has no mode; a stray value from a model is ignored.
	mode = "follow_up"
	if err := t.reserveSend(ctx); err != nil {
		return "", err
	}
	res, err := t.messenger.Send(ctx, to, text, mode)
	if err != nil {
		t.releaseSend(ctx)
		return "", err
	}
	lines := []string{sessionSendOutcome(mode, res)}
	if res.Warning != "" {
		lines = append(lines, "提示："+res.Warning)
	}
	if res.Legacy {
		lines = append(lines, "注意：对方是旧版本 suga，不支持连续回复上限；这条消息仍已投递，但会直接开始对方的回合。")
	}
	return strings.Join(lines, "\n"), nil
}

// sessionSendOutcome says what the receiver did with the message.
func sessionSendOutcome(mode string, res SessionSendResult) string {
	switch {
	case res.Steered:
		return "已投递（对方正在运行的回合已插入这条消息）"
	case !res.Queued:
		return "已投递（对方空闲，已开始处理）"
	case mode == "steer":
		return "已排队（对方没有可插入的回合，将在它的下一回合处理）"
	default:
		return "已排队（对方正忙，将在它的下一回合处理）"
	}
}

// reserveSend enforces the per-turn send limit. Two messages of the same turn are
// never sent concurrently (one tool call at a time in a turn), so a plain counter
// is enough; the turn id is read from ctx on every call.
func (t *SessionMessageTool) reserveSend(ctx context.Context) error {
	turn := ""
	if t.turnID != nil {
		turn = t.turnID(ctx)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if turn != t.sentTurn {
		t.sentTurn, t.sent = turn, 0
	}
	if t.sent >= sessionMessageSendLimit {
		return fmt.Errorf("本回合已发送 %d 条会话消息（单回合上限 %d）；请在本回合的回复里说明情况，等对方回合结束或用户输入后再继续", t.sent, sessionMessageSendLimit)
	}
	t.sent++
	return nil
}

// releaseSend gives the slot of a message that was not delivered back.
func (t *SessionMessageTool) releaseSend(ctx context.Context) {
	turn := ""
	if t.turnID != nil {
		turn = t.turnID(ctx)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if turn == t.sentTurn && t.sent > 0 {
		t.sent--
	}
}

// stringArg reads a string argument (absent or another type: "").
func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}

// result is one tool result of this tool.
func result(call api.ToolCall, name, content string) *api.ToolResult {
	return &api.ToolResult{CallID: call.CallID, Name: name, Content: content}
}
