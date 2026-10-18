package wiring

import (
	"net/http"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
)

// TestNetworkProxyWiring covers the runtime side of network.proxy: startup
// validation, the selector shared by the model clients and the native tools,
// and the per-URL decision it makes.
func TestNetworkProxyWiring(t *testing.T) {
	setProxy := func(t *testing.T, url string, sites []string) {
		t.Helper()
		assert.NoErr(t, gkconfig.Set("network", map[string]any{"proxy": map[string]any{"url": url, "sites": sites}}))
	}

	t.Run("unconfigured keeps every caller's default", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, validateNetworkProxy())
		assert.True(t, configuredNetworkProxy() == nil)
		assert.True(t, networkProxyFunc(nil) == nil)
		assert.True(t, nativeEnv(t.TempDir(), nil).NetworkProxy == nil)
	})
	t.Run("configured selector serves the clients and the native tools", func(t *testing.T) {
		resetConfig(t)
		setProxy(t, "http://127.0.0.1:10801", []string{"api.openai.com", ".anthropic.com"})
		assert.NoErr(t, validateNetworkProxy())
		selector := configuredNetworkProxy()
		assert.Require(t, assert.True(t, selector != nil))
		assert.Eq(t, selector, nativeEnv(t.TempDir(), nil).NetworkProxy)

		proxyFunc := networkProxyFunc(selector)
		assert.Require(t, assert.True(t, proxyFunc != nil))
		matched, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/chat/completions", nil)
		assert.Require(t, assert.NoErr(t, err))
		proxied, err := proxyFunc(matched)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "http://127.0.0.1:10801", proxied.String())

		other, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
		assert.Require(t, assert.NoErr(t, err))
		direct, err := proxyFunc(other)
		assert.NoErr(t, err)
		assert.True(t, direct == nil)
	})
	t.Run("bad global url fails startup", func(t *testing.T) {
		resetConfig(t)
		setProxy(t, "socks4://127.0.0.1:1080", []string{"api.openai.com"})
		assert.ErrMsgContains(t, validateNetworkProxy(), "network.proxy.url")
		_, err := buildMainProviderWithEstimate(appconfig.ModelSettings{Main: appconfig.ModelSpec{Model: "m", Provider: appconfig.ProviderConfig{ID: "p", Type: appconfig.ProviderTypeOpenAI}}},
			estimateOptionsForBudget(0, 0, "auto", 1))
		assert.ErrMsgContains(t, err, "network.proxy.url")
	})
	t.Run("bad site entry fails startup", func(t *testing.T) {
		resetConfig(t)
		setProxy(t, "http://127.0.0.1:10801", []string{"api.openai.com/path"})
		assert.ErrMsgContains(t, validateNetworkProxy(), "network.proxy.sites")
	})
	t.Run("bad legacy fetch proxy fails startup", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools", map[string]any{"web_fetch": map[string]any{"proxy": "127.0.0.1:7890"}}))
		assert.ErrMsgContains(t, validateNetworkProxy(), "tools.web_fetch.proxy")
	})
}
