package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// englishModelTexts is what a host injects for language: en (the catalog entry
// of internal/i18n/catalog/en/model.ini); the test keeps its own copy so the
// agentcore side is covered without the host catalogs.
func englishModelTexts() ModelTexts {
	return ModelTexts{
		ApprovedNote:       "[executed after manual approval]",
		OutputSavedRef:     "[Output %d bytes / %d lines saved as ref %s; first locate the relevant lines with session_search(query, ref), then read nearby windows with recall(ref, offset, limit); do not page through the whole output]",
		OutputCondensed:    "[Output condensed: original %d bytes / %d lines",
		OutputCondensedRef: "saved as ref %s; for details use session_search(query, ref) or recall(ref, offset, limit)",
	}
}

// approvedApprover answers as a person who allowed the call right now.
func approvedApprover() Approver {
	return ApproverFunc(func(ctx context.Context, _ api.ToolCall, _ api.Tool) error {
		ReportApprovalAnswer(ctx, ApprovedByUser, 0)
		return nil
	})
}

// TestModelTextsDefaults pins that the zero value (no injection, language: zh)
// produces exactly the wording the tool stack sent before phase 1b.
func TestModelTextsDefaults(t *testing.T) {
	defaults := DefaultModelTexts()
	t.Run("defaults are the historical literals", func(t *testing.T) {
		assert.Eq(t, ApprovedResultNote, defaults.ApprovedNote)
		assert.Eq(t, "[输出 %d 字节 / %d 行已保存为 ref %s；先用 session_search(query, ref) 定位相关行，再用 recall(ref, offset, limit) 读附近窗口，不要逐页读完]", defaults.OutputSavedRef)
		assert.Eq(t, "[已整理输出：原文 %d 字节 / %d 行", defaults.OutputCondensed)
		assert.Eq(t, "已保存为 ref %s，需要细节时用 session_search(query, ref) 或 recall(ref, offset, limit)", defaults.OutputCondensedRef)
	})
	t.Run("an empty set resolves to the defaults", func(t *testing.T) {
		assert.Eq(t, defaults, ModelTexts{}.resolve())
		// A partial set keeps the injected field and fills the rest.
		partial := ModelTexts{ApprovedNote: "note"}.resolve()
		assert.Eq(t, "note", partial.ApprovedNote)
		assert.Eq(t, defaults.OutputSavedRef, partial.OutputSavedRef)
	})
}

// TestOutputModelTextsInjected checks that the output layer uses the injected
// wording for the ref header, the condensed note and the approved note, and
// that the protocol parts (tool result body, ref marker, tool names) stay.
func TestOutputModelTextsInjected(t *testing.T) {
	texts := englishModelTexts()
	long := strings.Repeat("x", 5000)
	registry := outRegistry(&outTool{name: "act", res: &api.ToolResult{Content: long}})
	t.Run("ref header", func(t *testing.T) {
		res, err := NewOutput(registry, OutputConfig{InlineBytes: 100, Spiller: &fakeSpiller{}, Texts: texts}).
			ExecuteTool(context.Background(), api.ToolCall{CallID: "c1", Name: "act"})
		assert.NoErr(t, err)
		assert.StrContains(t, res.Content, fmt.Sprintf(texts.OutputSavedRef, len(long), 1, "out:sess-1:0123456789abcdef"))
		assert.StrContains(t, res.Content, "session_search(query, ref)")
		assert.False(t, strings.Contains(res.Content, "已保存为 ref"))
	})
	t.Run("condensed note", func(t *testing.T) {
		// 1000 bytes: the tidy text and its note fit, so the layers only
		// condense (a longer note would be spilled like any other result).
		res, err := NewOutput(registry, OutputConfig{InlineBytes: 1000, Spiller: &fakeSpiller{}, Texts: texts,
			Minimize: func(api.ToolCall, string) (string, bool) { return "tidy", true }}).
			ExecuteTool(context.Background(), api.ToolCall{CallID: "c2", Name: "act"})
		assert.NoErr(t, err)
		assert.Eq(t, "tidy\n"+fmt.Sprintf(texts.OutputCondensed, len(long), 1)+fmt.Sprintf(texts.OutputCondensedRef, "out:sess-1:0123456789abcdef")+"]", res.Content)
		assert.False(t, strings.Contains(res.Content, "已整理输出"))
	})
	t.Run("approved note", func(t *testing.T) {
		layer := NewOutput(NewApproval(registry, approvedApprover()), OutputConfig{Texts: texts})
		ctx, report := WithApprovalReport(context.Background())
		report.Answered, report.Source = true, ApprovedByUser
		res, err := layer.ExecuteTool(ctx, api.ToolCall{CallID: "c3", Name: "act"})
		assert.NoErr(t, err)
		assert.True(t, strings.HasSuffix(res.Content, "\n"+texts.ApprovedNote))
		assert.Eq(t, long+"\n"+texts.ApprovedNote, res.Content)
	})
}

// TestOutputModelTextsZhUnchanged is the byte-level check the plan asks for:
// without injected texts the three lines are identical to the pre-phase-1b
// text, including the separator bytes around the ref.
func TestOutputModelTextsZhUnchanged(t *testing.T) {
	long := strings.Repeat("x", 5000)
	registry := outRegistry(&outTool{name: "act", res: &api.ToolResult{Content: long}})
	res, err := NewOutput(registry, OutputConfig{InlineBytes: 100, Spiller: &fakeSpiller{}}).
		ExecuteTool(context.Background(), api.ToolCall{CallID: "c1", Name: "act"})
	assert.NoErr(t, err)
	assert.True(t, strings.HasPrefix(res.Content,
		"[输出 5000 字节 / 1 行已保存为 ref out:sess-1:0123456789abcdef；先用 session_search(query, ref) 定位相关行，再用 recall(ref, offset, limit) 读附近窗口，不要逐页读完]\n"))

	tidy, err := NewOutput(registry, OutputConfig{InlineBytes: 1000, Spiller: &fakeSpiller{},
		Minimize: func(api.ToolCall, string) (string, bool) { return "tidy", true }}).
		ExecuteTool(context.Background(), api.ToolCall{CallID: "c2", Name: "act"})
	assert.NoErr(t, err)
	// The note lines stay joined without a separator, as they always were.
	assert.Eq(t, "tidy\n[已整理输出：原文 5000 字节 / 1 行已保存为 ref out:sess-1:0123456789abcdef，需要细节时用 session_search(query, ref) 或 recall(ref, offset, limit)]", tidy.Content)
}
