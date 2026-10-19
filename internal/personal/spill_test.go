package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/testutil"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// shortTempDir returns a fresh directory with a short path that is removed when
// the test ends (session spool files live under it).
func shortTempDir(t *testing.T) string {
	t.Helper()
	return testutil.Dir(t)
}

var hex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestSessionSpiller(t *testing.T) {
	t.Run("file name is 16 hex digits inside the session outputs", func(t *testing.T) {
		dir := shortTempDir(t)
		sp := NewSessionSpiller(dir)
		ctx := withObserveScope(context.Background(), "sess_1", "turn_1")
		seen := map[string]bool{}
		for _, callID := range []string{"c:1", "../../escape", "a/b", "/abs/path", `C:\Windows\x`, ""} {
			ref, err := sp.Spill(ctx, agentcore.ToolCall{CallID: callID, Name: "x"}, "body "+callID)
			assert.Require(t, assert.NoErr(t, err))
			name, ok := strings.CutPrefix(ref, "out:sess_1:")
			assert.Require(t, assert.True(t, ok, ref))
			assert.True(t, hex16.MatchString(name), name)
			assert.Eq(t, OutputFileName("turn_1", callID), name)
			assert.False(t, seen[name])
			seen[name] = true
			got, err := os.ReadFile(filepath.Join(SessionOutputsDir(dir, "sess_1"), name+".txt"))
			assert.NoErr(t, err)
			assert.Eq(t, "body "+callID, string(got))
		}
		// Nothing but the session directory was created under the root.
		entries, err := os.ReadDir(dir)
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(entries))
		files, err := os.ReadDir(SessionOutputsDir(dir, "sess_1"))
		assert.NoErr(t, err)
		assert.Eq(t, 6, len(files))
		if runtime.GOOS != "windows" {
			st, err := os.Stat(SessionOutputsDir(dir, "sess_1"))
			assert.NoErr(t, err)
			assert.Eq(t, os.FileMode(0o700), st.Mode().Perm())
			st, err = os.Stat(filepath.Join(SessionOutputsDir(dir, "sess_1"), files[0].Name()))
			assert.NoErr(t, err)
			assert.Eq(t, os.FileMode(0o600), st.Mode().Perm())
		}
	})

	t.Run("secrets are masked in the file", func(t *testing.T) {
		dir := shortTempDir(t)
		ctx := withObserveScope(context.Background(), "sess_2", "turn_1")
		ref, err := NewSessionSpiller(dir).Spill(ctx, agentcore.ToolCall{CallID: "c1"}, "api_key=sk-live-123456 rest")
		assert.Require(t, assert.NoErr(t, err))
		got, err := os.ReadFile(filepath.Join(SessionOutputsDir(dir, "sess_2"), strings.TrimPrefix(ref, "out:sess_2:")+".txt"))
		assert.NoErr(t, err)
		assert.StrNotContains(t, string(got), "sk-live-123456")
		assert.StrContains(t, string(got), "[REDACTED]")
	})

	t.Run("invalid session ids are refused", func(t *testing.T) {
		dir := shortTempDir(t)
		for _, sid := range []string{"../evil", "a/b", `a\b`, "..", ".", "x:y", "/abs"} {
			ctx := withObserveScope(context.Background(), sid, "turn_1")
			_, err := NewSessionSpiller(dir).Spill(ctx, agentcore.ToolCall{CallID: "c1"}, "body")
			assert.Err(t, err, sid)
		}
		entries, _ := os.ReadDir(dir)
		assert.Eq(t, 0, len(entries))
		_, err := os.Stat(filepath.Join(dir, "..", "evil"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("no session in ctx is an error", func(t *testing.T) {
		_, err := NewSessionSpiller(shortTempDir(t)).Spill(context.Background(), agentcore.ToolCall{CallID: "c1"}, "body")
		assert.ErrMsgContains(t, err, "no session")
	})

	t.Run("a failed write leaves no half written output", func(t *testing.T) {
		dir := shortTempDir(t)
		ctx := withObserveScope(context.Background(), "sess_3", "turn_1")
		outputs := SessionOutputsDir(dir, "sess_3")
		name := OutputFileName("turn_1", "c1")
		// Block the temporary file with a directory of that name so the write
		// fails after the file was created: the content is published by rename
		// alone, so the final .txt must not exist as a half written file.
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Join(outputs, name+".txt.tmp"), 0o700)))
		_, err := NewSessionSpiller(dir).Spill(ctx, agentcore.ToolCall{CallID: "c1"}, "body")
		assert.Err(t, err)
		_, statErr := os.Stat(filepath.Join(outputs, name+".txt"))
		assert.True(t, os.IsNotExist(statErr), "half written .txt left behind")
		files, err := os.ReadDir(outputs)
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(files)) // only the blocking directory
	})

	t.Run("rewriting a call id replaces its output in place", func(t *testing.T) {
		dir := shortTempDir(t)
		ctx := withObserveScope(context.Background(), "sess_4", "turn_1")
		sp := NewSessionSpiller(dir)
		ref, err := sp.Spill(ctx, agentcore.ToolCall{CallID: "c1"}, strings.Repeat("x", 200))
		assert.Require(t, assert.NoErr(t, err))
		ref2, err := sp.Spill(ctx, agentcore.ToolCall{CallID: "c1"}, "short")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, ref, ref2)
		got, err := os.ReadFile(filepath.Join(SessionOutputsDir(dir, "sess_4"),
			strings.TrimPrefix(ref, "out:sess_4:")+".txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "short", string(got))
		files, err := os.ReadDir(SessionOutputsDir(dir, "sess_4"))
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(files)) // the temp file is gone
	})
}

// bigMCP is an MCP stub returning fixed content.
type bigMCP struct{ name, content string }

func (m bigMCP) Name() string        { return m.name }
func (m bigMCP) Description() string { return "big " + m.name }
func (m bigMCP) Kind() tools.Kind    { return tools.KindMCP }
func (m bigMCP) Schema() api.ToolSchema {
	return api.ToolSchema{Name: m.name, InputSchema: map[string]any{"type": "object"}}
}
func (m bigMCP) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	return &api.ToolResult{CallID: call.CallID, Name: m.name, Content: m.content}, nil
}

var refPattern = regexp.MustCompile(`ref (out:([^:\s]+):([0-9a-f]{16}))`)

// TestRunnerSpillsLargeToolOutput runs a turn through New's full stack
// (observe -> output -> approval -> registry): a 50 KB MCP result is stored
// as ref + snippet, its file holds the redacted full text, events and stats
// carry the sizes, and a denied call is still counted as a denial.
func TestRunnerSpillsLargeToolOutput(t *testing.T) {
	ctx := context.Background()
	repo := openObserveRepo(t)
	sessions := shortTempDir(t)
	big := "HEAD-MARK\napi_key=sk-live-abcdef123456\n" + strings.Repeat("row of mcp tool output\n", 2200) + "TAIL-MARK"
	assert.True(t, len(big) > 50000)
	// Sizes and the hash describe the stored (redacted) text.
	stored := redact.Secrets(big)
	reg := tools.NewRegistry()
	reg.Register(bigMCP{name: "mcp_big", content: big})
	reg.Register(vMCP{name: "mcp_write", server: "srv"})
	denyWrite := tools.ApproverFunc(func(_ context.Context, call agentcore.ToolCall, _ agentcore.Tool) error {
		if call.Name == "mcp_write" {
			return tools.Denied("需要许可：" + call.Name)
		}
		return nil
	})
	prov := &reqProv{steps: []*agentcore.GenerateResult{
		callStep("call:../../x", "mcp_big", nil),
		callStep("c2", "mcp_write", nil),
		{Content: "done", FinishReason: agentcore.FinishFinal},
	}}
	runner, err := New(Config{Repository: repo, Provider: prov, Tools: tools.NewApproval(reg, denyWrite),
		Spiller: NewSessionSpiller(sessions)})
	assert.Require(t, assert.NoErr(t, err))
	res, err := runner.Run(ctx, RunRequest{Prompt: "go"})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, RunCompleted, res.State)

	t.Run("denial accounting is unchanged", func(t *testing.T) {
		assert.Eq(t, []string{"mcp_write"}, res.DeniedTools)
		assert.Eq(t, 1, len(eventsOf(t, repo, res.SessionID, store.EventDenial)))
	})

	entries, err := repo.ListEntries(ctx, res.SessionID, store.EntryFilter{Types: []string{store.EntryTool}})
	assert.Require(t, assert.NoErr(t, err))
	var msg agentcore.Message
	for _, e := range entries {
		var m agentcore.Message
		assert.NoErr(t, json.Unmarshal(e.JSON, &m))
		if m.ToolResult != nil && m.ToolResult.Name == "mcp_big" {
			msg = m
		}
	}
	assert.Require(t, assert.NotNil(t, msg.ToolResult))

	t.Run("entry holds only ref and snippets", func(t *testing.T) {
		assert.True(t, len(msg.ToolResult.Content) <= tools.DefaultInlineOutputBytes, "stored %d", len(msg.ToolResult.Content))
		assert.True(t, len(msg.Content) <= tools.DefaultInlineOutputBytes, "stored %d", len(msg.Content))
		assert.StrContains(t, msg.ToolResult.Content, "HEAD-MARK")
		assert.StrContains(t, msg.ToolResult.Content, "TAIL-MARK")
		info, ok := tools.OutputInfoOf(msg.ToolResult.Metadata)
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, len(stored), info.Bytes)
		assert.Eq(t, 2203, info.Lines)
		assert.Eq(t, 64, len(info.SHA256))
	})

	t.Run("file holds the redacted full text", func(t *testing.T) {
		m := refPattern.FindStringSubmatch(msg.ToolResult.Content)
		assert.Require(t, assert.Eq(t, 4, len(m)))
		assert.Eq(t, res.SessionID, m[2])
		info, _ := tools.OutputInfoOf(msg.ToolResult.Metadata)
		assert.Eq(t, m[1], info.Ref)
		got, err := os.ReadFile(filepath.Join(SessionOutputsDir(sessions, res.SessionID), m[3]+".txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, redact.Secrets(big), string(got))
		assert.StrNotContains(t, string(got), "sk-live-abcdef123456")
		sum := sha256.Sum256(got)
		assert.Eq(t, hex.EncodeToString(sum[:]), info.SHA256) // the hash describes the stored file
	})

	t.Run("the model saw the ref line", func(t *testing.T) {
		assert.Require(t, assert.True(t, len(prov.reqs) >= 2))
		var seen bool
		for _, m := range prov.reqs[1].Messages {
			if m.Role == "tool" && refPattern.MatchString(m.Content+toolContent(m)) {
				seen = true
			}
		}
		assert.True(t, seen)
	})

	t.Run("smallest context truncation keeps the ref", func(t *testing.T) {
		b := &contextBuilder{truncLimit: truncateLadder[len(truncateLadder)-1]}
		cut := b.limited(msg)
		assert.True(t, len(cut.ToolResult.Content) < len(msg.ToolResult.Content))
		assert.True(t, refPattern.MatchString(cut.ToolResult.Content))
		assert.True(t, refPattern.MatchString(cut.Content))
	})

	t.Run("tool event and stats carry the sizes", func(t *testing.T) {
		var ev *store.Event
		for _, e := range eventsOf(t, repo, res.SessionID, store.EventTool) {
			if e.Name == "mcp_big" {
				ev = &e
			}
		}
		assert.Require(t, assert.NotNil(t, ev))
		assert.Eq(t, float64(len(stored)), ev.Attrs["raw_bytes"])
		assert.Eq(t, float64(len(msg.ToolResult.Content)), ev.Attrs["inline_bytes"])
		assert.Eq(t, true, ev.Attrs["spilled"])
		rep, err := repo.BuildReport(ctx, store.EventFilter{SessionID: res.SessionID})
		assert.NoErr(t, err)
		for _, st := range rep.Tools {
			if st.Name == "mcp_big" {
				assert.Eq(t, int64(len(stored)), st.RawBytes)
				assert.Eq(t, 1, st.Spilled)
			}
		}
	})
}

func toolContent(m agentcore.Message) string {
	if m.ToolResult == nil {
		return ""
	}
	return m.ToolResult.Content
}

func TestAddOutputAttrs(t *testing.T) {
	t.Run("untouched result: raw equals inline", func(t *testing.T) {
		attrs := map[string]any{}
		addOutputAttrs(attrs, &agentcore.ToolResult{Content: "hello"})
		assert.Eq(t, map[string]any{"raw_bytes": 5, "inline_bytes": 5, "spilled": false}, attrs)
	})
	t.Run("truncated without ref", func(t *testing.T) {
		attrs := map[string]any{}
		md := map[string]any{tools.OutputMetaKey: map[string]any{"bytes": 9000, "lines": 3, "sha256": "x"}}
		addOutputAttrs(attrs, &agentcore.ToolResult{Content: "short", Metadata: md})
		assert.Eq(t, map[string]any{"raw_bytes": 9000, "inline_bytes": 5, "spilled": false}, attrs)
	})
}
