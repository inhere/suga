package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
)

const completionBody = `{"choices":[{"message":{"role":"assistant","content":"hi there"}}],` +
	`"usage":{"prompt_tokens":1,"completion_tokens":1}}`

// forwardProxy answers every request itself and records the absolute URL a
// forward proxy receives, which proves which host the client asked it for.
func forwardProxy(t *testing.T, seen *[]string) *httptest.Server {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Host)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionBody))
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

// TestProviderNetworkProxy covers the network.proxy wiring of the model
// clients: the selector sends allowlisted hosts to the proxy for both the
// non-streamed and the streamed path, leaves other hosts direct, and an
// unconfigured proxy keeps the default transport.
func TestProviderNetworkProxy(t *testing.T) {
	req := &agentcoreapi.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}}

	t.Run("selector sends a matched host to the proxy", func(t *testing.T) {
		var seen []string
		proxy := forwardProxy(t, &seen)
		selector, err := netproxy.New(proxy.URL, []string{"provider.example"})
		assert.Require(t, assert.NoErr(t, err))
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: "http://provider.example/v1", Model: "m", Proxy: selector.ProxyFunc()})
		res, err := provider.Generate(context.Background(), req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "hi there", res.Content)
		assert.Eq(t, []string{"provider.example"}, seen)
	})
	t.Run("selector sends a matched streamed request to the proxy", func(t *testing.T) {
		var seen []string
		proxy := forwardProxy(t, &seen)
		selector, err := netproxy.New(proxy.URL, []string{"provider.example"})
		assert.Require(t, assert.NoErr(t, err))
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: "http://provider.example/v1", Model: "m", Proxy: selector.ProxyFunc()})
		res, err := provider.Stream(context.Background(), req, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "hi there", res.Content)
		assert.Eq(t, []string{"provider.example"}, seen)
	})
	t.Run("claude provider uses the selector too", func(t *testing.T) {
		var seen []string
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r.URL.Host)
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi there"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
		}))
		t.Cleanup(proxy.Close)
		selector, err := netproxy.New(proxy.URL, []string{"provider.example"})
		assert.Require(t, assert.NoErr(t, err))
		provider := NewClaudeProvider(ClaudeConfig{APIKey: "k", BaseURL: "http://provider.example", Model: "m", Proxy: selector.ProxyFunc()})
		res, err := provider.Generate(context.Background(), req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "hi there", res.Content)
		assert.Eq(t, []string{"provider.example"}, seen)
	})
	t.Run("unmatched host connects directly", func(t *testing.T) {
		var seen []string
		proxy := forwardProxy(t, &seen)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(completionBody))
		}))
		t.Cleanup(target.Close)
		selector, err := netproxy.New(proxy.URL, []string{"other.example"})
		assert.Require(t, assert.NoErr(t, err))
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: target.URL, Model: "m", Proxy: selector.ProxyFunc()})
		res, err := provider.Generate(context.Background(), req)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "hi there", res.Content)
		assert.Empty(t, seen)
	})
	t.Run("without a selector the environment proxy behavior stays", func(t *testing.T) {
		provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", BaseURL: "http://provider.example/v1", Model: "m"})
		client, ok := provider.client.Transport.(redirectRejectingTransport)
		assert.True(t, ok)
		// The environment behavior (HTTP_PROXY/HTTPS_PROXY/NO_PROXY) is the
		// default transport's ProxyFromEnvironment, not a second selector. It is a
		// clone now, so the connection-phase timeouts can be pinned on it.
		base, ok := client.base.(*http.Transport)
		assert.Require(t, assert.True(t, ok))
		assert.True(t, base.Proxy != nil)
		assert.Eq(t, DefaultConnectTimeout, base.TLSHandshakeTimeout)
		streamBase, ok := provider.streamClient.Transport.(redirectRejectingTransport)
		assert.True(t, ok)
		transport, ok := streamBase.base.(*http.Transport)
		assert.True(t, ok)
		assert.True(t, transport.Proxy != nil)
	})
}
