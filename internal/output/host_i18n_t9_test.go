package output

import (
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
)

// TestHostNoticesEnglish: the text-mode notices of a run (wrap-up, output
// limit, retry) read the injected catalog; the Chinese text is covered by the
// existing tests through the fallback.
func TestHostNoticesEnglish(t *testing.T) {
	tr, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("wrap-up names its reason", func(t *testing.T) {
		assert.Eq(t, "the turn ended at the step limit (3 steps); the answer above only uses what was available before",
			WrapUpNotice(tr, personal.StopMaxSteps, 3))
		assert.Eq(t, "the turn ended because the cost budget ran out; the answer above only uses what was available before",
			WrapUpNotice(tr, "max_cost_usd", 0))
		assert.Eq(t, "", WrapUpNotice(tr, "", 0))
	})

	t.Run("truncation names the limit and how to raise it", func(t *testing.T) {
		notice := OutputLimitNotice(tr, 32000)
		assert.StrContains(t, notice, "the answer hit the output limit (32000 tokens)")
		assert.StrContains(t, notice, "max_output_tokens")
		assert.StrNotContains(t, notice, "输出上限")
		assert.StrContains(t, WrapUpTruncatedNotice(tr, 9000), "the wrap-up answer also hit the output limit (9000 tokens)")
	})

	t.Run("retry notice", func(t *testing.T) {
		notice := RetryNotice(tr, personal.Event{Kind: "retry", Text: "HTTP 503",
			Retry: &personal.RetryInfo{Attempt: 2, Wait: time.Second, Restart: true}})
		assert.StrContains(t, notice, "model request failed (HTTP 503)")
		assert.StrContains(t, notice, "retrying in 1s (attempt 2)")
		assert.StrContains(t, notice, "the reply above is incomplete")
	})
}
