package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// outTool is a stub tool with a configurable kind, meta and result.
type outTool struct {
	name string
	kind Kind
	meta Meta
	res  *api.ToolResult
	err  error
}

func (t *outTool) Name() string           { return t.name }
func (t *outTool) Description() string    { return t.name }
func (t *outTool) Kind() Kind             { return t.kind }
func (t *outTool) ToolMeta() Meta         { return t.meta }
func (t *outTool) Schema() api.ToolSchema { return api.ToolSchema{Name: t.name} }
func (t *outTool) Execute(_ context.Context, call api.ToolCall) (*api.ToolResult, error) {
	if t.err != nil {
		return nil, t.err
	}
	res := *t.res
	res.CallID, res.Name = call.CallID, t.name
	return &res, nil
}

// fakeSpiller records what it was asked to store.
type fakeSpiller struct {
	calls   int
	content string
	err     error
}

func (s *fakeSpiller) Spill(_ context.Context, call api.ToolCall, content string) (string, error) {
	s.calls++
	s.content = content
	if s.err != nil {
		return "", s.err
	}
	return "out:sess-1:0123456789abcdef", nil
}

// bigText builds n lines of distinct text with a recognisable head and tail.
func bigText(n int) string {
	var b strings.Builder
	b.WriteString("HEAD-MARK\n")
	for i := 0; i < n; i++ {
		b.WriteString("line of tool output that is fairly long ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	b.WriteString("TAIL-MARK")
	return b.String()
}

func outRegistry(ts ...*outTool) *Registry {
	reg := NewRegistry()
	for _, t := range ts {
		reg.Register(t)
	}
	return reg
}

func TestOutputApprovedNote(t *testing.T) {
	// 注明只进模型看到的结果，行为经 output + approval 的真实栈，不直接调内部函数。
	layer := NewOutput(NewApproval(outRegistry(&outTool{name: "act", res: &api.ToolResult{Content: "done"}}),
		ApproverFunc(func(ctx context.Context, call api.ToolCall, _ api.Tool) error {
			switch call.CallID {
			case "answered":
				ReportApprovalAnswer(ctx, ApprovedByUser, 120)
			case "always":
				ReportApproval(ctx, ApprovedAlways, 0)
			case "rule":
				ReportApproval(ctx, ApprovedByRule, 0)
			}
			return nil
		})), OutputConfig{})

	t.Run("human answer gets the note once", func(t *testing.T) {
		ctx, _ := WithApprovalReport(context.Background())
		res, err := layer.ExecuteTool(ctx, api.ToolCall{CallID: "answered", Name: "act"})
		assert.NoErr(t, err)
		assert.Eq(t, "done\n"+ApprovedResultNote, res.Content)
	})
	t.Run("remembered always and rules stay unnoted", func(t *testing.T) {
		for _, call := range []api.ToolCall{{CallID: "always", Name: "act"}, {CallID: "rule", Name: "act"}, {Name: "act"}} {
			ctx, _ := WithApprovalReport(context.Background())
			res, err := layer.ExecuteTool(ctx, call)
			assert.NoErr(t, err)
			assert.Eq(t, "done", res.Content, call.CallID)
		}
	})
	t.Run("denied calls keep their error and get no result", func(t *testing.T) {
		denying := NewOutput(NewApproval(outRegistry(&outTool{name: "act", res: &api.ToolResult{Content: "done"}}),
			ApproverFunc(func(_ context.Context, _ api.ToolCall, _ api.Tool) error {
				return Denied("nope")
			})), OutputConfig{})
		ctx, _ := WithApprovalReport(context.Background())
		res, err := denying.ExecuteTool(ctx, api.ToolCall{CallID: "x", Name: "act"})
		assert.ErrIs(t, err, ErrDenied)
		assert.Nil(t, res)
	})

	t.Run("report survives json with the new field", func(t *testing.T) {
		ctx, rep := WithApprovalReport(context.Background())
		ReportApprovalAnswer(ctx, ApprovedByUser, 100)
		created, err := json.Marshal(rep)
		assert.NoErr(t, err)
		var decoded ApprovalReport
		assert.NoErr(t, json.Unmarshal(created, &decoded))
		assert.Eq(t, ApprovalReport{Source: ApprovedByUser, Wait: 100, Answered: true}, decoded)
	})
}

func TestOutput(t *testing.T) {
	ctx := context.Background()
	big := bigText(2000) // ~90 KB
	sum := sha256.Sum256([]byte(big))
	wantSHA := hex.EncodeToString(sum[:])
	mcpBig := &outTool{name: "mcp_big", kind: KindMCP, res: &api.ToolResult{Content: big, Metadata: map[string]any{"exit_code": 0}}}
	small := &outTool{name: "small", res: &api.ToolResult{Content: "fine"}}
	reread := &outTool{name: "file_read", meta: Meta{Rereadable: true}, res: &api.ToolResult{Content: big}}
	// An MCP tool cannot claim Rereadable to dodge the output layer.
	fakeReread := &outTool{name: "mcp_reread", kind: KindMCP, meta: Meta{Rereadable: true}, res: &api.ToolResult{Content: big}}
	errBig := &outTool{name: "err_big", res: &api.ToolResult{Content: big, IsError: true}}
	denied := &outTool{name: "denied", err: Denied("not allowed")}
	reg := outRegistry(mcpBig, small, reread, fakeReread, errBig, denied)

	t.Run("small result passes unchanged", func(t *testing.T) {
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp}).ExecuteTool(ctx, api.ToolCall{CallID: "c1", Name: "small"})
		assert.NoErr(t, err)
		assert.Eq(t, "fine", res.Content)
		assert.Nil(t, res.Metadata)
		assert.Eq(t, 0, sp.calls)
	})

	t.Run("sanitize runs before hashing and spilling", func(t *testing.T) {
		sp := &fakeSpiller{}
		clean := func(s string) string { return strings.ReplaceAll(s, "HEAD-MARK", "[REDACTED]") }
		res, err := NewOutput(reg, OutputConfig{Spiller: sp, Sanitize: clean}).ExecuteTool(ctx, api.ToolCall{CallID: "c9", Name: "mcp_big"})
		assert.Require(t, assert.NoErr(t, err))
		want := clean(big)
		assert.Eq(t, want, sp.content)
		info, ok := OutputInfoOf(res.Metadata)
		assert.Require(t, assert.True(t, ok))
		sum := sha256.Sum256([]byte(want))
		assert.Eq(t, hex.EncodeToString(sum[:]), info.SHA256)
		assert.Eq(t, len(want), info.Bytes)
		assert.NotContains(t, res.Content, "HEAD-MARK")
	})

	t.Run("large MCP result is spilled with a ref line first", func(t *testing.T) {
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp}).ExecuteTool(ctx, api.ToolCall{CallID: "c2", Name: "mcp_big"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, sp.calls)
		assert.Eq(t, big, sp.content)
		assert.True(t, len(res.Content) <= DefaultInlineOutputBytes, "inline %d", len(res.Content))
		first, rest, _ := strings.Cut(res.Content, "\n")
		assert.StrContains(t, first, "ref out:sess-1:0123456789abcdef")
		assert.StrContains(t, first, "recall(ref, offset, limit)")
		assert.True(t, strings.HasPrefix(rest, "HEAD-MARK"))
		assert.True(t, strings.HasSuffix(res.Content, "TAIL-MARK"))
		assert.StrContains(t, res.Content, "省略")

		info, ok := OutputInfoOf(res.Metadata)
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, "out:sess-1:0123456789abcdef", info.Ref)
		assert.Eq(t, len(big), info.Bytes)
		assert.Eq(t, 2002, info.Lines)
		assert.Eq(t, wantSHA, info.SHA256)
		// Keys the tool set itself are kept.
		assert.Eq(t, 0, res.Metadata["exit_code"])
		// The inner result's metadata map is not mutated.
		assert.Nil(t, mcpBig.res.Metadata[OutputMetaKey])
	})

	t.Run("a picture result keeps its image part", func(t *testing.T) {
		// Design 2026-10-23 risk 2: file_read is Rereadable, so the output layer must
		// neither truncate its text nor drop the image part.
		part := api.ImagePart{MediaType: "image/jpeg", Width: 4, Height: 2, Data: []byte("jpeg")}
		reread.res.Images = []api.ImagePart{part}
		t.Cleanup(func() { reread.res.Images = nil })
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp}).ExecuteTool(ctx, api.ToolCall{CallID: "c10", Name: "file_read"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, big, res.Content)
		assert.Require(t, assert.Eq(t, 1, len(res.Images)))
		assert.Eq(t, "image/jpeg", res.Images[0].MediaType)
		assert.Eq(t, 0, sp.calls)
	})

	t.Run("rereadable tool passes through untouched", func(t *testing.T) {
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp}).ExecuteTool(ctx, api.ToolCall{CallID: "c3", Name: "file_read"})
		assert.NoErr(t, err)
		assert.Eq(t, big, res.Content)
		assert.Eq(t, 0, sp.calls)
		_, ok := OutputInfoOf(res.Metadata)
		assert.False(t, ok)
	})

	t.Run("MCP tool claiming rereadable is still spilled", func(t *testing.T) {
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp}).ExecuteTool(ctx, api.ToolCall{CallID: "c4", Name: "mcp_reread"})
		assert.NoErr(t, err)
		assert.Eq(t, 1, sp.calls)
		assert.True(t, len(res.Content) <= DefaultInlineOutputBytes)
	})

	t.Run("error result is truncated, never spilled", func(t *testing.T) {
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp}).ExecuteTool(ctx, api.ToolCall{CallID: "c5", Name: "err_big"})
		assert.NoErr(t, err)
		assert.True(t, res.IsError)
		assert.Eq(t, 0, sp.calls)
		assert.Eq(t, TruncateHeadTail(big, DefaultInlineOutputBytes), res.Content)
		info, ok := OutputInfoOf(res.Metadata)
		assert.True(t, ok)
		assert.Eq(t, "", info.Ref)
	})

	t.Run("errors pass through as is", func(t *testing.T) {
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp}).ExecuteTool(ctx, api.ToolCall{CallID: "c6", Name: "denied"})
		assert.Nil(t, res)
		assert.True(t, errors.Is(err, ErrDenied))
		assert.Eq(t, "not allowed", err.Error())
	})

	t.Run("no spiller falls back to head/tail truncation", func(t *testing.T) {
		res, err := NewOutput(reg, OutputConfig{InlineBytes: 3000}).ExecuteTool(ctx, api.ToolCall{CallID: "c7", Name: "mcp_big"})
		assert.NoErr(t, err)
		assert.Eq(t, TruncateHeadTail(big, 3000), res.Content)
		info, ok := OutputInfoOf(res.Metadata)
		assert.True(t, ok)
		assert.Eq(t, "", info.Ref)
		assert.Eq(t, len(big), info.Bytes)
	})

	t.Run("spill failure keeps a snippet and says the text was not saved", func(t *testing.T) {
		sp := &fakeSpiller{err: errors.New("disk full")}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp}).ExecuteTool(ctx, api.ToolCall{CallID: "c8", Name: "mcp_big"})
		assert.NoErr(t, err)
		first, _, _ := strings.Cut(res.Content, "\n")
		assert.StrContains(t, first, "未能保存原文")
		assert.StrNotContains(t, res.Content, "ref out:")
		assert.True(t, len(res.Content) <= DefaultInlineOutputBytes)
		info, ok := OutputInfoOf(res.Metadata)
		assert.True(t, ok)
		assert.Eq(t, "", info.Ref)
		assert.Eq(t, wantSHA, info.SHA256)
	})

	t.Run("snippet never splits a rune", func(t *testing.T) {
		cjk := &outTool{name: "cjk", kind: KindMCP, res: &api.ToolResult{Content: strings.Repeat("中文输出", 3000)}}
		res, err := NewOutput(outRegistry(cjk), OutputConfig{Spiller: &fakeSpiller{}}).ExecuteTool(ctx, api.ToolCall{Name: "cjk"})
		assert.NoErr(t, err)
		assert.True(t, utf8.ValidString(res.Content))
		assert.True(t, len(res.Content) <= DefaultInlineOutputBytes)
	})

	t.Run("lookup and listing pass through", func(t *testing.T) {
		out := NewOutput(reg, OutputConfig{})
		tool, err := out.Get("mcp_big")
		assert.NoErr(t, err)
		assert.True(t, IsMCP(tool))
		schemas, err := out.ListTools(ctx)
		assert.NoErr(t, err)
		assert.Eq(t, 6, len(schemas))
	})

	t.Run("info survives a JSON round trip", func(t *testing.T) {
		res, err := NewOutput(reg, OutputConfig{Spiller: &fakeSpiller{}}).ExecuteTool(ctx, api.ToolCall{Name: "mcp_big"})
		assert.NoErr(t, err)
		raw, err := json.Marshal(res.Metadata)
		assert.NoErr(t, err)
		var md map[string]any
		assert.NoErr(t, json.Unmarshal(raw, &md))
		info, ok := OutputInfoOf(md)
		assert.True(t, ok)
		assert.Eq(t, len(big), info.Bytes)
		assert.Eq(t, 2002, info.Lines)
	})
}

func TestTruncateHeadTailShared(t *testing.T) {
	assert.Eq(t, "abc", TruncateHeadTail("abc", 0))
	assert.Eq(t, "abc", TruncateHeadTail("abc", 10))
	out := TruncateHeadTail(strings.Repeat("中", 100), 31)
	assert.True(t, utf8.ValidString(out))
	assert.StrContains(t, out, "省略")
}

func TestOutputMinimize(t *testing.T) {
	ctx := context.Background()
	noisy := strings.Repeat("noise\n", 300) + "result"
	shell := &outTool{name: "shell_exec", res: &api.ToolResult{Content: noisy}}
	failed := &outTool{name: "failing", res: &api.ToolResult{Content: noisy, IsError: true}}
	huge := &outTool{name: "huge", res: &api.ToolResult{Content: strings.Repeat("z", 20000)}}
	reg := outRegistry(shell, failed, huge)
	tidy := func(_ api.ToolCall, content string) (string, bool) {
		if strings.HasPrefix(content, "noise") {
			return "noise (x300)\nresult", true
		}
		return "", false
	}

	t.Run("the tidy text replaces the result and the original is spilled", func(t *testing.T) {
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp, Minimize: tidy}).ExecuteTool(ctx, api.ToolCall{CallID: "c", Name: "shell_exec"})
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, strings.HasPrefix(res.Content, "noise (x300)\nresult\n[已整理输出：原文 "))
		assert.StrContains(t, res.Content, "ref out:sess-1:0123456789abcdef")
		assert.Eq(t, noisy, sp.content)
		info, ok := OutputInfoOf(res.Metadata)
		assert.True(t, ok)
		assert.True(t, info.Minimized)
		assert.Eq(t, len(noisy), info.Bytes)
	})
	t.Run("errors are not tidied", func(t *testing.T) {
		sp := &fakeSpiller{}
		res, err := NewOutput(reg, OutputConfig{Spiller: sp, Minimize: tidy}).ExecuteTool(ctx, api.ToolCall{CallID: "c", Name: "failing"})
		assert.NoErr(t, err)
		assert.Eq(t, noisy, res.Content)
		assert.Eq(t, 0, sp.calls)
	})
	t.Run("a tidy text over the threshold is cut, not spilled twice", func(t *testing.T) {
		sp := &fakeSpiller{}
		long := func(api.ToolCall, string) (string, bool) { return strings.Repeat("y", 5000), true }
		res, err := NewOutput(reg, OutputConfig{Spiller: sp, Minimize: long, InlineBytes: 1000}).ExecuteTool(ctx, api.ToolCall{CallID: "c", Name: "huge"})
		assert.NoErr(t, err)
		assert.Eq(t, 1, sp.calls)
		assert.True(t, len(res.Content) <= 1000, "len %d", len(res.Content))
		info, _ := OutputInfoOf(res.Metadata)
		assert.True(t, info.Minimized)
	})
}
