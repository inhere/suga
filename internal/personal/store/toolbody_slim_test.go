package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// Stubs written by v0.38 or older kept the whole call, the reasoning and two
// 256-rune previews (~1.5 KB per row in the real ledger). The `--slim` pass that
// narrowed them was removed in v0.41.0, so such rows stay as they are: these tests
// pin the read path over a ledger of the old shape.

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

// oldStubLedger moves three sessions' tool bodies out with the current code and
// then puts every stub back into the old shape, so the ledger looks like one a
// previous release wrote. before maps each session to the entries as they were
// inline (the bytes a reader must still get).
func oldStubLedger(t *testing.T) (s *Store, before map[string]map[int64]string) {
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

// TestOldShapeStubsStillRead: the fat stubs a v0.40 or older ledger holds are
// never rewritten any more, and reading them must return the original body, the
// call link and a clean check; --undo still restores every row byte for byte.
func TestOldShapeStubsStillRead(t *testing.T) {
	ctx := context.Background()
	s, before := oldStubLedger(t)

	t.Run("ListEntries hands back the original bodies", func(t *testing.T) {
		for id, want := range before {
			assert.Eq(t, want, storedEntries(t, s, id), id)
		}
	})

	t.Run("the call links come from the old stub", func(t *testing.T) {
		refs, err := s.ListEntryRefs(ctx, "s1")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 6, len(refs))
		assert.Eq(t, "", refs[0].CallID, "a user entry links no call")
		assert.Eq(t, "cs10", refs[1].CallID)
		assert.Eq(t, "cs11", refs[3].CallID)
	})

	t.Run("status and check accept the old shape", func(t *testing.T) {
		status, err := s.ToolBodyStatus(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(9), status.MovedRows, "an old-shape stub is a moved row")
		check, err := s.ToolBodyCheck(ctx)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, int64(9), check.Checked)
		assert.True(t, check.OK())
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
