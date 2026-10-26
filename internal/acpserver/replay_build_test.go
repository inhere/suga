package acpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

// TestBuildReplayTruncatesOldest: past the byte limit only the most recent
// messages are replayed, and a leading note gives the whole size and how many
// were left out; under the limit everything is replayed with no note.
func TestBuildReplayTruncatesOldest(t *testing.T) {
	big := strings.Repeat("字", 400) // 1200 bytes each
	hist := personal.SegmentHistory{Summary: "前情提要"}
	for i := 0; i < 4; i++ {
		hist.Items = append(hist.Items, personal.SegmentItem{Seq: int64(i + 1),
			Msg: agentcore.Message{Role: "assistant", Content: big + string(rune('A'+i))}})
	}

	t.Run("under the limit", func(t *testing.T) {
		updates := buildReplay(nil, hist, 1<<20)
		assert.Eq(t, 5, len(updates)) // summary note + 4 messages
		assert.Require(t, assert.True(t, updates[0].AgentThoughtChunk != nil))
		assert.StrContains(t, textOf(updates[0].AgentThoughtChunk.Content), "前情提要")
	})

	t.Run("over the limit keeps the newest", func(t *testing.T) {
		updates := buildReplay(nil, hist, 2600)
		// summary note, truncation note, then the two newest messages
		assert.Eq(t, 4, len(updates))
		note := textOf(updates[1].AgentThoughtChunk.Content)
		assert.StrContains(t, note, "仅回放最近 2 条消息，更早的 2 条未包含")
		// The size is the whole history's, above the limit - not what is left.
		var kb float64
		_, err := fmt.Sscanf(note[strings.Index(note, "约 ")+len("约 "):], "%f KB", &kb)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, kb*1024 > 2600, note)
		last := textOf(updates[3].AgentMessageChunk.Content)
		assert.True(t, strings.HasSuffix(last, "D"))
		assert.True(t, strings.HasSuffix(textOf(updates[2].AgentMessageChunk.Content), "C"))
	})
}
