package chat

import (
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/process"
)

// TestSessionDeliveryLine: a message of another session is labelled with that
// session's short name and full ID, and a sender that already tagged its text
// keeps it as it is.
func TestSessionDeliveryLine(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		from     string
		fromName string
		want     string
	}{
		{"session message", "工作目录里有什么？", "session:ab12", "suga-k7", "[来自会话 suga-k7（ab12）的消息] 工作目录里有什么？"},
		{"name unknown", "hi", "session:ab12", "", "[来自会话 未知会话（ab12）的消息] hi"},
		{"already tagged", "[report] done", "session:ab12", "suga-k7", "[report] done"},
		{"plain sender unchanged", "hi", "cli", "", "[来自 cli] hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, deliveryLineFor(tc.text, tc.from, tc.fromName))
		})
	}
}

// TestSessionDeliveryCarriesSourceAndHops: the event a session message becomes
// carries the label the model reads, the sender's name and the hop depth (design
// session_message "来源标记" / D3).
func TestSessionDeliveryCarriesSourceAndHops(t *testing.T) {
	source := &chatInputSource{deliveries: make(chan chatInputEvent, 8)}
	queued, _, err := source.DeliverImages("工作目录里有什么？", process.SessionFromPrefix+"ab12", "suga-k7", 2, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.False(t, queued)
	event := <-source.deliveries
	assert.Eq(t, "[来自会话 suga-k7（ab12）的消息] 工作目录里有什么？", event.line)
	assert.Eq(t, "suga-k7", event.fromName)
	assert.Eq(t, 2, event.hops)
	assert.True(t, event.injected)
}

// TestShouldHoldSessionMessage: only a session message is held, and only once its
// hop depth reaches tools.session_message.max_hops (default 3).
func TestShouldHoldSessionMessage(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.False(t, shouldHoldSessionMessage(process.SessionFromPrefix+"ab12", 2))
	assert.True(t, shouldHoldSessionMessage(process.SessionFromPrefix+"ab12", 3))
	assert.True(t, shouldHoldSessionMessage(process.SessionFromPrefix+"ab12", 4))
	// A person's own input and `suga send` are never held, however deep.
	assert.False(t, shouldHoldSessionMessage("cli", 9))
	assert.Require(t, assert.NoErr(t, gkconfig.Set("tools.session_message.max_hops", 1)))
	assert.True(t, shouldHoldSessionMessage(process.SessionFromPrefix+"ab12", 1))
}

// TestSessionQueuedHint: the person is told why nothing ran and when the message
// will be handled.
func TestSessionQueuedHint(t *testing.T) {
	hint := sessionQueuedHint("suga-k7", 3)
	assert.StrContains(t, hint, "来自会话 suga-k7 的消息已排队")
	assert.StrContains(t, hint, "连续自动回复已达上限")
	assert.StrContains(t, hint, "下次输入时一起处理")
}

// TestHeldSessionMessagesJoinTheNextTurn: a held session message waits in the
// chat's own queue and is taken exactly once, as the context of the next turn.
func TestHeldSessionMessagesJoinTheNextTurn(t *testing.T) {
	state := &chatState{}
	state.holdSessionMessage("[来自会话 suga-k7（ab12）的消息] 你好")
	assert.Eq(t, 1, state.heldSessionCount())
	taken := state.takeHeldSessionMessages()
	assert.Eq(t, 1, len(taken))
	assert.StrContains(t, taken[0], "suga-k7")
	// Taken once: the message must not run a second time.
	assert.Eq(t, 0, state.heldSessionCount())
	assert.Eq(t, 0, len(state.takeHeldSessionMessages()))
}

// TestTurnHopsStartAtZeroForThePersonsOwnTurn: a turn the person starts carries no
// depth, and runOneTurn takes the depth of the delivery it runs for (design
// session_message D3).
func TestTurnHopsStartAtZeroForThePersonsOwnTurn(t *testing.T) {
	state := &chatState{}
	assert.Eq(t, 0, state.turnHops)
	state.turnHops = 2
	assert.Eq(t, 2, state.turnHops)
}
