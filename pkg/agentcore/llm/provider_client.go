package llm

import (
	"fmt"
	"net/http"
	"time"

	"github.com/inhere/suga/pkg/agentcore/netproxy"
)

// proxyTransport clones the default transport and pins the global proxy
// selector on it, so this client stops reading HTTP_PROXY/HTTPS_PROXY/NO_PROXY.
// A nil selector returns an untyped nil RoundTripper: the caller then keeps the
// default transport, i.e. the environment behavior of an unconfigured
// network.proxy.
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
		transport = http.DefaultTransport
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
