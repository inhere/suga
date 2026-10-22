// Package outschema implements the output contract of `suga run
// --output-schema` and sugasdk.Prompt.OutputSchema (design
// docs/design/2026-10-22-output-schema-design.md): compile a caller's JSON
// Schema in a sandbox, extract JSON from a model answer, check it, and render
// what a report, a trace event and a correction message need.
//
// The package is pure: no store, no runner, no I/O beyond what a caller hands
// in. A schema is treated as data - it never loads external resources and
// nothing in it is executed.
package outschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// MaxSchemaBytes caps one contract (design 6.2): the schema travels with
	// every request of the call, so a bigger one would cost more context than it
	// buys; splitting it into inline $defs is the caller's remedy.
	MaxSchemaBytes = 32 << 10
	// schemaURL is the only resource the compiler may load: the caller's schema,
	// added to it inline (design 3.2). Every other URL is refused.
	schemaURL = "urn:suga:output-schema"
)

// Compile error kinds; the entry layer reports them as usage errors (exit 2).
const (
	// KindSize: the schema is bigger than MaxSchemaBytes.
	KindSize = "size"
	// KindTopLevel: the schema is neither a JSON object nor a boolean.
	KindTopLevel = "toplevel"
	// KindSyntax: the schema is not JSON at all.
	KindSyntax = "syntax"
	// KindExternalRef: the schema refers to a resource that is not inline.
	KindExternalRef = "external_ref"
	// KindUnknownDraft: $schema names a draft this compiler does not know.
	KindUnknownDraft = "unknown_draft"
	// KindInvalidSchema: the schema does not satisfy its meta schema.
	KindInvalidSchema = "invalid_schema"
)

// CompileError is a schema the caller has to fix before a turn may start.
// Message is one line: the entry layer prints it as is.
type CompileError struct {
	Kind    string
	Message string
}

func (e *CompileError) Error() string { return e.Message }

// knownDrafts are the $schema values v6 compiles without loading a meta schema.
// Any other value makes the compiler try to fetch the meta schema, which the
// deny loader refuses; catching it here reports "unsupported draft" instead of
// an external-resource error (design 3.1).
var knownDrafts = map[string]bool{
	"http://json-schema.org/draft-04/schema":        true,
	"http://json-schema.org/draft-04/schema#":       true,
	"http://json-schema.org/draft-06/schema":        true,
	"http://json-schema.org/draft-06/schema#":       true,
	"http://json-schema.org/draft-07/schema":        true,
	"http://json-schema.org/draft-07/schema#":       true,
	"https://json-schema.org/draft/2019-09/schema":  true,
	"https://json-schema.org/draft/2019-09/schema#": true,
	"https://json-schema.org/draft/2020-12/schema":  true,
	"https://json-schema.org/draft/2020-12/schema#": true,
}

// denyLoader is the compiler's only loader: it refuses everything. Internal
// references ($defs, anchors) are resolved by the compiler itself and never
// reach it, so a loader error always means the schema points outside itself.
type denyLoader struct{}

func (denyLoader) Load(url string) (any, error) {
	return nil, &CompileError{Kind: KindExternalRef,
		Message: fmt.Sprintf("输出契约引用了外部资源 %s（不支持，请改用内联 $defs 引用）", oneLine(url))}
}

// Contract is one compiled caller schema.
type Contract struct {
	schema *jsonschema.Schema
	raw    []byte
}

// Raw is the schema text the contract was compiled from (trimmed), for the
// per-request context block.
func (c *Contract) Raw() []byte { return c.raw }

// Compile validates and compiles a caller schema. The returned error is always
// a *CompileError; every failure is a usage error of the caller, not a bug.
func Compile(raw []byte) (*Contract, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, &CompileError{Kind: KindSyntax, Message: "输出契约（JSON Schema）为空"}
	}
	if len(trimmed) > MaxSchemaBytes {
		return nil, &CompileError{Kind: KindSize, Message: fmt.Sprintf(
			"输出契约超过 %d 字节上限（实际 %d 字节）", MaxSchemaBytes, len(trimmed))}
	}
	// The library's own decoder keeps a number's literal text (probe: 9007199254740993
	// validates exactly), which the schema side needs as much as the instance side.
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(trimmed))
	if err != nil {
		return nil, &CompileError{Kind: KindSyntax, Message: "输出契约不是合法 JSON: " + oneLine(err.Error())}
	}
	switch value := doc.(type) {
	case map[string]any:
		if name, ok := value["$schema"].(string); ok && !knownDrafts[name] {
			return nil, &CompileError{Kind: KindUnknownDraft, Message: fmt.Sprintf(
				"输出契约的 $schema %q 不受支持（支持 draft-04/06/07/2019-09/2020-12）", oneLine(name))}
		}
	case bool:
		// "true"/"false" accept or refuse every value; nothing to check.
	default:
		return nil, &CompileError{Kind: KindTopLevel, Message: "输出契约的顶层必须是 JSON 对象或布尔值"}
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	// No RegisterFormat/AssertFormat: a format is an annotation here, so results
	// do not drift with the library's format tables (design 3.1).
	compiler.UseLoader(denyLoader{})
	if err := compiler.AddResource(schemaURL, doc); err != nil {
		return nil, &CompileError{Kind: KindInvalidSchema, Message: "输出契约无效: " + oneLine(err.Error())}
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		kind, msg := compileFailure(err)
		return nil, &CompileError{Kind: kind, Message: msg}
	}
	return &Contract{schema: schema, raw: append([]byte(nil), trimmed...)}, nil
}

// compileFailure maps a compiler error to a kind and a one-line message: a
// refused load is an external reference, anything else means the schema itself
// does not satisfy its meta schema.
func compileFailure(err error) (kind, message string) {
	var loadErr *jsonschema.LoadURLError
	if errors.As(err, &loadErr) {
		return KindExternalRef, "输出契约引用了外部资源（不支持，请改用内联 $defs 引用）"
	}
	return KindInvalidSchema, "输出契约无效: " + oneLine(err.Error())
}

// decodeInstance decodes one candidate JSON text the way the contract checks
// it: numbers keep their literal text, so a value beyond 2^53 is validated
// exactly instead of being rounded through float64.
func decodeInstance(text string) (any, error) {
	return jsonschema.UnmarshalJSON(strings.NewReader(text))
}

// compactJSON is the value a passing check reports: the extracted piece itself,
// compacted, never re-encoded through any - so numbers keep their text and keys
// their order (design 3.3).
func compactJSON(text string) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(text)); err != nil {
		return json.RawMessage(text)
	}
	return json.RawMessage(buf.Bytes())
}

// Result is the outcome of checking one answer against a contract.
type Result struct {
	// Passed: a candidate satisfied the schema.
	Passed bool
	// Source says where the checked value came from (whole/fenced/substring);
	// no_json means the answer held no JSON value at all.
	Source string
	// Value is the passing candidate's raw JSON, compacted.
	Value json.RawMessage
	// Errors are the problems of the last candidate tried (empty on success).
	Errors []Error
}

// Check extracts JSON from answer in the fixed order of design 3.3 and checks
// every candidate until one satisfies the schema. A failing check reports the
// errors of the last candidate (the likeliest final answer).
func (c *Contract) Check(answer string) Result {
	candidates := extract(answer)
	if len(candidates) == 0 {
		return Result{Source: SourceNone, Errors: []Error{{Path: "$", Message: "回答里没有找到 JSON 值"}}}
	}
	var last Result
	for _, cand := range candidates {
		value, err := decodeInstance(cand.Raw)
		if err != nil {
			last = Result{Source: cand.Source, Errors: []Error{{Path: "$", Message: "不是合法的 JSON: " + oneLine(err.Error())}}}
			continue
		}
		if err := c.schema.Validate(value); err != nil {
			last = Result{Source: cand.Source, Errors: validationErrors(err)}
			continue
		}
		return Result{Passed: true, Source: cand.Source, Value: compactJSON(cand.Raw)}
	}
	return last
}
