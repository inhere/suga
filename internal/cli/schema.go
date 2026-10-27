package cli

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/outschema"
)

// The --output-schema entry (design docs/design/2026-10-22-output-schema-design.md
// 2.1/4.4): the caller's schema is a parameter of this call, compiled before a
// session exists, and a failing final answer ends the run with exit code 6.

// ErrOutputContract ends a run with exit code 6: after one correction round the
// answer still does not satisfy the caller's schema. It is kept apart from
// ErrVerifyFailed (5): "the delivery did not pass its check" is not the same
// failure as "the answer's shape is wrong".
var ErrOutputContract = errors.New("回答不符合调用方的输出契约")

// schemaSource reads the raw schema of --output-schema: "-" is stdin, a value
// starting with { or [ is inline JSON, anything else is a file path relative to
// the working directory (design 2.1, fixed order). A read failure is a usage
// error (exit code 2).
func schemaSource(value string, stdin io.Reader, tr i18n.Translator) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "-":
		raw, err := io.ReadAll(io.LimitReader(stdin, outschema.MaxSchemaBytes+1))
		if err != nil {
			return nil, processExitError{code: 2, err: localizedWrap(cliText(tr, "cli.schema.stdin_failed"), err)}
		}
		return raw, nil
	case strings.HasPrefix(trimmed, "{"), strings.HasPrefix(trimmed, "["):
		return []byte(trimmed), nil
	default:
		raw, err := os.ReadFile(trimmed)
		if err != nil {
			return nil, processExitError{code: 2, err: localizedWrap(cliText(tr, "cli.schema.file_failed"), err)}
		}
		return raw, nil
	}
}

// compileOutputSchema resolves and compiles --output-schema. Every failure here
// is a usage error (exit code 2): no session is created and no model is asked
// (the rule --image follows).
func compileOutputSchema(value string, stdin io.Reader, tr i18n.Translator) (*outschema.Contract, []byte, error) {
	raw, err := schemaSource(value, stdin, tr)
	if err != nil {
		return nil, nil, err
	}
	contract, err := outschema.Compile(raw)
	if err != nil {
		// The cause stays in the chain (a usage error still answers errors.As) and the
		// text printed is the localized one.
		return nil, nil, processExitError{code: 2, err: &localizedWrapError{
			message:   cliText(tr, "cli.schema.input_invalid"),
			cause:     err,
			causeText: compileErrorText(tr, err),
		}}
	}
	return contract, contract.Raw(), nil
}

// schemaErrorKey picks the catalog key of a compile error's human line. Two kinds
// carry two wordings, and the parameter tells them apart: a syntax error names the
// decoder message unless the schema is empty, and a refused reference names the
// URL when the sandbox loader refused it but the library's load error otherwise.
func schemaErrorKey(ce *outschema.CompileError) (string, bool) {
	switch ce.Kind {
	case outschema.KindSyntax:
		if _, ok := ce.Params["detail"]; ok {
			return "cli.schema.error.syntax", true
		}
		return "cli.schema.error.empty", true
	case outschema.KindSize:
		return "cli.schema.error.size", true
	case outschema.KindTopLevel:
		return "cli.schema.error.toplevel", true
	case outschema.KindExternalRef:
		if _, ok := ce.Params["url"]; ok {
			return "cli.schema.error.external_ref", true
		}
		return "cli.schema.error.external_ref_detail", true
	case outschema.KindUnknownDraft:
		return "cli.schema.error.unknown_draft", true
	case outschema.KindInvalidSchema:
		return "cli.schema.error.invalid_schema", true
	}
	return "", false
}

// compileErrorText is the human line of a schema the caller has to fix: Kind and
// its Params through the catalog, or the error's own Message when the catalog has
// no line for it (Message stays the text sugasdk and its callers read).
func compileErrorText(tr i18n.Translator, err error) string {
	var ce *outschema.CompileError
	if !errors.As(err, &ce) {
		return err.Error()
	}
	key, ok := schemaErrorKey(ce)
	if !ok {
		return ce.Message
	}
	// T answers the key itself when the catalog lacks it: that is the fallback.
	if text := cliText(tr, key, ce.Params); text != key {
		return text
	}
	return ce.Message
}

// localizedWrap prefixes err with a translated message and keeps err in the
// chain: a localized usage error still answers errors.Is/As for its cause.
type localizedWrapError struct {
	message string
	cause   error
	// causeText overrides what the cause prints when its own Error() is not the
	// human line (a CompileError prints Message, which stays untranslated).
	causeText string
}

func (e *localizedWrapError) Error() string {
	text := e.causeText
	if text == "" {
		text = e.cause.Error()
	}
	return e.message + " " + text
}

func (e *localizedWrapError) Unwrap() error { return e.cause }

func localizedWrap(message string, cause error) error {
	return &localizedWrapError{message: message, cause: cause}
}

// contractSummary is text mode's closing line of a contract that never passed:
// how many correction rounds ran and the first error. stdout stays the answer.
func contractSummary(rep personal.ContractReport, tr i18n.Translator) string {
	detail := cliText(tr, "cli.schema.no_json")
	if len(rep.Errors) > 0 {
		detail = rep.Errors[0].String()
		if len(rep.Errors) > 1 {
			detail = cliText(tr, "cli.schema.detail_count", map[string]any{"detail": detail, "count": len(rep.Errors)})
		}
	}
	return cliText(tr, "cli.schema.invalid", map[string]any{"rounds": max(rep.Rounds-1, 0), "detail": detail})
}
