package wiring

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

func resetStandardsConfig(t *testing.T) {
	t.Helper()
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("tools.dependency_roots", false))
	// The skill roots are first-party too: the assertions below pin the
	// standards roots alone, so the automatic skill roots stay off here.
	assert.NoErr(t, gkconfig.Set("tools.skill_roots", false))
}

// isolatedStandardsWorkspace pins the test workspace at its own directory. This
// prevents a parent checkout's tmp/idev-std marker from changing the result when
// the test runner's TMP is inside that checkout.
func isolatedStandardsWorkspace(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(workspace, "standards.json"), []byte("{}\n"), 0o644))
	return workspace
}

func TestStandardsRootsThroughNativeEnv(t *testing.T) {
	t.Run("release environment variable", func(t *testing.T) {
		resetStandardsConfig(t)
		releases := t.TempDir()
		t.Setenv("IDEV_STD_RELEASES_ROOT", releases)
		assert.Eq(t, []string{releases}, nativeEnv(isolatedStandardsWorkspace(t), nil).ReadRoots)
	})

	t.Run("compatible environment variable", func(t *testing.T) {
		resetStandardsConfig(t)
		releases := t.TempDir()
		t.Setenv("INHERE_DEV_STANDARDS_ROOT", releases)
		assert.Eq(t, []string{releases}, nativeEnv(isolatedStandardsWorkspace(t), nil).ReadRoots)
	})

	t.Run("missing release directory is ignored", func(t *testing.T) {
		resetStandardsConfig(t)
		t.Setenv("IDEV_STD_RELEASES_ROOT", filepath.Join(t.TempDir(), "missing"))
		assert.Empty(t, nativeEnv(isolatedStandardsWorkspace(t), nil).ReadRoots)
	})

	t.Run("workspace pin finds loading artifacts while walking upward", func(t *testing.T) {
		resetStandardsConfig(t)
		root := t.TempDir()
		workspace := filepath.Join(root, "repo", "subdir")
		assert.NoErr(t, os.MkdirAll(workspace, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(root, "standards.json"), []byte("{}\n"), 0o644))
		artifacts := filepath.Join(root, "tmp", "idev-std")
		assert.NoErr(t, os.MkdirAll(artifacts, 0o755))
		assert.Eq(t, []string{artifacts}, nativeEnv(workspace, nil).ReadRoots)
	})

	t.Run("pinned root wins over a nearer fallback marker", func(t *testing.T) {
		resetStandardsConfig(t)
		root := t.TempDir()
		workspace := filepath.Join(root, "repo", "subdir")
		near := filepath.Join(root, "repo", "tmp", "idev-std")
		far := filepath.Join(root, "tmp", "idev-std")
		assert.NoErr(t, os.MkdirAll(workspace, 0o755))
		assert.NoErr(t, os.MkdirAll(near, 0o755))
		assert.NoErr(t, os.MkdirAll(far, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(root, "standards.json"), []byte("{}\n"), 0o644))
		assert.Eq(t, []string{far}, nativeEnv(workspace, nil).ReadRoots)
	})

	t.Run("tmp artifacts also identify a workspace root", func(t *testing.T) {
		resetStandardsConfig(t)
		root := t.TempDir()
		workspace := filepath.Join(root, "repo")
		assert.NoErr(t, os.MkdirAll(filepath.Join(root, "tmp", "idev-std"), 0o755))
		assert.NoErr(t, os.MkdirAll(workspace, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(root, "standards.json"), []byte("{}\n"), 0o644))
		assert.Eq(t, []string{filepath.Join(root, "tmp", "idev-std")}, nativeEnv(workspace, nil).ReadRoots)
	})

	t.Run("unbound workspace has no loading root", func(t *testing.T) {
		resetStandardsConfig(t)
		workspace := isolatedStandardsWorkspace(t)
		assert.Empty(t, nativeEnv(workspace, nil).ReadRoots)
	})

	t.Run("the detected root is readable by the file tool", func(t *testing.T) {
		resetStandardsConfig(t)
		releases := t.TempDir()
		path := filepath.Join(releases, "loader.txt")
		assert.NoErr(t, os.WriteFile(path, []byte("release payload\n"), 0o644))
		t.Setenv("IDEV_STD_RELEASES_ROOT", releases)
		byName := map[string]api.Tool{}
		for _, tool := range native.BuildTools(nativeEnv(isolatedStandardsWorkspace(t), nil), false) {
			byName[tool.Name()] = tool
		}
		result, err := byName["file_read"].Execute(context.Background(), api.ToolCall{
			CallID: "read-standards", Arguments: map[string]any{"path": path},
		})
		assert.NoErr(t, err)
		assert.StrContains(t, result.Content, "release payload")
	})

	t.Run("the switch disables both automatic roots", func(t *testing.T) {
		resetStandardsConfig(t)
		releases := t.TempDir()
		root := t.TempDir()
		t.Setenv("IDEV_STD_RELEASES_ROOT", releases)
		assert.NoErr(t, os.MkdirAll(filepath.Join(root, "tmp", "idev-std"), 0o755))
		assert.NoErr(t, gkconfig.Set("tools.standards_roots", false))
		assert.Empty(t, nativeEnv(filepath.Join(root, "repo"), nil).ReadRoots)
	})

	t.Run("configured roots are deduplicated", func(t *testing.T) {
		resetStandardsConfig(t)
		releases := t.TempDir()
		artifactsRoot := t.TempDir()
		workspace := filepath.Join(artifactsRoot, "repo")
		artifacts := filepath.Join(artifactsRoot, "tmp", "idev-std")
		assert.NoErr(t, os.MkdirAll(artifacts, 0o755))
		assert.NoErr(t, os.MkdirAll(workspace, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(artifactsRoot, "standards.json"), []byte("{}\n"), 0o644))
		t.Setenv("IDEV_STD_RELEASES_ROOT", releases)
		configured := []string{releases, artifacts}
		if runtime.GOOS == "windows" {
			configured = []string{strings.ToUpper(releases), strings.ToUpper(artifacts)}
		}
		assert.NoErr(t, gkconfig.Set("tools.read_roots", configured))
		assert.Eq(t, []string{releases, artifacts}, nativeEnv(workspace, nil).ReadRoots)
	})
}
