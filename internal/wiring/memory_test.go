package wiring

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/adapters/gofermem"
	"github.com/inhere/suga/internal/adapters/memindex"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// memRepo builds a fake repository: <tmp>/repo with .git, optionally a root
// MEMORY.md and a .gofer/tracker directory, plus a nested workspace dir.
func memRepo(t *testing.T, withIndex, withTracker bool) (root, sub string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "My Repo")
	sub = filepath.Join(root, "pkg", "inner")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700)))
	assert.Require(t, assert.NoErr(t, os.MkdirAll(sub, 0o700)))
	if withIndex {
		assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("# Notes\n"), 0o600)))
	}
	if withTracker {
		assert.Require(t, assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".gofer", "tracker"), 0o700)))
	}
	return root, sub
}

func TestSelectProjectBackend(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name        string
		index       bool
		tracker     bool
		configured  string
		wantBackend string
		wantReason  string
		wantRepo    bool // files in repository mode (location = repo root)
	}{
		{name: "repo MEMORY.md", index: true, wantBackend: "files", wantReason: MemoryReasonRepoIndex, wantRepo: true},
		{name: "MEMORY.md wins over gofer tracker", index: true, tracker: true, wantBackend: "files", wantReason: MemoryReasonRepoIndex, wantRepo: true},
		{name: "gofer tracker", tracker: true, wantBackend: "gofer", wantReason: MemoryReasonGoferTracker},
		{name: "default files under SUGA_HOME", wantBackend: "files", wantReason: MemoryReasonDefault},
		{name: "config files overrides tracker", tracker: true, configured: "files", wantBackend: "files", wantReason: MemoryReasonConfig},
		{name: "config files keeps repo MEMORY.md", index: true, tracker: true, configured: "files", wantBackend: "files", wantReason: MemoryReasonConfig, wantRepo: true},
		{name: "config gofer overrides MEMORY.md", index: true, configured: "gofer", wantBackend: "gofer", wantReason: MemoryReasonConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, sub := memRepo(t, tc.index, tc.tracker)
			st, choice, err := selectProjectBackend(sub, MemoryConfig{ProjectBackend: tc.configured, SugaHome: home})
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, tc.wantBackend, choice.Backend)
			assert.Eq(t, tc.wantReason, choice.Reason)
			assert.Eq(t, tc.wantBackend, memory.BackendName(st))
			assert.Eq(t, memory.ProjectScope(projectKey(root)), choice.Scope)

			switch {
			case tc.wantBackend == "gofer":
				_, ok := st.(*gofermem.Store)
				assert.True(t, ok)
				assert.Eq(t, root, choice.Location, "gofer runs in the repository root")
			case tc.wantRepo:
				assert.Eq(t, root, choice.Location)
			default:
				assert.Eq(t, filepath.Join(home, "projects", choice.Scope.Key, "memory"), choice.Location)
			}

			// Files backends really write where the choice says.
			if tc.wantBackend == "files" {
				e, err := st.Write(context.Background(), choice.Scope, memory.Op{Action: memory.ActionAdd,
					Title: "Probe", Body: "where am I", Kind: memory.KindProject})
				assert.Require(t, assert.NoErr(t, err))
				entry := filepath.Join(choice.Location, e.ID+".md")
				if tc.wantRepo {
					entry = filepath.Join(root, ".suga", "memory", e.ID+".md")
				}
				_, serr := os.Stat(entry)
				assert.NoErr(t, serr)
			}
		})
	}

	t.Run("errors", func(t *testing.T) {
		_, sub := memRepo(t, false, true)
		for _, tc := range []struct{ name, configured, want string }{
			{"exec without command", "exec", "memory.project.command is not set"},
			{"unknown backend", "sqlite", "unknown backend"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, _, err := selectProjectBackend(sub, MemoryConfig{ProjectBackend: tc.configured, SugaHome: home})
				assert.Err(t, err)
				assert.StrContains(t, err.Error(), tc.want)
			})
		}
		_, _, err := selectProjectBackend("", MemoryConfig{SugaHome: home})
		assert.Err(t, err)
		_, _, err = selectProjectBackend(sub, MemoryConfig{ProjectBackend: "files"})
		assert.Err(t, err, "the SUGA_HOME layout needs an absolute home")
	})

	t.Run("MEMORY.md directory and tracker file are ignored", func(t *testing.T) {
		root, sub := memRepo(t, false, false)
		assert.NoErr(t, os.MkdirAll(filepath.Join(root, "MEMORY.md"), 0o700))
		assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".gofer"), 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(root, ".gofer", "tracker"), nil, 0o600))
		_, choice, err := selectProjectBackend(sub, MemoryConfig{SugaHome: home})
		assert.NoErr(t, err)
		assert.Eq(t, MemoryReasonDefault, choice.Reason)
	})

	t.Run("workspace itself is used when no parent git root exists", func(t *testing.T) {
		ws := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(ws, ".git"), 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(ws, "MEMORY.md"), nil, 0o600))
		_, choice, err := selectProjectBackend(ws, MemoryConfig{SugaHome: home})
		assert.NoErr(t, err)
		assert.Eq(t, MemoryReasonRepoIndex, choice.Reason)
		assert.Eq(t, ws, choice.Location)
	})
}

func TestExplicitProjectMemoryKey(t *testing.T) {
	home := t.TempDir()
	_, sub := memRepo(t, false, true)
	setup, err := buildMemoryWithProjectKey(sub, MemoryConfig{ProjectBackend: memoryBackendGofer, SugaHome: home}, "topic-A")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, MemoryBackendChoice{Scope: memory.ProjectScope("topic-A"), Backend: memoryBackendFiles,
		Reason: MemoryReasonHostKey, Location: filepath.Join(home, "projects", "topic-A", "memory")}, setup.Project)
	assert.Eq(t, filepath.Join(home, "projects", "topic-A", "memory"), setup.Project.Location)
	backend, err := setup.Router.Route(memory.ProjectScope("topic-A"))
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "files", memory.BackendName(backend))

	for _, key := range []string{"", ".", "..", "topic/a", strings.Repeat("x", 97)} {
		if key == "" {
			continue
		}
		_, _, err := selectExplicitProjectBackend(key, MemoryConfig{SugaHome: home})
		assert.Err(t, err)
	}
}

func TestProjectKey(t *testing.T) {
	keyPattern := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*-[0-9a-f]{8}$`)
	t.Run("directory name plus hash of the full path", func(t *testing.T) {
		k := projectKey("/D/Work/inhere-tools/Suga_Dev")
		assert.True(t, keyPattern.MatchString(k), k)
		assert.True(t, strings.HasPrefix(k, "suga-dev-"), k)
		assert.Eq(t, len("suga-dev-")+8, len(k))
		assert.Eq(t, k, projectKey("/D/Work/inhere-tools/Suga_Dev/"), "stable after Clean")
		assert.NoErr(t, memory.ProjectScope(k).Validate())
	})
	t.Run("same directory name, different paths", func(t *testing.T) {
		a, b := projectKey("/work/a/suga"), projectKey("/work/b/suga")
		assert.NotEq(t, a, b)
		assert.True(t, strings.HasPrefix(a, "suga-") && strings.HasPrefix(b, "suga-"))
	})
	t.Run("long and non-ASCII names", func(t *testing.T) {
		long := projectKey("/x/" + strings.Repeat("very-long-name-", 6))
		assert.True(t, keyPattern.MatchString(long), long)
		assert.True(t, len(long) <= projectKeySlugLen+9)
		cn := projectKey("/项目/记忆")
		assert.True(t, regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(cn), cn)
	})
	t.Run("legacy key keeps the old whole-path form", func(t *testing.T) {
		k := legacyProjectKey("/D/Work/inhere-tools/Suga_Dev")
		assert.True(t, strings.HasPrefix(k, "d-work-inhere-tools-suga-dev-"), k)
		assert.Eq(t, k[len(k)-8:], projectKey("/D/Work/inhere-tools/Suga_Dev")[len("suga-dev-"):]) // same hash
	})
}

func TestMigrateProjectDir(t *testing.T) {
	root := "/work/repo/suga"
	key, legacy := projectKey(root), legacyProjectKey(root)
	write := func(t *testing.T, home, k, text string) {
		t.Helper()
		dir := filepath.Join(home, "projects", k, "memory")
		assert.NoErr(t, os.MkdirAll(dir, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte(text), 0o644))
	}
	t.Run("the legacy directory moves to the new key", func(t *testing.T) {
		home := t.TempDir()
		write(t, home, legacy, "old")
		dir := migrateProjectDir(home, key, legacy)
		assert.Eq(t, filepath.Join(home, "projects", key), dir)
		data, err := os.ReadFile(filepath.Join(dir, "memory", "note.md"))
		assert.NoErr(t, err)
		assert.Eq(t, "old", string(data))
		_, err = os.Stat(filepath.Join(home, "projects", legacy))
		assert.True(t, os.IsNotExist(err))
	})
	t.Run("an existing new directory wins and the legacy one is left alone", func(t *testing.T) {
		home := t.TempDir()
		write(t, home, legacy, "old")
		write(t, home, key, "new")
		dir := migrateProjectDir(home, key, legacy)
		assert.Eq(t, filepath.Join(home, "projects", key), dir)
		_, err := os.Stat(filepath.Join(home, "projects", legacy, "memory", "note.md"))
		assert.NoErr(t, err)
	})
	t.Run("nothing to move", func(t *testing.T) {
		home := t.TempDir()
		assert.Eq(t, filepath.Join(home, "projects", key), migrateProjectDir(home, key, legacy))
	})
	t.Run("selectProjectBackend uses the moved directory", func(t *testing.T) {
		home := t.TempDir()
		repoRoot, sub := memRepo(t, false, false)
		write(t, home, legacyProjectKey(repoRoot), "old")
		_, choice, err := selectProjectBackend(sub, MemoryConfig{SugaHome: home})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, filepath.Join(home, "projects", projectKey(repoRoot), "memory"), choice.Location)
		data, err := os.ReadFile(filepath.Join(choice.Location, "note.md"))
		assert.NoErr(t, err)
		assert.Eq(t, "old", string(data))
	})
}

func TestBuildMemory(t *testing.T) {
	home := t.TempDir()
	_, sub := memRepo(t, false, true)

	t.Run("user files and project gofer", func(t *testing.T) {
		setup, err := buildMemory(sub, MemoryConfig{SugaHome: home})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []memory.RouteInfo{{Scope: "project", Backend: "gofer"}, {Scope: "user", Backend: "files"}}, setup.Router.Backends())
		assert.Eq(t, MemoryBackendChoice{Scope: memory.UserScope(), Backend: "files", Reason: MemoryReasonDefault,
			Location: filepath.Join(home, "memory", "user")}, setup.User)
		assert.Eq(t, MemoryReasonGoferTracker, setup.Project.Reason)

		userBackend, err := setup.Router.Route(memory.UserScope())
		assert.Require(t, assert.NoErr(t, err))
		_, ok := userBackend.(*memindex.Store)
		assert.True(t, ok)
		repo, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
		assert.Require(t, assert.NoErr(t, err))
		defer repo.Close()
		setup.Bind(repo)

		// The user scope writes under SUGA_HOME through the router.
		e, err := setup.Router.Write(context.Background(), memory.UserScope(), memory.Op{Action: memory.ActionAdd,
			Title: "Prefers Chinese replies", Body: "reply in Chinese", Kind: memory.KindUser})
		assert.Require(t, assert.NoErr(t, err))
		_, serr := os.Stat(filepath.Join(home, "memory", "user", e.ID+".md"))
		assert.NoErr(t, serr)
		hits, err := setup.Router.Search(context.Background(), memory.UserScope(), "Chinese", 10)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, hits, 1)
	})
	t.Run("user backend config", func(t *testing.T) {
		setup, err := buildMemory(sub, MemoryConfig{UserBackend: "files", SugaHome: home})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, MemoryReasonConfig, setup.User.Reason)
		for _, bad := range []string{"gofer", "exec", "nope"} {
			_, err := buildMemory(sub, MemoryConfig{UserBackend: bad, SugaHome: home})
			assert.Err(t, err, bad)
		}
	})
	t.Run("project error propagates", func(t *testing.T) {
		_, err := buildMemory(sub, MemoryConfig{ProjectBackend: "exec", SugaHome: home})
		assert.Err(t, err)
	})
}

func TestMemoryExecBackend(t *testing.T) {
	home := t.TempDir()
	_, sub := memRepo(t, true, true) // MEMORY.md and tracker present: config still wins

	t.Run("project exec choice", func(t *testing.T) {
		st, choice, err := selectProjectBackend(sub, MemoryConfig{
			ProjectBackend: "exec", SugaHome: home,
			ProjectExec: MemoryExecConfig{Command: "/opt/mem-backend", Args: []string{"--x"}, TimeoutSec: 3},
		})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "exec", choice.Backend)
		assert.Eq(t, MemoryReasonConfig, choice.Reason)
		assert.Eq(t, "/opt/mem-backend", choice.Location)
		assert.Eq(t, "exec", memory.BackendName(st))
	})
	t.Run("user exec choice", func(t *testing.T) {
		st, choice, err := selectUserBackend(MemoryConfig{
			UserBackend: "exec", SugaHome: home, UserExec: MemoryExecConfig{Command: "mem-user"},
		})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "exec", choice.Backend)
		assert.Eq(t, "mem-user", choice.Location)
		assert.Eq(t, "exec", memory.BackendName(st))
	})
	t.Run("missing command is a clear error", func(t *testing.T) {
		_, _, err := selectUserBackend(MemoryConfig{UserBackend: "exec", SugaHome: home})
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "memory.user.command is not set")
		// A command configured for the other scope is not borrowed.
		_, _, err = selectProjectBackend(sub, MemoryConfig{ProjectBackend: "exec", SugaHome: home,
			UserExec: MemoryExecConfig{Command: "mem-user"}})
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "memory.project.command is not set")
	})
}

func TestLoadMemoryConfig(t *testing.T) {
	gkconfig.AddDriver(yaml.Driver)
	resetConfig(t)
	cfg, err := loadMemoryConfig()
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "", cfg.ProjectBackend)
	assert.Eq(t, "", cfg.UserBackend)
	assert.True(t, filepath.IsAbs(cfg.SugaHome))

	assert.Require(t, assert.NoErr(t, gkconfig.LoadStrings(gkconfig.Yaml, "memory:\n  user:\n    backend: Files\n  project:\n    backend: ' gofer '\n")))
	cfg, err = loadMemoryConfig()
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "files", cfg.UserBackend)
	assert.Eq(t, "gofer", cfg.ProjectBackend)
}

func TestLoadMemoryExecConfig(t *testing.T) {
	gkconfig.AddDriver(yaml.Driver)
	resetConfig(t)
	yml := "memory:\n  user:\n    backend: exec\n    command: ' /bin/mem '\n    args: [--a, b]\n    timeout_sec: 7\n  project:\n    backend: exec\n    command: pm\n"
	assert.Require(t, assert.NoErr(t, gkconfig.LoadStrings(gkconfig.Yaml, yml)))
	cfg, err := loadMemoryConfig()
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "exec", cfg.UserBackend)
	assert.Eq(t, MemoryExecConfig{Command: "/bin/mem", Args: []string{"--a", "b"}, TimeoutSec: 7}, cfg.UserExec)
	assert.Eq(t, MemoryExecConfig{Command: "pm"}, cfg.ProjectExec)

	t.Run("bad types", func(t *testing.T) {
		for _, bad := range []string{
			"memory:\n  user:\n    command: [a]\n",
			"memory:\n  user:\n    args: nope\n",
			"memory:\n  user:\n    args: [1]\n",
			"memory:\n  user:\n    timeout_sec: 0\n",
		} {
			resetConfig(t)
			assert.Require(t, assert.NoErr(t, gkconfig.LoadStrings(gkconfig.Yaml, bad)))
			_, err := loadMemoryConfig()
			assert.Err(t, err, bad)
		}
	})
}
