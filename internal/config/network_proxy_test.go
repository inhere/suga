package axconfig

import (
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// loadProxyConfig loads body as $SUGA_HOME/config.yaml in an isolated home, the
// same way the CLI reads a real configuration file.
func loadProxyConfig(t *testing.T, body string) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	root := t.TempDir()
	t.Setenv("SUGA_HOME", root)
	t.Setenv("SUGA_CONFIG", "")
	t.Chdir(t.TempDir())
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0600))
	assert.NoErr(t, Load())
}

func TestReadNetworkProxy(t *testing.T) {
	t.Run("absent means no global proxy", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\n")
		got, err := ReadNetworkProxy()
		assert.NoErr(t, err)
		assert.Eq(t, NetworkProxySettings{}, got)
	})
	t.Run("url and sites are read", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\nnetwork:\n  proxy:\n    url: \"http://127.0.0.1:10801\"\n    sites: [\"api.openai.com\", \".anthropic.com\"]\n")
		got, err := ReadNetworkProxy()
		assert.NoErr(t, err)
		assert.Eq(t, "http://127.0.0.1:10801", got.URL)
		assert.Eq(t, []string{"api.openai.com", ".anthropic.com"}, got.Sites)
	})
	t.Run("environment reference is expanded", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\nnetwork:\n  proxy:\n    url: \"${SUGA_TEST_PROXY_URL}\"\n    sites: [\"* \"]\n")
		t.Setenv("SUGA_TEST_PROXY_URL", "http://user:pw@127.0.0.1:10801")
		got, err := ReadNetworkProxy()
		assert.NoErr(t, err)
		assert.Eq(t, "http://user:pw@127.0.0.1:10801", got.URL)
		// blank entries are dropped, not treated as a match-anything rule
		assert.Eq(t, []string{"*"}, got.Sites)
	})
	t.Run("unresolved reference fails", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\nnetwork:\n  proxy:\n    url: \"${SUGA_TEST_MISSING_PROXY}\"\n")
		t.Setenv("SUGA_TEST_MISSING_PROXY", "")
		_, err := ReadNetworkProxy()
		assert.ErrMsgContains(t, err, "network.proxy.url")
	})
	t.Run("bad url fails without echoing credentials", func(t *testing.T) {
		for _, bad := range []string{"ftp://user:secret@127.0.0.1:10801", "127.0.0.1:10801"} {
			loadProxyConfig(t, "model: openai/m\nnetwork:\n  proxy:\n    url: \""+bad+"\"\n")
			_, err := ReadNetworkProxy()
			assert.ErrMsgContains(t, err, "network.proxy.url")
			assert.NotContains(t, err.Error(), "secret")
		}
	})
	t.Run("bad site entry fails", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\nnetwork:\n  proxy:\n    url: \"http://127.0.0.1:10801\"\n    sites: [\"http://api.openai.com\"]\n")
		_, err := ReadNetworkProxy()
		assert.ErrMsgContains(t, err, "network.proxy.sites")
	})
	t.Run("bad site entry fails without a url too", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\nnetwork:\n  proxy:\n    sites: [\"api.openai.com/proxy\"]\n")
		_, err := ReadNetworkProxy()
		assert.ErrMsgContains(t, err, "network.proxy.sites")
	})
}

// TestNetworkProxyRegistry: the keys are known, the URL never prints its value
// and the site list stays writable.
func TestNetworkProxyRegistry(t *testing.T) {
	t.Run("descriptors", func(t *testing.T) {
		urlKey, err := MatchConfigKey("network.proxy.url")
		assert.NoErr(t, err)
		assert.Eq(t, ConfigTypeString, urlKey.Type)
		assert.True(t, urlKey.Sensitive)
		assert.NoErr(t, ValidateConfigKey("network.proxy.url"))

		sitesKey, err := MatchConfigKey("network.proxy.sites")
		assert.NoErr(t, err)
		assert.Eq(t, ConfigTypeStringList, sitesKey.Type)
		assert.True(t, sitesKey.Writable)
		assert.NoErr(t, ValidateConfigKey("network.proxy.sites"))
	})
	t.Run("read masks the url", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\nnetwork:\n  proxy:\n    url: \"http://user:secret@127.0.0.1:10801\"\n    sites: [\"api.openai.com\"]\n")
		got, err := ReadConfigValue("network.proxy.url")
		assert.NoErr(t, err)
		assert.True(t, got.Sensitive)
		assert.NotContains(t, maskSecret(got.Value), "secret")
		assert.NotContains(t, maskSecret(got.Value), "10801")
		sites, err := ReadConfigValue("network.proxy.sites")
		assert.NoErr(t, err)
		assert.Eq(t, []string{"api.openai.com"}, sites.Value)
	})
	t.Run("invalid value is rejected by the reader", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\nnetwork:\n  proxy:\n    url: \"socks4://127.0.0.1:1080\"\n")
		err := ValidateConfigKey("network.proxy.url")
		assert.ErrMsgContains(t, err, "network.proxy.url")
	})
	t.Run("config set writes sites and lists them back", func(t *testing.T) {
		loadProxyConfig(t, "model: openai/m\n")
		value, err := ParseConfigValue("network.proxy.sites", "api.openai.com, .anthropic.com")
		assert.NoErr(t, err)
		assert.Eq(t, []string{"api.openai.com", ".anthropic.com"}, value)
	})
}
