package axconfig

import (
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

func TestLoadReadsPrivateModelSettingsFromUserHome(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_TEST_PRIVATE_API_KEY", "")
	t.Chdir(t.TempDir())

	privateDir := filepath.Join(home, ".suga")
	assert.NoErr(t, os.MkdirAll(privateDir, 0700))
	assert.NoErr(t, os.WriteFile(filepath.Join(privateDir, ".env"), []byte("SUGA_TEST_PRIVATE_API_KEY=test-key\n"), 0600))
	config := "model: openai/private-model\nproviders:\n  openai:\n    api_key: \"${SUGA_TEST_PRIVATE_API_KEY}\"\n"
	assert.NoErr(t, os.WriteFile(filepath.Join(privateDir, "config.yaml"), []byte(config), 0600))

	assert.NoErr(t, Load())
	settings, err := ReadModelSettings("")
	assert.NoErr(t, err)
	assert.Eq(t, "private-model", settings.Main.Model)
	assert.Eq(t, "test-key", settings.Main.Provider.APIKey)
}

func TestLoadUsesSugaConfigFromSugaHome(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	root := t.TempDir()
	t.Setenv("SUGA_HOME", root)
	t.Setenv("SUGA_CONFIG", "")
	t.Chdir(t.TempDir())
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: openai/home-model\n"), 0600))
	assert.NoErr(t, Load())
	assertMainModel(t, "home-model")
}

// TestLoadedHomeRecordsTheRoot covers the record an embedding host reads to
// keep one configuration per process (pkg/sugasdk's SUGA_HOME guard).
func TestLoadedHomeRecordsTheRoot(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	ResetLoadedStateForTest()
	t.Cleanup(ResetLoadedStateForTest)
	root := t.TempDir()
	t.Setenv("SUGA_HOME", root)
	t.Setenv("SUGA_CONFIG", "")
	t.Chdir(t.TempDir())
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: openai/home-model\n"), 0600))

	_, loaded := LoadedHome()
	assert.False(t, loaded, "an unloaded process reports no root")
	assert.NoErr(t, Load())
	home, loaded := LoadedHome()
	assert.True(t, loaded)
	assert.Eq(t, root, home)
}

func TestLoadUsesExplicitConfigFile(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	root := t.TempDir()
	t.Setenv("SUGA_HOME", root)
	t.Chdir(t.TempDir())
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: openai/home-model\n"), 0600))
	configPath := filepath.Join(t.TempDir(), "chosen.yaml")
	assert.NoErr(t, os.WriteFile(configPath, []byte("model: openai/chosen-model\n"), 0600))
	t.Setenv("SUGA_CONFIG", configPath)
	assert.NoErr(t, Load())
	assertMainModel(t, "chosen-model")
}

func TestLoadIgnoresWorkingDirectoryDotenv(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	root := t.TempDir()
	t.Setenv("SUGA_HOME", root)
	t.Setenv("SUGA_CONFIG", "")
	t.Setenv("SUGA_TEST_CWD_ONLY", "")
	t.Setenv("SUGA_TEST_CWD_ONLY2", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	other := t.TempDir()
	body := "SUGA_TEST_CWD_ONLY2=1\nSUGA_HOME=" + other + "\nSUGA_TEST_CWD_ONLY=1\n"
	assert.NoErr(t, os.WriteFile(filepath.Join(cwd, ".env"), []byte(body), 0600))
	assert.NoErr(t, os.WriteFile(filepath.Join(cwd, ".env.local"), []byte(body), 0600))
	assert.NoErr(t, Load())
	assert.Eq(t, "", os.Getenv("SUGA_TEST_CWD_ONLY2"))
	assert.Eq(t, "", os.Getenv("SUGA_TEST_CWD_ONLY"))
	assert.Eq(t, root, os.Getenv("SUGA_HOME"))
}

func TestLoadIgnoresWorkingDirectoryConfig(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	root := t.TempDir()
	t.Setenv("SUGA_HOME", root)
	t.Setenv("SUGA_CONFIG", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: openai/home-model\n"), 0600))
	assert.NoErr(t, os.MkdirAll(filepath.Join(cwd, "config"), 0700))
	assert.NoErr(t, os.WriteFile(filepath.Join(cwd, "config", "config.yaml"), []byte("model: openai/project-model\n"), 0600))
	assert.NoErr(t, os.WriteFile(filepath.Join(cwd, "config.yaml"), []byte("model: openai/project-model\n"), 0600))
	assert.NoErr(t, Load())
	assertMainModel(t, "home-model")
}

func TestLoadSugaHomeDotenv(t *testing.T) {
	t.Run("ordinary variables apply", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		root := t.TempDir()
		t.Setenv("SUGA_HOME", root)
		t.Setenv("SUGA_TEST_HOME_VAR", "")
		t.Chdir(t.TempDir())
		assert.NoErr(t, os.WriteFile(filepath.Join(root, ".env"), []byte("SUGA_TEST_HOME_VAR=ok\n"), 0600))
		assert.NoErr(t, Load())
		assert.Eq(t, "ok", os.Getenv("SUGA_TEST_HOME_VAR"))
	})
	t.Run("SUGA_HOME and SUGA_CONFIG in .env do not move the root", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		root := t.TempDir()
		other := t.TempDir()
		t.Setenv("SUGA_HOME", root)
		// Unset in the process env: the .env value must not stick afterwards.
		t.Setenv("SUGA_CONFIG", "")
		os.Unsetenv("SUGA_CONFIG")
		t.Chdir(t.TempDir())
		body := "SUGA_HOME=" + other + "\nSUGA_CONFIG=" + filepath.Join(other, "x.yaml") + "\n"
		assert.NoErr(t, os.WriteFile(filepath.Join(root, ".env"), []byte(body), 0600))
		assert.NoErr(t, Load())
		assert.Eq(t, root, os.Getenv("SUGA_HOME"))
		_, has := os.LookupEnv("SUGA_CONFIG")
		assert.False(t, has)
		got, err := ResolveSugaHome()
		assert.NoErr(t, err)
		assert.Eq(t, root, got)
	})
}

func assertMainModel(t *testing.T, want string) {
	t.Helper()
	settings, err := ReadModelSettings("")
	assert.NoErr(t, err)
	assert.Eq(t, want, settings.Main.Model)
}

// providers.<id>.headers keeps both env references and the {{session_id}} placeholder,
// with the header name spelled as written.
func TestLoadReadsProviderHeaders(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	root := t.TempDir()
	t.Setenv("SUGA_HOME", root)
	t.Setenv("SUGA_CONFIG", "")
	t.Setenv("SUGA_TEST_GATEWAY_TAG", "team-a")
	t.Chdir(t.TempDir())
	config := "model: gw/m\nproviders:\n  gw:\n    type: openai\n    headers:\n      x-opencode-session: \"{{session_id}}\"\n      X-Gateway-Tag: \"${SUGA_TEST_GATEWAY_TAG}\"\n"
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0600))
	assert.NoErr(t, Load())
	settings, err := ReadModelSettings("")
	assert.NoErr(t, err)
	headers := settings.Main.Provider.Headers
	assert.Eq(t, "{{session_id}}", headers["x-opencode-session"])
	assert.Eq(t, "team-a", headers["X-Gateway-Tag"])
}
