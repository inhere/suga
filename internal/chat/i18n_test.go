package chat

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
)

// The zh catalog lines the tests expect: the production code takes them from the
// catalog (trOf), so the expectation is read from the same place instead of a
// second copy of the text that could drift from it.
var (
	zhInterruptNotice      = i18n.Fallback().T("approval.render.notice.interrupt_turn")
	zhUndoBusyHint         = i18n.Fallback().T("status.undo.busy")
	zhLocalCommandBusyHint = i18n.Fallback().T("status.local.command_busy")
	zhSpinnerLabel         = i18n.Fallback().T("status.spinner.thinking")
)

// TestChatTranslator covers the one place chat code takes its translator from:
// an injected catalog wins, a missing one falls back to Chinese.
func TestChatTranslator(t *testing.T) {
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	args := map[string]any{"name": "suga"}

	t.Run("renderer", func(t *testing.T) {
		assert.Eq(t, "Hello suga", newRenderer(rendererOptions{Translator: en}).tr().T("common.language.greeting", args))
		assert.Eq(t, "你好 suga", newRenderer(rendererOptions{}).tr().T("common.language.greeting", args))
	})
	t.Run("chat state", func(t *testing.T) {
		assert.Eq(t, "Hello suga", (&chatState{translator: en}).tr().T("common.language.greeting", args))
		assert.Eq(t, "你好 suga", (&chatState{}).tr().T("common.language.greeting", args))
		var nilState *chatState
		assert.Eq(t, "你好 suga", nilState.tr().T("common.language.greeting", args))
	})
}
