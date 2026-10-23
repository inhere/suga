package llm

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/inhere/suga/pkg/agentcore/netproxy"
)

// newProviderTransport clones the default transport for a provider client and
// pins the connection-phase timeouts on it (DefaultConnectTimeout: dialing and
// the TLS handshake), so a dead endpoint is noticed within that bound instead
// of the default dialer's 30s. responseHeader bounds the wait for a streamed
// request's response headers (0 = no bound). A non-nil proxy replaces the
// environment proxies; a nil one keeps http.ProxyFromEnvironment, the behavior
// of an unconfigured network.proxy.
func newProviderTransport(responseHeader time.Duration, proxy netproxy.ProxyFunc) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: DefaultConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = DefaultConnectTimeout
	transport.ResponseHeaderTimeout = responseHeader
	if proxy != nil {
		transport.Proxy = proxy
	}
	return transport
}

// proxyTransport clones the default transport and pins the global proxy
// selector on it, so this client stops reading HTTP_PROXY/HTTPS_PROXY/NO_PROXY.
// A nil selector returns an untyped nil RoundTripper: the caller then keeps the
// default behavior, i.e. the environment proxies of an unconfigured
// network.proxy (newProviderClient pins the connect timeouts on it).
func proxyTransport(proxy netproxy.ProxyFunc) http.RoundTripper {
	if proxy == nil {
		return nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxy
	return transport
}

// newProviderClient creates every provider HTTP client with the same redirect
// policy. Provider requests carry credentials in headers that Go would otherwise
// preserve on a cross-host redirect (and 307/308 can resend the request body).
func newProviderClient(timeout time.Duration, transport http.RoundTripper) *http.Client {
	if transport == nil {
		// No selector: the default transport's environment proxy behavior, with
		// the connection-phase timeouts pinned on a clone of it.
		transport = newProviderTransport(0, nil)
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     redirectRejectingTransport{base: transport},
		CheckRedirect: rejectProviderRedirect,
	}
}

// redirectRejectingTransport sees the response status and Location before
// net/http invokes CheckRedirect, whose callback intentionally lacks the
// previous response. This keeps the diagnostic status code accurate.
type redirectRejectingTransport struct{ base http.RoundTripper }

func (t redirectRejectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.StatusCode < 300 || resp.StatusCode > 399 {
		return resp, err
	}
	if resp.Body != nil {
		resp.Body.Close()
	}
	target := ""
	if location := resp.Header.Get("Location"); location != "" {
		if url, parseErr := req.URL.Parse(location); parseErr == nil {
			target = url.Host
		}
	}
	return nil, statusErr(resp.StatusCode, fmt.Errorf(
		"provider 返回重定向 %d → %s，已拒绝跟随（防止 API 密钥被带到其他主机）",
		resp.StatusCode, target,
	))
}

func rejectProviderRedirect(req *http.Request, via []*http.Request) error {
	status := 0
	if len(via) > 0 && via[len(via)-1].Response != nil {
		status = via[len(via)-1].Response.StatusCode
	}
	return statusErr(status, fmt.Errorf(
		"provider 返回重定向 %d → %s，已拒绝跟随（防止 API 密钥被带到其他主机）",
		status, req.URL.Host,
	))
}
