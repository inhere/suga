package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

// schemaServer answers every turn with answer(index) through the real OpenAI
// provider (the same fake server nudge_test.go uses).
func schemaServer(t *testing.T, answer func(index int) string) (*int, string) {
	t.Helper()
	server, calls := nudgeServer(t, answer)
	return calls, server.URL
}

// schemaEvents returns the session's output_schema trace events.
func schemaEvents(t *testing.T, sessionID string) []store.Event {
	t.Helper()
	repo, err := openCoreStore()
	assert.Require(t, assert.NoErr(t, err))
	defer repo.Close()
	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: sessionID, Kinds: []string{store.EventOutputSchema}})
	assert.Require(t, assert.NoErr(t, err))
	return events
}

// resultSchemaField is the result line's output_schema object.
func resultSchemaField(t *testing.T, out []byte) map[string]any {
	t.Helper()
	line := lastStreamLine(t, out)
	assert.Eq(t, "result", line["type"])
	field, _ := line["output_schema"].(map[string]any)
	return field
}

// TestRunOutputSchemaEndToEnd drives the real `suga run` pipeline (wiring,
// session, provider, nudge, contract) against a fake provider.
func TestRunOutputSchemaEndToEnd(t *testing.T) {
	const schema = `{"type":"object","required":["status"],"properties":{"status":{"type":"string"}}}`
	t.Run("a fenced answer passes without a correction", func(t *testing.T) {
		verifyRunDir(t)
		calls, url := schemaServer(t, func(int) string { return "结论如下：\n```json\n{\"status\":\"ok\"}\n```" })
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--output-format", "stream-json", "--session-id", "schema-pass",
			"--output-schema", schema,
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, *calls)

		field := resultSchemaField(t, out.Bytes())
		assert.Eq(t, true, field["checked"])
		assert.Eq(t, true, field["passed"])
		assert.Eq(t, float64(1), field["rounds"])
		assert.Eq(t, "fenced", field["source"])
		assert.Eq(t, `{"status":"ok"}`, mustJSON(t, field["value"]))

		events := schemaEvents(t, "schema-pass")
		assert.Require(t, assert.Len(t, events, 1))
		assert.Eq(t, personal.ContractActionOK, events[0].Attrs["action"])
	})

	t.Run("a wrong answer is corrected once and then passes", func(t *testing.T) {
		verifyRunDir(t)
		calls, url := schemaServer(t, func(index int) string {
			if index == 1 {
				return "I think the status is fine: {\"status\":1}"
			}
			return `{"status":"ok"}`
		})
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--output-format", "stream-json", "--session-id", "schema-fix",
			"--output-schema", schema,
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, *calls)

		field := resultSchemaField(t, out.Bytes())
		assert.Eq(t, true, field["passed"])
		assert.Eq(t, float64(2), field["rounds"])
		// The result line still carries the answer text.
		assert.Eq(t, `{"status":"ok"}`, lastStreamLine(t, out.Bytes())["result"])

		events := schemaEvents(t, "schema-fix")
		assert.Require(t, assert.Len(t, events, 2))
		assert.Eq(t, personal.ContractActionCorrected, events[0].Attrs["action"])
		assert.Eq(t, personal.ContractActionOK, events[1].Attrs["action"])
	})

	t.Run("two failing answers end with exit code 6", func(t *testing.T) {
		verifyRunDir(t)
		calls, url := schemaServer(t, func(int) string { return "no json here at all" })
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--output-format", "stream-json", "--session-id", "schema-fail",
			"--output-schema", schema,
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.Err(t, err))
		assert.True(t, errors.Is(err, ErrOutputContract))
		assert.Eq(t, 6, ExitCode(err))
		assert.Eq(t, 2, *calls)

		field := resultSchemaField(t, out.Bytes())
		assert.Eq(t, true, field["checked"])
		assert.Eq(t, false, field["passed"])
		assert.Eq(t, float64(2), field["rounds"])
		assert.Eq(t, "no_json", field["source"])
		assert.NotEmpty(t, field["errors"])
	})

	t.Run("text mode prints the answer and ends with exit code 6", func(t *testing.T) {
		verifyRunDir(t)
		_, url := schemaServer(t, func(int) string { return "the answer is not JSON" })
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--session-id", "schema-text", "--output-schema", schema,
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 6, ExitCode(err))
		// stdout still carries the last answer; stderr names the failure once.
		assert.StrContains(t, out.String(), "the answer is not JSON")
		assert.StrContains(t, diag.String(), "[output schema]")
		assert.StrContains(t, diag.String(), "已纠正 1 次")
	})

	t.Run("an invalid schema is a usage error before any session exists", func(t *testing.T) {
		verifyRunDir(t)
		calls, url := schemaServer(t, func(int) string { return `{"status":"ok"}` })
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--session-id", "schema-bad", "--output-schema", `{"type":`,
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), "output-schema")
		assert.Eq(t, 0, *calls)
	})

	t.Run("schema from stdin", func(t *testing.T) {
		verifyRunDir(t)
		calls, url := schemaServer(t, func(int) string { return `{"status":"ok"}` })
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--output-format", "stream-json", "--session-id", "schema-stdin",
			"--output-schema", "-",
		}, strings.NewReader(schema), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, *calls)
		assert.Eq(t, true, resultSchemaField(t, out.Bytes())["passed"])
	})

	t.Run("schema from a file", func(t *testing.T) {
		verifyRunDir(t)
		calls, url := schemaServer(t, func(int) string { return `{"status":"ok"}` })
		setTestProvider(t, url)
		path := filepath.Join(t.TempDir(), "review.json")
		assert.Require(t, assert.NoErr(t, os.WriteFile(path, []byte(schema), 0o600)))

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--output-format", "stream-json", "--session-id", "schema-file",
			"--output-schema", path,
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, *calls)
		assert.Eq(t, true, resultSchemaField(t, out.Bytes())["passed"])
	})

	t.Run("an unknown draft is a usage error", func(t *testing.T) {
		verifyRunDir(t)
		calls, url := schemaServer(t, func(int) string { return `{}` })
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--session-id", "schema-draft",
			"--output-schema", `{"$schema":"https://json-schema.org/draft/2030-01/schema","type":"object"}`,
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), "不受支持")
		assert.Eq(t, 0, *calls)
	})

	t.Run("chat refuses the flag", func(t *testing.T) {
		verifyRunDir(t)
		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "chat", []string{"--output-schema", schema}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "only supported in run mode")
	})

	t.Run("without a schema the result line is as it was", func(t *testing.T) {
		verifyRunDir(t)
		_, url := schemaServer(t, func(int) string { return `{"status":"ok"}` })
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--output-format", "stream-json", "--session-id", "schema-none",
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		line := lastStreamLine(t, out.Bytes())
		_, has := line["output_schema"]
		assert.Eq(t, false, has)
		assert.Eq(t, `{"status":"ok"}`, line["result"])
	})

	t.Run("verify and the contract both run, verify first", func(t *testing.T) {
		verifyRunDir(t)
		calls, url := schemaServer(t, func(index int) string {
			if index == 1 {
				return "not json"
			}
			return `{"status":"ok"}`
		})
		setTestProvider(t, url)

		var out, diag bytes.Buffer
		err := RunPersonalCLI(context.Background(), "run", []string{
			"--prompt", "summarise", "--output-format", "stream-json", "--session-id", "schema-verify",
			"--output-schema", schema, "--verify", "exit 0", "--verify-rounds", "1",
		}, strings.NewReader(""), &out, &diag)
		assert.Require(t, assert.NoErr(t, err))
		// First turn, contract correction; the verify command passes on the first run.
		assert.Eq(t, 2, *calls)
		var order []string
		for _, line := range streamLines(t, out.Bytes()) {
			switch line["type"] {
			case "verify":
				order = append(order, "verify")
			case "result":
				order = append(order, "result")
			}
		}
		assert.Eq(t, []string{"verify", "result"}, order)
		line := lastStreamLine(t, out.Bytes())
		assert.Eq(t, true, line["output_schema"].(map[string]any)["passed"])
		assert.Eq(t, true, line["verify"].(map[string]any)["passed"])
	})
}

// mustJSON renders a decoded stream-json value back to compact JSON.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	assert.Require(t, assert.NoErr(t, err))
	return string(raw)
}
