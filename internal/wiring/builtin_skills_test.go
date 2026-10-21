package wiring

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	capskills "github.com/inhere/suga/pkg/agentcore/skills"
	builtinskills "github.com/inhere/suga/skills"
)

// guideFS is the embedded layout the release tests use in place of the real
// skills.FS: one skill directory with a SKILL.md and a reference file, whose
// content is tagged so an upgrade is visible.
func guideFS(tag string) fstest.MapFS {
	return fstest.MapFS{
		"suga-guide/SKILL.md": {
			Data: []byte("---\nname: suga-guide\ndescription: " + tag + "\n---\n\n# " + tag + "\n"),
		},
		"suga-guide/references/a.md": {Data: []byte("reference " + tag + "\n")},
	}
}

// releasedDir is the released copy of the built-in skill under home.
func releasedDir(home string) string {
	return filepath.Join(home, builtinSkillsRoot, "suga-guide")
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	assert.Require(t, assert.NoErr(t, err))
	return string(data)
}

func mustReadMarker(t *testing.T, dir string) builtinMarker {
	t.Helper()
	marker, ok := readBuiltinMarker(dir)
	assert.Require(t, assert.True(t, ok))
	return marker
}

func TestReleaseBuiltinSkillsWritesTheFirstRelease(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, releaseBuiltinSkills(home, "1.2.3", guideFS("one")))

	dir := releasedDir(home)
	body := mustReadFile(t, filepath.Join(dir, "SKILL.md"))
	assert.StrContains(t, body, "one")
	assert.StrContains(t, mustReadFile(t, filepath.Join(dir, "references", "a.md")), "reference one")

	marker := mustReadMarker(t, dir)
	assert.Eq(t, "1.2.3", marker.Version)
	assert.Eq(t, 2, len(marker.Files))
	assert.Eq(t, hashBytes([]byte(body)), marker.Files["SKILL.md"])
	_, recorded := marker.Files[builtinMarkerFile]
	assert.False(t, recorded) // the marker does not record itself
}

// TestReleaseBuiltinSkillsKeepsACurrentCopyUntouched: when the embedded content
// is what is already on disk, the release compares hashes and writes nothing.
func TestReleaseBuiltinSkillsKeepsACurrentCopyUntouched(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, releaseBuiltinSkills(home, "1.2.3", guideFS("one")))
	before, err := os.Stat(releasedDir(home))
	assert.Require(t, assert.NoErr(t, err))

	time.Sleep(20 * time.Millisecond) // the compared timestamps are coarse on some filesystems
	assert.NoErr(t, releaseBuiltinSkills(home, "1.2.3", guideFS("one")))
	after, err := os.Stat(releasedDir(home))
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, before.ModTime(), after.ModTime())
}

// TestReleaseBuiltinSkillsReplacesAnUntouchedCopy: a suga upgrade replaces a
// copy nobody edited (the marker says what suga wrote, the hashes still match).
func TestReleaseBuiltinSkillsReplacesAnUntouchedCopy(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, releaseBuiltinSkills(home, "1.2.3", guideFS("one")))
	assert.NoErr(t, releaseBuiltinSkills(home, "1.3.0", guideFS("two")))

	dir := releasedDir(home)
	body := mustReadFile(t, filepath.Join(dir, "SKILL.md"))
	assert.StrContains(t, body, "two")
	assert.StrContains(t, mustReadFile(t, filepath.Join(dir, "references", "a.md")), "reference two")
	marker := mustReadMarker(t, dir)
	assert.Eq(t, "1.3.0", marker.Version)
	assert.Eq(t, hashBytes([]byte(body)), marker.Files["SKILL.md"])

	// Neither the temporary copy nor the one moved aside is left behind.
	entries, err := os.ReadDir(filepath.Join(home, builtinSkillsRoot))
	assert.Require(t, assert.NoErr(t, err))
	assert.Len(t, entries, 1)
	assert.Eq(t, "suga-guide", entries[0].Name())
}

// TestReleaseBuiltinSkillsLeavesAUserEditedCopyAlone: an edited or added file
// means the directory is the user's; nothing is overwritten, marker included.
func TestReleaseBuiltinSkillsLeavesAUserEditedCopyAlone(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, releaseBuiltinSkills(home, "1.2.3", guideFS("one")))
	dir := releasedDir(home)
	mine := "---\nname: suga-guide\ndescription: mine\n---\n\nedited\n"
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(mine), 0o644))

	t.Run("an edited file", func(t *testing.T) {
		assert.NoErr(t, releaseBuiltinSkills(home, "1.3.0", guideFS("two")))
		assert.Eq(t, mine, mustReadFile(t, filepath.Join(dir, "SKILL.md")))
		assert.Eq(t, "1.2.3", mustReadMarker(t, dir).Version)
	})
	t.Run("an added file", func(t *testing.T) {
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("mine\n"), 0o644))
		assert.NoErr(t, releaseBuiltinSkills(home, "1.4.0", guideFS("three")))
		assert.Eq(t, mine, mustReadFile(t, filepath.Join(dir, "SKILL.md")))
		assert.Eq(t, "1.2.3", mustReadMarker(t, dir).Version)
	})
}

// TestReleaseBuiltinSkillsLeavesForeignDirectoriesAlone: without a usable
// marker the directory belongs to the user and is never written into.
func TestReleaseBuiltinSkillsLeavesForeignDirectoriesAlone(t *testing.T) {
	t.Run("a directory without a marker", func(t *testing.T) {
		home := t.TempDir()
		dir := releasedDir(home)
		writeSkill(t, dir, "suga-guide", "Mine.")
		assert.NoErr(t, releaseBuiltinSkills(home, "1.3.0", guideFS("two")))
		assert.StrContains(t, mustReadFile(t, filepath.Join(dir, "SKILL.md")), "Mine.")
		_, err := os.Stat(filepath.Join(dir, builtinMarkerFile))
		assert.True(t, err != nil) // no marker is added to the user's directory
	})
	t.Run("an unreadable marker", func(t *testing.T) {
		home := t.TempDir()
		dir := releasedDir(home)
		writeSkill(t, dir, "suga-guide", "Mine.")
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, builtinMarkerFile), []byte("{not json"), 0o644))
		assert.NoErr(t, releaseBuiltinSkills(home, "1.3.0", guideFS("two")))
		assert.StrContains(t, mustReadFile(t, filepath.Join(dir, "SKILL.md")), "Mine.")
	})
}

// TestReleaseBuiltinSkillsFailureIsNotFatal: an unwritable SUGA_HOME makes the
// release report an error (the caller only logs it) and the skill registry keeps
// working from whatever is on disk.
func TestReleaseBuiltinSkillsFailureIsNotFatal(t *testing.T) {
	home := t.TempDir()
	// A file where the skills root belongs: no skill directory can be created.
	assert.NoErr(t, os.WriteFile(filepath.Join(home, builtinSkillsRoot), []byte("x"), 0o644))
	assert.Err(t, releaseBuiltinSkills(home, "1.0.0", guideFS("one")))

	t.Setenv("SUGA_HOME", home)
	assert.Eq(t, 0, buildDefaultSkillRegistry(t.TempDir()).Len())
}

// dirEntries names the direct entries of dir, sorted; an unreadable or missing
// directory is an error.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	assert.Require(t, assert.NoErr(t, err))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

// TestBuildReadOnlySkillRegistryWritesNothing: the manifest path enumerates the
// built-in skills from the embedded resources without releasing them into
// SUGA_HOME, so the directory stays exactly as it was.
func TestBuildReadOnlySkillRegistryWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	before, err := os.ReadDir(home)
	assert.Require(t, assert.NoErr(t, err))
	assert.Empty(t, before, "the test needs an empty SUGA_HOME")

	reg := buildReadOnlySkillRegistry(t.TempDir())
	assert.True(t, reg.Len() > 0, "the embedded skills are enumerated")
	builtin, ok := reg.Get("suga-guide")
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, "", builtin.Path)
	// Nothing was written: not the skills root, not even the home directory.
	assert.Eq(t, []string{}, dirEntries(t, home))

	t.Run("an already released skill keeps the disk entry", func(t *testing.T) {
		assert.NoErr(t, releaseBuiltinSkills(home, "1.0.0", builtinskills.FS))
		released, ok := buildReadOnlySkillRegistry(t.TempDir()).Get("suga-guide")
		assert.Require(t, assert.True(t, ok))
		assert.StrContains(t, released.Path, filepath.Join(home, builtinSkillsRoot))
	})
}

// TestReleaseBuiltinSkillsSurvivesConcurrentReleases stands in for two
// processes starting at once: whichever wins, the released copy is complete.
func TestReleaseBuiltinSkillsSurvivesConcurrentReleases(t *testing.T) {
	home := t.TempDir()
	const writers = 8
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = releaseBuiltinSkills(home, "1.0.0", guideFS("one"))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		assert.NoErr(t, err, "release", i)
	}

	dir := releasedDir(home)
	assert.StrContains(t, mustReadFile(t, filepath.Join(dir, "SKILL.md")), "one")
	assert.StrContains(t, mustReadFile(t, filepath.Join(dir, "references", "a.md")), "reference one")
	assert.Eq(t, "1.0.0", mustReadMarker(t, dir).Version)
}

// TestSkillRootsUseTheResolvedSugaHome: the roots carry the resolved SUGA_HOME,
// not the user's home directory.
func TestSkillRootsUseTheResolvedSugaHome(t *testing.T) {
	sugaHome, workspace := t.TempDir(), t.TempDir()
	t.Setenv("SUGA_HOME", sugaHome)
	assert.Contains(t, skillRoots(workspace), capskills.Root{Dir: filepath.Join(sugaHome, builtinSkillsRoot)})
}

// TestSugaHomeSkillsAreLoaded: <SUGA_HOME>/skills is scanned (the released
// built-in guide included) and a workspace skill of the same name wins.
func TestSugaHomeSkillsAreLoaded(t *testing.T) {
	sugaHome, workspace, home := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("SUGA_HOME", sugaHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeSkill(t, filepath.Join(sugaHome, builtinSkillsRoot, "my-notes"), "my-notes", "Suga home skill.")

	reg := buildDefaultSkillRegistry(workspace)
	mine, ok := reg.Get("my-notes")
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, filepath.Join(sugaHome, builtinSkillsRoot, "my-notes", "SKILL.md"), mine.Path)
	assert.False(t, mine.Workspace)

	guide, ok := reg.Get("suga-guide")
	assert.Require(t, assert.True(t, ok))
	assert.Eq(t, filepath.Join(sugaHome, builtinSkillsRoot, "suga-guide", "SKILL.md"), guide.Path)
	assert.NotEmpty(t, guide.Description)
	// Releasing the guide left the user's own skill directory alone.
	_, err := os.Stat(filepath.Join(sugaHome, builtinSkillsRoot, "my-notes", builtinMarkerFile))
	assert.True(t, err != nil)

	t.Run("a workspace skill of the same name wins", func(t *testing.T) {
		writeSkill(t, filepath.Join(workspace, "skills", "suga-guide"), "suga-guide", "Workspace guide.")
		won, ok := buildDefaultSkillRegistry(workspace).Get("suga-guide")
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, "Workspace guide.", won.Description)
	})
}

// TestSkillsPromptEmptyRegistry keeps the empty case covered (no skill at all:
// no section, zero stats).
func TestSkillsPromptEmptyRegistry(t *testing.T) {
	text, stats := skillsPrompt(capskills.NewRegistry(), skillPolicy{listMax: defaultSkillListMax, alwaysLoad: map[string]bool{}})
	assert.Eq(t, "", text)
	assert.Eq(t, personal.SkillsStats{}, stats)
}
