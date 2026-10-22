package output

import (
	"encoding/json"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/outschema"
)

// TestWithOutputSchema: the result line's output_schema object of a contract run
// (design 2026-10-22 4.4): the checked value on a pass, the errors on a failure,
// and checked:false with a reason when the turn left nothing to judge.
func TestWithOutputSchema(t *testing.T) {
	t.Run("a pass carries the checked value", func(t *testing.T) {
		line := WithOutputSchema(map[string]any{"type": "result"}, personal.ContractReport{
			Checked: true, Passed: true, Rounds: 1, Source: "fenced",
			Value: json.RawMessage(`{"status":"ok","id":9007199254740993}`),
		})
		field := line["output_schema"].(map[string]any)
		assert.Eq(t, true, field["checked"])
		assert.Eq(t, true, field["passed"])
		assert.Eq(t, 1, field["rounds"])
		assert.Eq(t, "fenced", field["source"])
		// The value goes out as JSON, so a big integer is not a quoted string.
		raw, err := json.Marshal(field["value"])
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, `{"status":"ok","id":9007199254740993}`, string(raw))
	})

	t.Run("a failure carries the errors and no value", func(t *testing.T) {
		line := WithOutputSchema(map[string]any{}, personal.ContractReport{
			Checked: true, Passed: false, Rounds: 2, Source: outschema.SourceNone,
			Errors: []outschema.Error{{Path: "$", Message: "missing property 'status'"}},
		})
		field := line["output_schema"].(map[string]any)
		assert.Eq(t, false, field["passed"])
		assert.Eq(t, "no_json", field["source"])
		assert.Empty(t, field["value"])
		errs := field["errors"].([]map[string]string)
		assert.Require(t, assert.Len(t, errs, 1))
		assert.Eq(t, "$", errs[0]["path"])
		assert.Eq(t, "missing property 'status'", errs[0]["message"])
	})

	t.Run("an unchecked turn carries the reason", func(t *testing.T) {
		line := WithOutputSchema(map[string]any{}, personal.ContractReport{
			Checked: false, Reason: personal.ContractReasonWrapUp,
		})
		field := line["output_schema"].(map[string]any)
		assert.Eq(t, false, field["checked"])
		assert.Eq(t, "wrap_up", field["reason"])
		_, hasPassed := field["passed"]
		assert.Eq(t, false, hasPassed)
	})
}
