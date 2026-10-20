package wiring

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"

	"github.com/inhere/suga/internal/adapters/gofermem"
	"github.com/inhere/suga/internal/adapters/memindex"
	axconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/memory/execstore"
	"github.com/inhere/suga/pkg/agentcore/memory/files"
)

// Memory backend names accepted by memory.<scope>.backend.
const (
	memoryBackendFiles = files.Name
	memoryBackendGofer = gofermem.Name
	memoryBackendExec  = execstore.Name
)

// Reasons recorded in MemoryBackendChoice.Reason (shown by the manifest).
const (
	MemoryReasonConfig       = "config"         // memory.<scope>.backend set explicitly
	MemoryReasonRepoIndex    = "repo_memory_md" // MEMORY.md exists at the repository root
	MemoryReasonGoferTracker = "gofer_tracker"  // .gofer/tracker exists at the repository root
	MemoryReasonDefault      = "default"        // built-in files backend under SUGA_HOME
)

// MemoryConfig is the memory part of the suga configuration.
type MemoryConfig struct {
	// UserBackend is memory.user.backend: "" (files), files or exec.
	UserBackend string
	// ProjectBackend is memory.project.backend: "" (auto), files, gofer or exec.
	ProjectBackend string
	// UserExec / ProjectExec are memory.<scope>.command/args/timeout_sec,
	// used only when that scope's backend is exec.
	UserExec, ProjectExec MemoryExecConfig
	// SugaHome is the absolute SUGA_HOME used for the built-in file layouts.
	SugaHome string
}

// loadMemoryConfig reads memory.*.backend from the loaded configuration.
func loadMemoryConfig() (MemoryConfig, error) {
	home, err := axconfig.ResolveSugaHome()
	if err != nil {
		return MemoryConfig{}, err
	}
	userExec, err := loadMemoryExec("user")
	if err != nil {
		return MemoryConfig{}, err
	}
	projectExec, err := loadMemoryExec("project")
	if err != nil {
		return MemoryConfig{}, err
	}
	return MemoryConfig{
		UserBackend:    normBackend(gkconfig.String("memory.user.backend")),
		ProjectBackend: normBackend(gkconfig.String("memory.project.backend")),
		UserExec:       userExec,
		ProjectExec:    projectExec,
		SugaHome:       home,
	}, nil
}

// MemoryExecConfig is the external command of an exec memory backend. The
// command comes only from the configuration file; nothing is auto-discovered.
type MemoryExecConfig struct {
	Command    string
	Args       []string
	TimeoutSec int
}

// loadMemoryExec reads memory.<scope>.command / args / timeout_sec. Wrong
// types are startup errors; a missing command is reported by the selector
// only when the scope really uses exec.
func loadMemoryExec(scope string) (MemoryExecConfig, error) {
	key := "memory." + scope + "."
	var c MemoryExecConfig
	if raw := gkconfig.Get(key + "command"); raw != nil {
		cmd, ok := raw.(string)
		if !ok {
			return c, fmt.Errorf("%scommand must be a string, got %v", key, raw)
		}
		c.Command = strings.TrimSpace(cmd)
	}
	if raw := gkconfig.Get(key + "args"); raw != nil {
		var items []any
		switch v := raw.(type) {
		case []string:
			for _, a := range v {
				items = append(items, a)
			}
		case []any:
			items = v
		default:
			return c, fmt.Errorf("%sargs must be a list of strings, got %v", key, raw)
		}
		for _, it := range items {
			a, ok := it.(string)
			if !ok {
				return c, fmt.Errorf("%sargs must be a list of strings, got %v", key, it)
			}
			c.Args = append(c.Args, a)
		}
	}
	if raw := gkconfig.Get(key + "timeout_sec"); raw != nil {
		n := gkconfig.Int(key + "timeout_sec")
		if n <= 0 {
			return c, fmt.Errorf("%stimeout_sec must be a positive integer, got %v", key, raw)
		}
		c.TimeoutSec = n
	}
	return c, nil
}

// newExecStore builds the exec backend of one scope; the command is required.
func newExecStore(scope string, c MemoryExecConfig) (memory.Store, error) {
	if c.Command == "" {
		return nil, fmt.Errorf("memory.%s.backend is exec but memory.%s.command is not set (set the command to run, args are optional)", scope, scope)
	}
	return execstore.New(execstore.Config{Command: c.Command, Args: c.Args, Timeout: time.Duration(c.TimeoutSec) * time.Second})
}

func normBackend(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

// MemoryBackendChoice describes the backend serving one scope and why it was
// chosen.
type MemoryBackendChoice struct {
	Scope   memory.Scope
	Backend string // files, gofer or exec
	Reason  string // MemoryReason*
	// Location is the files layout root (directory or repository root) or,
	// for gofer, the working directory gofer runs in; for exec, the command.
	Location string
}

// MemorySetup is the memory router with the user and project scopes
// registered, plus the selection for each scope.
type MemorySetup struct {
	Router  *memory.Router
	User    MemoryBackendChoice
	Project MemoryBackendChoice

	indexes []*memindex.Store
}

// Bind attaches the session database to files-backed memory index wrappers.
// Manifest construction does not call this, so it remains a read-only path.
func (s *MemorySetup) Bind(repo *store.Store) {
	if s == nil {
		return
	}
	for _, index := range s.indexes {
		if index != nil {
			index.Bind(repo)
		}
	}
}

// buildMemory selects the user and project backends for workspaceRoot and
// registers them on a new Router. It runs no backend command.
func buildMemory(workspaceRoot string, cfg MemoryConfig) (*MemorySetup, error) {
	user, userChoice, err := selectUserBackend(cfg)
	if err != nil {
		return nil, err
	}
	project, projectChoice, err := selectProjectBackend(workspaceRoot, cfg)
	if err != nil {
		return nil, err
	}
	user, userIndex := wrapMemoryIndex(user)
	project, projectIndex := wrapMemoryIndex(project)
	r := memory.NewRouter()
	if err := r.Register(memory.ScopeUser, user); err != nil {
		return nil, err
	}
	if err := r.Register(memory.ScopeProject, project); err != nil {
		return nil, err
	}
	return &MemorySetup{Router: r, User: userChoice, Project: projectChoice,
		indexes: []*memindex.Store{userIndex, projectIndex}}, nil
}

// wrapMemoryIndex adds the files-only derived FTS layer. Other backends are
// intentionally left untouched and continue to own their search semantics.
func wrapMemoryIndex(base memory.Store) (memory.Store, *memindex.Store) {
	if _, ok := base.(memory.Enumerator); !ok {
		return base, nil
	}
	indexed := memindex.New(base)
	return indexed, indexed
}

// selectUserBackend returns the user-scope backend: files under
// <SUGA_HOME>/memory/user.
func selectUserBackend(cfg MemoryConfig) (memory.Store, MemoryBackendChoice, error) {
	choice := MemoryBackendChoice{Scope: memory.UserScope(), Backend: memoryBackendFiles, Reason: MemoryReasonDefault}
	switch cfg.UserBackend {
	case "":
	case memoryBackendFiles:
		choice.Reason = MemoryReasonConfig
	case memoryBackendExec:
		choice.Backend, choice.Reason, choice.Location = memoryBackendExec, MemoryReasonConfig, cfg.UserExec.Command
		st, err := newExecStore("user", cfg.UserExec)
		return st, choice, err
	case memoryBackendGofer:
		return nil, choice, errors.New("memory.user.backend: gofer only serves project memory; use files")
	default:
		return nil, choice, fmt.Errorf("memory.user.backend: unknown backend %q (want files or exec)", cfg.UserBackend)
	}
	if err := requireHome(cfg); err != nil {
		return nil, choice, err
	}
	choice.Location = filepath.Join(cfg.SugaHome, "memory", "user")
	st, err := files.New(map[memory.Scope]files.Layout{choice.Scope: files.DirLayout{Dir: choice.Location}})
	return st, choice, err
}

// selectProjectBackend picks the project-scope backend (design "数据", Q1, Q2):
//
//  1. memory.project.backend when set (files still prefers the repository
//     MEMORY.md when one exists; exec runs memory.project.command);
//  2. MEMORY.md at the repository root → files in repository mode (the
//     file is never created by suga, only used when present);
//  3. .gofer/tracker at the repository root → gofer;
//  4. otherwise files under <SUGA_HOME>/projects/<key>/memory.
//
// The repository root is the Git root above workspaceRoot (workspaceRoot
// itself without one); <key> is derived from it by projectKey.
func selectProjectBackend(workspaceRoot string, cfg MemoryConfig) (memory.Store, MemoryBackendChoice, error) {
	var choice MemoryBackendChoice
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, choice, errors.New("memory: the workspace root is unknown")
	}
	ws, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, choice, err
	}
	root, err := gitRoot(ws)
	if err != nil {
		return nil, choice, err
	}
	choice.Scope = memory.ProjectScope(projectKey(root))

	hasIndex, err := isFile(filepath.Join(root, "MEMORY.md"))
	if err != nil {
		return nil, choice, err
	}
	backend, reason := cfg.ProjectBackend, MemoryReasonConfig
	switch backend {
	case "":
		hasTracker, terr := isDir(filepath.Join(root, ".gofer", "tracker"))
		if terr != nil {
			return nil, choice, terr
		}
		switch {
		case hasIndex:
			backend, reason = memoryBackendFiles, MemoryReasonRepoIndex
		case hasTracker:
			backend, reason = memoryBackendGofer, MemoryReasonGoferTracker
		default:
			backend, reason = memoryBackendFiles, MemoryReasonDefault
		}
	case memoryBackendFiles, memoryBackendGofer, memoryBackendExec:
	default:
		return nil, choice, fmt.Errorf("memory.project.backend: unknown backend %q (want files, gofer or exec)", backend)
	}
	choice.Backend, choice.Reason = backend, reason

	if backend == memoryBackendExec {
		choice.Location = cfg.ProjectExec.Command
		st, eerr := newExecStore("project", cfg.ProjectExec)
		return st, choice, eerr
	}
	if backend == memoryBackendGofer {
		choice.Location = root
		st, gerr := gofermem.New(gofermem.Config{Dir: root})
		return st, choice, gerr
	}
	var layout files.Layout
	if hasIndex {
		choice.Location = root
		layout = files.RepoLayout{Root: root}
	} else {
		if err := requireHome(cfg); err != nil {
			return nil, choice, err
		}
		dir := migrateProjectDir(cfg.SugaHome, choice.Scope.Key, legacyProjectKey(root))
		choice.Location = filepath.Join(dir, "memory")
		layout = files.DirLayout{Dir: choice.Location}
	}
	st, err := files.New(map[memory.Scope]files.Layout{choice.Scope: layout})
	return st, choice, err
}

// OpenMemoryStore builds the memory backends of workspaceRoot from the loaded
// configuration: the same selection the agent runtime uses, so maintenance
// commands and the agent always see the same entries. It runs no backend
// command.
func OpenMemoryStore(workspaceRoot string) (*MemorySetup, error) {
	cfg, err := loadMemoryConfig()
	if err != nil {
		return nil, err
	}
	return buildMemory(workspaceRoot, cfg)
}

func requireHome(cfg MemoryConfig) error {
	if cfg.SugaHome == "" || !filepath.IsAbs(cfg.SugaHome) {
		return fmt.Errorf("memory: SUGA_HOME must be an absolute path, got %q", cfg.SugaHome)
	}
	return nil
}

// gitRoot returns the nearest directory at or above dir that contains .git
// (a directory or a worktree file), or dir itself when there is none. It
// matches the workspace root used for project rules.
func gitRoot(dir string) (string, error) {
	for cur := dir; ; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(cur) == cur {
			return dir, nil
		}
	}
}

// projectKeySlugLen bounds the readable part of a project key.
const projectKeySlugLen = 48

// projectKey turns an absolute repository path into a short, file-name safe
// key: the slug of the directory name (lowercase ASCII letters and digits
// joined by '-') followed by a short hash of the full path, so two
// repositories with the same directory name (or a non-ASCII one) do not
// collide, e.g. "suga-5cb0fa32". Windows paths are hashed case-insensitively.
func projectKey(root string) string {
	p := projectPath(root)
	slug := keySlug(path.Base(p))
	if len(slug) > projectKeySlugLen {
		slug = strings.TrimRight(slug[:projectKeySlugLen], "-")
	}
	return joinKey(slug, pathHash(p))
}

// legacyProjectKey is the key before 0.13: the slug of the whole path (its
// last projectKeySlugLen characters) plus the same hash. Only used to move an
// existing <SUGA_HOME>/projects/<key> directory to the new key.
func legacyProjectKey(root string) string {
	p := projectPath(root)
	slug := keySlug(p)
	if len(slug) > projectKeySlugLen {
		slug = strings.TrimLeft(slug[len(slug)-projectKeySlugLen:], "-")
	}
	return joinKey(slug, pathHash(p))
}

// projectPath is the cleaned, slash-separated path a key is derived from
// (lowercased on Windows).
func projectPath(root string) string {
	p := filepath.ToSlash(filepath.Clean(root))
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// keySlug keeps lowercase ASCII letters and digits, joining runs with '-'.
func keySlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	return b.String()
}

// pathHash is the first 4 bytes of the path's SHA-256, in hex.
func pathHash(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:4])
}

func joinKey(slug, hash string) string {
	if slug == "" {
		return hash
	}
	return slug + "-" + hash
}

// migrateProjectDir moves <home>/projects/<legacy key> to the new key when
// only the legacy directory exists, so project memory written before the key
// change is kept. It returns the directory to use: the new one, or the legacy
// one when the move failed (with a warning).
func migrateProjectDir(home, key, legacy string) string {
	dir := filepath.Join(home, "projects", key)
	if legacy == key {
		return dir
	}
	old := filepath.Join(home, "projects", legacy)
	if ok, _ := isDir(dir); ok {
		return dir
	}
	if ok, _ := isDir(old); !ok {
		return dir
	}
	if err := os.Rename(old, dir); err != nil {
		slog.Warn("memory: could not move the project directory to its new key; keep using the old one",
			"from", old, "to", dir, "err", err)
		return old
	}
	slog.Info("memory: moved the project directory to its new key", "from", old, "to", dir)
	return dir
}

func isFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func isDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
