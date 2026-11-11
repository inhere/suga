package output

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestWithModel pins the "model" field of the first and closing stream-json
// lines: provider/model when known, no field at all when it is not.
func TestWithModel(t *testing.T) {
	t.Run("the session line carries the model", func(t *testing.T) {
		line := WithModel(SessionLine("s1"), "opencode-go/deepseek-v4.1-flash")
		assert.Eq(t, "opencode-go/deepseek-v4.1-flash", line["model"])
		assert.Eq(t, "session", line["type"])
	})
	t.Run("an unknown model adds no field", func(t *testing.T) {
		line := WithModel(SessionLine("s1"), "")
		_, ok := line["model"]
		assert.False(t, ok)
	})
}

// TestWithApprovalMode pins the "approval_mode" field of the session line: the
// tier when the run set one, no field at all otherwise (old output unchanged).
func TestWithApprovalMode(t *testing.T) {
	t.Run("the session line carries the tier", func(t *testing.T) {
		line := WithApprovalMode(SessionLine("s1"), "trust")
		assert.Eq(t, "trust", line["approval_mode"])
		assert.Eq(t, "session", line["type"])
	})
	t.Run("no tier adds no field", func(t *testing.T) {
		line := WithApprovalMode(SessionLine("s1"), "")
		_, ok := line["approval_mode"]
		assert.False(t, ok)
	})
}
