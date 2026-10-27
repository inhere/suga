package personal

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore"
)

// resumeReq builds a request whose messages are msg-0 .. msg-n.
func resumeReq(system string, n int) *agentcore.GenerateRequest {
	msgs := make([]agentcore.Message, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, agentcore.Message{Role: "user", Content: fmt.Sprintf("msg-%d", i)})
	}
	return &agentcore.GenerateRequest{System: system, Messages: msgs,
		Tools: []agentcore.ToolSchema{{Name: "file_read"}}}
}

// TestPrefixResume covers the persisted request fingerprint: the first request
// of a new process is compared with the record the previous one stored, so a
// resumed session is resume_same (only appended) or resume_changed (an earlier
// part differs) instead of a bare first.
func TestPrefixResume(t *testing.T) {
	// persist is what the previous process did at the end of its turn.
	persist := func(req *agentcore.GenerateRequest) *printRecord {
		tr := newPrefixTracker()
		tr.observe("s1", req)
		rec := tr.print("s1")
		assert.Require(t, assert.NotNil(t, rec))
		return rec
	}
	// resume sends req in a fresh tracker seeded with rec.
	resume := func(rec *printRecord, req *agentcore.GenerateRequest) prefixReport {
		tr := newPrefixTracker()
		tr.seed("s1", rec)
		tr.observe("s1", req)
		rep, ok := tr.report("s1")
		assert.Require(t, assert.True(t, ok))
		return rep
	}

	t.Run("only appended is resume_same", func(t *testing.T) {
		rec := persist(resumeReq("sys", 3))
		rep := resume(rec, resumeReq("sys", 5))
		assert.Eq(t, resumeSame, rep.Resume)
		assert.Eq(t, prefixAppend, rep.Reason)
		assert.Gt(t, rep.Reused, 0) // the persisted prefix is reused, so its size is known
		assert.Eq(t, 0, rep.Rebuilt)
	})
	t.Run("a changed system prompt is resume_changed", func(t *testing.T) {
		rec := persist(resumeReq("sys", 3))
		rep := resume(rec, resumeReq("other", 3))
		assert.Eq(t, resumeChanged, rep.Resume)
		assert.Eq(t, prefixSystem, rep.Reason)
		assert.Eq(t, 0, rep.Reused)
		assert.Gt(t, rep.Rebuilt, 0)
	})
	t.Run("an edited earlier message is resume_changed", func(t *testing.T) {
		rec := persist(resumeReq("sys", 3))
		changed := resumeReq("sys", 4)
		changed.Messages[1].Content = "edited"
		rep := resume(rec, changed)
		assert.Eq(t, resumeChanged, rep.Resume)
		assert.Eq(t, prefixRewrite, rep.Reason)
		assert.Gt(t, rep.Rebuilt, 0)
	})
	t.Run("a changed tool table is resume_changed", func(t *testing.T) {
		rec := persist(resumeReq("sys", 3))
		cur := resumeReq("sys", 3)
		cur.Tools = append(cur.Tools, agentcore.ToolSchema{Name: "file_edit"})
		rep := resume(rec, cur)
		assert.Eq(t, resumeChanged, rep.Resume)
		assert.Eq(t, prefixTools, rep.Reason)
	})
	t.Run("the baseline is used by the first request only", func(t *testing.T) {
		tr := newPrefixTracker()
		// A request already seen in this process wins: the seed is ignored, so the
		// second call is an ordinary in-process comparison.
		tr.observe("s1", resumeReq("sys", 3))
		tr.seed("s1", persist(resumeReq("sys", 3)))
		tr.observe("s1", resumeReq("sys", 4))
		rep, _ := tr.report("s1")
		assert.Eq(t, "", rep.Resume)
		assert.Eq(t, prefixAppend, rep.Reason)
	})
	t.Run("a long conversation keeps its tail window", func(t *testing.T) {
		long := resumeReq("sys", resumeKeep+10)
		rec := persist(long)
		assert.Eq(t, resumeKeep, len(rec.Msgs))
		assert.Eq(t, resumeKeep+10, rec.MsgCount)
		// Appending is still resume_same, and the dropped head counts as reused
		// through the cumulative hash of the first kept message.
		rep := resume(rec, resumeReq("sys", resumeKeep+12))
		assert.Eq(t, resumeSame, rep.Resume)
		assert.Gt(t, rep.Reused, 0)
		// An edit inside the kept window is seen.
		edited := resumeReq("sys", resumeKeep+10)
		edited.Messages[len(edited.Messages)-1].Content = "edited"
		assert.Eq(t, resumeChanged, resume(rec, edited).Resume)
		// A request shorter than the dropped head cannot be verified: changed.
		assert.Eq(t, resumeChanged, resume(rec, resumeReq("sys", 2)).Resume)
	})
	t.Run("an unreadable record is no fingerprint", func(t *testing.T) {
		tr := newPrefixTracker()
		tr.seed("s1", &printRecord{})
		tr.observe("s1", resumeReq("sys", 2))
		rep, _ := tr.report("s1")
		assert.Eq(t, "", rep.Resume)
		assert.Eq(t, prefixFirst, rep.Reason)
	})
	t.Run("the record survives the meta round trip", func(t *testing.T) {
		rec := persist(resumeReq("sys", 4))
		raw, err := json.Marshal(rec)
		assert.Require(t, assert.NoErr(t, err))
		back := prefixPrintFromMeta(map[string]any{metaPrefixPrint: string(raw)})
		assert.Require(t, assert.NotNil(t, back))
		rep := resume(back, resumeReq("sys", 5))
		assert.Eq(t, resumeSame, rep.Resume)
		assert.True(t, prefixPrintFromMeta(map[string]any{metaPrefixPrint: "{not json"}) == nil) // unreadable = no base
	})
}
