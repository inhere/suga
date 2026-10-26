package store

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
)

// TestUsageSuggestionsTranslated covers the data-usage maintenance hints in
// both languages: an injected English catalog wins, nil keeps the Chinese text.
func TestUsageSuggestionsTranslated(t *testing.T) {
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))

	newReport := func() *UsageReport {
		rep := &UsageReport{}
		rep.File.FreePages, rep.File.FreeBytes = 3, 3*4096
		rep.Events.Rows = 1
		return rep
	}
	t.Run("english", func(t *testing.T) {
		rep := newReport()
		usageSuggestions(rep, en)
		assert.StrContains(t, rep.File.Suggestion, "3 free pages")
		assert.StrContains(t, rep.File.Suggestion, "suga data vacuum")
		assert.Eq(t, "events are deleted with sessions: `suga data gc --retention` (or --older-than)", rep.Events.Suggestion)
	})
	t.Run("chinese fallback", func(t *testing.T) {
		rep := newReport()
		usageSuggestions(rep, nil)
		assert.StrContains(t, rep.File.Suggestion, "有 3 个空闲页")
		assert.Eq(t, "事件随会话一起删除：`suga data gc --retention`（或 --older-than）", rep.Events.Suggestion)
	})
}
