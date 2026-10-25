package llm

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// transportOf returns a streamed client's transport: its response header
// timeout lives there, the client itself has no deadline.
func transportOf(t *testing.T, c *http.Client) *http.Transport {
	t.Helper()
	switch tr := c.Transport.(type) {
	case *http.Transport:
		return tr
	case redirectRejectingTransport:
		base, ok := tr.base.(*http.Transport)
		assert.Require(t, assert.True(t, ok))
		return base
	default:
		assert.Require(t, assert.True(t, false))
		return nil
	}
}

// The two knobs reach both clients of both provider types: RequestTimeout bounds
// the non-streamed client as a whole, ResponseHeaderTimeout the streamed
// client's wait for the response headers.
func TestProviderRequestTimeouts(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p := NewOpenAIProvider(OpenAIConfig{})
		assert.Eq(t, DefaultRequestTimeout, p.client.Timeout)
		assert.Eq(t, DefaultResponseHeaderTimeout, transportOf(t, p.streamClient).ResponseHeaderTimeout)

		c := NewClaudeProvider(ClaudeConfig{})
		assert.Eq(t, DefaultRequestTimeout, c.client.Timeout)
		assert.Eq(t, DefaultResponseHeaderTimeout, transportOf(t, c.streamClient).ResponseHeaderTimeout)
	})

	t.Run("configured", func(t *testing.T) {
		p := NewOpenAIProvider(OpenAIConfig{RequestTimeout: 5 * time.Second, ResponseHeaderTimeout: 7 * time.Second})
		assert.Eq(t, 5*time.Second, p.client.Timeout)
		assert.Eq(t, 7*time.Second, transportOf(t, p.streamClient).ResponseHeaderTimeout)

		c := NewClaudeProvider(ClaudeConfig{RequestTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second})
		assert.Eq(t, 3*time.Second, c.client.Timeout)
		assert.Eq(t, 4*time.Second, transportOf(t, c.streamClient).ResponseHeaderTimeout)
	})

	t.Run("NoTimeout disables both", func(t *testing.T) {
		p := NewOpenAIProvider(OpenAIConfig{RequestTimeout: NoTimeout, ResponseHeaderTimeout: NoTimeout})
		assert.Eq(t, time.Duration(0), p.client.Timeout)
		assert.Eq(t, time.Duration(0), transportOf(t, p.streamClient).ResponseHeaderTimeout)
	})
}

// A request that outlives RequestTimeout fails instead of hanging; the error is
// shaped like a transient failure, which is what the retry policy expects.
// Runs inside a synctest bubble on a net.Pipe connection (no sockets): the
// 300ms slow sender and the 50ms client timeout both run on the fake clock, so
// the timeout fires at exactly the configured bound in zero real time.
func TestRequestTimeoutEndsASlowRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := `{"choices":[{"message":{"role":"assistant","content":"late"},"finish_reason":"stop"}]}`
		slow := make(chan struct{})
		transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c1, c2 := net.Pipe()
			// The slow sender: someone who answers only after RequestTimeout. It
			// leaves when the client deadline cancelled the request (slow).
			go func() {
				defer c2.Close()
				select {
				case <-time.After(300 * time.Millisecond):
					_, err := c2.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
						strconv.Itoa(len(body)) + "\r\n\r\n" + body))
					_ = err // a write after the cancellation only fails
				case <-slow:
				}
			}()
			return c1, nil
		}}
		defer func() { close(slow); transport.CloseIdleConnections() }()
		p := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: "http://fixture", Model: "m",
			RequestTimeout: 50 * time.Millisecond, Retry: RetryPolicy{MaxAttempts: 1}})
		p.client = &http.Client{Timeout: 50 * time.Millisecond, Transport: transport}
		_, err := p.Generate(context.Background(), &agentcoreapi.GenerateRequest{
			Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}})
		assert.Err(t, err)
		assert.ErrMsgContains(t, err, "request failed after 1 attempts")
		assert.ErrIs(t, err, context.DeadlineExceeded)
	})
}
