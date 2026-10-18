package native

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
)

// fetchTestDNS is the fake resolver of the network-proxy tests: one public
// host, one redirecting public host and one host that resolves to a private IP.
var fetchTestDNS = map[string]string{
	"public.example":   "93.184.216.34",
	"redirect.example": "93.184.216.35",
	"internal.example": "10.0.0.5",
}

func fetchTestLookup(_ context.Context, host string) ([]netip.Addr, error) {
	if ip, ok := fetchTestDNS[host]; ok {
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}
	return nil, errors.New("no such host")
}

// TestFetchWithNetworkProxy covers web_fetch under the global proxy selector:
// allowlisted hosts are checked by name and sent to the proxy, other hosts stay
// direct, and the private-address rules survive both paths.
func TestFetchWithNetworkProxy(t *testing.T) {
	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.URL.Host)
		if r.URL.Host == "redirect.example" {
			http.Redirect(w, r, "http://internal.example/admin", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("via proxy: " + r.URL.String()))
	}))
	t.Cleanup(proxy.Close)
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct body"))
	}))
	t.Cleanup(direct.Close)

	selector, err := netproxy.New(proxy.URL, []string{"public.example", "redirect.example", "internal.example", "10.1.2.3"})
	assert.Require(t, assert.NoErr(t, err))
	tool := newWebFetchTool(fetchOptions{allow: publicIP, selector: selector, lookup: fetchTestLookup})
	// allowedTool reaches what the public check refuses, for the cases that need
	// a real local target (the httptest servers) and for allow_private=true.
	allowedTool := newWebFetchTool(fetchOptions{allow: func(netip.Addr) bool { return true }, selector: selector, lookup: fetchTestLookup})

	t.Run("allowlisted public host goes through the proxy", func(t *testing.T) {
		assert.StrContains(t, execTool(t, tool, map[string]any{"url": "http://public.example/doc"}), "via proxy: http://public.example/doc")
	})
	t.Run("host outside the allowlist goes direct", func(t *testing.T) {
		before := len(proxied)
		content := execTool(t, allowedTool, map[string]any{"url": direct.URL + "/doc"})
		assert.StrContains(t, content, "direct body")
		assert.NotContains(t, content, "via proxy")
		assert.Eq(t, before, len(proxied))
	})
	t.Run("private literal is refused even when it is allowlisted", func(t *testing.T) {
		before := len(proxied)
		_, err := tool.Execute(t.Context(), callWith(map[string]any{"url": "http://10.1.2.3/"}))
		assert.ErrMsgContains(t, err, "refused")
		assert.Eq(t, before, len(proxied))
	})
	t.Run("host resolving to a private address is refused before the proxy", func(t *testing.T) {
		before := len(proxied)
		_, err := tool.Execute(t.Context(), callWith(map[string]any{"url": "http://internal.example/"}))
		assert.ErrMsgContains(t, err, "not a public address")
		assert.Eq(t, before, len(proxied))
	})
	t.Run("unresolvable host is refused", func(t *testing.T) {
		_, err := tool.Execute(t.Context(), callWith(map[string]any{"url": "http://unknown.example/"}))
		assert.ErrMsgContains(t, err, "refused")
	})
	t.Run("redirect to a private host is refused", func(t *testing.T) {
		_, err := tool.Execute(t.Context(), callWith(map[string]any{"url": "http://redirect.example/"}))
		assert.ErrMsgContains(t, err, "not a public address")
		assert.NotContains(t, strings.Join(proxied, " "), "internal.example")
	})
	t.Run("allow_private keeps working with a selector", func(t *testing.T) {
		assert.StrContains(t, execTool(t, allowedTool, map[string]any{"url": "http://10.1.2.3/private"}), "via proxy: http://10.1.2.3/private")
	})
}

// TestSearchWithNetworkProxy covers web_search selecting the proxy per URL.
func TestSearchWithNetworkProxy(t *testing.T) {
	const results = `{"organic_results":[{"title":"Go","link":"https://go.dev","snippet":"s"}]}`
	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.URL.Host)
		_, _ = w.Write([]byte(results))
	}))
	t.Cleanup(proxy.Close)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(results))
	}))
	t.Cleanup(target.Close)
	targetHost := strings.TrimPrefix(target.URL, "http://")

	t.Run("allowlisted endpoint goes through the proxy", func(t *testing.T) {
		selector, err := netproxy.New(proxy.URL, []string{targetHost})
		assert.Require(t, assert.NoErr(t, err))
		tool := NewWebSearchTool("k")
		tool.endpoint = target.URL
		tool.SetNetworkProxy(selector)
		res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: "web_search", Arguments: map[string]any{"query": "go"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "1. Go")
		assert.Eq(t, []string{targetHost}, proxied)
	})
	t.Run("endpoint outside the allowlist stays direct", func(t *testing.T) {
		selector, err := netproxy.New(proxy.URL, []string{"serpapi.com"})
		assert.Require(t, assert.NoErr(t, err))
		tool := NewWebSearchTool("k")
		tool.endpoint = target.URL
		tool.SetNetworkProxy(selector)
		res, err := tool.Execute(context.Background(), api.ToolCall{CallID: "c", Name: "web_search", Arguments: map[string]any{"query": "go"}})
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Content, "1. Go")
		assert.Eq(t, []string{targetHost}, proxied) // unchanged by the direct call
	})
	t.Run("nil selector keeps the default client", func(t *testing.T) {
		tool := NewWebSearchTool("k")
		transport, _ := tool.client.Transport.(*http.Transport)
		assert.True(t, transport == nil) // DefaultTransport, so the environment applies
		tool.SetNetworkProxy(nil)
		transport, _ = tool.client.Transport.(*http.Transport)
		assert.True(t, transport == nil)
	})
}

// TestParseFetchProxyDoesNotEchoTheValue: an invalid legacy proxy setting is
// reported without its value, which may hold user:password.
func TestParseFetchProxyDoesNotEchoTheValue(t *testing.T) {
	_, err := ParseFetchProxy("ftp://user:s3cret@proxy.example:21")
	assert.Require(t, assert.Err(t, err))
	assert.NotContains(t, err.Error(), "s3cret")
}
