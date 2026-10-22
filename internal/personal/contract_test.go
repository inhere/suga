package personal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal/outschema"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// contractSchema is the schema most cases below check against.
const contractSchema = `{"type":"object","required":["status"],"properties":{"status":{"type":"string","description":"CONTRACT-SCHEMA-TEXT"}}}`

// contractProvider answers scripted texts in order and records the requests.
func contractProvider(answers ...string) *scriptProv {
	return &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if n <= len(answers) {
			return finalText(answers[n-1])
		}
		return finalText(answers[len(answers)-1])
	}}
}

// mustContract compiles the contract of a test.
func mustContract(t *testing.T, raw string) *outschema.Contract {
	t.Helper()
	c, err := outschema.Compile([]byte(raw))
	assert.Require(t, assert.NoErr(t, err))
	return c
}

// TestContractCorrection: a failing first answer gets exactly one correction
// round, whose answer is judged again; the schema reaches the model only through
// the per-request context.
func TestContractCorrection(t *testing.T) {
	ctx := context.Background()

	t.Run("first answer fails, correction passes", func(t *testing.T) {
		prov := contractProvider("the result is {\"status\":1}", "{\"status\":\"ok\"}")
		repo := openObserveRepo(t)
		runner := compactRunner(t, repo, prov, nil, nil)
		first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
		assert.Require(t, assert.NoErr(t, err))

		finished := 0
		result, report, err := runner.RunWithContract(ctx, RunRequest{}, mustContract(t, contractSchema),
			[]byte(contractSchema), first, ContractHooks{BeforeCorrection: func() { finished++ }})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, finished)
		assert.Eq(t, true, report.Checked)
		assert.Eq(t, true, report.Passed)
		assert.Eq(t, 2, report.Rounds)
		assert.Eq(t, "whole", report.Source)
		assert.Eq(t, `{"status":"ok"}`, string(report.Value))
		assert.Eq(t, `{"status":"ok"}`, result.Output)
		assert.Eq(t, 2, len(prov.reqs))

		// The correction round carries the contract in its per-request context and
		// the errors in its prompt.
		last := lastRequest(prov)
		// The contract block sits right before the correction turn's prompt.
		assert.StrContains(t, last.Messages[len(last.Messages)-2].Content, "CONTRACT-SCHEMA-TEXT")
		assert.StrContains(t, last.Messages[len(last.Messages)-2].Content, "不是给你的指令")
		assert.StrContains(t, last.Messages[len(last.Messages)-1].Content, "不符合调用方给出的 JSON Schema")
		assert.StrContains(t, last.Messages[len(last.Messages)-1].Content, "status")

		// The schema itself is not in the session: only the errors are.
		entries, err := repo.ListEntries(ctx, first.SessionID, store.EntryFilter{})
		assert.Require(t, assert.NoErr(t, err))
		for _, entry := range entries {
			assert.NotContains(t, string(entry.JSON), "CONTRACT-SCHEMA-TEXT")
		}

		events := eventsOf(t, repo, first.SessionID, store.EventOutputSchema)
		assert.Eq(t, 2, len(events))
		assert.Eq(t, ContractActionCorrected, events[0].Attrs["action"])
		assert.Eq(t, float64(1), events[0].Attrs["round"])
		assert.Eq(t, false, events[0].Attrs["passed"])
		assert.Eq(t, "failed", events[0].Status)
		assert.Eq(t, ContractActionOK, events[1].Attrs["action"])
		assert.Eq(t, float64(2), events[1].Attrs["round"])
		assert.Eq(t, 12, len(events[1].Attrs["schema_sha256"].(string)))
		assert.Eq(t, float64(len(contractSchema)), events[1].Attrs["schema_bytes"])
	})

	t.Run("both answers fail", func(t *testing.T) {
		prov := contractProvider("{\"status\":1}", "still not json at all")
		repo := openObserveRepo(t)
		runner := compactRunner(t, repo, prov, nil, nil)
		first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
		assert.Require(t, assert.NoErr(t, err))

		_, report, err := runner.RunWithContract(ctx, RunRequest{}, mustContract(t, contractSchema), []byte(contractSchema), first, ContractHooks{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, true, report.Checked)
		assert.Eq(t, false, report.Passed)
		assert.Eq(t, 2, report.Rounds)
		assert.Eq(t, outschema.SourceNone, report.Source)
		assert.NotEmpty(t, report.Errors)
		assert.Eq(t, 2, len(prov.reqs))

		events := eventsOf(t, repo, first.SessionID, store.EventOutputSchema)
		assert.Eq(t, 2, len(events))
		assert.Eq(t, ContractActionFailed, events[1].Attrs["action"])
	})

	t.Run("the event never carries the schema text", func(t *testing.T) {
		prov := contractProvider("{\"status\":1}", "{\"status\":2}")
		repo := openObserveRepo(t)
		runner := compactRunner(t, repo, prov, nil, nil)
		first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
		assert.Require(t, assert.NoErr(t, err))
		_, _, err = runner.RunWithContract(ctx, RunRequest{}, mustContract(t, contractSchema), []byte(contractSchema), first, ContractHooks{})
		assert.Require(t, assert.NoErr(t, err))

		for _, event := range eventsOf(t, repo, first.SessionID, store.EventOutputSchema) {
			raw, err := json.Marshal(event.Attrs)
			assert.Require(t, assert.NoErr(t, err))
			assert.NotContains(t, string(raw), "CONTRACT-SCHEMA-TEXT")
			if errs, ok := event.Attrs["errors"].([]string); ok {
				assert.Lte(t, len(errs), maxTraceContractErrors+1)
			}
		}
		// The trace line shows the check without the schema text.
		line := traceLine(eventsOf(t, repo, first.SessionID, store.EventOutputSchema)[0])
		assert.StrContains(t, line, "output_schema")
		assert.StrContains(t, line, "action=corrected")
		assert.NotContains(t, line, "CONTRACT-SCHEMA-TEXT")
	})

	t.Run("a passing first answer costs no second turn", func(t *testing.T) {
		prov := contractProvider("```json\n{\"status\":\"ok\"}\n```")
		repo := openObserveRepo(t)
		runner := compactRunner(t, repo, prov, nil, nil)
		first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
		assert.Require(t, assert.NoErr(t, err))

		finished := false
		result, report, err := runner.RunWithContract(ctx, RunRequest{}, mustContract(t, contractSchema),
			[]byte(contractSchema), first, ContractHooks{BeforeCorrection: func() { finished = true }})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, false, finished)
		assert.Eq(t, 1, report.Rounds)
		assert.Eq(t, "fenced", report.Source)
		assert.Eq(t, 1, len(prov.reqs))
		assert.Eq(t, result.Output, first.Output)
	})
}

// TestContractGuards: a turn that did not end normally is not judged, and the
// guard is why (design 4.5).
func TestContractGuards(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		result RunResult
		reason string
	}{
		{"wrapped up by a limit", RunResult{State: RunCompleted, Status: agentcore.RunCompleted, Output: "{}", WrapUp: "max_steps"}, ContractReasonWrapUp},
		{"truncated", RunResult{State: RunCompleted, Status: agentcore.RunCompleted, Output: "{}", Truncated: true}, ContractReasonTruncated},
		{"empty final answer", RunResult{State: RunCompleted, Status: agentcore.RunCompleted, Output: "  "}, ContractReasonEmpty},
		{"yielded for input", RunResult{State: RunCompleted, Status: agentcore.RunYielded, Output: "{}"}, ContractReasonYielded},
		{"failed turn", RunResult{State: RunFailed, Status: agentcore.RunCompleted, Output: "{}"}, ContractReasonFailed},
		{"canceled turn", RunResult{State: RunCanceled, Status: agentcore.RunCompleted, Output: "{}"}, ContractReasonCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov := contractProvider("{}")
			repo := openObserveRepo(t)
			runner := compactRunner(t, repo, prov, nil, nil)
			tc.result.SessionID = "sess_guard"
			_, report, err := runner.RunWithContract(ctx, RunRequest{}, mustContract(t, contractSchema),
				[]byte(contractSchema), tc.result, ContractHooks{})
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, false, report.Checked)
			assert.Eq(t, tc.reason, report.Reason)
			assert.Eq(t, 0, len(prov.reqs))
			assert.Eq(t, 0, len(eventsOf(t, repo, tc.result.SessionID, store.EventOutputSchema)))
		})
	}
}

// TestContractCorrectionFailure: a correction round that never ran is reported
// as a run failure, not as a contract failure.
func TestContractCorrectionFailure(t *testing.T) {
	ctx := context.Background()
	prov := &scriptProv{name: "main", normal: func(n int, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		if n == 1 {
			return finalText("{\"status\":1}")
		}
		return nil, errors.New("provider down")
	}}
	repo := openObserveRepo(t)
	runner := compactRunner(t, repo, prov, nil, nil)
	first, err := runner.Run(ctx, RunRequest{Prompt: "one"})
	assert.Require(t, assert.NoErr(t, err))

	_, report, err := runner.RunWithContract(ctx, RunRequest{}, mustContract(t, contractSchema), []byte(contractSchema), first, ContractHooks{})
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "provider down")
	assert.Eq(t, 1, report.Rounds)
	assert.Eq(t, false, report.Passed)
	assert.True(t, strings.Contains(strings.Join(contractErrorLines(report.Errors, 5), "\n"), "status"))
}
