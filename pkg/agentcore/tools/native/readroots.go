package native

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadRoot is a directory outside the workspace that the read-only file tools
// (file_read, file_list, file_grep, file_find) may read. Writes stay confined
// to the workspace.
type ReadRoot struct {
	Label string // what the directory holds, e.g. "Go modules"
	Path  string // absolute and cleaned
}

// The language-specific read roots (the Go module cache, the Rust crates, the
// Maven repository, ...) are declared one language per file: see lang.go and
// DependencyRoots there.

// ConfiguredReadRoots turns tools.read_roots entries into read roots: "~" is
// expanded, the path must be absolute and an existing directory. Rejected
// entries are returned as errors so the caller can report them.
func ConfiguredReadRoots(entries []string) ([]ReadRoot, []error) {
	home, _ := os.UserHomeDir()
	var out []ReadRoot
	var errs []error
	for _, entry := range entries {
		dir := strings.TrimSpace(entry)
		if dir == "" {
			continue
		}
		if home != "" && (dir == "~" || strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`)) {
			dir = filepath.Join(home, dir[1:])
		}
		if !filepath.IsAbs(dir) {
			errs = append(errs, fmt.Errorf("tools.read_roots: %q is not an absolute path", entry))
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			errs = append(errs, fmt.Errorf("tools.read_roots: %q is not an existing directory", entry))
			continue
		}
		out = append(out, ReadRoot{Label: "configured", Path: filepath.Clean(dir)})
	}
	return out, errs
}

// RootPaths returns the paths of roots, in order.
func RootPaths(roots []ReadRoot) []string {
	paths := make([]string, len(roots))
	for i, r := range roots {
		paths[i] = r.Path
	}
	return paths
}

// readTarget is where a tool's path argument points: base is the directory that
// confines the I/O (the workspace, a read root or an added read-write
// directory) and abs the absolute path under it.
type readTarget struct {
	base, abs string
	outside   bool // base is a read root, not the workspace
}

// show turns a slash-separated path relative to base into what the tool
// prints: unchanged inside the workspace, absolute under a read root so the
// model can pass it straight back to another tool.
func (r readTarget) show(rel string) string {
	if !r.outside {
		return rel
	}
	return filepath.Join(r.base, filepath.FromSlash(rel))
}

// matchUnder reports whether the glob matches rel (slash-separated, relative
// to base) or rel taken relative to the search start: models naturally write
// a glob relative to the path they search (path=goutil@v0.8.2, glob=x/assert/*.go),
// while the walk reports paths relative to the workspace or read root.
func (r readTarget) matchUnder(glob, rel string) bool {
	if globMatch(glob, rel) {
		return true
	}
	start, err := filepath.Rel(r.base, r.abs)
	if err != nil || start == "." {
		return false
	}
	prefix := filepath.ToSlash(start) + "/"
	return strings.HasPrefix(rel, prefix) && globMatch(glob, strings.TrimPrefix(rel, prefix))
}

// resolveRead resolves a read-only tool's path argument: inside the workspace
// as ResolveSandboxedPath does, otherwise an absolute path under one of roots.
func resolveRead(workspace string, roots []string, arg string) (readTarget, error) {
	return resolveRooted(workspace, roots, arg, "the read-only roots")
}

// resolveWrite resolves a write tool's path argument the same way against the
// session's read-write directories (--add-dir ...:rw). Such a directory is
// treated exactly like the workspace: same approvals, same delete guards.
func resolveWrite(workspace string, roots []string, arg string) (readTarget, error) {
	// Same pre-check as the read side: name the character a path cannot carry
	// instead of letting the OS answer opaquely (path_chars.go).
	if err := checkPathChars(arg); err != nil {
		return readTarget{}, err
	}
	return resolveRooted(workspace, roots, arg, "the added read-write directories")
}

// resolveRooted maps a tool's path argument to the directory that confines the
// I/O: the workspace, or one of roots (absolute, in order). A relative path is
// only ever resolved inside the workspace. what names the roots in the
// refusal, so the model learns where it may go instead.
func resolveRooted(workspace string, roots []string, arg, what string) (readTarget, error) {
	abs, err := ResolveSandboxedPath(workspace, arg)
	if err == nil {
		return readTarget{base: workspace, abs: abs}, nil
	}
	if !filepath.IsAbs(arg) || len(roots) == 0 {
		return readTarget{}, err
	}
	for _, root := range roots {
		if mapped, ok := absUnderWorkspace(root, arg, foldPathCase); ok {
			return readTarget{base: root, abs: mapped, outside: true}, nil
		}
	}
	return readTarget{}, fmt.Errorf("path escape: %q is outside the workspace and %s", arg, what)
}

// withAddDirs appends the session's added directories to a tool's configured
// read roots: whatever their mode, they are readable; only writing differs.
// The static list comes back as is when the session added nothing, so the
// common case allocates nothing.
func withAddDirs(static []string, dirs *AddDirs) []string {
	extra := dirs.ReadPaths()
	if len(extra) == 0 {
		return static
	}
	out := make([]string, 0, len(static)+len(extra))
	out = append(out, static...)
	return append(out, extra...)
}
