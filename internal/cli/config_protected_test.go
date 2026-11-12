package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
)

// protectedTestKey is a writable protected key: a session must not change it,
// a human at a terminal outside suga still can.
const protectedTestKey = "tools.origin_judge.enabled"

// runConfigProtected runs `suga config ...` through the real root dispatch
// (GR110): the gate lives in the leaf handler, so a direct handler call would
// not prove the command path.
func runConfigProtected(t *testing.T, stdin io.Reader, args ...string) error {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return newCLI(context.Background(), stdin, &stdout, &stderr).RunWithArgs(args)
}

func readConfigFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	return string(body)
}

// protectedSnapshotFile is the on-disk form of a protected snapshot, decoded
// here because the snapshot reader is not exported from internal/config.
type protectedSnapshotFile struct {
	Version int               `json:"version"`
	Mode    string            `json:"mode"`
	Keys    map[string]string `json:"keys"`
	File    string            `json:"file"`
}

// defaultSnapshotPath is where the plan fixes the snapshot of the default
// SUGA_HOME/config.yaml that setupConfigCLI writes.
func defaultSnapshotPath(home string) string {
	return filepath.Join(home, "run", "protected-config", "default.json")
}

func readSnapshotFile(t *testing.T, path string) protectedSnapshotFile {
	t.Helper()
	body, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	var snapshot protectedSnapshotFile
	assert.Require(t, assert.NoErr(t, json.Unmarshal(body, &snapshot)))
	return snapshot
}

func digestConfigFile(t *testing.T, path string) appconfig.ProtectedDigest {
	t.Helper()
	digest, err := appconfig.DigestProtected([]byte(readConfigFile(t, path)))
	assert.Require(t, assert.NoErr(t, err))
	return digest
}

// TestConfigProtected covers design 2026-11-11 §2 at the command layer: a
// protected key is only writable by a human at a terminal outside suga, and a
// refused write leaves the config file byte-identical.
func TestConfigProtected(t *testing.T) {
	refused := func(t *testing.T, args ...string) {
		t.Helper()
		err := runConfigProtected(t, strings.NewReader(""), args...)
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), args[2])
		assert.StrContains(t, err.Error(), "config.yaml")
	}

	t.Run("a suga session refuses set and unset", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "session-1")
		configPath := filepath.Join(home, "config.yaml")
		before := readConfigFile(t, configPath)

		t.Run("set", func(t *testing.T) {
			refused(t, "config", "set", protectedTestKey, "false")
		})
		t.Run("unset", func(t *testing.T) {
			refused(t, "config", "unset", protectedTestKey)
		})
		assert.Eq(t, before, readConfigFile(t, configPath))
	})

	t.Run("a piped stdin is refused outside a session", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configStdinIsTerminal = func(io.Reader) bool { return false }
		configPath := filepath.Join(home, "config.yaml")
		before := readConfigFile(t, configPath)

		t.Run("set", func(t *testing.T) {
			refused(t, "config", "set", protectedTestKey, "false")
		})
		t.Run("unset", func(t *testing.T) {
			refused(t, "config", "unset", protectedTestKey)
		})
		assert.Eq(t, before, readConfigFile(t, configPath))
	})

	t.Run("a terminal outside a session writes and unsets", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configPath := filepath.Join(home, "config.yaml")

		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "set", protectedTestKey, "false"))
		body := readConfigFile(t, configPath)
		assert.StrContains(t, body, "origin_judge:")
		assert.StrContains(t, body, "enabled: false")

		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "unset", protectedTestKey))
		body = readConfigFile(t, configPath)
		assert.NotContains(t, body, "origin_judge:")
	})

	t.Run("a non-protected key is writable without a terminal", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configStdinIsTerminal = func(io.Reader) bool { return false }
		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "set", "max_steps", "8"))
		assert.StrContains(t, readConfigFile(t, filepath.Join(home, "config.yaml")), "max_steps: 8")
	})

	t.Run("a read-only protected key stays read-only", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		err := runConfigProtected(t, strings.NewReader(""), "config", "set", "hooks.pre_tool_use.tools", "Bash")
		assert.Require(t, assert.Err(t, err))
		assert.Eq(t, 2, ExitCode(err))
		assert.StrContains(t, err.Error(), "read-only")
	})

	// A human write is not an outside change: the snapshot records the written key
	// so the next chat start has nothing to report for it.
	t.Run("a terminal set updates only the written key in the snapshot", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\ntools:\n  origin_judge:\n    enabled: true\n  read_roots:\n    - a\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configPath := filepath.Join(home, "config.yaml")
		snapshotPath := defaultSnapshotPath(home)

		started := digestConfigFile(t, configPath)
		assert.Eq(t, appconfig.ProtectedModeKeys, started.Mode)
		assert.Eq(t, 2, len(started.Keys), "the fixture should set two protected keys")
		// The first comparison is the chat start that creates the snapshot.
		change, err := appconfig.CompareProtected(configPath, started)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, change.Empty())

		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "set", protectedTestKey, "false"))

		written := digestConfigFile(t, configPath)
		assert.Neq(t, started.Keys[protectedTestKey], written.Keys[protectedTestKey])
		otherKey := "tools.read_roots"

		stored := readSnapshotFile(t, snapshotPath)
		assert.Eq(t, appconfig.ProtectedModeKeys, stored.Mode)
		// The written key moved to its new digest...
		assert.Eq(t, written.Keys[protectedTestKey], stored.Keys[protectedTestKey])
		// ...and no other key did.
		assert.Eq(t, started.Keys[otherKey], stored.Keys[otherKey])
		assert.Eq(t, len(started.Keys), len(stored.Keys))
		// A single-key write does not absorb the change into the whole-file digest;
		// that one waits for the banner.
		assert.Eq(t, started.File, stored.File)

		// The snapshot is in step with the file: the next start reports nothing.
		pending, err := appconfig.CompareProtected(configPath, written)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, pending.Empty())
	})

	t.Run("a set without a snapshot does not create one", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\n")
		t.Setenv("SUGA_SESSION_ID", "")
		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "set", protectedTestKey, "false"))
		assert.StrContains(t, readConfigFile(t, filepath.Join(home, "config.yaml")), "enabled: false")

		_, err := os.Stat(defaultSnapshotPath(home))
		assert.True(t, os.IsNotExist(err), "config set must not create the snapshot: %v", err)
	})

	// A file that cannot be compared key by key (here a literal dotted top-level
	// key) is compared whole, so a single-key write cannot update the snapshot.
	t.Run("a set leaves a whole-file snapshot alone", func(t *testing.T) {
		home := setupConfigCLI(t, "model: openai/config\ntools.read_roots: [a]\n")
		t.Setenv("SUGA_SESSION_ID", "")
		configPath := filepath.Join(home, "config.yaml")
		snapshotPath := defaultSnapshotPath(home)

		started := digestConfigFile(t, configPath)
		assert.Eq(t, appconfig.ProtectedModeFile, started.Mode)
		change, err := appconfig.CompareProtected(configPath, started)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, change.Empty())
		before := readConfigFile(t, snapshotPath)

		assert.NoErr(t, runConfigProtected(t, strings.NewReader(""), "config", "set", protectedTestKey, "false"))
		assert.StrContains(t, readConfigFile(t, configPath), "origin_judge:")

		assert.Eq(t, before, readConfigFile(t, snapshotPath), "the whole-file snapshot waits for the banner")
		// The write is still reported at the next start.
		pending, err := appconfig.CompareProtected(configPath, digestConfigFile(t, configPath))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, pending.WholeFile)
	})
}
