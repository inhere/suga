package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/inhere/suga/pkg/agentcore/netproxy"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"golang.org/x/net/html/charset"

	"github.com/inhere/suga/pkg/agentcore/api"
)

const (
	fetchTimeout         = 20 * time.Second
	fetchMaxDownload     = 5 << 20
	fetchMaxRedirects    = 5
	defaultFetchMaxBytes = 20000
	// maxFetchURLs is how many URLs one urls call may fetch (one goroutine each).
	maxFetchURLs = 5
	// defaultFetchUserAgent is the User-Agent web_fetch sends when
	// tools.web_fetch.user_agent is empty.
	defaultFetchUserAgent = "suga-web-fetch/1.0"
)

// errBlockedAddress marks a connection refused because of its target IP.
var errBlockedAddress = errors.New("blocked address")

// cgnat is 100.64.0.0/10 (carrier-grade NAT), not covered by IsPrivate.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// publicIP reports whether ip may be fetched: not loopback, private,
// link-local (cloud metadata lives there), unspecified, multicast or CGNAT.
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsInterfaceLocalMulticast() && !ip.IsMulticast() &&
		!ip.IsUnspecified() && !cgnat.Contains(ip)
}

// WebFetchTool downloads a web page and returns its readable text.
type WebFetchTool struct {
	client *http.Client
	maxOut int // raw result cap in bytes; 0 = DefaultRawOutputBytes
	// userAgent is the User-Agent every request sends
	// (tools.web_fetch.user_agent); empty keeps defaultFetchUserAgent.
	userAgent string
	// checkTarget vets a URL before it is requested (and on every redirect) when
	// a proxy is used; nil without a proxy, where the dial check applies. With a
	// selector in play it is set for direct requests too, so the dial check may
	// skip the proxy's own address safely.
	checkTarget func(ctx context.Context, u *url.URL) error
}

// fetchOptions configure newWebFetchTool.
type fetchOptions struct {
	allow func(netip.Addr) bool
	// proxy, when set, receives every request (tools.web_fetch.proxy).
	proxy *url.URL
	// selector, when set, decides per target URL whether the global proxy
	// (network.proxy) receives the request; it takes precedence over proxy.
	selector *netproxy.Selector
	// lookup resolves a host name; tests replace it.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
}

// NewWebFetchTool builds the tool without a global proxy. Unless allowPrivate
// is set, private and local addresses are refused. Without a proxy every
// connection is checked at dial time against the IP actually connected to,
// which also covers redirects and DNS rebinding; environment proxies are
// ignored so the check sees the real target. With proxy (http, https or socks5
// URL) connections go only to the proxy, and the target host is resolved and
// checked before the request and on every redirect instead (the proxy may
// still resolve a name differently: a weaker guarantee, accepted to reach
// sites that need one).
func NewWebFetchTool(allowPrivate bool, proxy string) (*WebFetchTool, error) {
	return NewWebFetchToolWithSelector(allowPrivate, proxy, nil)
}

// NewWebFetchToolWithSelector builds the tool with the global network proxy
// selector (network.proxy). A non-nil selector decides per target URL between
// the proxy and a direct connection and takes precedence over the legacy proxy
// argument; a nil one keeps the legacy tools.web_fetch.proxy behavior
// described in NewWebFetchTool. Because the dial check cannot tell the proxy's
// own address from a target, a selector always checks every request by name
// (checkHost) and keeps the dial check for direct connections.
func NewWebFetchToolWithSelector(allowPrivate bool, proxy string, selector *netproxy.Selector) (*WebFetchTool, error) {
	opts := fetchOptions{allow: publicIP, selector: selector, lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}}
	if allowPrivate {
		opts.allow = func(netip.Addr) bool { return true }
	}
	if selector == nil {
		u, err := ParseFetchProxy(proxy)
		if err != nil {
			return nil, err
		}
		opts.proxy = u
	}
	return newWebFetchTool(opts), nil
}

// ParseFetchProxy validates tools.web_fetch.proxy; empty means no proxy.
func ParseFetchProxy(proxy string) (*url.URL, error) {
	if proxy = strings.TrimSpace(proxy); proxy == "" {
		return nil, nil
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
		// The value is not echoed: a proxy URL may carry user:password.
		return nil, errors.New("tools.web_fetch.proxy must be an http://, https:// or socks5:// URL")
	}
	return u, nil
}

func newWebFetchTool(opts fetchOptions) *WebFetchTool {
	allow := opts.allow
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	t := &WebFetchTool{}
	var proxyFunc func(*http.Request) (*url.URL, error)
	// checkHost is the name-based target check used whenever a proxy is in play.
	// An unproxied request is covered by the dial check instead (see
	// NewWebFetchToolWithSelector on why both run with a selector).
	checkTarget := func(ctx context.Context, u *url.URL) error {
		return checkHost(ctx, u.Hostname(), allow, opts.lookup)
	}
	switch {
	case opts.selector != nil:
		// Per-URL decision: dials to the proxy itself are not target dials, so
		// the dial check skips them; a direct dial is still checked, and every
		// request is checked by name before it is sent.
		dialer.Control = dialControl(allow, opts.lookup, opts.selector.URL())
		proxyFunc = opts.selector.ProxyFunc()
		t.checkTarget = checkTarget
	case opts.proxy != nil:
		// Legacy: every connection goes to the proxy, so no dial check applies.
		proxyFunc = http.ProxyURL(opts.proxy)
		t.checkTarget = checkTarget
	default:
		dialer.Control = dialControl(allow, opts.lookup, nil)
	}
	transport := &http.Transport{
		Proxy:                 proxyFunc,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: fetchTimeout,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	t.client = &http.Client{
		Transport: transport,
		Timeout:   fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= fetchMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", fetchMaxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
			}
			if t.checkTarget != nil {
				return t.checkTarget(req.Context(), req.URL)
			}
			return nil
		},
	}
	return t
}

// dialControl refuses a connection whose remote IP is not allowed, the check
// that makes a direct fetch safe against DNS rebinding. own, when non-nil, is
// the set of "host:port" addresses of a configured proxy: a dial to it is not a
// target dial, and every request that can be proxied is checked by name before
// it is sent anyway (see newWebFetchTool).
func dialControl(allow func(netip.Addr) bool, lookup func(context.Context, string) ([]netip.Addr, error),
	proxy *url.URL) func(_, address string, _ syscall.RawConn) error {
	own := proxyDialAddrs(proxy, lookup)
	return func(_, address string, _ syscall.RawConn) error {
		if own[address] {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !allow(ip) {
			return fmt.Errorf("%w: %s is not a public address", errBlockedAddress, host)
		}
		return nil
	}
}

// proxyDialAddrs collects the "host:port" the proxy may be dialed as: the
// configured host and, for a host name, the addresses it resolves to (the
// transport dials the resolved IP). A failed lookup just leaves the
// configured form, and the proxy host is a trusted configuration value.
func proxyDialAddrs(u *url.URL, lookup func(context.Context, string) ([]netip.Addr, error)) map[string]bool {
	if u == nil {
		return nil
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5":
			port = "1080"
		default:
			port = "80"
		}
	}
	own := map[string]bool{net.JoinHostPort(u.Hostname(), port): true}
	if _, err := netip.ParseAddr(u.Hostname()); err == nil || lookup == nil {
		return own
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := lookup(ctx, u.Hostname())
	if err != nil {
		return own
	}
	for _, ip := range addrs {
		own[net.JoinHostPort(ip.String(), port)] = true
	}
	return own
}

// checkHost refuses host when it is, or resolves to, an address allow
// rejects. Every resolved address must pass; a failed lookup is refused too.
func checkHost(ctx context.Context, host string, allow func(netip.Addr) bool,
	lookup func(context.Context, string) ([]netip.Addr, error)) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		if !allow(ip) {
			return fmt.Errorf("%w: %s is not a public address", errBlockedAddress, host)
		}
		return nil
	}
	addrs, err := lookup(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("%w: cannot resolve %s to check it: %v", errBlockedAddress, host, err)
	}
	for _, ip := range addrs {
		if !allow(ip) {
			return fmt.Errorf("%w: %s resolves to %s, not a public address", errBlockedAddress, host, ip)
		}
	}
	return nil
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

// Description drops the untrusted-data sentence: the stable system prompt
// carries that convention for every tool that brings content in (suga-eygi;
// docs/research/2026-10-23-fixed-prefix-tools-report.md 三 #18).
func (t *WebFetchTool) Description() string {
	return "Fetch http(s) URLs and return their readable text (HTML is converted to text keeping headings, " +
		"links and code blocks; scripts and styles are dropped). Give one url, or urls (up to " +
		fmt.Sprint(maxFetchURLs) + ") to fetch several concurrently, each in its own section of the result. " +
		"Private and local network addresses are refused."
}

// userAgentOrDefault is the User-Agent header every request of this tool sends.
func (t *WebFetchTool) userAgentOrDefault() string {
	if ua := strings.TrimSpace(t.userAgent); ua != "" {
		return ua
	}
	return defaultFetchUserAgent
}

// ToolMeta marks the tool as a read-only fetch.
func (*WebFetchTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryFetch, ReadOnly: true, External: true}
}
func (t *WebFetchTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string",
					"description": `http or https URL; give it alone, as the alternative to "urls"`},
				"urls": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
					"description": fmt.Sprintf("Several http(s) URLs to fetch concurrently (1-%d), as the "+
						"alternative to url; each gets its own section of the result in input order and a "+
						"failing one does not stop the others", maxFetchURLs),
				},
				"max_bytes": map[string]any{"type": "integer", "description": fmt.Sprintf("Maximum bytes of text to return per URL (default %d)", defaultFetchMaxBytes)},
			},
			// url and urls are alternatives, so neither is required; Execute
			// rejects a call that gives both or neither.
			"required": []any{},
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	raw, _ := call.Arguments["url"].(string)
	urls, err := fetchURLArgs(raw, call.Arguments["urls"])
	if err != nil {
		return nil, err
	}
	maxBytes := defaultFetchMaxBytes
	if n, ok := numberArg(call.Arguments["max_bytes"]); ok && n >= 1 {
		maxBytes = int(n)
	}
	maxBytes = min(maxBytes, rawLimit(t.maxOut))

	if len(urls) == 1 {
		content, err := t.fetch(ctx, urls[0], maxBytes)
		if err != nil {
			return nil, err
		}
		return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: content}, nil
	}
	return t.fetchMany(ctx, call, urls, maxBytes), nil
}

// fetchURLArgs validates the url / urls arguments: exactly one of them, urls
// being a non-empty list of at most maxFetchURLs absolute http(s) URLs.
func fetchURLArgs(raw string, rawList any) ([]string, error) {
	single := strings.TrimSpace(raw)
	urls, err := urlListArg(rawList)
	if err != nil {
		return nil, err
	}
	switch {
	case single != "" && len(urls) > 0:
		return nil, errors.New("web_fetch: give either url or urls, not both")
	case single != "":
		urls = []string{single}
	case len(urls) == 0:
		return nil, errors.New("web_fetch: \"url\" or \"urls\" is required")
	case len(urls) > maxFetchURLs:
		return nil, fmt.Errorf("web_fetch: at most %d urls per call, got %d", maxFetchURLs, len(urls))
	}
	for i, target := range urls {
		if err := checkFetchURL(target); err != nil {
			if len(urls) == 1 && single != "" {
				return nil, fmt.Errorf("web_fetch: url must be an absolute http(s) URL, got %q", raw)
			}
			return nil, fmt.Errorf("web_fetch: urls[%d]: %w", i, err)
		}
	}
	return urls, nil
}

// urlListArg reads the urls argument: an array of strings, or the JSON text of
// one (some models send it that way). Absent or empty gives no URL; a blank
// entry is dropped.
func urlListArg(raw any) ([]string, error) {
	if text, isText := raw.(string); isText {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		if !strings.HasPrefix(strings.TrimSpace(text), "[") {
			raw = []any{text}
		} else {
			var list []string
			if err := json.Unmarshal([]byte(text), &list); err != nil {
				return nil, fmt.Errorf("web_fetch: urls must be an array of strings: %w", err)
			}
			raw = list
		}
	}
	if raw == nil {
		return nil, nil
	}
	items, isList := raw.([]any)
	if !isList {
		if list, isStrings := raw.([]string); isStrings {
			return cleanURLList(list)
		}
		return nil, errors.New("web_fetch: urls must be an array of strings")
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		text, isText := item.(string)
		if !isText {
			return nil, fmt.Errorf("web_fetch: urls[%d] must be a string", i)
		}
		out = append(out, text)
	}
	return cleanURLList(out)
}

// cleanURLList trims the entries and drops the blank ones.
func cleanURLList(list []string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if target := strings.TrimSpace(item); target != "" {
			out = append(out, target)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// checkFetchURL requires an absolute http(s) URL with a host.
func checkFetchURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("must be an absolute http(s) URL, got %q", raw)
	}
	return nil
}

// fetch fetches one URL and returns its formatted result text: every rule (the
// private-address check included) applies per URL.
func (t *WebFetchTool) fetch(ctx context.Context, raw string, maxBytes int) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("web_fetch: url must be an absolute http(s) URL, got %q", raw)
	}

	if t.checkTarget != nil {
		if err := t.checkTarget(ctx, u); err != nil {
			return "", fmt.Errorf("web_fetch: refused: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	req.Header.Set("User-Agent", t.userAgentOrDefault())
	req.Header.Set("Accept", "text/html,text/plain,application/json;q=0.9,*/*;q=0.5")
	resp, err := t.client.Do(req)
	if err != nil {
		if errors.Is(err, errBlockedAddress) {
			return "", fmt.Errorf("web_fetch: refused: %w", err)
		}
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxDownload+1))
	if err != nil {
		return "", fmt.Errorf("web_fetch: read body: %w", err)
	}
	clipped := len(body) > fetchMaxDownload
	if clipped {
		body = body[:fetchMaxDownload]
	}
	contentType := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)

	var header strings.Builder
	fmt.Fprintf(&header, "URL: %s\nStatus: %s\n", resp.Request.URL, resp.Status)
	var text string
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml" || (mediaType == "" && looksHTML(body)):
		title, content := htmlToText(decodeBody(body, contentType), resp.Request.URL)
		if title != "" {
			fmt.Fprintf(&header, "Title: %s\n", title)
		}
		text = content
	case strings.HasPrefix(mediaType, "text/") || strings.HasSuffix(mediaType, "json") || strings.HasSuffix(mediaType, "xml"):
		text = decodeBody(body, contentType)
	default:
		return header.String() + fmt.Sprintf("[unsupported content type %q, %d bytes; not shown]", mediaType, len(body)), nil
	}
	if clipped {
		header.WriteString("[download stopped at 5MB]\n")
	}
	content, _, cut := TruncateLines(text, maxBytes)
	if cut {
		content += fmt.Sprintf("\n[truncated at %d bytes of %d]", maxBytes, len(text))
	}
	return header.String() + "\n" + content, nil
}

// fetchMany fetches the URLs of one call concurrently (one goroutine each, at
// most maxFetchURLs) and returns one section per URL in input order. A failing
// URL is reported in its own section so the others still come back; only a
// call where every URL failed is an error result. The whole result stays within
// the raw output cap (rawLimit), the sections that no longer fit being named
// instead of shown.
func (t *WebFetchTool) fetchMany(ctx context.Context, call api.ToolCall, urls []string, maxBytes int) *api.ToolResult {
	type fetchResult struct {
		content string
		err     error
	}
	results := make([]fetchResult, len(urls))
	var wg sync.WaitGroup
	for i, target := range urls {
		wg.Add(1)
		go func(i int, target string) {
			defer wg.Done()
			content, err := t.fetch(ctx, target, maxBytes)
			results[i] = fetchResult{content: content, err: err}
		}(i, target)
	}
	wg.Wait()

	var sb strings.Builder
	failed, skipped := 0, 0
	limit := rawLimit(t.maxOut)
	for i, res := range results {
		var section strings.Builder
		fmt.Fprintf(&section, "## [%d/%d] %s\n", i+1, len(urls), urls[i])
		switch {
		case res.err != nil:
			failed++
			fmt.Fprintf(&section, "Status: failed: %v\n", res.err)
		default:
			// The section starts with the URL already, so its own "URL:" line is
			// dropped: only the status line and the content follow the head.
			if _, rest, found := strings.Cut(res.content, "\n"); found {
				section.WriteString(rest)
			}
		}
		// One call stays within the raw output cap (tools.max_spill_bytes, as
		// every other tool's result): a section that no longer fits is named,
		// not shown, and the model fetches it in another call.
		if sb.Len() > 0 && sb.Len()+section.Len() > limit {
			skipped++
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(section.String())
	}
	if skipped > 0 {
		fmt.Fprintf(&sb, "\n\n[output cap reached: %d section(s) not shown (limit %d bytes); fetch them in another call]",
			skipped, limit)
	}
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: sb.String(), IsError: failed == len(urls)}
}

// decodeBody converts body to UTF-8 using the declared or sniffed charset.
func decodeBody(body []byte, contentType string) string {
	r, err := charset.NewReader(strings.NewReader(string(body)), contentType)
	if err != nil {
		return strings.ToValidUTF8(string(body), "�")
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return strings.ToValidUTF8(string(body), "�")
	}
	return string(out)
}

// looksHTML sniffs a body served without a content type.
func looksHTML(body []byte) bool {
	return http.DetectContentType(body) == "text/html; charset=utf-8"
}
