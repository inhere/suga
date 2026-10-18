package wiring

import (
	gkconfig "github.com/gookit/config/v2"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// networkProxySelector resolves network.proxy: nil when no global proxy URL is
// configured, so every caller keeps its previous behavior (web_fetch ignores
// environment proxies, model and search requests follow them). One selector
// serves all three call sites, and because it never reads the environment a
// configured URL replaces the environment proxies instead of mixing with them.
func networkProxySelector() (*netproxy.Selector, error) {
	settings, err := appconfig.ReadNetworkProxy()
	if err != nil {
		return nil, err
	}
	if settings.URL == "" {
		return nil, nil
	}
	return netproxy.New(settings.URL, settings.Sites)
}

// networkProxyFunc is the transport callback of the model clients; nil keeps
// their default transport.
func networkProxyFunc(selector *netproxy.Selector) netproxy.ProxyFunc {
	if selector == nil {
		return nil
	}
	return selector.ProxyFunc()
}

// validateNetworkProxy fails startup on a bad global or legacy web_fetch proxy
// instead of silently sending those requests out without one.
func validateNetworkProxy() error {
	if _, err := networkProxySelector(); err != nil {
		return err
	}
	// A bad proxy is a startup error rather than a silently missing web_fetch.
	_, err := native.ParseFetchProxy(gkconfig.String("tools.web_fetch.proxy"))
	return err
}
