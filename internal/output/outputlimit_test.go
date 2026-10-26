package output

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
)

func TestOutputLimitNotice(t *testing.T) {
	t.Run("names the limit and how to raise it", func(t *testing.T) {
		notice := OutputLimitNotice(nil, 32000)
		assert.StrContains(t, notice, "输出上限（32000 tokens）")
		assert.StrContains(t, notice, "max_output_tokens")
		assert.StrContains(t, notice, "--max-output-tokens")
	})
	t.Run("without a configured limit it names none", func(t *testing.T) {
		notice := OutputLimitNotice(nil, 0)
		assert.StrContains(t, notice, "输出上限")
		assert.StrNotContains(t, notice, "tokens）")
		assert.StrContains(t, notice, "max_output_tokens")
	})
}

// TestResultLineTruncated: the result line marks an answer cut off at the
// output limit (and names the limit), while a normal answer stays unchanged.
func TestResultLineTruncated(t *testing.T) {
	t.Run("a normal answer carries neither field", func(t *testing.T) {
		line := ResultLine("s", personal.RunResult{RunID: "r", State: personal.RunCompleted, Output: "ok"})
		assert.Nil(t, line["truncated"])
		assert.Nil(t, line["output_limit"])
	})
	t.Run("a cut-off answer names the limit", func(t *testing.T) {
		line := ResultLine("s", personal.RunResult{RunID: "r", State: personal.RunCompleted,
			Output: "half", Truncated: true, OutputLimit: 8000})
		assert.Eq(t, true, line["truncated"])
		assert.Eq(t, 8000, line["output_limit"])
	})
	t.Run("an unknown limit is left out", func(t *testing.T) {
		line := ResultLine("s", personal.RunResult{RunID: "r", State: personal.RunCompleted, Output: "half", Truncated: true})
		assert.Eq(t, true, line["truncated"])
		assert.Nil(t, line["output_limit"])
	})
}
