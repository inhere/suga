package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// `suga data tool-bodies --slim`: the stubs an older version wrote kept the whole
// call, the reasoning and two 256-rune previews (~1.5 KB per row in the real
// ledger); the pass narrows them to the link fields plus one short preview.

// oldToolBodyStub is the stub shape of the previous release: the full
// api.ToolCall (arguments included), the reasoning and a preview of both the
// content and the result.
type oldToolBodyStub struct {
	Role       string             `json:"Role"`
	Content    string             `json:"Content"`
	ToolCall   *api.ToolCall      `json:"ToolCall,omitempty"`
	ToolResult *oldToolBodyResult `json:"ToolResult,omitempty"`
	Reasoning  string             `json:"Reasoning,omitempty"`
}

// oldToolBodyResult is the old stub's tool result: the link fields plus its own
// 256-rune preview.
type oldToolBodyResult struct {
	CallID  string `json:"CallID,omitempty"`
	Name    string `json:"Name,omitempty"`
	IsError bool   `json:"IsError,omitempty"`
	Preview string `json:"Preview,omitempty"`
}

// oldPreview cuts s to 256 runes and marks the cut, as the previous release did.
func oldPreview(s string) string {
	runes := []rune(s)
	if len(runes) <= 256 {
		return s
	}
	return string(runes[:256]) + "…"
}

// oldStubJSON builds the stub the previous release would have stored for m.
func oldStubJSON(t *testing.T, m api.Message) []byte {
	t.Helper()
	out := oldToolBodyStub{Role: m.Role, Content: oldPreview(m.Content), ToolCall: m.ToolCall, Reasoning: m.Reasoning}
	if m.ToolResult != nil {
		out.ToolResult = &oldToolBodyResult{CallID: m.ToolResult.CallID, Name: m.ToolResult.Name,
			IsError: m.ToolResult.IsError, Preview: oldPreview(m.ToolResult.Content)}
	}
	raw, err := json.Marshal(out)
	assert.Require(t, assert.NoErr(t, err))
	return raw
}

// slimFixture moves three sessions' tool bodies out with the current code and
// then puts every stub back into the old shape, so the ledger looks like one a
// previous release wrote. before maps each session to the entries as they were
// inline (the bytes a reader must still get).
func slimFixture(t *testing.T) (s *Store, before map[string]map[int64]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db", "sessions.db")
	inline, err := Open(path, WithToolBodyInlineBytes(0))
	assert.Require(t, assert.NoErr(t, err))
	before = map[string]map[int64]string{}
	for _, id := range []string{"s1", "s2", "s3"} {
		createSession(t, inline, id, "/w")
		for n := 0; n < 3; n++ {
			msg := bigToolMessage("c" + id + string(rune('0'+n)))
			msg.Reasoning = "thinking about " + id
			_, err := inline.AppendEntries(context.Background(), id,
				user(t, "run"), NewEntry{Type: EntryTool, JSON: msgJSON(t, msg)})
			assert.Require(t, assert.NoErr(t, err))
		}
		before[id] = storedEntries(t, inline, id)
	}
	assert.Require(t, assert.NoErr(t, inline.Close()))

	s = openStoreAt(t, path)
	// Move the bodies out first, then rewrite every stub to the old shape: the
	// body_ref of each row keeps pointing at the same record.
	_, err = s.MoveToolBodies(context.Background(), ToolBodyMaintOptions{Batch: 100})
	assert.Require(t, assert.NoErr(t, err))
	for _, id := range []string{"s1", "s2", "s3"} {
		for n := 0; n < 3; n++ {
			old := oldStubJSON(t, bigToolMessage("c"+id+string(rune('0'+n))))
			_, err := s.db.Exec(`UPDATE entries SET json = ? WHERE session_id = ? AND seq = ?`, string(old), id, int64(2*(n+1)))
			assert.Require(t, assert.NoErr(t, err))
		}
	}
	return s, before
}

// openStoreAt opens the store at path with the default tool body threshold.
func openStoreAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestToolBodyStubKeepsOnlyTheLinkFields: a moved row holds the role, the call
// id, the name and one 120-rune preview around 200 bytes, not the ~1.5 KB the
// old shape cost.
func TestToolBodyStubKeepsOnlyTheLinkFields(t *testing.T) {
	s := openTemp(t)
	createSession(t, s, "s1", "/w")
	var sizes []int
	for n := 0; n < 3; n++ {
		msg := bigToolMessage("c" + string(rune('0'+n)))
		msg.Reasoning = strings.Repeat("why ", 100)
		msg.ToolCall.Arguments = map[string]any{"cmd": strings.Repeat("x", 400), "note": strings.Repeat("y", 400)}
		appended := appendAll(t, s, "s1", NewEntry{Type: EntryTool, JSON: msgJSON(t, msg)})
		stub, ref := storedRow(t, s, "s1", appended[0].Seq)
		assert.True(t, ref != "", "the body moved out")
		sizes = append(sizes, len(stub))

		t.Run("shape", func(t *testing.T) {
			var fields map[string]json.RawMessage
			assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(stub), &fields)))
			assert.Eq(t, 4, len(fields), "Role, Content, ToolCall, ToolResult only: %s", stub)
			for _, key := range []string{"Role", "Content", "ToolCall", "ToolResult"} {
				assert.True(t, fields[key] != nil, "missing %s", key)
			}
			var call map[string]any
			assert.Require(t, assert.NoErr(t, json.Unmarshal(fields["ToolCall"], &call)))
			assert.Eq(t, map[string]any{"CallID": msg.ToolCall.CallID, "Name": msg.ToolCall.Name}, call,
				"the call keeps no arguments")
			var result map[string]any
			assert.Require(t, assert.NoErr(t, json.Unmarshal(fields["ToolResult"], &result)))
			assert.Eq(t, 2, len(result), "CallID and Name, no second preview: %v", result)
			assert.Eq(t, msg.ToolResult.CallID, result["CallID"])
		})
		t.Run("preview", func(t *testing.T) {
			var fields struct{ Content string }
			assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(stub), &fields)))
			assert.Eq(t, toolBodyPreviewRunes+1, len([]rune(fields.Content)), "one 120-rune preview, marked")
			assert.True(t, strings.HasSuffix(fields.Content, "…"))
			assert.True(t, strings.HasPrefix(msg.Content, strings.TrimSuffix(fields.Content, "…")))
		})
	}
	var total int
	for _, size := range sizes {
		assert.True(t, size < 300, "stub of %d bytes is too large", size)
		total += size
	}
	assert.True(t, total/len(sizes) < 300, "average stub must stay well below 300 bytes")
}

// TestListEntryRefsOnSlimStubs: the link extraction still finds the call id of a
// slimmed row, which is the only thing SQL reads from a stub.
func TestListEntryRefsOnSlimStubs(t *testing.T) {
	ctx := context.Background()
	s, _ := slimFixture(t)
	report, err := s.SlimToolBodies(ctx, ToolBodyMaintOptions{Batch: 4})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), report.Rows)

	refs, err := s.ListEntryRefs(ctx, "s1")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 6, len(refs))
	assert.Eq(t, "", refs[0].CallID, "a user entry links no call")
	assert.Eq(t, "cs10", refs[1].CallID)
	assert.Eq(t, "cs11", refs[3].CallID)
}

// TestSlimToolBodiesRewritesOldStubs covers the pass itself: the batches, the
// unchanged reads, the untouched files, the rerun and the byte-for-byte undo.
func TestSlimToolBodiesRewritesOldStubs(t *testing.T) {
	ctx := context.Background()
	s, before := slimFixture(t)
	file := filepath.Join(s.sessionsDir, "s1", ToolBodyFileName)
	fileBefore, err := os.ReadFile(file)
	assert.Require(t, assert.NoErr(t, err))

	status, err := s.ToolBodyStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), status.SlimRows, "every stub is still in the old shape")
	assert.True(t, status.SlimReclaimBytes > 9*400, "the old shape costs about 1 KB a row")

	report, err := s.SlimToolBodies(ctx, ToolBodyMaintOptions{Batch: 2})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), report.Rows)
	assert.True(t, report.Batches >= 5, "the batch size must be honored")
	assert.True(t, report.Complete)
	assert.Eq(t, status.SlimReclaimBytes, report.Bytes, "the estimate of --status matches the pass")

	t.Run("the stubs are in the current shape and read back the original bytes", func(t *testing.T) {
		for id, want := range before {
			assert.Eq(t, want, storedEntries(t, s, id), id)
			for seq := range want {
				stub, ref := storedRow(t, s, id, seq)
				if ref == "" {
					continue
				}
				_, changed := slimToolBodyStub([]byte(stub))
				assert.False(t, changed, "stub of %s/%d is still in the old shape: %s", id, seq, stub)
			}
		}
	})

	t.Run("the session files are untouched", func(t *testing.T) {
		fileAfter, err := os.ReadFile(file)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, string(fileBefore), string(fileAfter))
		status, err := s.ToolBodyStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(9), status.MovedRows, "a slimmed row is still a moved row")
		assert.Eq(t, int64(0), status.SlimRows)
		check, err := s.ToolBodyCheck(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(9), check.Checked)
		assert.True(t, check.OK())
	})

	t.Run("a rerun rewrites nothing", func(t *testing.T) {
		rows := map[string]string{}
		for _, id := range []string{"s1", "s2", "s3"} {
			for seq := int64(1); seq <= 6; seq++ {
				stub, _ := storedRow(t, s, id, seq)
				rows[id+"/"+string(rune('0'+seq))] = stub
			}
		}
		// The pass finished, so its checkpoint sits at the last row: drop it to walk
		// the stubs again, which is what --abort does.
		assert.Require(t, assert.NoErr(t, s.ResetToolBodyState(ctx)))
		again, err := s.SlimToolBodies(ctx, ToolBodyMaintOptions{Batch: 4})
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, again.Batches >= 1, "the pass walked the stubs")
		assert.Eq(t, int64(0), again.Rows, "an already slim stub is skipped")
		assert.Eq(t, int64(0), again.Bytes)
		for _, id := range []string{"s1", "s2", "s3"} {
			for seq := int64(1); seq <= 6; seq++ {
				stub, _ := storedRow(t, s, id, seq)
				assert.Eq(t, rows[id+"/"+string(rune('0'+seq))], stub)
			}
		}
	})

	t.Run("--undo still restores the bodies byte for byte", func(t *testing.T) {
		undo, err := s.UndoToolBodies(ctx, ToolBodyMaintOptions{Batch: 4})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(9), undo.Rows)
		for id, want := range before {
			assert.Eq(t, want, storedEntries(t, s, id), id)
		}
		var stubs int
		assert.NoErr(t, s.db.QueryRow(`SELECT COUNT(1) FROM entries WHERE COALESCE(body_ref, '') <> ''`).Scan(&stubs))
		assert.Eq(t, 0, stubs)
	})
}

// TestSlimToolBodiesSkipsAssistantStubs: assistant entries also move out to
// stubs (design 2026-10-20, 0.3 修订), and their stub keeps the full Content for
// the full-text search, so --slim must leave those rows as they are and --status
// must not count them as old-shape rows. The fixture's tool stubs are still in
// the old shape, so a pass that touched the assistant stub would report 10 rows.
func TestSlimToolBodiesSkipsAssistantStubs(t *testing.T) {
	ctx := context.Background()
	s, _ := slimFixture(t)
	// An assistant stub the way the assistant move will write it: the full
	// content (240 runes, beyond the 120-rune preview) beside a body_ref. The
	// body_ref is reused from another stub: the pass only tests it for non-empty.
	long := strings.Repeat("回答内容", 60)
	stub, err := json.Marshal(toolBodyStub{Role: "assistant", Content: long})
	assert.Require(t, assert.NoErr(t, err))
	_, ref := storedRow(t, s, "s1", 2)
	appended := appendAll(t, s, "s1", NewEntry{Type: EntryAssistant, JSON: msgJSON(t, api.Message{Role: "assistant", Content: long})})
	_, err = s.db.Exec(`UPDATE entries SET json = ?, body_ref = ? WHERE session_id = ? AND seq = ?`,
		string(stub), ref, "s1", appended[0].Seq)
	assert.Require(t, assert.NoErr(t, err))
	want, gotRef := storedRow(t, s, "s1", appended[0].Seq)
	assert.True(t, gotRef != "", "the assistant stub carries a body_ref")

	status, err := s.ToolBodyStatus(ctx)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), status.SlimRows, "--status counts tool stubs only")

	report, err := s.SlimToolBodies(ctx, ToolBodyMaintOptions{Batch: 4})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(9), report.Rows, "the pass rewrites the tool stubs, not the assistant stub")
	row, _ := storedRow(t, s, "s1", appended[0].Seq)
	assert.Eq(t, want, row, "--slim leaves the assistant stub's bytes alone")
}

// TestSlimToolBodiesRestartsAfterComplete: a pass that completed sits on the
// last row; a rerun must scan from the start again so it sees the stubs written
// since it finished (a resumed cursor would hide them, suga-rykn).
func TestSlimToolBodiesRestartsAfterComplete(t *testing.T) {
	ctx := context.Background()
	s, _ := slimFixture(t)
	first, err := s.SlimToolBodies(ctx, ToolBodyMaintOptions{Batch: 8})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, first.Complete)
	assert.Eq(t, int64(9), first.Rows)

	// A new old-shape stub appears after the finish: its key (s3/7) is past the
	// finished cursor, so a pass that resumes at the cursor would never see it.
	msg := bigToolMessage("cNew")
	appended := appendAll(t, s, "s3", NewEntry{Type: EntryTool, JSON: msgJSON(t, msg)})
	_, err = s.db.Exec(`UPDATE entries SET json = ? WHERE session_id = ? AND seq = ?`,
		string(oldStubJSON(t, msg)), "s3", appended[0].Seq)
	assert.Require(t, assert.NoErr(t, err))

	again, err := s.SlimToolBodies(ctx, ToolBodyMaintOptions{Batch: 8})
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, again.Complete)
	assert.Eq(t, int64(1), again.Rows, "the rerun scans from the start and rewrites the new stub")
	stub, _ := storedRow(t, s, "s3", appended[0].Seq)
	_, changed := slimToolBodyStub([]byte(stub))
	assert.False(t, changed, "the rerun brought the new stub to the current shape")
}

// TestSlimToolBodiesResumeAfterInterrupt: a cancelled pass keeps its checkpoint,
// the rerun finishes the rest and nothing is rewritten twice.
func TestSlimToolBodiesResumeAfterInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, before := slimFixture(t)
	first, err := s.SlimToolBodies(ctx, ToolBodyMaintOptions{Batch: 2, OnBatch: func(p ToolBodyMaintProgress) {
		if p.Rows >= 4 {
			cancel()
		}
	}})
	assert.True(t, err != nil, "the cancelled pass reports the context error")
	assert.Eq(t, int64(4), first.Rows)
	assert.False(t, first.Complete)
	assert.True(t, first.CursorSession != "", "the interrupted pass keeps its cursor")

	report, err := s.SlimToolBodies(context.Background(), ToolBodyMaintOptions{Batch: 2})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, int64(5), report.Rows, "the rerun only rewrites what the interrupt left")
	assert.True(t, report.Complete)
	for id, want := range before {
		assert.Eq(t, want, storedEntries(t, s, id), id)
	}
}
