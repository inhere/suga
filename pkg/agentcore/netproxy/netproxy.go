// Package netproxy selects, per request, whether the global proxy configured
// under network.proxy receives the request or the request connects directly.
// The selector is pure: it never reads the environment, so a configured proxy
// replaces (rather than mixes with) HTTP_PROXY/HTTPS_PROXY/NO_PROXY.
package netproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ProxyFunc is the callback shape of http.Transport.Proxy.
type ProxyFunc func(*http.Request) (*url.URL, error)

// all is the network.proxy.sites entry that opts every host in.
const all = "*"

// siteRule is one validated network.proxy.sites entry.
type siteRule struct {
	host   string // lower-case host, without a leading dot
	suffix bool   // ".example.com": the domain itself and all of its subdomains
	port   string // "" = every port
}

// Selector routes requests whose target host is on the allowlist through one
// fixed proxy URL and lets every other request connect directly. It is
// immutable and safe for concurrent use; a nil Selector selects nothing.
type Selector struct {
	proxy *url.URL
	sites []siteRule
}

// New validates a proxy URL and its site allowlist. The URL is required;
// sites may be empty, which opts no host in (an empty list never means
// "everything": write "*" for that).
func New(proxyURL string, sites []string) (*Selector, error) {
	u, err := ParseProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	rules, err := ParseSites(sites)
	if err != nil {
		return nil, err
	}
	return &Selector{proxy: u, sites: rules}, nil
}

// ParseProxyURL validates network.proxy.url: an http://, https:// or socks5://
// URL with a host. The value may carry credentials (keep them in ${VAR}); it is
// deliberately not part of the error text, which reaches logs and traces.
func ParseProxyURL(proxyURL string) (*url.URL, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil, fmt.Errorf("network.proxy.url is required when a proxy is configured")
	}
	u, err := url.Parse(proxyURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
		return nil, fmt.Errorf("network.proxy.url must be an http://, https:// or socks5:// URL with a host")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("network.proxy.url must not contain a path")
	}
	return u, nil
}

// ParseSites validates every network.proxy.sites entry: "*", a host, a
// leading-dot suffix (".example.com" covers the domain and its subdomains) or
// a host:port. Paths and schemes are rejected, so a list can only name hosts.
func ParseSites(sites []string) ([]siteRule, error) {
	rules := make([]siteRule, 0, len(sites))
	for i, raw := range sites {
		rule, err := parseSite(raw)
		if err != nil {
			return nil, fmt.Errorf("network.proxy.sites[%d]: %w", i, err)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func parseSite(raw string) (siteRule, error) {
	site := strings.ToLower(strings.TrimSpace(raw))
	if site == "" {
		return siteRule{}, fmt.Errorf("entry is empty")
	}
	if site == all {
		return siteRule{host: all}, nil
	}
	if strings.ContainsAny(site, "/ \t") || strings.Contains(site, "://") {
		return siteRule{}, fmt.Errorf("entry %q must be a host, .suffix, host:port or \"*\"", site)
	}
	host, port := site, ""
	if strings.Contains(site, ":") {
		h, p, err := net.SplitHostPort(site)
		if err != nil {
			return siteRule{}, fmt.Errorf("entry %q must be a host:port", site)
		}
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return siteRule{}, fmt.Errorf("entry %q has an invalid port", site)
		}
		host, port = h, p
	}
	suffix := strings.HasPrefix(host, ".")
	host = strings.TrimPrefix(host, ".")
	if host == "" || !validSiteHost(host) {
		return siteRule{}, fmt.Errorf("entry %q must be a host, .suffix, host:port or \"*\"", site)
	}
	return siteRule{host: host, suffix: suffix, port: port}, nil
}

// validSiteHost rejects anything that is not a plain DNS name or IP literal.
func validSiteHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return !strings.Contains(host, "..")
}

// Match reports whether u is on the allowlist and therefore goes through the
// proxy. Only the URL's host and port are used; no DNS resolution is involved.
func (s *Selector) Match(u *url.URL) bool {
	if s == nil || u == nil {
		return false
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	for _, rule := range s.sites {
		if rule.host == all {
			return true
		}
		if rule.port != "" && rule.port != port {
			continue
		}
		if host == rule.host || (rule.suffix && strings.HasSuffix(host, "."+rule.host)) {
			return true
		}
	}
	return false
}

// ProxyFunc returns the http.Transport.Proxy function of the selector: the
// configured proxy for a matched request, nil (direct) for every other one. A
// nil selector returns a nil function, so callers keep their previous transport.
func (s *Selector) ProxyFunc() ProxyFunc {
	if s == nil {
		return nil
	}
	return func(req *http.Request) (*url.URL, error) {
		if req == nil || !s.Match(req.URL) {
			return nil, nil
		}
		return s.proxy, nil
	}
}

// URL returns the proxy URL, e.g. for the dial check of a fetch target; nil
// for a nil selector.
func (s *Selector) URL() *url.URL {
	if s == nil {
		return nil
	}
	return s.proxy
}
