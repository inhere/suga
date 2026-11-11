//go:build windows

package native

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"golang.org/x/sys/windows"
)

// shortPath returns the 8.3 short spelling of path.
func shortPath(t *testing.T, path string) string {
	t.Helper()
	ptr, err := windows.UTF16PtrFromString(path)
	assert.Require(t, assert.NoErr(t, err))
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetShortPathName(ptr, &buf[0], uint32(len(buf)))
		assert.Require(t, assert.NoErr(t, err))
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n])
		}
		buf = make([]uint16, n+1)
	}
}

// shortSpelledDir returns a fresh temporary directory spelled with its 8.3
// short name, the shape a CI runner's %TEMP% has
// ("C:\Users\RUNNER~1\AppData\Local\Temp"): the same location then has a long
// spelling too, which is what the outside-directory authorization records. The
// test is skipped when the volume does not generate short names (8.3 disabled),
// because the shape cannot occur there.
func shortSpelledDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	short := shortPath(t, dir)
	if short == dir {
		t.Skip("8.3 short names are not generated on this volume")
	}
	return short
}

// TestCanonicalPath: the canonical spelling of a location is the same whichever
// spelling comes in, so the comparisons built on it cannot be fooled by an 8.3
// short name.
func TestCanonicalPath(t *testing.T) {
	dir := shortSpelledDir(t)
	long := resolved(t, dir)

	t.Run("a short name is expanded to the long one", func(t *testing.T) {
		assert.Eq(t, long, CanonicalPath(dir))
		assert.Eq(t, long, CanonicalPath(long))
		assert.Eq(t, long, CanonicalPath(dir+string(filepath.Separator)))
	})
	t.Run("the missing tail is kept", func(t *testing.T) {
		assert.Eq(t, filepath.Join(long, "new", "a.txt"), CanonicalPath(filepath.Join(dir, "new", "a.txt")))
	})
	t.Run("a whole missing path stays as it is", func(t *testing.T) {
		missing := filepath.Join(long, "not-there", "~deep", "a.txt")
		assert.Eq(t, missing, CanonicalPath(missing))
	})
	t.Run("a relative path is cleaned and kept", func(t *testing.T) {
		assert.Eq(t, filepath.Join("a", "b.txt"), CanonicalPath(filepath.Join("a", "sub", "..", "b.txt")))
	})
}

// TestShortNameRoots: a path spelled with a Windows 8.3 short name and the same
// path spelled long are one location for every read and write judgement,
// whichever side carries which spelling. An authorization of an outside
// directory records the resolved (long) real path, while the tool's argument
// arrives in the spelling the model used, so the two sides really do differ in
// practice (suga-o9md).
func TestShortNameRoots(t *testing.T) {
	// The workspace as a CI runner names it, the added directories as the
	// authorization records them (long) and the paths as a caller spells them
	// (short, the way %TEMP% reads).
	ws := shortSpelledDir(t)
	ro := shortSpelledDir(t)
	rw := shortSpelledDir(t)
	wsLong, roLong, rwLong := resolved(t, ws), resolved(t, ro), resolved(t, rw)
	assert.NoErr(t, os.WriteFile(filepath.Join(roLong, "seed.txt"), []byte("seed\n"), 0o644))
	dirs := NewAddDirs()
	dirs.Set([]AddDir{{Path: roLong}, {Path: rwLong, Writable: true}})

	t.Run("an added directory recorded long, asked for short", func(t *testing.T) {
		target, err := resolveRead(ws, withAddDirs(nil, dirs), ReadDenyList{}, filepath.Join(ro, "seed.txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, filepath.Join(roLong, "seed.txt"), target.abs)
		assert.True(t, target.outside)
	})
	t.Run("an added directory recorded short, asked for long", func(t *testing.T) {
		short := NewAddDirs()
		short.Set([]AddDir{{Path: ro}})
		target, err := resolveRead(ws, withAddDirs(nil, short), ReadDenyList{}, filepath.Join(roLong, "seed.txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, filepath.Join(ro, "seed.txt"), target.abs)
	})
	t.Run("a configured read root recorded long, asked for short", func(t *testing.T) {
		target, err := resolveRead(ws, []string{roLong}, ReadDenyList{}, filepath.Join(ro, "seed.txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, filepath.Join(roLong, "seed.txt"), target.abs)
	})
	t.Run("a file that does not exist yet", func(t *testing.T) {
		target, err := resolveRead(ws, []string{roLong}, ReadDenyList{}, filepath.Join(ro, "new", "deep", "a.txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, filepath.Join(roLong, "new", "deep", "a.txt"), target.abs)
	})
	t.Run("a write into an added read-write directory", func(t *testing.T) {
		target, err := resolveWrite(ws, dirs.WritePaths(), filepath.Join(rw, "new.txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, filepath.Join(rwLong, "new.txt"), target.abs)
		// A read-only added directory stays unwritable, the spelling apart.
		_, err = resolveWrite(ws, dirs.WritePaths(), filepath.Join(ro, "new.txt"))
		assert.ErrMsgContains(t, err, "outside the workspace and the added read-write directories")
	})
	t.Run("the workspace recorded long, asked for short", func(t *testing.T) {
		target, err := resolveRead(wsLong, nil, ReadDenyList{}, filepath.Join(ws, "a.txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, filepath.Join(wsLong, "a.txt"), target.abs)
		assert.False(t, target.outside)
	})
	t.Run("the workspace recorded short, asked for long", func(t *testing.T) {
		target, err := resolveRead(ws, nil, ReadDenyList{}, filepath.Join(wsLong, "a.txt"))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, filepath.Join(ws, "a.txt"), target.abs)
		assert.False(t, target.outside)
	})
	t.Run("the outside check sees the recorded directory", func(t *testing.T) {
		// What the authorization recorded (the long spelling) is reachable, so a
		// second call under it raises no new question.
		_, ok := OutsideOf(ws, nil, dirs, "file_read", map[string]any{"path": filepath.Join(ro, "seed.txt")})
		assert.False(t, ok)
	})
	t.Run("a file really outside is still refused", func(t *testing.T) {
		other := t.TempDir()
		_, err := resolveRead(ws, []string{roLong}, ReadDenyList{}, filepath.Join(other, "x.txt"))
		assert.ErrMsgContains(t, err, "outside the workspace")
	})
}
