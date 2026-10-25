package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// fixedBackoff is a backoff on a fake clock without jitter.
func fixedBackoff(p RetryPolicy, clock *time.Time) *backoff {
	b := newBackoff(p)
	b.now = func() time.Time { return *clock }
	b.jitter = func() float64 { return 0 }
	return b
}

func TestBackoff(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 4, BaseDelay: time.Second, MaxDelay: 10 * time.Second, ResetAfter: time.Minute}
	t.Run("doubles up to the cap", func(t *testing.T) {
		clock := time.Unix(0, 0)
		b := fixedBackoff(policy, &clock)
		var got []time.Duration
		for range 6 {
			got = append(got, b.next(0))
		}
		assert.Eq(t, []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}, got)
		assert.Eq(t, 4, b.attempts())
	})
	t.Run("falls back after a quiet ResetAfter", func(t *testing.T) {
		clock := time.Unix(0, 0)
		b := fixedBackoff(policy, &clock)
		b.next(0)
		b.next(0)
		clock = clock.Add(30 * time.Second) // still within ResetAfter: keeps growing
		assert.Eq(t, 4*time.Second, b.next(0))
		clock = clock.Add(2 * time.Minute)
		assert.Eq(t, time.Second, b.next(0))
	})
	t.Run("Retry-After raises the wait, capped", func(t *testing.T) {
		clock := time.Unix(0, 0)
		b := fixedBackoff(policy, &clock)
		assert.Eq(t, 5*time.Second, b.next(5*time.Second))
		assert.Eq(t, 10*time.Second, b.next(time.Hour))
	})
	t.Run("jitter stays within 20%", func(t *testing.T) {
		b := newBackoff(policy)
		for range 50 {
			b.level = 0
			d := b.next(0)
			assert.True(t, d >= 800*time.Millisecond && d <= 1200*time.Millisecond, d)
		}
	})
	t.Run("zero fields take the defaults", func(t *testing.T) {
		p := RetryPolicy{BaseDelay: 2 * time.Minute}.withDefaults()
		assert.Eq(t, DefaultRetryPolicy.MaxAttempts, p.MaxAttempts)
		assert.Eq(t, 2*time.Minute, p.MaxDelay) // never below BaseDelay
	})
}

func TestRetryAfterOf(t *testing.T) {
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	h := http.Header{}
	assert.Eq(t, time.Duration(0), retryAfterOf(h, now))
	h.Set("Retry-After", "7")
	assert.Eq(t, 7*time.Second, retryAfterOf(h, now))
	h.Set("Retry-After", now.Add(90*time.Second).Format(http.TimeFormat))
	assert.Eq(t, 90*time.Second, retryAfterOf(h, now))
	h.Set("Retry-After", "soon")
	assert.Eq(t, time.Duration(0), retryAfterOf(h, now))
}

// flakyServer answers the first len(statuses) requests with those statuses
// (with a Retry-After header when retryAfter is set), later ones with ok.
func flakyServer(t *testing.T, statuses []int, retryAfter, ok string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		n := int(hits.Add(1))
		if n <= len(statuses) {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(statuses[n-1])
			_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, ok)
	}))
	t.Cleanup(server.Close)
	return server
}

// statusRoundTripper answers each request with the next status (the last one
// repeats) and a JSON error body; used with a synctest bubble where the retry
// waits run on the fake clock.
type statusRoundTripper struct {
	statuses []int
	hits     atomic.Int32
}

func (rt *statusRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	n := int(rt.hits.Add(1))
	i := n - 1
	if i >= len(rt.statuses) {
		i = len(rt.statuses) - 1
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: rt.statuses[i], Header: header,
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`)), Request: req}, nil
}

func TestProviderRetries(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	okOpenAI := `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
	openai := func(url string) *OpenAIProvider {
		return NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: url, Model: "m", Retry: fastRetry})
	}

	t.Run("503 and 429 then 200", func(t *testing.T) {
		var hits atomic.Int32
		server := flakyServer(t, []int{503, 429}, "", okOpenAI, &hits)
		ctx, report := withRetryReport(context.Background())
		res, err := openai(server.URL).Generate(ctx, req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "done", res.Content)
		assert.Eq(t, int32(3), hits.Load())
		assert.Eq(t, 2, report.Retries)
		assert.Eq(t, "HTTP 429", report.Last)
	})
	t.Run("gives up after MaxAttempts", func(t *testing.T) {
		var hits atomic.Int32
		server := flakyServer(t, []int{502, 502, 502, 502}, "", okOpenAI, &hits)
		_, err := openai(server.URL).Generate(context.Background(), req)
		assert.ErrMsgContains(t, err, "request failed after 3 attempts")
		assert.ErrMsgContains(t, err, "server error 502")
		assert.Eq(t, int32(3), hits.Load())
	})
	t.Run("a 400 is not retried", func(t *testing.T) {
		var hits atomic.Int32
		server := flakyServer(t, []int{400}, "", okOpenAI, &hits)
		_, err := openai(server.URL).Generate(context.Background(), req)
		assert.Err(t, err)
		assert.Eq(t, int32(1), hits.Load())
	})
	t.Run("Retry-After is honoured up to MaxDelay", func(t *testing.T) {
		var hits atomic.Int32
		server := flakyServer(t, []int{429}, "1", okOpenAI, &hits)
		p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: server.URL, Model: "m",
			Retry: RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 50 * time.Millisecond}})
		ctx, report := withRetryReport(context.Background())
		start := time.Now()
		_, err := p.Generate(ctx, req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 50*time.Millisecond, report.Wait) // 1s asked, capped
		assert.True(t, time.Since(start) < time.Second)
	})
	t.Run("cancel during the wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// The two 503s are served from memory (no sockets), so the whole
			// round trip runs on the bubble's fake clock: the 1-minute retry
			// wait and the 50ms request deadline resolve in zero real time,
			// and the earlier deadline is the one that fires.
			rt := &statusRoundTripper{statuses: []int{503, 503}}
			p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: "http://fixture", Model: "m",
				Retry: RetryPolicy{BaseDelay: time.Minute}})
			p.client = &http.Client{Transport: rt}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err := p.Generate(ctx, req)
			assert.ErrIs(t, err, context.DeadlineExceeded)
			assert.Eq(t, int32(1), rt.hits.Load())
		})
	})
	t.Run("claude stream: 529 announced, then streams", func(t *testing.T) {
		var hits atomic.Int32
		whole := strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":1}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_stop"}`,
		}, "\n") + "\n"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			if hits.Add(1) == 1 {
				w.WriteHeader(529) // Anthropic "overloaded"
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, whole)
		}))
		t.Cleanup(server.Close)
		var events []agentcoreapi.StreamEvent
		p := NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: server.URL, Retry: fastRetry})
		res, err := p.Stream(context.Background(), req, collect(&events))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "hi", res.Content)
		assert.Eq(t, agentcoreapi.StreamEventRetry, events[0].Kind)
		assert.Eq(t, "HTTP 529", events[0].Delta)
		assert.Eq(t, false, events[0].Metadata["restart"])
	})
}
