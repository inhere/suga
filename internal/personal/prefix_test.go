package personal

import (
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/llm"
)

func TestPrefixTracker(t *testing.T) {
	user := agentcore.Message{Role: "user", Content: "fix the test"}
	call := agentcore.Message{Role: "assistant", Reasoning: "long reasoning that folding removes",
		ToolCall: &agentcore.ToolCall{CallID: "c1", Name: "file_read", Arguments: map[string]any{"path": "a.go"}}}
	result := agentcore.Message{Role: "tool", Content: "package a",
		ToolResult: &agentcore.ToolResult{CallID: "c1", Name: "file_read", Content: "package a"}}
	answer := agentcore.Message{Role: "assistant", Content: "done"}
	req := func(system string, msgs ...agentcore.Message) *agentcore.GenerateRequest {
		return &agentcore.GenerateRequest{System: system, Messages: msgs,
			Tools: []agentcore.ToolSchema{{Name: "file_read"}}}
	}
	// step sends a after b and returns the report of a.
	step := func(b, a *agentcore.GenerateRequest) prefixReport {
		tr := newPrefixTracker()
		if b != nil {
			tr.observe("s1", b)
		}
		tr.observe("s1", a)
		rep, ok := tr.report("s1")
		assert.Require(t, assert.True(t, ok))
		return rep
	}

	t.Run("first request is all fresh", func(t *testing.T) {
		rep := step(nil, req("sys", user))
		assert.Eq(t, prefixFirst, rep.Reason)
		assert.Eq(t, 0, rep.Reused)
		assert.Gt(t, rep.Fresh, 0)
	})
	t.Run("append reuses the whole previous request", func(t *testing.T) {
		prev := req("sys", user)
		cur := req("sys", user, call, result)
		rep := step(prev, cur)
		assert.Eq(t, prefixAppend, rep.Reason)
		assert.Eq(t, 0, rep.Rebuilt)
		whole := printRequest(prev)
		assert.Eq(t, whole.systemSize+whole.toolsSize+whole.msgs[0].size, rep.Reused)
	})
	t.Run("a shorter earlier message is a fold", func(t *testing.T) {
		folded := call
		folded.Reasoning = ""
		folded.Metadata = map[string]any{llm.ReasoningFoldedMetaKey: true}
		rep := step(req("sys", user, call, result), req("sys", user, folded, result, answer))
		assert.Eq(t, prefixFold, rep.Reason)
		assert.Gt(t, rep.Rebuilt, 0) // the folded call and its result are sent again
		assert.Gt(t, rep.Fresh, 0)   // the answer is new
	})
	t.Run("a segment context message starts a segment", func(t *testing.T) {
		seg := agentcore.Message{Role: "user", Content: "segment context", Metadata: map[string]any{llm.SegmentContextMetaKey: true}}
		rep := step(req("sys", user, call, result), req("sys", seg, user))
		assert.Eq(t, prefixSegment, rep.Reason)
		assert.Eq(t, 0, rep.Reused-printRequest(req("sys")).systemSize-printRequest(req("sys")).toolsSize)
	})
	t.Run("system and tool changes", func(t *testing.T) {
		assert.Eq(t, prefixSystem, step(req("sys", user), req("other", user, call)).Reason)
		changed := req("sys", user, call)
		changed.Tools = append(changed.Tools, agentcore.ToolSchema{Name: "file_edit"})
		rep := step(req("sys", user), changed)
		assert.Eq(t, prefixTools, rep.Reason)
		assert.Eq(t, 0, rep.Reused)
	})
	t.Run("any other change is a rewrite", func(t *testing.T) {
		other := user
		other.Content = "something else"
		assert.Eq(t, prefixRewrite, step(req("sys", user, call), req("sys", other, call)).Reason)
	})
	t.Run("empty request does not replace the previous request", func(t *testing.T) {
		tr := newPrefixTracker()
		previous := req("sys", user)
		tr.observeRequest("s1", previous)
		tr.observeRequest("s1", &agentcore.GenerateRequest{System: "new"})
		assert.Same(t, previous, tr.requests["s1"])
		copy := tr.lastRequest("s1")
		assert.NotNil(t, copy)
		assert.NotSame(t, previous, copy)
	})
	t.Run("sessions are tracked apart", func(t *testing.T) {
		tr := newPrefixTracker()
		tr.observe("s1", req("sys", user))
		tr.observe("s2", req("sys", user))
		rep, _ := tr.report("s2")
		assert.Eq(t, prefixFirst, rep.Reason)
	})
}
