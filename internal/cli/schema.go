package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
func schemaSource(value string, stdin io.Reader) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "-":
		raw, err := io.ReadAll(io.LimitReader(stdin, outschema.MaxSchemaBytes+1))
		if err != nil {
			return nil, processExitError{code: 2, err: fmt.Errorf("读取 --output-schema（stdin）失败: %w", err)}
		}
		return raw, nil
	case strings.HasPrefix(trimmed, "{"), strings.HasPrefix(trimmed, "["):
		return []byte(trimmed), nil
	default:
		raw, err := os.ReadFile(trimmed)
		if err != nil {
			return nil, processExitError{code: 2, err: fmt.Errorf("读取 --output-schema 文件失败: %w", err)}
		}
		return raw, nil
	}
}

// compileOutputSchema resolves and compiles --output-schema. Every failure here
// is a usage error (exit code 2): no session is created and no model is asked
// (the rule --image follows).
func compileOutputSchema(value string, stdin io.Reader) (*outschema.Contract, []byte, error) {
	raw, err := schemaSource(value, stdin)
	if err != nil {
		return nil, nil, err
	}
	contract, err := outschema.Compile(raw)
	if err != nil {
		return nil, nil, processExitError{code: 2, err: fmt.Errorf("--output-schema 无效: %w", err)}
	}
	return contract, contract.Raw(), nil
}

// contractSummary is text mode's closing line of a contract that never passed:
// how many correction rounds ran and the first error. stdout stays the answer.
func contractSummary(rep personal.ContractReport) string {
	tr := cliTranslator()
	detail := cliText(tr, "cli.schema.no_json")
	if len(rep.Errors) > 0 {
		detail = rep.Errors[0].String()
		if len(rep.Errors) > 1 {
			detail = fmt.Sprintf("%s（共 %d 条）", detail, len(rep.Errors))
		}
	}
	return cliText(tr, "cli.schema.invalid", map[string]any{"rounds": max(rep.Rounds-1, 0), "detail": detail})
}
