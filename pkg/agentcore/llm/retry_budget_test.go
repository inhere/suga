package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// budgetRetry is the policy of a request that keeps failing: many attempts with
// short waits, so only retry.max_elapsed can end it quickly.
var budgetRetry = RetryPolicy{MaxAttempts: 50, BaseDelay: 20 * time.Millisecond, MaxDelay: 20 * time.Millisecond,
	ResetAfter: time.Minute, MaxElapsed: 200 * time.Millisecond}

// A provider that keeps failing ends the request once retry.max_elapsed is
// spent, instead of retrying its way to minutes (suga-255e).
func TestRetryMaxElapsedBoundsOneRequest(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}

	t.Run("http failures: no attempt after the budget", func(t *testing.T) {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
		}))
		t.Cleanup(server.Close)

		p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Retry: budgetRetry})
		start := time.Now()
		_, err := p.Generate(context.Background(), req)
		elapsed := time.Since(start)
		assert.ErrMsgContains(t, err, "request failed after")
		assert.True(t, elapsed < 2*time.Second, "the request took %s", elapsed)
		assert.True(t, int(hits.Load()) < budgetRetry.MaxAttempts, "%d attempts made", hits.Load())
	})

	t.Run("broken streams: the whole request, not each attempt", func(t *testing.T) {
		var hits atomic.Int32
		server := brokenStreamServer(t, sseLines(`data: {"choices":[{"delta":{"content":"x"}}]}`), "", 100, &hits)
		p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Retry: budgetRetry})
		start := time.Now()
		_, err := p.Stream(context.Background(), req, nil)
		elapsed := time.Since(start)
		assert.ErrMsgContains(t, err, "stream broke")
		assert.True(t, elapsed < 2*time.Second, "the request took %s", elapsed)
		assert.True(t, int(hits.Load()) < budgetRetry.MaxAttempts, "%d attempts made", hits.Load())
	})

	t.Run("off keeps retrying up to max_attempts", func(t *testing.T) {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(server.Close)

		policy := budgetRetry
		policy.MaxElapsed = NoTimeout // off: no wall-clock budget
		policy.MaxAttempts = 3
		p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m", Retry: policy})
		_, err := p.Generate(context.Background(), req)
		assert.ErrMsgContains(t, err, "request failed after 3 attempts")
		assert.Eq(t, int32(3), hits.Load())
	})
}

// A provider that used up its own budget still leaves the fallback its full
// chance: the budget belongs to one request of one provider, not to the turn.
func TestRetryMaxElapsedLeavesTheFallbackItsTurn(t *testing.T) {
	var primaryHits atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(primary.Close)
	var fallbackHits atomic.Int32
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"from the fallback"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(fallback.Close)

	pool := NewPool([]Provider{
		NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: primary.URL, Model: "m", Name: "gw1", Retry: budgetRetry}),
		NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: fallback.URL, Model: "m", Name: "gw2", Retry: fastRetry}),
	})
	res, err := pool.Generate(context.Background(), &agentcoreapi.GenerateRequest{
		Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "from the fallback", res.Content)
	assert.Eq(t, int32(1), fallbackHits.Load())
	assert.True(t, int(primaryHits.Load()) < budgetRetry.MaxAttempts, "%d primary attempts", primaryHits.Load())
}
