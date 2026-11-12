package axconfig

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// protectTestConfig writes body to path and returns its protected digest.
func protectTestConfig(t *testing.T, path, body string) ProtectedDigest {
	t.Helper()
	assert.NoErr(t, os.WriteFile(path, []byte(body), 0o600))
	return protectTestDigest(t, body)
}

func protectTestDigest(t *testing.T, body string) ProtectedDigest {
	t.Helper()
	digest, err := DigestProtected([]byte(body))
	assert.Require(t, assert.NoErr(t, err))
	return digest
}

// protectTestPaths isolates SUGA_HOME and returns the config path plus its
// snapshot path.
func protectTestPaths(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_CONFIG", "")
	configPath := filepath.Join(home, "config.yaml")
	snapshotPath, err := protectedSnapshotPath(configPath)
	assert.Require(t, assert.NoErr(t, err))
	return configPath, snapshotPath
}

func TestDigestProtected(t *testing.T) {
	t.Run("per key by default", func(t *testing.T) {
		digest := protectTestDigest(t, strings.Join([]string{
			"model: openai/gpt-4.1",
			"tools:",
			"  read_roots:",
			"    - .",
			"providers:",
			"  gateway:",
			"    type: openai",
			"    base_url: https://gateway.test",
			"    models:",
			"      gpt-4.1:",
			"        base_url: https://models.test",
			"",
		}, "\n"))
		assert.Eq(t, ProtectedModeKeys, digest.Mode)
		assert.NotContains(t, digest.Keys, "model", "a non-protected key is not digested")
		for _, key := range []string{
			"tools.read_roots",
			"providers.gateway.type",
			"providers.gateway.base_url",
			// A dotted model name is a nested key, not a literal dotted key.
			"providers.gateway.models.gpt-4.1.base_url",
		} {
			assert.True(t, digest.Keys[key] != "", "missing digest for "+key)
		}
		assert.False(t, digest.File == "")
	})

	t.Run("unset keys are absent", func(t *testing.T) {
		digest := protectTestDigest(t, "model: openai/gpt-4.1\n")
		assert.Eq(t, ProtectedModeKeys, digest.Mode)
		assert.Eq(t, 0, len(digest.Keys))
	})

	t.Run("comments and indentation do not change the key digests", func(t *testing.T) {
		plain := protectTestDigest(t, "tools:\n  read_roots: [a, b]\n")
		commented := protectTestDigest(t, "# leading comment\ntools:\n    # inner comment\n    read_roots:\n      - a\n      - b\n")
		assert.Eq(t, plain.Mode, commented.Mode)
		assert.Eq(t, plain.Keys, commented.Keys)
		assert.NotEq(t, plain.File, commented.File)
	})

	t.Run("list order is kept", func(t *testing.T) {
		first := protectTestDigest(t, "tools:\n  read_roots: [a, b]\n")
		reordered := protectTestDigest(t, "tools:\n  read_roots: [b, a]\n")
		assert.NotEq(t, first.Keys["tools.read_roots"], reordered.Keys["tools.read_roots"])
	})

	t.Run("empty and comment only files have no keys", func(t *testing.T) {
		for name, body := range map[string]string{
			"empty":        "",
			"comment only": "# just a comment\n# and another\n",
		} {
			t.Run(name, func(t *testing.T) {
				digest := protectTestDigest(t, body)
				assert.Eq(t, ProtectedModeKeys, digest.Mode)
				assert.Eq(t, 0, len(digest.Keys))
			})
		}
	})

	fileMode := map[string]string{
		"literal dotted top-level key": "tools.read_roots: [a]\n",
		"anchor":                       "tools: &base\n  read_roots: [a]\nother: *base\n",
		"inline merge key":             "base: &base\n  read_roots: [a]\ntools:\n  <<: {read_roots: [b]}\n",
		"merge key":                    "base: &base\n  read_roots: [a]\ntools:\n  <<: *base\n",
		"multiple documents":           "tools:\n  read_roots: [a]\n---\ntools:\n  read_roots: [b]\n",
	}
	for name, body := range fileMode {
		t.Run(name+" compares the whole file", func(t *testing.T) {
			digest := protectTestDigest(t, body)
			assert.Eq(t, ProtectedModeFile, digest.Mode)
			assert.False(t, digest.File == "")
		})
	}

	t.Run("parse error is returned", func(t *testing.T) {
		_, err := DigestProtected([]byte("tools: [unclosed\n"))
		assert.Err(t, err)
	})
}

func TestProtectedSnapshot(t *testing.T) {
	t.Run("first run creates the snapshot without a change", func(t *testing.T) {
		configPath, snapshotPath := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools:\n  read_roots: [a]\n")
		change, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		assert.True(t, change.Empty())

		stored, err := readProtectedSnapshot(snapshotPath)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, ProtectedModeKeys, stored.Mode)
		assert.Eq(t, digest.Keys, stored.Keys)
		assert.Eq(t, digest.File, stored.File)
	})

	t.Run("no change is not reported", func(t *testing.T) {
		configPath, _ := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools:\n  read_roots: [a]\n")
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		change, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		assert.True(t, change.Empty())
	})

	t.Run("a changed key is reported by name", func(t *testing.T) {
		configPath, _ := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools:\n  read_roots: [a]\n")
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		changed := protectTestConfig(t, configPath, "tools:\n  read_roots: [a, b]\n")
		change, err := CompareProtected(configPath, changed)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"tools.read_roots"}, change.Keys)
		assert.False(t, change.WholeFile)
	})

	t.Run("a secret key change reports the key name only", func(t *testing.T) {
		configPath, _ := protectTestPaths(t)
		body := "network:\n  proxy:\n    url: http://user:pass@proxy.test\n"
		digest := protectTestConfig(t, configPath, body)
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		changed := protectTestConfig(t, configPath, "network:\n  proxy:\n    url: http://other.test\n")
		change, err := CompareProtected(configPath, changed)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"network.proxy.url"}, change.Keys)
	})

	t.Run("a hook gate change is reported", func(t *testing.T) {
		configPath, _ := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "hooks:\n  pre_tool_use:\n    tools: [shell_exec]\n")
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		changed := protectTestConfig(t, configPath, "hooks:\n  pre_tool_use:\n    tools: []\n")
		change, err := CompareProtected(configPath, changed)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"hooks.pre_tool_use.tools"}, change.Keys)
	})

	t.Run("adding and removing a provider is reported", func(t *testing.T) {
		configPath, _ := protectTestPaths(t)
		one := "providers:\n  gateway:\n    base_url: https://gateway.test\n"
		digest := protectTestConfig(t, configPath, one)
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)

		two := one + "  backup:\n    base_url: https://backup.test\n"
		added := protectTestConfig(t, configPath, two)
		change, err := CompareProtected(configPath, added)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"providers.backup.base_url"}, change.Keys)

		_, err = CompareProtected(configPath, added)
		assert.NoErr(t, err)
		assert.NoErr(t, AdvanceProtected(configPath, added))
		removed := protectTestConfig(t, configPath, one)
		change, err = CompareProtected(configPath, removed)
		assert.NoErr(t, err)
		assert.Eq(t, []string{"providers.backup.base_url"}, change.Keys)
	})

	t.Run("a single key update does not absorb other changes", func(t *testing.T) {
		configPath, _ := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools:\n  approval: ask\n  read_roots: [a]\n")
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)

		// Another writer changes a protected key the snapshot has not shown yet.
		assert.NoErr(t, os.WriteFile(configPath, []byte("tools:\n  approval: ask\n  read_roots: [a, b]\n"), 0o600))
		// The human then sets another protected key in a terminal.
		_, err = WriteConfigValue(configPath, "tools.approval", "auto", ConfigWriteOptions{})
		assert.NoErr(t, err)

		written, err := os.ReadFile(configPath)
		assert.Require(t, assert.NoErr(t, err))
		change, err := CompareProtected(configPath, protectTestDigest(t, string(written)))
		assert.NoErr(t, err)
		assert.Eq(t, []string{"tools.read_roots"}, change.Keys)
	})

	t.Run("file mode snapshot is not updated by a single key write", func(t *testing.T) {
		configPath, snapshotPath := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools.read_roots: [a]\n")
		assert.Eq(t, ProtectedModeFile, digest.Mode)
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)

		_, err = WriteConfigValue(configPath, "tools.approval", "auto", ConfigWriteOptions{})
		assert.NoErr(t, err)

		stored, err := readProtectedSnapshot(snapshotPath)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, ProtectedModeFile, stored.Mode)
		assert.Eq(t, digest.File, stored.File, "the whole file changed but the snapshot must wait for the banner")

		written, err := os.ReadFile(configPath)
		assert.Require(t, assert.NoErr(t, err))
		change, err := CompareProtected(configPath, protectTestDigest(t, string(written)))
		assert.NoErr(t, err)
		assert.True(t, change.WholeFile)
		assert.Eq(t, 0, len(change.Keys))
	})

	t.Run("a whole file change is reported", func(t *testing.T) {
		configPath, _ := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools:\n  read_roots: [a]\n")
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		changed := protectTestConfig(t, configPath, "tools:\n  read_roots: [a]\n# a comment changes the whole file digest\n")
		change, err := CompareProtected(configPath, changed)
		assert.NoErr(t, err)
		assert.True(t, change.Empty(), "a comment in key mode is not a change")

		dotted := protectTestConfig(t, configPath, "tools.read_roots: [a]\n")
		change, err = CompareProtected(configPath, dotted)
		assert.NoErr(t, err)
		assert.True(t, change.WholeFile)
	})

	t.Run("a damaged snapshot reports every current key", func(t *testing.T) {
		configPath, snapshotPath := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools:\n  read_roots: [a]\n")
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		assert.NoErr(t, os.WriteFile(snapshotPath, []byte("{not json"), 0o600))

		change, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		assert.True(t, change.SnapshotUnreadable)
		assert.Eq(t, []string{"tools.read_roots"}, change.Keys)
	})

	t.Run("advance replaces the snapshot", func(t *testing.T) {
		configPath, _ := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools:\n  read_roots: [a]\n")
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)
		changed := protectTestConfig(t, configPath, "tools:\n  read_roots: [a, b]\n")
		change, err := CompareProtected(configPath, changed)
		assert.NoErr(t, err)
		assert.False(t, change.Empty())

		assert.NoErr(t, AdvanceProtected(configPath, changed))
		change, err = CompareProtected(configPath, changed)
		assert.NoErr(t, err)
		assert.True(t, change.Empty())
	})

	t.Run("SUGA_CONFIG keeps a snapshot per file", func(t *testing.T) {
		home, _ := protectTestPaths(t)
		_ = home
		configPath, _ := protectTestPaths(t)
		other := filepath.Join(t.TempDir(), "chosen.yaml")
		t.Setenv("SUGA_CONFIG", other)

		firstPath, err := protectedSnapshotPath(configPath)
		assert.NoErr(t, err)
		secondPath, err := protectedSnapshotPath(other)
		assert.NoErr(t, err)
		assert.NotEq(t, firstPath, secondPath)
		assert.Eq(t, filepath.Dir(firstPath), filepath.Dir(secondPath))
	})

	t.Run("concurrent compare and advance keep the snapshot parseable", func(t *testing.T) {
		configPath, snapshotPath := protectTestPaths(t)
		digest := protectTestConfig(t, configPath, "tools:\n  read_roots: [a]\n")
		_, err := CompareProtected(configPath, digest)
		assert.NoErr(t, err)

		var wg sync.WaitGroup
		for worker := 0; worker < 4; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for round := 0; round < 5; round++ {
					if worker%2 == 0 {
						_, _ = CompareProtected(configPath, digest)
						continue
					}
					_ = AdvanceProtected(configPath, digest)
				}
			}(worker)
		}
		wg.Wait()

		stored, err := readProtectedSnapshot(snapshotPath)
		assert.NoErr(t, err)
		assert.Eq(t, ProtectedModeKeys, stored.Mode)
	})
}

func TestProtectedPathNormalize(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_CONFIG", "")

	t.Run("relative and parent segments normalize", func(t *testing.T) {
		cwd := t.TempDir()
		t.Chdir(cwd)
		want := normalizeConfigPath(filepath.Join(cwd, "config.yaml"))
		assert.Eq(t, want, normalizeConfigPath("config.yaml"))
		assert.Eq(t, want, normalizeConfigPath(filepath.Join(cwd, "sub", "..", "config.yaml")))
	})

	t.Run("the default config uses default.json", func(t *testing.T) {
		snapshotPath, err := protectedSnapshotPath(filepath.Join(home, "config.yaml"))
		assert.NoErr(t, err)
		assert.Eq(t, filepath.Join(home, "run", "protected-config", "default.json"), snapshotPath)
	})

	t.Run("lock name and snapshot name share the normalization", func(t *testing.T) {
		other := filepath.Join(home, "other", "chosen.yaml")
		snapshotPath, err := protectedSnapshotPath(other)
		assert.NoErr(t, err)
		lockPath := configLockPath(other)
		name := strings.TrimSuffix(filepath.Base(lockPath), ".lock")
		assert.Eq(t, "config-"+strings.TrimSuffix(filepath.Base(snapshotPath), ".json"), name)
		assert.Eq(t, filepath.Join(home, "run", "locks"), filepath.Dir(lockPath))
	})

	t.Run("relative SUGA_CONFIG and its absolute path share one snapshot", func(t *testing.T) {
		cwd := t.TempDir()
		t.Chdir(cwd)
		t.Setenv("SUGA_CONFIG", "chosen.yaml")
		relative, err := protectedSnapshotPath("chosen.yaml")
		assert.NoErr(t, err)
		absolute, err := protectedSnapshotPath(filepath.Join(cwd, "chosen.yaml"))
		assert.NoErr(t, err)
		assert.Eq(t, relative, absolute)
	})
}
