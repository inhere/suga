package llm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// failingTransport answers every request with err and counts the calls, so the
// number of attempts a policy makes is observable without a network.
type failingTransport struct {
	err  error
	hits atomic.Int32
}

func (tr *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	tr.hits.Add(1)
	return nil, tr.err
}

// buildTestRequest builds the request postWithRetry sends in these tests.
func buildTestRequest() (*http.Request, error) {
	return http.NewRequest(http.MethodPost, "http://provider.example/v1/chat/completions", strings.NewReader("{}"))
}

// attemptRetry is a fast policy whose waits are short but long enough that a
// fully retried request takes noticeably more wall-clock time than one attempt:
// 5 attempts with 100ms between them.
var attemptRetry = RetryPolicy{MaxAttempts: 5, BaseDelay: 100 * time.Millisecond, MaxDelay: 100 * time.Millisecond, ResetAfter: time.Minute}

// dialRefused is the error of a TCP connect a closed endpoint gives.
func dialRefused() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
}

// TestConnectFailureEndsRetriesWhenFallbackExists: a failure in the connection
// phase (DNS, dial, TLS handshake) means the endpoint is unreachable now, so a
// request that has a fallback model to move on to is not retried; a request
// phase failure (an awaited response header, 5xx) keeps the policy's retries,
// and a request without a fallback keeps the old behavior as well.
//
// Reverse verification: removing the `fallbackAvailable(ctx)` branch in
// postWithRetry makes the first case retry like the others (hits 5, elapsed
// above 400ms), so its assertions fail.
func TestConnectFailureEndsRetriesWhenFallbackExists(t *testing.T) {
	t.Run("connect failure with a fallback: one attempt", func(t *testing.T) {
		tr := &failingTransport{err: dialRefused()}
		client := newProviderClient(time.Second, tr)
		start := time.Now()
		_, err := postWithRetry(withFallbackAvailable(context.Background()), client, newBackoff(attemptRetry), "primary", "/chat/completions", buildTestRequest)
		elapsed := time.Since(start)

		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, ErrorKindTransient, ErrorKind(err))
		assert.True(t, isConnectFailure(err), "the dial failure is classified as a connect failure")
		assert.Eq(t, int32(1), tr.hits.Load(), "the unreachable endpoint is not retried")
		assert.True(t, elapsed < 300*time.Millisecond, "one attempt took %s", elapsed)
	})

	t.Run("connect failure without a fallback keeps the policy", func(t *testing.T) {
		tr := &failingTransport{err: dialRefused()}
		client := newProviderClient(time.Second, tr)
		start := time.Now()
		_, err := postWithRetry(context.Background(), client, newBackoff(attemptRetry), "primary", "/chat/completions", buildTestRequest)
		elapsed := time.Since(start)

		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, int32(attemptRetry.MaxAttempts), tr.hits.Load(), "no fallback: every attempt is made")
		assert.True(t, elapsed >= 300*time.Millisecond, "the retries waited only %s", elapsed)
	})

	t.Run("request failure with a fallback keeps retrying", func(t *testing.T) {
		tr := &failingTransport{err: errors.New("http2: timeout awaiting response headers")}
		client := newProviderClient(time.Second, tr)
		_, err := postWithRetry(withFallbackAvailable(context.Background()), client, newBackoff(attemptRetry), "primary", "/chat/completions", buildTestRequest)

		assert.Require(t, assert.Err(t, err))
		assert.False(t, isConnectFailure(err), "a slow response is a request-phase failure")
		assert.Eq(t, int32(attemptRetry.MaxAttempts), tr.hits.Load(), "request-phase failures keep the retry policy")
	})

	t.Run("5xx with a fallback is unchanged", func(t *testing.T) {
		server, hits := countingServer(t, http.StatusServiceUnavailable)
		client := newProviderClient(time.Second, nil)
		build := func() (*http.Request, error) {
			return http.NewRequest(http.MethodPost, server.URL+"/chat/completions", strings.NewReader("{}"))
		}
		_, err := postWithRetry(withFallbackAvailable(context.Background()), client, newBackoff(attemptRetry), "primary", "/chat/completions", build)

		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, int32(attemptRetry.MaxAttempts), hits.Load(), "a 503 is still retried")
	})
}

// TestProviderConnectTimeouts: both clients of both provider types bound the
// connection phase with DefaultConnectTimeout, independent of the overall
// request timeout (which the streamed client does not have at all).
func TestProviderConnectTimeouts(t *testing.T) {
	assertConnectTimeouts := func(t *testing.T, client *http.Client) {
		t.Helper()
		transport := transportOf(t, client)
		assert.Eq(t, DefaultConnectTimeout, transport.TLSHandshakeTimeout)
		assert.True(t, transport.DialContext != nil, "the dial wait is bounded by DefaultConnectTimeout")
	}
	t.Run("openai", func(t *testing.T) {
		p := NewOpenAIProvider(OpenAIConfig{})
		assertConnectTimeouts(t, p.client)
		assertConnectTimeouts(t, p.streamClient)
		assert.Eq(t, time.Duration(0), p.streamClient.Timeout, "a long answer is not bounded as a whole")
	})
	t.Run("claude", func(t *testing.T) {
		c := NewClaudeProvider(ClaudeConfig{})
		assertConnectTimeouts(t, c.client)
		assertConnectTimeouts(t, c.streamClient)
	})

	t.Run("a refused connect fails at once, not after the request timeout", func(t *testing.T) {
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: closedServerURL(t), Model: "m", Name: "dead",
			RequestTimeout: 30 * time.Second, Retry: RetryPolicy{MaxAttempts: 1}})
		start := time.Now()
		_, err := provider.Generate(context.Background(), &agentcoreapi.GenerateRequest{})
		assert.Require(t, assert.Err(t, err))
		assert.True(t, isConnectFailure(err), "err = %v", err)
		assert.True(t, time.Since(start) < DefaultConnectTimeout, "the connect took %s", time.Since(start))
	})
}

// closedServerURL returns the URL of a server that is already closed: a
// connection to it is refused immediately.
func closedServerURL(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	return url
}

// connectDeadlineRetry is the policy of the pool cases: 6 attempts with 300ms
// between them, i.e. more than 1.5s per candidate - what the old strategy spent
// on a dead endpoint before asking the fallback.
var connectDeadlineRetry = RetryPolicy{MaxAttempts: 6, BaseDelay: 300 * time.Millisecond, MaxDelay: 300 * time.Millisecond, ResetAfter: time.Minute}

// TestPoolConnectFailureSwitchesToFallback: an unreachable primary endpoint
// hands the request to the fallback model after one attempt, in both the
// non-streamed and the streamed path, and the trace's fallback reason says
// connect_failed.
//
// Reverse verification: dropping withFallbackAvailable in Pool.Generate makes
// the primary retry 6 times with 300ms waits, so the elapsed bound fails;
// dropping the connect_failed suffix in fallbackReason fails the reason check.
func TestPoolConnectFailureSwitchesToFallback(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}
	const streamBody = "data: {\"choices\":[{\"delta\":{\"content\":\"backup\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

	t.Run("generate", func(t *testing.T) {
		resetCooldowns()
		t.Cleanup(resetCooldowns)
		backup, backupHits := jsonServer(t, wholeCompletion)
		pool := NewPool([]Provider{
			NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: closedServerURL(t), Model: "m", Name: "command-code", Retry: connectDeadlineRetry}),
			NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: backup.URL, Model: "m", Name: "opencode-go", Retry: connectDeadlineRetry}),
		})
		var attempts []Attempt
		pool.SetObserver(func(_ context.Context, a Attempt) { attempts = append(attempts, a) })

		start := time.Now()
		res, err := pool.Generate(context.Background(), req)
		elapsed := time.Since(start)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "backup", res.Content)
		assert.Eq(t, int32(1), backupHits.Load())
		assert.True(t, elapsed < time.Second, "the fallback was reached after %s", elapsed)
		assert.Require(t, assert.Eq(t, 2, len(attempts)))
		assert.StrContains(t, attempts[0].Fallback, phaseConnectFailed)
		assert.Eq(t, "opencode-go", attempts[1].Provider)
	})

	t.Run("stream", func(t *testing.T) {
		resetCooldowns()
		t.Cleanup(resetCooldowns)
		backup, backupHits := sseServerCount(t, streamBody)
		pool := NewPool([]Provider{
			NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: closedServerURL(t), Model: "m", Name: "command-code", Retry: connectDeadlineRetry}),
			NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: backup.URL, Model: "m", Name: "opencode-go", Retry: connectDeadlineRetry}),
		})

		start := time.Now()
		res, err := pool.Stream(context.Background(), req, nil)
		elapsed := time.Since(start)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "backup", res.Content)
		assert.Eq(t, int32(1), backupHits.Load())
		assert.True(t, elapsed < time.Second, "the fallback was reached after %s", elapsed)
	})
}

// TestConnectTimeoutSwitchesToFallback: a connect that times out (the dial waits
// for its own deadline, as a network dropping the SYN does) is handled like a
// refused one - one attempt, then the fallback - and is not masked by the
// request timeout.
func TestConnectTimeoutSwitchesToFallback(t *testing.T) {
	resetCooldowns()
	t.Cleanup(resetCooldowns)

	const dialWait = 150 * time.Millisecond
	// silentDial stands in for a TCP connect nobody answers: it waits for the
	// dialer's own deadline and reports it as a dial timeout.
	silentDial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		timer := time.NewTimer(dialWait)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil, &net.OpError{Op: "dial", Net: network, Err: errDialTimeout}
		case <-ctx.Done():
			return nil, &net.OpError{Op: "dial", Net: network, Err: ctx.Err()}
		}
	}
	deadTransport := newProviderTransport(0, nil)
	deadTransport.DialContext = silentDial
	primary := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: "http://127.0.0.1:1/v1", Model: "m", Name: "command-code",
		RequestTimeout: 30 * time.Second})
	primary.client = newProviderClient(30*time.Second, deadTransport)
	primary.streamClient = newProviderClient(0, deadTransport)

	backup, _ := jsonServer(t, wholeCompletion)
	pool := NewPool([]Provider{
		primary,
		NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: backup.URL, Model: "m", Name: "opencode-go", Retry: fastRetry}),
	})

	start := time.Now()
	res, err := pool.Generate(context.Background(), &agentcoreapi.GenerateRequest{
		Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}})
	elapsed := time.Since(start)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "backup", res.Content)
	// One dial wait, not the request timeout (30s) and not the connectDeadlineRetry
	// waits: the upper bound leaves room for a slow CI machine.
	assert.True(t, elapsed < dialWait*4, "the fallback was reached after %s", elapsed)
}

// errDialTimeout is the error a dial that outlived its deadline returns.
var errDialTimeout = &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}
