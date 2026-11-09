package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// TestErrorKindOfHTTPStatuses: the HTTP layer's own failures are classified.
// The retryable statuses (408/429/5xx) run out of attempts and are transient -
// the request was fine, another attempt or another model may work - while the
// other 4xx are rejections of the request itself. ShouldFallback tells the two
// groups of rejections apart: only 401/403/404 name this provider (its key, its
// models), so only they are worth another model. The API's own message survives
// the classification (only rejections keep it: a retried status reports the
// last server error instead).
//
// Reverse verification: dropping statusErr from the providers' response paths,
// or giving postWithRetry back its unclassified error, turns every kind below
// into "" and the assertions fail; see pool_fallback_test.go for the pool's
// side of the rule.
func TestErrorKindOfHTTPStatuses(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	for _, tc := range []struct {
		name     string
		status   int
		kind     string
		fallback bool
	}{
		{"400 rejects the request", http.StatusBadRequest, ErrorKindRejected, false},
		{"401 rejects the credentials", http.StatusUnauthorized, ErrorKindRejected, true},
		{"403 rejects the credentials", http.StatusForbidden, ErrorKindRejected, true},
		{"404 rejects the model", http.StatusNotFound, ErrorKindRejected, true},
		{"422 rejects the arguments", http.StatusUnprocessableEntity, ErrorKindRejected, false},
		{"408 ran out of attempts", http.StatusRequestTimeout, ErrorKindTransient, true},
		{"429 ran out of attempts", http.StatusTooManyRequests, ErrorKindTransient, true},
		{"500 ran out of attempts", http.StatusInternalServerError, ErrorKindTransient, true},
		{"503 ran out of attempts", http.StatusServiceUnavailable, ErrorKindTransient, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := sseServer(t, tc.status, `{"error":{"message":"nope"}}`, nil)
			provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Retry: fastRetry})
			_, err := provider.Generate(context.Background(), req)
			assert.Require(t, assert.Err(t, err))
			assert.Eq(t, tc.kind, ErrorKind(err))
			assert.Eq(t, tc.fallback, ShouldFallback(err))
			if tc.kind == ErrorKindRejected {
				assert.Eq(t, "openai api error: nope", err.Error())
				var status *statusError
				assert.Require(t, assert.True(t, errors.As(err, &status)))
				assert.Eq(t, tc.status, status.Status())
				return
			}
			assert.StrContains(t, err.Error(), fmt.Sprintf("server error %d", tc.status))
			assert.StrContains(t, err.Error(), fmt.Sprintf("after %d attempts", fastRetry.MaxAttempts))
		})
	}

	t.Run("a dropped connection is transient", func(t *testing.T) {
		// A closed server: every attempt fails to connect.
		dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		dead.Close()
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: dead.URL, Model: "m", Retry: fastRetry})
		_, err := provider.Generate(context.Background(), req)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
		assert.Eq(t, true, ShouldFallback(err))
	})

	t.Run("a streamed rejection keeps its status", func(t *testing.T) {
		server := sseServer(t, http.StatusNotFound, `{"error":{"message":"unknown model"}}`, nil)
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Retry: fastRetry})
		_, err := provider.Stream(context.Background(), req, nil)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, "openai api error: unknown model", err.Error())
		assert.Eq(t, ErrorKindRejected, ErrorKind(err))
		assert.Eq(t, true, ShouldFallback(err))
	})

	t.Run("a 200 with a permanent api error body is a bad response", func(t *testing.T) {
		// A gateway answering 200 with a permanent error object: retrying or
		// another model of the same request would only fail the same way.
		server := sseServer(t, http.StatusOK, `{"error":{"type":"invalid_request_error","message":"bad request"}}`, nil)
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Retry: fastRetry})
		_, err := provider.Generate(context.Background(), req)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, ErrorKindBadResponse, ErrorKind(err))
		assert.Eq(t, false, ShouldFallback(err))
	})
	t.Run("a 200 with an unknown api error body is transient", func(t *testing.T) {
		// suga-ct2r: an error nothing marks as permanent is treated as
		// temporary; once the retries run out it is provider_transient.
		server := sseServer(t, http.StatusOK, `{"error":{"message":"rate limited"}}`, nil)
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Retry: fastRetry})
		_, err := provider.Generate(context.Background(), req)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
	})
}
