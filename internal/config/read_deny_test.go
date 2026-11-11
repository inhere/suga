package axconfig

import (
	"path/filepath"
	"runtime"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// readDenyEnv isolates HOME and SUGA_HOME and clears the config, so the
// expansion of "~" and "$SUGA_HOME" never touches the developer's real files.
func readDenyEnv(t *testing.T) (home, sugaHome string) {
	t.Helper()
	home, sugaHome = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("SUGA_HOME", sugaHome)
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	return home, sugaHome
}

func TestReadDenyBuiltinList(t *testing.T) {
	home, sugaHome := readDenyEnv(t)
	list, err := LoadReadDeny(nil)
	assert.Require(t, assert.NoErr(t, err))

	t.Run("dotenv anywhere, examples excepted", func(t *testing.T) {
		assert.True(t, list.Denied(filepath.Join(t.TempDir(), ".env")))
		assert.True(t, list.Denied(filepath.Join(t.TempDir(), "sub", ".env.production")))
		assert.False(t, list.Denied(filepath.Join(t.TempDir(), ".env.example")))
		assert.False(t, list.Denied(filepath.Join(t.TempDir(), ".env.sample")))
		assert.False(t, list.Denied(filepath.Join(t.TempDir(), ".env.template")))
	})
	t.Run("home credentials", func(t *testing.T) {
		assert.True(t, list.Denied(filepath.Join(home, ".ssh", "id_rsa")))
		assert.True(t, list.Denied(filepath.Join(home, ".aws", "credentials")))
		assert.True(t, list.Denied(filepath.Join(home, ".netrc")))
		assert.True(t, list.Denied(filepath.Join(home, ".kube", "config")))
		assert.False(t, list.Denied(filepath.Join(home, ".aws", "config")))
		assert.False(t, list.Denied(filepath.Join(home, "notes.txt")))
	})
	t.Run("suga home env file", func(t *testing.T) {
		assert.True(t, list.Denied(filepath.Join(sugaHome, ".env")))
		assert.False(t, list.Denied(filepath.Join(sugaHome, "config.yaml")))
	})
}

func TestReadDenyConfigured(t *testing.T) {
	home, _ := readDenyEnv(t)
	t.Run("tools.read_deny entries are appended, ~ expanded", func(t *testing.T) {
		assert.NoErr(t, gkconfig.Set("tools.read_deny", []string{"~/vault/**", "secret.txt"}))
		list, err := LoadReadDeny(nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, list.Denied(filepath.Join(home, "vault", "a", "b")))
		assert.True(t, list.Denied(filepath.Join(t.TempDir(), "secret.txt")))
		assert.True(t, list.Denied(filepath.Join(t.TempDir(), ".env")), "built-in list stays")
	})
	t.Run("host extras append and cannot remove built-ins", func(t *testing.T) {
		gkconfig.Reset()
		list, err := LoadReadDeny([]string{"~/hostonly/*.key"})
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, list.Denied(filepath.Join(home, "hostonly", "a.key")))
		assert.True(t, list.Denied(filepath.Join(t.TempDir(), ".env")))
		// An exception from the host could lift a built-in entry: refused.
		_, err = LoadReadDeny([]string{"!**/.env"})
		assert.Err(t, err)
	})
	t.Run("tools.read_deny_defaults: false drops the built-in list", func(t *testing.T) {
		gkconfig.Reset()
		assert.NoErr(t, gkconfig.Set("tools.read_deny_defaults", false))
		assert.NoErr(t, gkconfig.Set("tools.read_deny", []string{"only.txt"}))
		list, err := LoadReadDeny(nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, list.Denied(filepath.Join(t.TempDir(), ".env")))
		assert.False(t, list.Denied(filepath.Join(home, ".ssh", "id_rsa")))
		assert.True(t, list.Denied(filepath.Join(t.TempDir(), "only.txt")))
	})
}

func TestReadDenyValidation(t *testing.T) {
	readDenyEnv(t)
	for _, tc := range []struct{ name, pattern string }{
		{"unterminated class", "**/[abc"},
		{"empty exception", "!"},
		{"relative path with a slash", "config/secrets.yml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadReadDeny([]string{tc.pattern})
			assert.Err(t, err)
			assert.StrContains(t, err.Error(), "tools.read_deny")
		})
	}
	t.Run("accepted forms", func(t *testing.T) {
		abs := "/data/keys/**"
		if runtime.GOOS == "windows" {
			abs = "C:/data/keys/**"
		}
		for _, p := range []string{"**/*.pem", "secret.txt", abs, "~/x", "$SUGA_HOME/vault/*"} {
			_, err := LoadReadDeny([]string{p})
			assert.NoErr(t, err, p)
		}
	})
}

func TestReadDenyKeysAreNotWritable(t *testing.T) {
	for _, key := range []string{"tools.read_deny", "tools.read_deny_defaults"} {
		d, err := MatchConfigKey(key)
		assert.Require(t, assert.NoErr(t, err), key)
		assert.False(t, d.Writable, key)
	}
}
