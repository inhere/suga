package axconfig

import (
	"fmt"
	"strings"

	gkconfig "github.com/gookit/config/v2"

	"github.com/inhere/suga/pkg/agentcore/netproxy"
)

// NetworkProxySettings is the resolved network.proxy configuration: the global
// proxy URL (environment references already expanded) plus the site allowlist
// that opts hosts into it.
type NetworkProxySettings struct {
	URL   string
	Sites []string
}

// ReadNetworkProxy validates network.proxy.url and network.proxy.sites. An
// empty URL means "no global proxy": web_fetch, the model clients and
// web_search then keep their own previous behavior (see pkg/agentcore/netproxy).
// A configured URL is validated here, at startup, instead of failing per
// request; the reader never returns the URL in an error, because it may embed
// proxy credentials.
func ReadNetworkProxy() (NetworkProxySettings, error) {
	// The raw read keeps ${VAR} visible (gkconfig's typed getters expand it
	// already), so an unresolvable reference can be told from "not configured".
	url, err := expandNetworkProxyURL(stringValue(gkconfig.Get("network.proxy.url")))
	if err != nil {
		return NetworkProxySettings{}, err
	}
	sites := normalizeProxySites(gkconfig.Strings("network.proxy.sites"))
	if _, err := netproxy.ParseSites(sites); err != nil {
		return NetworkProxySettings{}, err
	}
	if url != "" {
		if _, err := netproxy.ParseProxyURL(url); err != nil {
			return NetworkProxySettings{}, err
		}
	}
	return NetworkProxySettings{URL: url, Sites: sites}, nil
}

// expandNetworkProxyURL resolves ${VAR} references like providers.* does. A
// reference that expands to nothing is an error: a missing .env entry must not
// silently turn the global proxy off and send those requests out directly.
func expandNetworkProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	value := expandEnv(raw)
	if value == "" && strings.Contains(raw, "${") {
		return "", fmt.Errorf("network.proxy.url has an unresolved environment reference")
	}
	return value, nil
}

// normalizeProxySites trims entries and drops empty ones, so an empty list
// stays "no host is proxied" instead of becoming a match-anything rule.
func normalizeProxySites(sites []string) []string {
	out := make([]string, 0, len(sites))
	for _, site := range sites {
		if site = strings.TrimSpace(site); site != "" {
			out = append(out, site)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
