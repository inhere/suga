package personal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/llm"
)

// sseTestServer answers every request with the given event-stream body.
func sseTestServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// statusTestServer answers every request with status and an API error body, so
// a test can drive a real provider into a rejection or a transient failure.
func statusTestServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"status %d"}}`, status)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestTurnEventRecordsErrorKind: a turn that fails on a provider error carries
// the kind (llm.ErrorKind) both in the result and as attr error_kind on its turn
// event, which is what `suga inspect trace --json` and the analysis read; a cut stream
// (transient) and an unusable answer (bad response) are told apart.
func TestTurnEventRecordsErrorKind(t *testing.T) {
	// Waits of milliseconds: a cut stream is retried (llm.retry), an unusable
	// answer is not.
	retry := llm.RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
	cases := []struct {
		name string
		body string
		want string
	}{
		{"a cut stream", `data: {"choices":[{"delta":{"content":"Hel`, llm.ErrorKindTransient},
		{"an unusable answer", "data: {not json}\n", llm.ErrorKindBadResponse},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := openObserveRepo(t)
			server := sseTestServer(t, c.body)
			provider := llm.NewOpenAIProvider(llm.OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Retry: retry})
			runner, err := New(Config{Repository: repo, Provider: provider})
			assert.NoErr(t, err)

			res, runErr := runner.Run(context.Background(), RunRequest{Prompt: "hi"})
			assert.Require(t, assert.True(t, runErr != nil))
			assert.Eq(t, c.want, res.ErrorKind)

			events := eventsOf(t, repo, res.SessionID, store.EventTurn)
			assert.Require(t, assert.Eq(t, 1, len(events)))
			assert.Eq(t, "failed", events[0].Status)
			assert.Eq(t, c.want, events[0].Attrs["error_kind"])
		})
	}
}
