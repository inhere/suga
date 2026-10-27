package wiring

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// TestResolveModelTexts pins the host side of phase 1b: the Chinese catalog
// (and a nil catalog) resolves to exactly the strings agentcore defaults to,
// so a language: zh request stays byte-identical, and the English catalog
// translates every line while keeping its format verbs.
func TestResolveModelTexts(t *testing.T) {
	zh, err := i18n.New(i18n.LanguageZH)
	assert.Require(t, assert.NoErr(t, err))
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("tool texts: zh and nil equal the agentcore defaults", func(t *testing.T) {
		assert.Eq(t, tools.DefaultModelTexts(), resolveToolTexts(zh))
		assert.Eq(t, tools.DefaultModelTexts(), resolveToolTexts(nil))
	})
	t.Run("tool texts: en is translated", func(t *testing.T) {
		texts := resolveToolTexts(en)
		assert.Eq(t, "[executed after manual approval]", texts.ApprovedNote)
		assert.StrContains(t, texts.OutputSavedRef, "session_search(query, ref)")
		assert.False(t, strings.Contains(texts.OutputSavedRef, "输出"))
		// The format verbs of every line survive the translation.
		assert.Eq(t, 3, strings.Count(texts.OutputSavedRef, "%"))
		assert.Eq(t, 2, strings.Count(texts.OutputCondensed, "%d"))
		assert.Eq(t, 1, strings.Count(texts.OutputCondensedRef, "%s"))
	})
	t.Run("memory texts: zh and nil equal the agentcore defaults", func(t *testing.T) {
		assert.Eq(t, native.DefaultMemoryTexts(), resolveMemoryTexts(zh))
		assert.Eq(t, native.DefaultMemoryTexts(), resolveMemoryTexts(nil))
	})
	t.Run("memory texts: en is translated with the same verbs", func(t *testing.T) {
		texts := resolveMemoryTexts(en)
		assert.False(t, strings.Contains(texts.ReadOnly, "只读"))
		assert.Eq(t, strings.Count(native.DefaultMemoryTexts().OverBudget, "%"), strings.Count(texts.OverBudget, "%"))
		assert.Eq(t, strings.Count(native.DefaultMemoryTexts().Failed, "%"), strings.Count(texts.Failed, "%"))
		assert.StrContains(t, texts.ReadOnly, "get/search still work")
	})
}
