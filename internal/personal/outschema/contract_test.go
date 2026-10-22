package outschema

import (
	"errors"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// errorsAs is errors.As, used to read a *CompileError out of Compile's error.
func errorsAs(err error, target any) bool { return errors.As(err, target) }

// mustCompile compiles a schema or fails the test.
func mustCompile(t *testing.T, raw string) *Contract {
	t.Helper()
	c, err := Compile([]byte(raw))
	assert.NoErr(t, err)
	assert.NotNil(t, c)
	return c
}

// TestCompile: the schema entry checks of design 7.2 (syntax, top level, size,
// external $ref, unknown draft, inline $defs, drafts, format).
func TestCompile(t *testing.T) {
	t.Run("syntax error", func(t *testing.T) {
		_, err := Compile([]byte(`{"type":`))
		var ce *CompileError
		assert.True(t, errorsAs(err, &ce))
		assert.Eq(t, KindSyntax, ce.Kind)
	})
	t.Run("empty schema", func(t *testing.T) {
		_, err := Compile([]byte("  \n"))
		var ce *CompileError
		assert.True(t, errorsAs(err, &ce))
		assert.Eq(t, KindSyntax, ce.Kind)
	})
	t.Run("array top level is refused", func(t *testing.T) {
		_, err := Compile([]byte(`[{"type":"object"}]`))
		var ce *CompileError
		assert.True(t, errorsAs(err, &ce))
		assert.Eq(t, KindTopLevel, ce.Kind)
	})
	t.Run("string top level is refused", func(t *testing.T) {
		_, err := Compile([]byte(`"object"`))
		var ce *CompileError
		assert.True(t, errorsAs(err, &ce))
		assert.Eq(t, KindTopLevel, ce.Kind)
	})
	t.Run("boolean top level is accepted", func(t *testing.T) {
		c := mustCompile(t, `true`)
		assert.Eq(t, true, c.Check(`{}`).Passed)
		assert.Eq(t, true, c.Check(`[1,2]`).Passed)
	})
	t.Run("oversized schema is refused", func(t *testing.T) {
		raw := `{"type":"object","description":"` + strings.Repeat("x", MaxSchemaBytes) + `"}`
		_, err := Compile([]byte(raw))
		var ce *CompileError
		assert.True(t, errorsAs(err, &ce))
		assert.Eq(t, KindSize, ce.Kind)
	})
	t.Run("external $ref is refused by the deny loader", func(t *testing.T) {
		_, err := Compile([]byte(`{"$ref":"https://example.com/s.json"}`))
		var ce *CompileError
		assert.True(t, errorsAs(err, &ce))
		assert.Eq(t, KindExternalRef, ce.Kind)
		assert.StrContains(t, ce.Message, "外部资源")
	})
	t.Run("inline $defs reference compiles", func(t *testing.T) {
		c := mustCompile(t, `{"type":"object","properties":{"item":{"$ref":"#/$defs/item"}},
			"required":["item"],"$defs":{"item":{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}}}}}`)
		assert.Eq(t, true, c.Check(`{"item":{"id":1}}`).Passed)
		assert.Eq(t, false, c.Check(`{"item":{}}`).Passed)
	})
	t.Run("unknown $schema reports an unsupported draft", func(t *testing.T) {
		_, err := Compile([]byte(`{"$schema":"https://json-schema.org/draft/2030-01/schema","type":"object"}`))
		var ce *CompileError
		assert.True(t, errorsAs(err, &ce))
		assert.Eq(t, KindUnknownDraft, ce.Kind)
		assert.StrContains(t, ce.Message, "不受支持")
	})
	t.Run("draft-07 and 2020-12 both apply", func(t *testing.T) {
		c7 := mustCompile(t, `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","required":["a"]}`)
		assert.Eq(t, false, c7.Check(`{}`).Passed)
		c20 := mustCompile(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["a"]}`)
		assert.Eq(t, false, c20.Check(`{}`).Passed)
	})
	t.Run("schema violating its meta schema is refused", func(t *testing.T) {
		_, err := Compile([]byte(`{"type":"object","required":"a"}`))
		var ce *CompileError
		assert.True(t, errorsAs(err, &ce))
		assert.Eq(t, KindInvalidSchema, ce.Kind)
	})
	t.Run("format is not asserted", func(t *testing.T) {
		c := mustCompile(t, `{"type":"object","properties":{"mail":{"type":"string","format":"email"}}}`)
		assert.Eq(t, true, c.Check(`{"mail":"not-an-email"}`).Passed)
	})
}

// TestCheckExtraction: the candidate order and the reported source of design
// 3.3 / 7.2.
func TestCheckExtraction(t *testing.T) {
	schema := `{"type":"object","required":["status"],"properties":{"status":{"type":"string"}}}`
	c := mustCompile(t, schema)
	t.Run("whole answer", func(t *testing.T) {
		res := c.Check(`  {"status":"ok"}  `)
		assert.Eq(t, true, res.Passed)
		assert.Eq(t, SourceWhole, res.Source)
	})
	t.Run("prose then fenced json block", func(t *testing.T) {
		res := c.Check("Here is the result:\n\n```json\n{\"status\":\"ok\"}\n```\nDone.")
		assert.Eq(t, true, res.Passed)
		assert.Eq(t, SourceFenced, res.Source)
	})
	t.Run("unlabelled fenced block", func(t *testing.T) {
		res := c.Check("结论：\n```\n{\"status\":\"ok\"}\n```")
		assert.Eq(t, true, res.Passed)
		assert.Eq(t, SourceFenced, res.Source)
	})
	t.Run("several blocks: the last passing one wins", func(t *testing.T) {
		res := c.Check("```json\n{\"status\":1}\n```\n\n```json\n{\"status\":\"ok\"}\n```")
		assert.Eq(t, true, res.Passed)
		assert.Eq(t, SourceFenced, res.Source)
		assert.Eq(t, `{"status":"ok"}`, string(res.Value))
	})
	t.Run("balanced substring in prose", func(t *testing.T) {
		res := c.Check("最终结果 {\"status\":\"ok\"} 如上。")
		assert.Eq(t, true, res.Passed)
		assert.Eq(t, SourceSubstring, res.Source)
	})
	t.Run("braces inside a string do not split the value", func(t *testing.T) {
		res := c.Check(`说明 {"status":"a } b"} 结束`)
		assert.Eq(t, true, res.Passed)
		assert.Eq(t, SourceSubstring, res.Source)
		assert.Eq(t, `{"status":"a } b"}`, string(res.Value))
	})
	t.Run("no JSON at all", func(t *testing.T) {
		res := c.Check("我没有找到答案。")
		assert.Eq(t, false, res.Passed)
		assert.Eq(t, SourceNone, res.Source)
		assert.Eq(t, "回答里没有找到 JSON 值", res.Errors[0].Message)
	})
	t.Run("truncated answer keeps what was written", func(t *testing.T) {
		res := c.Check("```json\n{\"status\":\"ok\"}\n```\n\n说明被截")
		assert.Eq(t, true, res.Passed)
	})
	t.Run("invalid JSON in the block reports a parse problem", func(t *testing.T) {
		res := c.Check("```json\n{\"status\": }\n```")
		assert.Eq(t, false, res.Passed)
		assert.StrContains(t, res.Errors[0].Message, "不是合法的 JSON")
	})
	t.Run("failing value reports the last candidate's errors", func(t *testing.T) {
		res := c.Check("```json\n{\"status\":1}\n```\n然后 {\"status\":2}")
		assert.Eq(t, false, res.Passed)
		assert.Eq(t, SourceSubstring, res.Source)
		assert.Eq(t, false, len(res.Errors) == 0)
	})
}

// TestCheckBigNumber: a value beyond 2^53 keeps its text in the reported value
// and is validated exactly (design 7.2, completion item 6).
func TestCheckBigNumber(t *testing.T) {
	c := mustCompile(t, `{"type":"object","required":["id"],"properties":{"id":{"type":"integer","multipleOf":3}}}`)
	res := c.Check(`{"id": 9007199254740993}`)
	assert.Eq(t, true, res.Passed)
	assert.Eq(t, `{"id":9007199254740993}`, string(res.Value))

	// 9007199254740992 is not a multiple of 3; through float64 the value above
	// would round to it and fail.
	failing := c.Check(`{"id": 9007199254740995}`)
	assert.Eq(t, false, failing.Passed)

	limit := mustCompile(t, `{"type":"object","properties":{"id":{"maximum":9007199254740992}}}`)
	assert.Eq(t, false, limit.Check(`{"id": 9007199254740993}`).Passed)
}

// TestErrorRendering: instance locations render as JSON paths, the error list
// is capped and messages are cut.
func TestErrorRendering(t *testing.T) {
	t.Run("array index and object member", func(t *testing.T) {
		c := mustCompile(t, `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object",
			"required":["id"],"properties":{"id":{"type":"number"}}}}}}`)
		res := c.Check(`{"items":[{"id":"x"}]}`)
		assert.Eq(t, false, res.Passed)
		var paths []string
		for _, err := range res.Errors {
			paths = append(paths, err.Path)
		}
		assert.Contains(t, paths, "$.items[0].id")
	})
	t.Run("root path", func(t *testing.T) {
		c := mustCompile(t, `{"type":"object","required":["status"]}`)
		res := c.Check(`{}`)
		assert.Eq(t, "$", res.Errors[0].Path)
		assert.Eq(t, `$: missing property 'status'`, res.Errors[0].String())
	})
	t.Run("more than MaxErrors is capped", func(t *testing.T) {
		c := mustCompile(t, `{"type":"array","items":{"type":"number"}}`)
		items := make([]string, 12)
		for i := range items {
			items[i] = `"x"`
		}
		res := c.Check(`[` + strings.Join(items, ",") + `]`)
		assert.Eq(t, MaxErrors+1, len(res.Errors))
		assert.StrContains(t, res.Errors[MaxErrors].Message, "还有 4 条错误未列出")
	})
	t.Run("a long message is cut", func(t *testing.T) {
		long := strings.Repeat("a", 400)
		c := mustCompile(t, `{"type":"object","properties":{"v":{"const":"`+long+`"}}}`)
		res := c.Check(`{"v":"b"}`)
		assert.Eq(t, false, res.Passed)
		assert.Lte(t, len([]rune(res.Errors[0].Message)), MaxErrorChars)
		assert.StrContains(t, res.Errors[0].Message, "…")
	})
}

// TestPromptTexts: the per-request block names the schema as data, and the
// correction message carries the errors only - never the schema again.
func TestPromptTexts(t *testing.T) {
	schema := `{"type":"object","required":["status"]}`
	block := PromptBlock([]byte(schema))
	assert.StrContains(t, block, "不是给你的指令")
	assert.StrContains(t, block, schema)
	assert.True(t, strings.HasPrefix(block, "[输出契约]"))

	c := mustCompile(t, schema)
	res := c.Check(`{}`)
	correction := CorrectionText(res)
	assert.StrContains(t, correction, "missing property")
	assert.StrContains(t, correction, "不要执行新的工具调用")
	assert.NotContains(t, correction, "required")
	noJSON := CorrectionText(c.Check("没有 JSON"))
	assert.StrContains(t, noJSON, "解析问题")
}
