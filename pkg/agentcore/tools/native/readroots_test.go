package native

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// readRootFixture makes a workspace with a.txt and a separate dependency root
// holding mod/lib.go; it returns both directories.
func readRootFixture(t *testing.T) (ws, dep string) {
	t.Helper()
	ws, dep = t.TempDir(), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "a.txt"), []byte("alpha\n"), 0o644))
	assert.NoErr(t, os.MkdirAll(filepath.Join(dep, "mod"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(dep, "mod", "lib.go"), []byte("package lib\n\nfunc Exported() {}\n"), 0o644))
	return ws, dep
}

func TestResolveRead(t *testing.T) {
	ws, dep := readRootFixture(t)
	roots := []string{dep}

	t.Run("workspace paths stay in the workspace", func(t *testing.T) {
		target, err := resolveRead(ws, roots, ReadDenyList{}, "a.txt")
		assert.NoErr(t, err)
		assert.Eq(t, ws, target.base)
		assert.False(t, target.outside)
		assert.Eq(t, "x/y.go", target.show("x/y.go"))
	})
	t.Run("absolute path under a read root", func(t *testing.T) {
		target, err := resolveRead(ws, roots, ReadDenyList{}, filepath.Join(dep, "mod", "lib.go"))
		assert.NoErr(t, err)
		assert.Eq(t, dep, target.base)
		assert.True(t, target.outside)
		assert.Eq(t, filepath.Join(dep, "mod", "lib.go"), target.show("mod/lib.go"))
	})
	t.Run("outside both is refused", func(t *testing.T) {
		_, err := resolveRead(ws, roots, ReadDenyList{}, filepath.Join(filepath.Dir(dep), "elsewhere.txt"))
		assert.ErrMsgContains(t, err, "outside the workspace and the read-only roots")
	})
	t.Run("relative escape is refused even with roots", func(t *testing.T) {
		rel, err := filepath.Rel(ws, filepath.Join(dep, "mod", "lib.go"))
		assert.NoErr(t, err)
		_, err = resolveRead(ws, roots, ReadDenyList{}, rel)
		assert.ErrMsgContains(t, err, "outside workspace")
	})
	t.Run("no roots keeps the workspace error", func(t *testing.T) {
		_, err := resolveRead(ws, nil, ReadDenyList{}, filepath.Join(dep, "mod", "lib.go"))
		assert.ErrMsgContains(t, err, "outside workspace")
	})
}

func TestReadRootTools(t *testing.T) {
	ws, dep := readRootFixture(t)
	libPath := filepath.Join(dep, "mod", "lib.go")
	env := Env{Workspace: ws, ReadRoots: []string{dep}}
	byName := map[string]api.Tool{}
	for _, tool := range BuildTools(env, true) {
		byName[tool.Name()] = tool
	}

	t.Run("file_read reads under a root", func(t *testing.T) {
		out := execTool(t, byName["file_read"], map[string]any{"path": libPath})
		assert.StrContains(t, out, "3\tfunc Exported() {}")
	})
	t.Run("file_list lists a root directory", func(t *testing.T) {
		out := execTool(t, byName["file_list"], map[string]any{"dir": filepath.Join(dep, "mod")})
		assert.Eq(t, "lib.go", out)
	})
	t.Run("file_grep shows absolute paths under a root", func(t *testing.T) {
		out := execTool(t, byName["file_grep"], map[string]any{"pattern": "Exported", "path": dep})
		assert.Eq(t, libPath+":3: func Exported() {}", out)
	})
	t.Run("file_find shows absolute paths under a root", func(t *testing.T) {
		out := execTool(t, byName["file_find"], map[string]any{"pattern": "**/*.go", "path": dep})
		assert.Eq(t, libPath, out)
	})
	t.Run("workspace results stay relative", func(t *testing.T) {
		out := execTool(t, byName["file_grep"], map[string]any{"pattern": "alpha"})
		assert.Eq(t, "a.txt:1: alpha", out)
	})
	t.Run("writes never reach a root", func(t *testing.T) {
		for _, call := range []struct {
			tool string
			args map[string]any
		}{
			{"file_write", map[string]any{"path": libPath, "content": "x"}},
			{"file_edit", map[string]any{"path": libPath, "old_string": "lib", "new_string": "x"}},
		} {
			_, err := byName[call.tool].Execute(context.Background(), api.ToolCall{CallID: "c", Arguments: call.args})
			assert.ErrMsgContains(t, err, "outside workspace", call.tool)
		}
		data, err := os.ReadFile(libPath)
		assert.NoErr(t, err)
		assert.StrContains(t, string(data), "package lib")
	})
	t.Run("a symlink leaving the root is refused", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on Windows")
		}
		secret := filepath.Join(t.TempDir(), "secret.txt")
		assert.NoErr(t, os.WriteFile(secret, []byte("secret\n"), 0o644))
		assert.NoErr(t, os.Symlink(secret, filepath.Join(dep, "link.txt")))
		_, err := byName["file_read"].Execute(context.Background(), api.ToolCall{CallID: "c",
			Arguments: map[string]any{"path": filepath.Join(dep, "link.txt")}})
		assert.ErrMsgContains(t, err, "path escape")
	})
}

// setHome points the user home at a fresh temp directory (Maven, Gradle,
// cargo and the go env file all look under it) so the test never reads the
// real ~/.m2 or ~/.gradle; it returns the directory.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GRADLE_USER_HOME", "")
	return home
}

func TestDependencyRoots(t *testing.T) {
	modCache, goRoot, cargo := t.TempDir(), t.TempDir(), t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(goRoot, "src"), 0o755))
	assert.NoErr(t, os.MkdirAll(filepath.Join(cargo, "registry", "src"), 0o755))

	t.Run("from environment variables", func(t *testing.T) {
		setHome(t) // no Maven or Gradle cache here
		t.Setenv("GOENV", "off")
		t.Setenv("GOMODCACHE", modCache)
		t.Setenv("GOROOT", goRoot)
		t.Setenv("CARGO_HOME", cargo)
		assert.Eq(t, []ReadRoot{
			{Label: "Go modules", Path: modCache},
			{Label: "Go standard library", Path: filepath.Join(goRoot, "src")},
			{Label: "Rust crates", Path: filepath.Join(cargo, "registry", "src")},
		}, DependencyRoots())
	})
	t.Run("from the go env file, GOPATH form", func(t *testing.T) {
		setHome(t)
		gopath := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(gopath, "pkg", "mod"), 0o755))
		envFile := filepath.Join(t.TempDir(), "env")
		assert.NoErr(t, os.WriteFile(envFile, []byte("GOPATH="+gopath+"\nGOROOT="+goRoot+"\n"), 0o644))
		t.Setenv("GOENV", envFile)
		t.Setenv("GOMODCACHE", "")
		t.Setenv("GOPATH", "")
		t.Setenv("GOROOT", "")
		t.Setenv("CARGO_HOME", filepath.Join(t.TempDir(), "none"))
		assert.Eq(t, []ReadRoot{
			{Label: "Go modules", Path: filepath.Join(gopath, "pkg", "mod")},
			{Label: "Go standard library", Path: filepath.Join(goRoot, "src")},
		}, DependencyRoots())
	})
	t.Run("missing directories are left out", func(t *testing.T) {
		setHome(t)
		t.Setenv("GOENV", "off")
		t.Setenv("GOMODCACHE", filepath.Join(modCache, "missing"))
		t.Setenv("GOROOT", filepath.Join(goRoot, "missing"))
		t.Setenv("CARGO_HOME", filepath.Join(cargo, "missing"))
		assert.Empty(t, DependencyRoots())
	})
}

// TestDependencyRootsJava covers the Java dependency roots: the Maven local
// repository (settings.xml or the default) and the Gradle module cache.
func TestDependencyRootsJava(t *testing.T) {
	// noGo hides the Go and Rust roots so only the Java ones are left.
	setup := func(t *testing.T, home string) {
		t.Setenv("GOENV", "off")
		t.Setenv("GOMODCACHE", filepath.Join(home, "no-mod-cache"))
		t.Setenv("GOROOT", filepath.Join(home, "no-goroot"))
		t.Setenv("CARGO_HOME", filepath.Join(home, "no-cargo"))
	}

	t.Run("the default repository and the gradle cache", func(t *testing.T) {
		home := setHome(t)
		setup(t, home)
		repo := filepath.Join(home, ".m2", "repository")
		gradle := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1")
		assert.NoErr(t, os.MkdirAll(repo, 0o755))
		assert.NoErr(t, os.MkdirAll(gradle, 0o755))
		assert.Eq(t, []ReadRoot{
			{Label: "Maven local repository", Path: repo},
			{Label: "Gradle module cache", Path: gradle},
		}, DependencyRoots())
	})

	t.Run("settings.xml moves the maven repository", func(t *testing.T) {
		home := setHome(t)
		setup(t, home)
		repo := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(home, ".m2"), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(home, ".m2", "settings.xml"), []byte(
			"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<settings>\n  <!-- <localRepository>"+filepath.Join(home, "commented")+"</localRepository> -->\n"+
				"  <localRepository>"+repo+"</localRepository>\n</settings>\n"), 0o644))
		assert.Eq(t, []ReadRoot{{Label: "Maven local repository", Path: repo}}, DependencyRoots())
	})

	t.Run("${user.home} in settings.xml is resolved", func(t *testing.T) {
		home := setHome(t)
		setup(t, home)
		repo := filepath.Join(home, "m2", "repository")
		assert.NoErr(t, os.MkdirAll(repo, 0o755))
		assert.NoErr(t, os.MkdirAll(filepath.Join(home, ".m2"), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(home, ".m2", "settings.xml"), []byte(
			"<settings><localRepository>${user.home}/m2/repository</localRepository></settings>\n"), 0o644))
		assert.Eq(t, []ReadRoot{{Label: "Maven local repository", Path: repo}}, DependencyRoots())
	})

	t.Run("a settings.xml without a value falls back to the default", func(t *testing.T) {
		home := setHome(t)
		setup(t, home)
		repo := filepath.Join(home, ".m2", "repository")
		assert.NoErr(t, os.MkdirAll(repo, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(home, ".m2", "settings.xml"), []byte("<settings><offline>true</offline></settings>\n"), 0o644))
		assert.Eq(t, []ReadRoot{{Label: "Maven local repository", Path: repo}}, DependencyRoots())
	})

	t.Run("GRADLE_USER_HOME wins over the home directory", func(t *testing.T) {
		home := setHome(t)
		setup(t, home)
		gradleHome := t.TempDir()
		t.Setenv("GRADLE_USER_HOME", gradleHome)
		gradle := filepath.Join(gradleHome, "caches", "modules-2", "files-2.1")
		assert.NoErr(t, os.MkdirAll(gradle, 0o755))
		assert.Eq(t, []ReadRoot{{Label: "Gradle module cache", Path: gradle}}, DependencyRoots())
	})
}

func TestConfiguredReadRoots(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	assert.NoErr(t, os.WriteFile(file, []byte("x"), 0o644))
	roots, errs := ConfiguredReadRoots([]string{dir, "", "relative/dir", file, filepath.Join(dir, "missing")})
	assert.Eq(t, []ReadRoot{{Label: "configured", Path: dir}}, roots)
	assert.Eq(t, 3, len(errs))
	assert.StrContains(t, errs[0].Error(), "not an absolute path")
	assert.StrContains(t, errs[1].Error(), "not an existing directory")

	t.Run("tilde expands to the home directory", func(t *testing.T) {
		home := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(home, "src"), 0o755))
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		roots, errs := ConfiguredReadRoots([]string{"~/src"})
		assert.Empty(t, errs)
		assert.Eq(t, 1, len(roots))
		assert.True(t, strings.HasSuffix(roots[0].Path, "src"))
		assert.Eq(t, filepath.Join(home, "src"), roots[0].Path)
	})
}

func TestGlobRelativeToSearchPath(t *testing.T) {
	ws, dep := readRootFixture(t)
	assert.NoErr(t, os.MkdirAll(filepath.Join(ws, "internal", "a"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "internal", "a", "x.go"), []byte("package a // Hello\n"), 0o644))
	grep := &FileGrepTool{workspace: ws, readRoots: []string{dep}}
	find := &FileFindTool{workspace: ws, readRoots: []string{dep}}

	t.Run("glob relative to path, under a read root", func(t *testing.T) {
		out := execTool(t, grep, map[string]any{"pattern": "Exported", "path": dep, "glob": "mod/*.go"})
		assert.StrContains(t, out, "lib.go:3:")
		out = execTool(t, grep, map[string]any{"pattern": "Exported", "path": filepath.Join(dep, "mod"), "glob": "*.go"})
		assert.StrContains(t, out, "lib.go:3:")
		out = execTool(t, find, map[string]any{"pattern": "mod/*.go", "path": dep})
		assert.StrContains(t, out, "lib.go")
	})
	t.Run("glob relative to path or to the workspace", func(t *testing.T) {
		assert.StrContains(t, execTool(t, grep, map[string]any{"pattern": "Hello", "path": "internal", "glob": "a/*.go"}), "internal/a/x.go:1:")
		assert.StrContains(t, execTool(t, grep, map[string]any{"pattern": "Hello", "path": "internal", "glob": "internal/a/*.go"}), "internal/a/x.go:1:")
		assert.StrContains(t, execTool(t, find, map[string]any{"pattern": "a/x.go", "path": "internal"}), "internal/a/x.go")
	})
	t.Run("a missing search path says so", func(t *testing.T) {
		_, err := find.Execute(context.Background(), api.ToolCall{CallID: "c", Arguments: map[string]any{"pattern": "*.go", "path": filepath.Join(dep, "nope")}})
		assert.ErrMsgContains(t, err, "does not exist")
	})
}
