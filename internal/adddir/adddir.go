// Package adddir resolves the additional directories of one session: the
// repeatable --add-dir flag and the SUGA_ADD_DIRS environment variable, both
// "<absolute directory>[:ro|:rw]" (default :ro), plus the set a session
// already carries in its metadata.
package adddir

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// EnvVar is the environment variable an outer tool (a job runner) uses instead
// of the flag: comma-separated entries in the value form of --add-dir. Each
// entry is "<absolute directory>[:ro|:rw]"; paths containing commas are not
// supported here and should use repeated --add-dir flags instead.
const EnvVar = "SUGA_ADD_DIRS"

// MetaKey is the session metadata key holding the session's added directories:
// a list of {path, mode} objects.
const MetaKey = "add_dirs"

// foldCase reports whether path comparison ignores case (Windows), matching
// how the native tools compare paths.
var foldCase = runtime.GOOS == "windows"

// Sources of an added directory: where it entered the session. The value is
// stored with the entry (ToMeta) so :dirs can still name the origin after a
// restart; an entry of an older session has none.
const (
	// SourceFlag: the --add-dir values of the invocation.
	SourceFlag = "flag"
	// SourceEnv: the SUGA_ADD_DIRS entries of the invocation.
	SourceEnv = "env"
	// SourceSession: typed in chat (:add-dir).
	SourceSession = "session"
	// SourceAsk: authorized at an outside-directory question in chat.
	SourceAsk = "ask"
)

// IsSource reports whether value names where a directory came from (one of the
// Source* constants).
func IsSource(value string) bool {
	switch value {
	case "", SourceFlag, SourceEnv, SourceSession, SourceAsk:
		return true
	}
	return false
}

// Dir is one additional directory of a session. Path is absolute and cleaned;
// Writable is the :rw mode (:ro, the default, is readable but not writable).
// Source is one of the Source* constants ("" when unknown, e.g. a session
// written by an older version).
type Dir struct {
	Path     string
	Writable bool
	Source   string
}

// Entry parses one --add-dir value (or one SUGA_ADD_DIRS item):
// "<absolute directory>[:ro|:rw]", default :ro. Only a trailing ":ro"/":rw"
// counts as a mode, so a Windows drive letter ("D:\docs") keeps its colon. "~"
// is expanded like tools.read_roots does, and the directory must exist.
func Entry(value string) (Dir, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return Dir{}, fmt.Errorf("%q is empty", value)
	}
	path, writable := StripMode(raw), strings.HasSuffix(raw, ":rw")
	if path == "" {
		return Dir{}, fmt.Errorf("%q names no directory", raw)
	}
	dir := Expand(path)
	if !filepath.IsAbs(dir) {
		return Dir{}, fmt.Errorf("%q is not an absolute path", raw)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Dir{}, fmt.Errorf("%q is not an existing directory", raw)
	}
	return Dir{Path: dir, Writable: writable}, nil
}

// StripMode removes a trailing ":ro"/":rw" mode from a value and trims the
// spaces around what is left. Only a trailing mode counts, the same rule Entry
// applies; a command that revokes a directory accepts such a value too, because
// it is copied from a listing that shows the mode.
func StripMode(value string) string {
	raw := strings.TrimSpace(value)
	switch {
	case strings.HasSuffix(raw, ":rw"), strings.HasSuffix(raw, ":ro"):
		raw = raw[:len(raw)-3]
	}
	return strings.TrimSpace(raw)
}

// Expand cleans a path and expands a leading "~", like Entry and the file
// tools do.
func Expand(path string) string {
	return filepath.Clean(expandHome(strings.TrimSpace(path)))
}

// IsAbsPath reports whether path names an absolute location once "~" is
// expanded.
func IsAbsPath(path string) bool { return filepath.IsAbs(Expand(path)) }

// expandHome turns a leading "~" into the user's home directory.
func expandHome(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	if dir == "~" {
		return home
	}
	if strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		return filepath.Join(home, dir[1:])
	}
	return dir
}

// Parse parses --add-dir values: every entry must be valid, because the user
// typed it just now.
func Parse(values []string) ([]Dir, error) {
	dirs := make([]Dir, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		dir, err := Entry(value)
		if err != nil {
			return nil, fmt.Errorf("--add-dir: %w", err)
		}
		dir.Source = SourceFlag
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// ParseEnv parses SUGA_ADD_DIRS: entries separated by commas. A bad entry
// cannot be fixed by the user of this process, so it is reported as a warning
// and skipped instead of failing the start. Paths containing commas are not
// supported; use repeated --add-dir flags for those paths.
func ParseEnv(value string) ([]Dir, []string) {
	var dirs []Dir
	var warnings []string
	for _, item := range strings.Split(value, ",") {
		if strings.TrimSpace(item) == "" {
			continue
		}
		dir, err := Entry(item)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: skipping %v", EnvVar, err))
			continue
		}
		dir.Source = SourceEnv
		dirs = append(dirs, dir)
	}
	return dirs, warnings
}

// Resolve parses the --add-dir values and SUGA_ADD_DIRS of this invocation
// into the directories to record for the session, flag entries first. A bad
// flag value is an error. The read-only run mode is reported as a warning:
// the session keeps the requested mode and Effective makes the run read-only.
func Resolve(values []string, env string, readOnly bool) ([]Dir, []string, error) {
	flags, err := Parse(values)
	if err != nil {
		return nil, nil, err
	}
	envDirs, warnings := ParseEnv(env)
	// Flag entries come first and win a path named twice: the flag is what the
	// user typed now, the environment is what an outer tool set.
	dirs := make([]Dir, 0, len(flags)+len(envDirs))
	seen := make(map[string]bool, len(flags)+len(envDirs))
	for _, list := range [][]Dir{flags, envDirs} {
		for _, dir := range list {
			key := pathKey(dir.Path)
			if seen[key] {
				continue
			}
			seen[key] = true
			dirs = append(dirs, dir)
		}
	}
	if readOnly {
		for _, dir := range dirs {
			if dir.Writable {
				warnings = append(warnings, fmt.Sprintf("--read-only: %s is readable only in this run (:rw ignored)", dir.Path))
				break
			}
		}
	}
	return dirs, warnings, nil
}

// Merge combines the directories a session already carries with the ones given
// now: the stored order is kept, a stored path given again takes the new mode,
// and paths only given now are appended in order.
func Merge(stored, added []Dir) []Dir {
	out := make([]Dir, 0, len(stored)+len(added))
	at := make(map[string]int, len(stored)+len(added))
	for _, list := range [][]Dir{stored, added} {
		for _, dir := range list {
			if dir.Path == "" {
				continue
			}
			key := pathKey(dir.Path)
			if i, seen := at[key]; seen {
				out[i] = dir
				continue
			}
			at[key] = len(out)
			out = append(out, dir)
		}
	}
	return out
}

// Effective applies a run mode to the set: a read-only run has no write tool at
// all, so every directory is reported read-only (the source is kept: it says
// where the directory came from, not what may be done with it).
func Effective(dirs []Dir, readOnly bool) []Dir {
	if !readOnly {
		return dirs
	}
	out := make([]Dir, len(dirs))
	for i, dir := range dirs {
		out[i] = Dir{Path: dir.Path, Source: dir.Source}
	}
	return out
}

// ToMeta renders the set for the session metadata (MetaKey): {path, mode} and,
// when known, where the directory came from (source).
func ToMeta(dirs []Dir) []map[string]any {
	out := make([]map[string]any, len(dirs))
	for i, dir := range dirs {
		mode := "ro"
		if dir.Writable {
			mode = "rw"
		}
		entry := map[string]any{"path": dir.Path, "mode": mode}
		if dir.Source != "" {
			entry["source"] = dir.Source
		}
		out[i] = entry
	}
	return out
}

// FromMeta reads the stored set back: the list ToMeta wrote, as the JSON store
// hands it over ([]any of objects). An absent value is an empty set; a
// malformed one is an error the caller reports and then ignores.
func FromMeta(value any) ([]Dir, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		typed, isTyped := value.([]map[string]any)
		if !isTyped {
			return nil, fmt.Errorf("%s: expected a list, got %T", MetaKey, value)
		}
		items = make([]any, len(typed))
		for i, item := range typed {
			items[i] = item
		}
	}
	dirs := make([]Dir, 0, len(items))
	for i, item := range items {
		obj, isObj := item.(map[string]any)
		if !isObj {
			return nil, fmt.Errorf("%s[%d]: expected a {path, mode} object, got %T", MetaKey, i, item)
		}
		path, _ := obj["path"].(string)
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("%s[%d]: %q is not an absolute path", MetaKey, i, path)
		}
		// An unknown source is dropped rather than refused: it only names where
		// the directory came from, and :dirs shows it as unknown then.
		source, _ := obj["source"].(string)
		if !IsSource(source) {
			source = ""
		}
		switch mode, _ := obj["mode"].(string); mode {
		case "", "ro":
			dirs = append(dirs, Dir{Path: filepath.Clean(path), Source: source})
		case "rw":
			dirs = append(dirs, Dir{Path: filepath.Clean(path), Writable: true, Source: source})
		default:
			return nil, fmt.Errorf("%s[%d]: unknown mode %q", MetaKey, i, mode)
		}
	}
	return dirs, nil
}

// Equal reports whether two sets name the same paths in the same order with
// the same modes and sources, i.e. whether the session metadata has to be
// rewritten.
func Equal(a, b []Dir) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if pathKey(a[i].Path) != pathKey(b[i].Path) || a[i].Writable != b[i].Writable || a[i].Source != b[i].Source {
			return false
		}
	}
	return true
}

// Without returns dirs without path, keeping the order of the rest. It is how
// chat's :rm-dir revokes a directory.
func Without(dirs []Dir, path string) []Dir {
	key := pathKey(path)
	out := make([]Dir, 0, len(dirs))
	for _, dir := range dirs {
		if pathKey(dir.Path) != key {
			out = append(out, dir)
		}
	}
	return out
}

// Find returns the entry of dirs for path; ok is false when path is not in the
// set.
func Find(dirs []Dir, path string) (Dir, bool) {
	key := pathKey(path)
	for _, dir := range dirs {
		if pathKey(dir.Path) == key {
			return dir, true
		}
	}
	return Dir{}, false
}

// Has reports whether path is one of dirs.
func Has(dirs []Dir, path string) bool {
	_, ok := Find(dirs, path)
	return ok
}

// pathKey normalises a path for the duplicate check (case-insensitive on
// Windows, where two spellings name one directory). native.CanonicalPath is the
// same normalization the file tools resolve a path with, so a directory is one
// entry whether it was typed with a Windows 8.3 short name, recorded as the
// authorized real path, or both.
func pathKey(path string) string {
	path = native.CanonicalPath(path)
	if foldCase {
		return strings.ToLower(path)
	}
	return path
}
