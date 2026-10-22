package sugasdk

// The output contract of one send (design 2026-10-22 2.2 / 7.2 item 3): a
// passing answer reports its value, a failing one reports ErrOutputContract with
// the report on the result, and a bad schema fails before any turn starts.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// schemaAnswer is the contract of the cases below.
const schemaAnswer = `{"type":"object","required":["status"],"properties":{"status":{"type":"string"},"id":{"type":"integer"}}}`

// schemaSDKServer answers every model request with answers(call) as plain text.
func schemaSDKServer(t *testing.T, answers func(call int) string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1))
		w.Header().Set("Content-Type", "application/json")
		quoted, err := json.Marshal(answers(n))
		assert.NoErr(t, err)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(quoted)+`},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

// openSchemaSession opens an engine and one session on a fresh test home.
func openSchemaSession(t *testing.T, baseURL string) *Session {
	t.Helper()
	ctx := context.Background()
	home := sdkTestHome(t, baseURL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })
	session, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir()})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestOutputSchema(t *testing.T) {
	ctx := context.Background()

	t.Run("a passing answer reports the value", func(t *testing.T) {
		server, calls := schemaSDKServer(t, func(int) string {
			return "结论：\n```json\n{\"status\":\"ok\",\"id\":9007199254740993}\n```"
		})
		session := openSchemaSession(t, server.URL)

		result, err := session.Send(ctx, Prompt{Text: "status?", OutputSchema: json.RawMessage(schemaAnswer)}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "COMPLETED", result.State)
		assert.Eq(t, 1, int(*calls))
		assert.Require(t, assert.NotNil(t, result.OutputContract))
		assert.Eq(t, true, result.OutputContract.Checked)
		assert.Eq(t, true, result.OutputContract.Passed)
		assert.Eq(t, 1, result.OutputContract.Rounds)
		assert.Eq(t, "fenced", result.OutputContract.Source)
		// The big integer keeps its literal text: no float64 round trip.
		assert.Eq(t, `{"status":"ok","id":9007199254740993}`, string(result.OutputValue))
	})

	t.Run("a failing answer returns ErrOutputContract after one correction", func(t *testing.T) {
		server, calls := schemaSDKServer(t, func(call int) string {
			if call == 1 {
				return "the status is {\"status\":1}"
			}
			return "still no json, sorry"
		})
		session := openSchemaSession(t, server.URL)

		result, err := session.Send(ctx, Prompt{Text: "status?", OutputSchema: json.RawMessage(schemaAnswer)}, nil)
		assert.Require(t, assert.Err(t, err))
		assert.True(t, errors.Is(err, ErrOutputContract))
		assert.Eq(t, 2, int(*calls))
		// The turn itself completed; only the answer's shape failed.
		assert.Eq(t, "COMPLETED", result.State)
		assert.Require(t, assert.NotNil(t, result.OutputContract))
		assert.Eq(t, true, result.OutputContract.Checked)
		assert.Eq(t, false, result.OutputContract.Passed)
		assert.Eq(t, 2, result.OutputContract.Rounds)
		assert.Eq(t, 0, len(result.OutputValue))
		assert.NotEmpty(t, result.OutputContract.Errors)
	})

	t.Run("an invalid schema fails before the turn", func(t *testing.T) {
		server, calls := schemaSDKServer(t, func(int) string { return "never asked" })
		session := openSchemaSession(t, server.URL)

		_, err := session.Send(ctx, Prompt{Text: "status?", OutputSchema: json.RawMessage(`{"type":`)}, nil)
		assert.Require(t, assert.Err(t, err))
		assert.True(t, errors.Is(err, ErrInvalidOutputSchema))
		assert.Eq(t, 0, int(*calls))
	})

	t.Run("without a schema nothing is checked", func(t *testing.T) {
		server, _ := schemaSDKServer(t, func(int) string { return "plain answer" })
		session := openSchemaSession(t, server.URL)

		result, err := session.Send(ctx, Prompt{Text: "hi"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "plain answer", result.Output)
		assert.Nil(t, result.OutputContract)
		assert.Eq(t, 0, len(result.OutputValue))
	})
}
