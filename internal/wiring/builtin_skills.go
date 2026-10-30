package wiring

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	appconfig "github.com/inhere/suga/internal/config"
	builtinskills "github.com/inhere/suga/skills"
)

const (
	// builtinSkillsRoot is the directory under SUGA_HOME the built-in skills
	// are released into and loaded from.
	builtinSkillsRoot = "skills"
	// builtinMarkerFile records what suga wrote into one released skill
	// directory. Its absence means the directory is the user's own.
	builtinMarkerFile = ".suga-builtin.json"
)

// sugaHomeWithBuiltinSkills resolves SUGA_HOME, releases the embedded built-in
// skills into <SUGA_HOME>/skills and returns that home for the skill roots (the
// suffix builtinSkillsRoot is added by defaultSkillRoots). It returns "" when
// SUGA_HOME cannot be resolved. A failed release only logs a warning: a command
// must never fail because its built-in skills could not be written, and what an
// earlier run released stays usable.
func sugaHomeWithBuiltinSkills() string {
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		slog.Warn("cannot resolve SUGA_HOME for the built-in skills", "err", err)
		return ""
	}
	if err := releaseBuiltinSkills(home, buildVersion, builtinskills.FS); err != nil {
		slog.Warn("cannot release the built-in skills", "suga_home", home, "err", err)
	}
	return home
}

// builtinMarker is the release record of one released skill directory.
type builtinMarker struct {
	// Version is the suga version that wrote the directory (informational:
	// the decision is made on the file hashes, so a build with another
	// version string but identical files is not rewritten).
	Version string `json:"version"`
	// Files maps every path relative to the skill directory (slash
	// separated) to the sha256 of what suga wrote there.
	Files map[string]string `json:"files"`
}

// releaseBuiltinSkills writes the built-in skills of src into
// <sugaHome>/skills, one directory per skill:
//
//   - the target does not exist: every file is written plus the marker;
//   - the target has a marker, its files are still exactly what suga wrote and
//     differ from the embedded content (suga was upgraded): the directory is
//     replaced and the marker updated;
//   - the target has a marker but the user changed, added or removed a file:
//     the whole directory is left as it is;
//   - the target has no marker (the user's own skill of that name) or an
//     unreadable marker: left as it is.
//
// A release never writes into the live directory: the new copy is built in a
// sibling temporary directory and renamed into place, so a failure cannot
// leave a half-written skill, and two processes starting at once cannot
// corrupt each other. The error is for the caller to log.
func releaseBuiltinSkills(sugaHome, version string, src fs.FS) error {
	names, err := builtinSkillNames(src)
	if err != nil {
		return err
	}
	root := filepath.Join(sugaHome, builtinSkillsRoot)
	for _, name := range names {
		if err := releaseBuiltinSkill(root, name, version, src); err != nil {
			return err
		}
	}
	return nil
}

// builtinSkillNames lists the skill directories of the embedded FS: a
// directory holding a SKILL.md.
func builtinSkillNames(src fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, fmt.Errorf("read the built-in skills: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := fs.Stat(src, path.Join(entry.Name(), "SKILL.md")); err != nil {
			continue // not a skill directory
		}
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names, nil
}

// releaseBuiltinSkill releases one embedded skill directory into root.
func releaseBuiltinSkill(root, name, version string, src fs.FS) error {
	files, err := builtinSkillFiles(src, name)
	if err != nil {
		return err
	}
	embedded := hashFiles(files)
	target := filepath.Join(root, name)
	marker, hasMarker := readBuiltinMarker(target)
	if !hasMarker {
		// No usable marker: either nothing is there yet (first release) or the
		// directory is the user's own skill, which is never touched.
		_, statErr := statRetry(target)
		switch {
		case statErr == nil:
			slog.Debug("built-in skill directory has no marker; left as is", "dir", target)
			return nil
		case !errors.Is(statErr, fs.ErrNotExist):
			return fmt.Errorf("check %s: %w", target, statErr)
		}
		return installBuiltinSkill(target, files, builtinMarker{Version: version, Files: embedded})
	}
	if maps.Equal(marker.Files, embedded) {
		return nil // already current: only the hashes above were compared
	}
	if !unchangedSince(target, marker.Files) {
		slog.Debug("the built-in skill was changed here; left as is", "dir", target)
		return nil
	}
	return installBuiltinSkill(target, files, builtinMarker{Version: version, Files: embedded})
}

// builtinSkillFiles reads every file of one embedded skill, keyed by the
// slash-separated path relative to its directory.
func builtinSkillFiles(src fs.FS, name string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(src, name, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, name+"/")
		if !fs.ValidPath(rel) {
			return fmt.Errorf("invalid file path %q", rel)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the built-in skill %s: %w", name, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("the built-in skill %s has no files", name)
	}
	return files, nil
}

// hashFiles maps every file to its sha256, keyed the same way.
func hashFiles(files map[string][]byte) map[string]string {
	out := make(map[string]string, len(files))
	for rel, data := range files {
		out[rel] = hashBytes(data)
	}
	return out
}

// hashBytes is the sha256 of one file's content, as recorded in the marker.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// readBuiltinMarker reads the release record of target. ok is false when there
// is no marker, it cannot be read or it cannot be parsed: such a directory is
// treated as the user's own and never overwritten.
func readBuiltinMarker(dir string) (builtinMarker, bool) {
	data, err := os.ReadFile(filepath.Join(dir, builtinMarkerFile))
	if err != nil {
		return builtinMarker{}, false
	}
	var m builtinMarker
	if err := json.Unmarshal(data, &m); err != nil || len(m.Files) == 0 {
		slog.Warn("the built-in skill marker is unreadable; the directory is left as is", "dir", dir, "err", err)
		return builtinMarker{}, false
	}
	return m, true
}

// unchangedSince reports whether dir holds exactly the files suga wrote (the
// marker's hashes) and nothing else, so an upgrade may replace it. A missing,
// changed or added file means the user edited the directory.
func unchangedSince(dir string, want map[string]string) bool {
	got := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == builtinMarkerFile {
			return nil // the marker itself is not part of what it records
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		got[rel] = hashBytes(data)
		return nil
	})
	if err != nil {
		slog.Warn("cannot compare the built-in skill with what suga wrote; left as is", "dir", dir, "err", err)
		return false
	}
	return maps.Equal(got, want)
}

// installRetries and installRetryDelay bound how long a release waits for
// another process's copy of the same skill to show up: Windows reports a
// directory that appeared in between as "Access is denied" rather than as an
// existing destination, and its Stat can briefly fail the same way.
const (
	installRetries    = 5
	installRetryDelay = 10 * time.Millisecond
)

// asideRetries and asideRetryDelay bound how long a release waits for another
// process to stop reading the released directory (see moveAside).
const (
	asideRetries    = 10
	asideRetryDelay = 50 * time.Millisecond
)

// targetReady reports whether target is a directory, polling briefly so a
// release that lost the race to another process sees that process's finished
// directory (the copy is complete by then: it is renamed into place).
func targetReady(target string) bool {
	for i := 0; i < installRetries; i++ {
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			return true
		}
		time.Sleep(installRetryDelay)
	}
	return false
}

// releasedCurrent reports whether target already holds the release this call
// wants: a directory present (polled like targetReady, because another release
// may have finished it a moment ago) whose marker records exactly embedded. The
// file hashes decide, not the version string, the same rule as the early
// return: a build with another version string but identical files is already
// what this release would write.
func releasedCurrent(target string, embedded map[string]string) bool {
	if !targetReady(target) {
		return false
	}
	marker, ok := readBuiltinMarker(target)
	return ok && maps.Equal(marker.Files, embedded)
}

// moveAside renames the directory target aside so the new copy can be renamed
// into place. Windows refuses that rename while another process reads a file
// inside target (ERROR_ACCESS_DENIED) or holds the directory itself open
// (ERROR_SHARING_VIOLATION), and a second suga starting at the same moment does
// exactly that: it reads the released marker (suga-se0o). Such a refusal is
// momentary, so it is retried for a moment; a target that went away in the
// meantime is no error either, because another release just moved it.
func moveAside(target, aside string) error {
	var err error
	for i := 0; i < asideRetries; i++ {
		switch err = os.Rename(target, aside); {
		case err == nil, errors.Is(err, fs.ErrNotExist):
			return nil
		case !renameConflict(err):
			return err
		}
		time.Sleep(asideRetryDelay)
	}
	return err
}

// renameIntoPlace renames the finished copy onto target. On Windows a rename
// onto a name another release is renaming onto (or has just created and is
// still being scanned by another process) is refused with a momentary
// access-denied; it is retried while the target has not appeared, and stops as
// soon as it exists, because then another release has won and its directory is
// complete (it was renamed into place).
func renameIntoPlace(tmp, target string) error {
	var err error
	for i := 0; i < asideRetries; i++ {
		if err = os.Rename(tmp, target); err == nil || !renameConflict(err) {
			return err
		}
		if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
			return err
		}
		time.Sleep(asideRetryDelay)
	}
	return err
}

// statRetry is os.Stat that retries the momentary access-denied Windows
// reports while another release is renaming a directory into place.
func statRetry(p string) (fs.FileInfo, error) {
	var (
		info fs.FileInfo
		err  error
	)
	for i := 0; i < installRetries; i++ {
		if info, err = os.Stat(p); err == nil || !renameConflict(err) {
			return info, err
		}
		time.Sleep(installRetryDelay)
	}
	return info, err
}

// installBuiltinSkill writes one released skill directory.
func installBuiltinSkill(target string, files map[string][]byte, marker builtinMarker) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create the skills root: %w", err)
	}
	// A hidden temporary directory next to the target: the rename below stays
	// inside one filesystem and the skill scanner ignores it.
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".tmp-")
	if err != nil {
		return fmt.Errorf("create the temporary skill directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := writeBuiltinSkill(tmp, files, marker); err != nil {
		return err
	}
	// An existing directory is moved aside first: renaming onto a non-empty
	// directory fails on every platform.
	var aside string
	if _, err := os.Stat(target); err == nil {
		// A concurrent release may have just finished this very copy: moving a
		// current directory aside only opens a window with no target at all, in
		// which every other racing release fails to rename onto it.
		if releasedCurrent(target, marker.Files) {
			slog.Debug("built-in skill already released by another process", "dir", target)
			return nil
		}
		aside = tmp + ".old"
		if err := moveAside(target, aside); err != nil {
			// Another release of the same version may have finished the upgrade
			// while this one waited for it to stop reading: then the directory
			// already holds what this release would write and it is done.
			if releasedCurrent(target, marker.Files) {
				slog.Debug("built-in skill already released by another process", "dir", target)
				return nil
			}
			return fmt.Errorf("move the old %s aside: %w", target, err)
		}
		defer func() { _ = os.RemoveAll(aside) }()
	}
	if err := renameIntoPlace(tmp, target); err != nil {
		// Another process may have released the same skill between the check
		// above and here: its directory is complete, so this release is done.
		if targetReady(target) {
			slog.Debug("built-in skill already released by another process", "dir", target)
			return nil
		}
		// Nothing was released: put the previous directory back, so a failed
		// upgrade is not a loss.
		if aside != "" {
			if _, statErr := os.Stat(aside); statErr == nil {
				if backErr := os.Rename(aside, target); backErr != nil && !targetReady(target) {
					return errors.Join(err, fmt.Errorf("restore %s: %w", target, backErr))
				}
			}
		}
		return fmt.Errorf("install %s: %w", target, err)
	}
	return nil
}

// writeBuiltinSkill writes files (paths relative to the skill directory) and
// the release marker into dir.
func writeBuiltinSkill(dir string, files map[string][]byte, marker builtinMarker) error {
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, files[rel], 0o644); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the built-in skill marker: %w", err)
	}
	markerPath := filepath.Join(dir, builtinMarkerFile)
	if err := os.WriteFile(markerPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", markerPath, err)
	}
	return nil
}
