package chat

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
)

// TestBannerProtectedNotice: the banner carries the protected settings notice
// only when the config file changed since the user last confirmed it.
func TestBannerProtectedNotice(t *testing.T) {
	render := func(t *testing.T, change appconfig.ProtectedChange) string {
		t.Helper()
		var out bytes.Buffer
		r := newRenderer(rendererOptions{Out: &out, Translator: i18n.Fallback(),
			ProtectedChange: change, ProtectedPath: "/home/u/.suga/config.yaml"})
		r.Banner()
		return out.String()
	}

	t.Run("no change adds no line", func(t *testing.T) {
		out := render(t, appconfig.ProtectedChange{})
		assert.StrContains(t, out, "Suga chat")
		assert.False(t, strings.Contains(out, "安全相关配置"), "banner of an unchanged config: %q", out)
	})

	t.Run("changed keys are named under the banner", func(t *testing.T) {
		out := render(t, appconfig.ProtectedChange{Keys: []string{"tools.read_roots", "tools.read_deny"}})
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		assert.Eq(t, "安全相关配置自上次确认以来有变化：tools.read_roots、tools.read_deny；如果不是你改的，请检查 /home/u/.suga/config.yaml",
			lines[len(lines)-1])
	})
}
