package native

import (
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// sentMessage is one delivery the fake messenger recorded.
type sentMessage struct {
	to, text, mode string
}

// fakeMessenger stands in for the host of session_message (wiring's messenger).
type fakeMessenger struct {
	peers  []SessionPeer
	sent   []sentMessage
	err    error
	result SessionSendResult
}

func (f *fakeMessenger) Peers(context.Context) ([]SessionPeer, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.peers, nil
}

func (f *fakeMessenger) Send(_ context.Context, to, text, mode string) (SessionSendResult, error) {
	f.sent = append(f.sent, sentMessage{to: to, text: text, mode: mode})
	if f.err != nil {
		return SessionSendResult{}, f.err
	}
	return f.result, nil
}

// turnOf reads the turn id session_message limits its sends by ("" = outside a
// turn).
type turnOfKey struct{}

func turnOf(ctx context.Context) string {
	turn, _ := ctx.Value(turnOfKey{}).(string)
	return turn
}

func newSessionMessageTool(m *fakeMessenger) *SessionMessageTool {
	return &SessionMessageTool{messenger: m, turnID: turnOf}
}

func execSessionMessage(t *testing.T, tool *SessionMessageTool, args map[string]any) (*api.ToolResult, error) {
	t.Helper()
	return tool.Execute(context.Background(), api.ToolCall{CallID: "c1", Name: "session_message", Arguments: args})
}

// TestSessionMessageList: the list names every peer with its short name, session
// ID, state and working directory, and marks a peer of an older version.
func TestSessionMessageList(t *testing.T) {
	messenger := &fakeMessenger{peers: []SessionPeer{
		{Name: "suga-k7", SessionID: "1a2b", CWD: "/work/a", State: "idle", HopsCapable: true},
		{Name: "suga-m3", SessionID: "9f8e", CWD: "/work/b", State: "busy"},
	}}
	res, err := execSessionMessage(t, newSessionMessageTool(messenger), map[string]any{"action": "list"})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, res.Content, "suga-k7")
	assert.StrContains(t, res.Content, "1a2b")
	assert.StrContains(t, res.Content, "idle")
	assert.StrContains(t, res.Content, "/work/a")
	// Only the second peer runs an older version (no hops feature).
	assert.Eq(t, 1, strings.Count(res.Content, "旧版本"))
	assert.StrContains(t, res.Content, "suga-m3")
}

// TestSessionMessageListEmpty: a machine with no other chat session says so
// instead of listing nothing.
func TestSessionMessageListEmpty(t *testing.T) {
	res, err := execSessionMessage(t, newSessionMessageTool(&fakeMessenger{}), map[string]any{"action": "list"})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, res.Content, "没有其它正在运行的 chat 会话")
}

// TestSessionMessageArgumentErrors: every missing or wrong argument names what
// the call has to carry, and nothing is delivered.
func TestSessionMessageArgumentErrors(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"action missing", map[string]any{}, "action 必须是 list 或 send"},
		{"to missing", map[string]any{"action": "send", "text": "hi"}, "需要 to"},
		{"text missing", map[string]any{"action": "send", "to": "suga-k7"}, "需要 text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messenger := &fakeMessenger{}
			_, err := execSessionMessage(t, newSessionMessageTool(messenger), tc.args)
			assert.Require(t, assert.Err(t, err))
			assert.StrContains(t, err.Error(), tc.want)
			assert.Eq(t, 0, len(messenger.sent))
		})
	}
}

// TestSessionMessageSendOutcomes: the result says what the receiver did with the
// message, including the warning a receiver reports and the note for a peer of an
// older version the hops limit cannot protect.
func TestSessionMessageSendOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		result SessionSendResult
		want   []string
	}{
		{"idle peer starts a turn", "", SessionSendResult{}, []string{"对方空闲，已开始处理"}},
		{"busy peer queues", "follow_up", SessionSendResult{Queued: true}, []string{"对方正忙"}},
		{"warning shown", "", SessionSendResult{Warning: "图片已转为文字"}, []string{"图片已转为文字"}},
		{"legacy peer", "", SessionSendResult{Legacy: true}, []string{"旧版本", "不支持连续回复上限"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messenger := &fakeMessenger{result: tc.result}
			args := map[string]any{"action": "send", "to": "suga-k7", "text": "在吗"}
			if tc.mode != "" {
				args["mode"] = tc.mode
			}
			res, err := execSessionMessage(t, newSessionMessageTool(messenger), args)
			assert.Require(t, assert.NoErr(t, err))
			for _, want := range tc.want {
				assert.StrContains(t, res.Content, want)
			}
			assert.Require(t, assert.Eq(t, 1, len(messenger.sent)))
			assert.Eq(t, "suga-k7", messenger.sent[0].to)
			assert.Eq(t, "在吗", messenger.sent[0].text)
		})
	}
}

// TestSessionMessageSendLimit: one turn sends at most five messages; the sixth is
// refused with the limit named and nothing is delivered, and the next turn starts
// with a fresh count.
func TestSessionMessageSendLimit(t *testing.T) {
	messenger := &fakeMessenger{}
	tool := newSessionMessageTool(messenger)
	ctx := context.WithValue(context.Background(), turnOfKey{}, "turn-1")
	for i := 1; i <= sessionMessageSendLimit; i++ {
		_, err := tool.Execute(ctx, api.ToolCall{CallID: "c", Arguments: map[string]any{"action": "send", "to": "suga-k7", "text": "hi"}})
		assert.Require(t, assert.NoErr(t, err))
	}
	_, err := tool.Execute(ctx, api.ToolCall{CallID: "c", Arguments: map[string]any{"action": "send", "to": "suga-k7", "text": "hi"}})
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), "单回合上限 5")
	assert.Eq(t, sessionMessageSendLimit, len(messenger.sent))

	next := context.WithValue(context.Background(), turnOfKey{}, "turn-2")
	_, err = tool.Execute(next, api.ToolCall{CallID: "c", Arguments: map[string]any{"action": "send", "to": "suga-k7", "text": "hi"}})
	assert.NoErr(t, err)
	assert.Eq(t, sessionMessageSendLimit+1, len(messenger.sent))
}

// TestSessionMessageAckErrorKeepsTheSlot: a message the host refused is not
// counted, so a failed send does not use up the turn's limit.
func TestSessionMessageAckErrorKeepsTheSlot(t *testing.T) {
	messenger := &fakeMessenger{err: errNoPeer}
	_, err := execSessionMessage(t, newSessionMessageTool(messenger), map[string]any{"action": "send", "to": "gone", "text": "hi"})
	assert.Require(t, assert.Err(t, err))
	messenger.err = nil
	tool := newSessionMessageTool(messenger)
	ctx := context.WithValue(context.Background(), turnOfKey{}, "turn-1")
	for i := 1; i <= sessionMessageSendLimit; i++ {
		_, err := tool.Execute(ctx, api.ToolCall{CallID: "c", Arguments: map[string]any{"action": "send", "to": "suga-k7", "text": "hi"}})
		assert.Require(t, assert.NoErr(t, err))
	}
}

// errNoPeer is the error a host returns for a session that does not exist.
var errNoPeer = errNoPeerError("对方会话不存在")

type errNoPeerError string

func (e errNoPeerError) Error() string { return string(e) }

// TestSessionMessageToolMeta: listing and messaging a local chat changes no file,
// so the tool is read-only (available in read-only sessions) and needs no
// approval.
func TestSessionMessageToolMeta(t *testing.T) {
	meta := tools.MetaOf(&SessionMessageTool{messenger: &fakeMessenger{}})
	assert.True(t, meta.ReadOnly)
}

// TestSessionMessageAbsentWithoutHost: without a host (tools.session_message
// turned off) the catalog does not build the tool at all.
func TestSessionMessageAbsentWithoutHost(t *testing.T) {
	var built api.Tool
	for _, spec := range Catalog() {
		if spec.Name == "session_message" {
			built = spec.New(Env{})
		}
	}
	assert.Nil(t, built)
}
