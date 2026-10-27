package chatui

import (
	"bytes"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestLiveTextStreamsWithMarkdown(t *testing.T) {
	var out bytes.Buffer
	live := NewPrefixedLiveText(&out, "▣ ")
	live.SetMarkdown(false)
	live.Handle(StreamEvent{Kind: StreamTextDelta, Text: "## Ti"})
	live.Handle(StreamEvent{Kind: StreamTextDelta, Text: "tle\nanswer"})
	live.Handle(StreamEvent{Kind: StreamMessageStop})
	assert.Eq(t, "▣ Title\nanswer\n", out.String())
}

func TestLiveTextWholeLines(t *testing.T) {
	var out bytes.Buffer
	live := NewPrefixedLiveText(&out, "▣ ")
	live.SetWholeLines(true)
	live.Handle(StreamEvent{Kind: StreamTextDelta, Text: "partial"})
	assert.Eq(t, "", out.String())
	live.Handle(StreamEvent{Kind: StreamMessageStop})
	assert.Eq(t, "▣ partial\n", out.String())
}
