package output

import (
	"bytes"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

func TestLiveTextPrintsDeltasPerMessage(t *testing.T) {
	var out bytes.Buffer
	live := NewLiveText(&out)
	delta := func(s string) personal.Event {
		return personal.Event{Kind: string(agentcore.StreamEventTextDelta), Text: s}
	}
	stop := personal.Event{Kind: string(agentcore.StreamEventMessageStop)}
	assert.Eq(t, true, live.Handle(delta("let me ")))
	live.Handle(delta("check"))
	live.Handle(stop) // a tool call follows: the commentary gets its own line
	live.Handle(delta("done\n"))
	live.Handle(stop)
	assert.Eq(t, false, live.Handle(personal.Event{Kind: "tool_result"}))
	assert.Eq(t, true, live.Finish())
	assert.Eq(t, "let me check\ndone\n", out.String())
	assert.Eq(t, false, NewLiveText(&out).Finish())
}

func TestPrefixedLiveTextMarksEachMessage(t *testing.T) {
	var out bytes.Buffer
	live := NewPrefixedLiveText(&out, "▣ ")
	delta := func(s string) personal.Event {
		return personal.Event{Kind: string(agentcore.StreamEventTextDelta), Text: s}
	}
	stop := personal.Event{Kind: string(agentcore.StreamEventMessageStop)}
	live.Handle(delta("let me "))
	live.Handle(delta("check")) // same message: no second marker
	live.Handle(stop)
	live.Handle(delta("")) // an empty delta does not open a message
	live.Handle(delta("done"))
	live.Finish()
	assert.Eq(t, "▣ let me check\n▣ done\n", out.String())
}

// TestLiveTextMarkdownStreamsRenderedLines: with markdown on, the text is
// rendered per line as it streams, and the message prefix stays on the first
// line.
func TestLiveTextMarkdownStreamsRenderedLines(t *testing.T) {
	var out bytes.Buffer
	live := NewPrefixedLiveText(&out, "▣ ")
	live.SetMarkdown(false)
	delta := func(s string) personal.Event {
		return personal.Event{Kind: string(agentcore.StreamEventTextDelta), Text: s}
	}
	stop := personal.Event{Kind: string(agentcore.StreamEventMessageStop)}

	live.Handle(delta("## Ti"))
	assert.Eq(t, "▣ ", out.String()) // the incomplete line waits for its newline
	live.Handle(delta("tle\n"))
	assert.Eq(t, "▣ Title\n", out.String())
	// A tool call follows the commentary: message_stop ends its last line.
	live.Handle(delta("**done**"))
	live.Handle(stop)
	assert.Eq(t, "▣ Title\ndone\n", out.String())
	// The next message gets its own prefix.
	live.Handle(delta("- item\n"))
	live.Finish()
	assert.Eq(t, "▣ Title\ndone\n▣ • item\n", out.String())
	assert.Eq(t, true, live.Finish())
}

// TestLiveTextMarkdownOffIsRaw: without markdown the deltas are printed byte for
// byte, prefix aside.
func TestLiveTextMarkdownOffIsRaw(t *testing.T) {
	var out bytes.Buffer
	live := NewPrefixedLiveText(&out, "▣ ")
	live.Handle(personal.Event{Kind: string(agentcore.StreamEventTextDelta), Text: "## Title\n**bold**"})
	live.Finish()
	assert.Eq(t, "▣ ## Title\n**bold**\n", out.String())
}

// TestLiveTextWholeLines: with whole lines on (chat keeps an input line on screen
// during a turn) only complete lines leave; the unfinished rest waits for its
// newline or for the end of the message, and with markdown on the message prefix
// goes out with the first line instead of as a write of its own (r3 M1).
func TestLiveTextWholeLines(t *testing.T) {
	delta := func(s string) personal.Event {
		return personal.Event{Kind: string(agentcore.StreamEventTextDelta), Text: s}
	}
	stop := personal.Event{Kind: string(agentcore.StreamEventMessageStop)}

	t.Run("only complete lines leave", func(t *testing.T) {
		var out bytes.Buffer
		live := NewPrefixedLiveText(&out, "▣ ")
		live.SetWholeLines(true)
		live.Handle(delta("第"))
		live.Handle(delta("一行\n第二")) // a delta may split a line anywhere
		assert.Eq(t, "▣ 第一行\n", out.String())
		live.Handle(stop) // the message ends: the tail gets its newline
		assert.Eq(t, "▣ 第一行\n第二\n", out.String())
		live.Handle(delta("最末一句"))
		live.Finish()
		assert.Eq(t, "▣ 第一行\n第二\n▣ 最末一句\n", out.String())
	})

	t.Run("turning it off streams the deltas as before", func(t *testing.T) {
		var out bytes.Buffer
		live := NewPrefixedLiveText(&out, "▣ ")
		live.SetWholeLines(true)
		live.SetWholeLines(false)
		live.Handle(delta("半"))
		assert.Eq(t, "▣ 半", out.String())
	})

	t.Run("markdown carries the prefix on the first line", func(t *testing.T) {
		var out bytes.Buffer
		live := NewPrefixedLiveText(&out, "▣ ")
		live.SetWholeLines(true)
		live.SetMarkdown(false)
		live.Handle(delta("## Ti"))
		assert.Eq(t, "", out.String()) // the incomplete line waits for its newline
		live.Handle(delta("tle\n"))
		assert.Eq(t, "▣ Title\n", out.String())
		live.Handle(stop)
		live.Answer("尾句") // a message of its own: the prefix again
		assert.Eq(t, "▣ Title\n▣ 尾句\n", out.String())
	})
}

func TestLiveTextRetry(t *testing.T) {
	var out bytes.Buffer
	live := NewPrefixedLiveText(&out, "▣ ")
	delta := func(s string) personal.Event {
		return personal.Event{Kind: string(agentcore.StreamEventTextDelta), Text: s}
	}
	retry := personal.Event{Kind: string(agentcore.StreamEventRetry), Text: "stream broken",
		Retry: &personal.RetryInfo{Attempt: 1, Wait: 1234 * time.Millisecond, Restart: true}}
	live.Handle(delta("Hel"))
	// The caller prints the notice: the line is closed and the next message
	// gets its marker again.
	assert.Eq(t, false, live.Handle(retry))
	out.WriteString(RetryNotice(nil, retry) + "\n")
	live.Handle(delta("Hello"))
	live.Finish()
	assert.Eq(t, "▣ Hel\n↻ 模型请求失败（stream broken），1.2s 后重试（第 1 次）；上面的回复不完整，将重新输出\n▣ Hello\n", out.String())
	assert.Eq(t, "↻ 模型请求失败（HTTP 503），0s 后重试（第 0 次）", RetryNotice(nil, personal.Event{Kind: "retry", Text: "HTTP 503"}))
}
