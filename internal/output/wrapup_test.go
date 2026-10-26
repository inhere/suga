package output

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
)

func TestResultLineWrapUp(t *testing.T) {
	t.Run("a normal answer has no wrap_up field", func(t *testing.T) {
		line := ResultLine("s1", personal.RunResult{RunID: "r1", Output: "done", State: personal.RunCompleted})
		_, ok := line["wrap_up"]
		assert.False(t, ok)
		assert.Eq(t, "result", line["type"])
	})
	t.Run("a wrap-up answer carries its reason", func(t *testing.T) {
		line := ResultLine("s1", personal.RunResult{
			RunID: "r1", Output: "partial", State: personal.RunCompleted, WrapUp: personal.StopMaxSteps,
		})
		assert.Eq(t, "max_steps", line["wrap_up"])
		assert.Eq(t, "partial", line["result"])
		assert.Eq(t, personal.RunCompleted, line["status"])
	})
}

func TestWrapUpNotice(t *testing.T) {
	t.Run("a normal answer has no notice", func(t *testing.T) {
		assert.Eq(t, "", WrapUpNotice(nil, "", 100))
	})
	t.Run("the step limit names the number of steps", func(t *testing.T) {
		assert.StrContains(t, WrapUpNotice(nil, personal.StopMaxSteps, 2), "步数上限（2 步）")
	})
	t.Run("the step limit without a known count still names it", func(t *testing.T) {
		assert.StrContains(t, WrapUpNotice(nil, personal.StopMaxSteps, 0), "步数上限")
	})
	t.Run("token and time limits name their own reason", func(t *testing.T) {
		assert.StrContains(t, WrapUpNotice(nil, personal.StopMaxTokens, 100), "token 预算用尽")
		assert.StrContains(t, WrapUpNotice(nil, personal.StopTimeout, 100), "时间上限")
	})
	t.Run("an unknown reason is reported as it is", func(t *testing.T) {
		assert.StrContains(t, WrapUpNotice(nil, "custom_reason", 100), "custom_reason")
	})
}
