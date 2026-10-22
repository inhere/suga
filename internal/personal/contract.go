package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/inhere/suga/internal/personal/outschema"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// The output contract of one call (design docs/design/2026-10-22-output-schema-design.md
// 4.1/4.3): the first turn is already done - nudge and --verify had their
// pushes - and the answer it returned is what the caller gets. The contract
// judges that answer; a failing one gets exactly one injected correction round.
const (
	// ContractActionOK: this check passed.
	ContractActionOK = "ok"
	// ContractActionCorrected: this check failed and one correction round follows.
	ContractActionCorrected = "corrected"
	// ContractActionFailed: the final check failed; the call ends with exit code 6.
	ContractActionFailed = "failed"
)

// Reasons a finished turn is not judged (design 4.5): the turn itself did not
// reach a normal end, so its failure is not the contract's.
const (
	ContractReasonFailed    = "failed"
	ContractReasonCanceled  = "canceled"
	ContractReasonYielded   = "yielded"
	ContractReasonWrapUp    = "wrap_up"
	ContractReasonTruncated = "truncated"
	ContractReasonEmpty     = "empty_final"
)

// maxTraceContractErrors caps the errors an output_schema event carries: the
// full list is in the report, the trace stays one line.
const maxTraceContractErrors = 5

// ContractReport is the outcome of judging one call's answer.
type ContractReport struct {
	// Checked is false when the turn's end left nothing to judge.
	Checked bool
	// Passed is the final check's outcome (meaningful only when Checked).
	Passed bool
	// Rounds counts the checks: 1 when the first answer was fine, 2 when the
	// correction round was needed.
	Rounds int
	// Source says where the checked JSON came from (whole/fenced/substring/no_json).
	Source string
	// Reason says why the answer was not checked (Checked=false only).
	Reason string
	// Errors are the last check's validation problems.
	Errors []outschema.Error
	// Value is the passing value, compacted raw JSON.
	Value json.RawMessage
}

// ContractHooks lets the caller act around the correction round: end its
// streamed line before the round starts, and receive the round's events.
type ContractHooks struct {
	// BeforeCorrection runs once, right before the injected correction turn.
	BeforeCorrection func()
	// Emit is the correction turn's event sink (the same one the first turn had).
	Emit EmitFunc
}

// RunWithContract judges the answer of a finished turn against contract and,
// when it fails, gives the model one correction round whose answer is judged
// once more (design 4.3: at most 2 model turns for the whole call).
// schema is the caller's schema text, which the correction round re-sends in
// its per-request context. The returned result is the last turn's.
func (r *Runner) RunWithContract(ctx context.Context, req RunRequest, contract *outschema.Contract,
	schema []byte, first RunResult, hooks ContractHooks) (RunResult, ContractReport, error) {
	if reason := contractSkipReason(first); reason != "" {
		return first, ContractReport{Reason: reason}, nil
	}
	checked := contract.Check(first.Output)
	report := ContractReport{Checked: true, Passed: checked.Passed, Rounds: 1,
		Source: checked.Source, Errors: checked.Errors, Value: checked.Value}
	action := ContractActionOK
	if !checked.Passed {
		action = ContractActionCorrected
	}
	RecordContract(ctx, r, first.SessionID, 1, action, checked, schema)
	if checked.Passed {
		return first, report, nil
	}

	if hooks.BeforeCorrection != nil {
		hooks.BeforeCorrection()
	}
	// The correction turn is an ordinary injected turn: its own turn record, its
	// own usage, and the session stores only the errors - the schema stays in the
	// per-request context (design 4.3, 5).
	next, err := r.Run(ctx, RunRequest{
		SessionID: first.SessionID, Prompt: outschema.CorrectionText(checked),
		Injected: true, EphemeralContext: outschema.PromptBlock(schema), Emit: hooks.Emit,
	})
	if err != nil {
		return next, report, err
	}
	if reason := contractSkipReason(next); reason != "" {
		// The correction round did not produce an answer to judge: the call keeps
		// that turn's own outcome, not a contract failure.
		report.Checked, report.Reason = false, reason
		return next, report, nil
	}
	final := contract.Check(next.Output)
	action = ContractActionFailed
	if final.Passed {
		action = ContractActionOK
	}
	RecordContract(ctx, r, first.SessionID, 2, action, final, schema)
	return next, ContractReport{Checked: true, Passed: final.Passed, Rounds: 2,
		Source: final.Source, Errors: final.Errors, Value: final.Value}, nil
}

// contractSkipReason says why a finished turn is not judged (design 4.5), "" when
// it is.
func contractSkipReason(result RunResult) string {
	switch {
	case result.State == RunCanceled:
		return ContractReasonCanceled
	case result.State != RunCompleted:
		return ContractReasonFailed
	case result.Status == agentcore.RunYielded:
		return ContractReasonYielded
	case result.WrapUp != "":
		return ContractReasonWrapUp
	case result.Truncated:
		return ContractReasonTruncated
	case strings.TrimSpace(result.Output) == "":
		return ContractReasonEmpty
	}
	return ""
}

// contractRecorder writes the output_schema trace event; *Runner implements it.
type contractRecorder interface {
	RecordEvent(ctx context.Context, sessionID string, ev HostEvent)
}

// RecordContract writes one "output_schema" trace event: round, action, passed,
// source, at most 5 errors, and the schema's hash and size - never its text
// (design 5, the event table must stay small).
func RecordContract(ctx context.Context, rec contractRecorder, sessionID string, round int, action string,
	res outschema.Result, schema []byte) {
	sum := sha256.Sum256(schema)
	attrs := map[string]any{
		"round": round, "action": action, "passed": res.Passed, "source": res.Source,
		"schema_sha256": hex.EncodeToString(sum[:])[:12], "schema_bytes": len(schema),
	}
	if errs := contractErrorLines(res.Errors, maxTraceContractErrors); len(errs) > 0 {
		attrs["errors"] = errs
	}
	status := eventOK
	if !res.Passed {
		status = "failed"
	}
	rec.RecordEvent(ctx, sessionID, HostEvent{Kind: store.EventOutputSchema, Name: "check", Status: status, Attrs: attrs})
}

// contractErrorLines renders at most n errors as "path: message", the form the
// trace event and the CLI summary use.
func contractErrorLines(errors []outschema.Error, n int) []string {
	if len(errors) == 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i, err := range errors {
		if i >= n {
			out = append(out, "…")
			break
		}
		out = append(out, err.String())
	}
	return out
}
