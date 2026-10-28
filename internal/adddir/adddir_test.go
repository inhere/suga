package adddir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestEntry(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	assert.NoErr(t, os.WriteFile(file, []byte("x"), 0o644))

	t.Run("a bare directory is read-only", func(t *testing.T) {
		got, err := Entry(dir)
		assert.NoErr(t, err)
		assert.Eq(t, Dir{Path: filepath.Clean(dir)}, got)
	})
	t.Run("the :ro and :rw suffixes", func(t *testing.T) {
		got, err := Entry(dir + ":ro")
		assert.NoErr(t, err)
		assert.False(t, got.Writable)
		got, err = Entry(dir + ":rw")
		assert.NoErr(t, err)
		assert.True(t, got.Writable)
	})
	t.Run("only a trailing suffix counts", func(t *testing.T) {
		// "ro" inside the path is no mode; the whole value is the directory.
		_, err := Entry(filepath.Join(dir, "ro"))
		assert.ErrMsgContains(t, err, "is not an existing directory")
	})
	t.Run("a relative path is refused", func(t *testing.T) {
		_, err := Entry("docs")
		assert.ErrMsgContains(t, err, "is not an absolute path")
	})
	t.Run("a missing directory is refused", func(t *testing.T) {
		_, err := Entry(filepath.Join(dir, "missing"))
		assert.ErrMsgContains(t, err, "is not an existing directory")
	})
	t.Run("a file is refused", func(t *testing.T) {
		_, err := Entry(file)
		assert.ErrMsgContains(t, err, "is not an existing directory")
	})
	t.Run("an empty value is refused", func(t *testing.T) {
		_, err := Entry("  ")
		assert.ErrMsgContains(t, err, "is empty")
		_, err = Entry(":rw")
		assert.ErrMsgContains(t, err, "names no directory")
	})
	t.Run("~ is the home directory", func(t *testing.T) {
		home := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(home, "src"), 0o755))
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		got, err := Entry("~/src:rw")
		assert.NoErr(t, err)
		assert.Eq(t, filepath.Join(home, "src"), got.Path)
		assert.True(t, got.Writable)
	})
}

func TestParse(t *testing.T) {
	dir := t.TempDir()

	t.Run("flag values keep their order and modes", func(t *testing.T) {
		dirs, err := Parse([]string{dir + ":rw", "", "  "})
		assert.NoErr(t, err)
		assert.Eq(t, []Dir{{Path: dir, Writable: true, Source: SourceFlag}}, dirs)
	})
	t.Run("a bad flag value is an error", func(t *testing.T) {
		_, err := Parse([]string{dir, "relative/dir"})
		assert.ErrMsgContains(t, err, "--add-dir:")
		assert.ErrMsgContains(t, err, "is not an absolute path")
	})
}

func TestParseEnv(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()

	t.Run("entries are separated and keep their modes", func(t *testing.T) {
		value := strings.Join([]string{a, b + ":rw"}, ",")
		dirs, warnings := ParseEnv(value)
		assert.Empty(t, warnings)
		assert.Eq(t, []Dir{{Path: a, Source: SourceEnv}, {Path: b, Writable: true, Source: SourceEnv}}, dirs)
	})
	t.Run("a bad entry warns and is skipped", func(t *testing.T) {
		value := strings.Join([]string{a, "relative", filepath.Join(b, "missing"), b + ":rw"}, ",")
		dirs, warnings := ParseEnv(value)
		assert.Eq(t, []Dir{{Path: a, Source: SourceEnv}, {Path: b, Writable: true, Source: SourceEnv}}, dirs)
		assert.Eq(t, 2, len(warnings))
		assert.StrContains(t, warnings[0], EnvVar+": skipping")
		assert.StrContains(t, warnings[0], "not an absolute path")
		assert.StrContains(t, warnings[1], "not an existing directory")
	})
	t.Run("an empty value is no directory", func(t *testing.T) {
		dirs, warnings := ParseEnv("")
		assert.Empty(t, dirs)
		assert.Empty(t, warnings)
	})
	t.Run("empty entries and surrounding whitespace are ignored", func(t *testing.T) {
		dirs, warnings := ParseEnv(" ,  " + a + " :ro , , " + b + ":rw  ")
		assert.Empty(t, warnings)
		assert.Eq(t, []Dir{{Path: a, Source: SourceEnv}, {Path: b, Writable: true, Source: SourceEnv}}, dirs)
	})
	t.Run("a single entry is accepted", func(t *testing.T) {
		dirs, warnings := ParseEnv("  " + a + "  ")
		assert.Empty(t, warnings)
		assert.Eq(t, []Dir{{Path: a, Source: SourceEnv}}, dirs)
	})
	t.Run("Windows drive colons stay inside comma-separated entries", func(t *testing.T) {
		dirs, warnings := ParseEnv(`C:\a:rw,D:\b`)
		assert.Empty(t, dirs)
		assert.Eq(t, 2, len(warnings))
		assert.StrContains(t, warnings[0], `C:\\a:rw`)
		assert.StrContains(t, warnings[1], `D:\\b`)
	})
}

func TestResolve(t *testing.T) {
	flagDir, envDir := t.TempDir(), t.TempDir()

	t.Run("flag entries come first", func(t *testing.T) {
		dirs, warnings, err := Resolve([]string{flagDir + ":rw"}, envDir, false)
		assert.NoErr(t, err)
		assert.Empty(t, warnings)
		assert.Eq(t, []Dir{{Path: flagDir, Writable: true, Source: SourceFlag}, {Path: envDir, Source: SourceEnv}}, dirs)
	})
	t.Run("the same path in both keeps the flag mode", func(t *testing.T) {
		dirs, _, err := Resolve([]string{flagDir + ":rw"}, flagDir, false)
		assert.NoErr(t, err)
		assert.Eq(t, []Dir{{Path: flagDir, Writable: true, Source: SourceFlag}}, dirs)
	})
	t.Run("read-only warns about a :rw entry", func(t *testing.T) {
		dirs, warnings, err := Resolve([]string{flagDir + ":rw", envDir}, "", true)
		assert.NoErr(t, err)
		assert.Eq(t, []Dir{{Path: flagDir, Writable: true, Source: SourceFlag}, {Path: envDir, Source: SourceFlag}}, dirs)
		assert.Eq(t, 1, len(warnings))
		assert.StrContains(t, warnings[0], "--read-only:")
	})
	t.Run("read-only does not warn without :rw", func(t *testing.T) {
		_, warnings, err := Resolve([]string{flagDir}, "", true)
		assert.NoErr(t, err)
		assert.Empty(t, warnings)
	})
	t.Run("a bad flag value fails the whole resolve", func(t *testing.T) {
		_, _, err := Resolve([]string{"nope"}, envDir, false)
		assert.ErrMsgContains(t, err, "--add-dir:")
	})
}

func TestMerge(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()

	t.Run("stored first, new paths appended", func(t *testing.T) {
		got := Merge([]Dir{{Path: a}}, []Dir{{Path: b, Writable: true}})
		assert.Eq(t, []Dir{{Path: a}, {Path: b, Writable: true}}, got)
	})
	t.Run("a path given again takes the new mode", func(t *testing.T) {
		got := Merge([]Dir{{Path: a, Writable: true}, {Path: b}}, []Dir{{Path: a}, {Path: c, Writable: true}})
		assert.Eq(t, []Dir{{Path: a}, {Path: b}, {Path: c, Writable: true}}, got)
	})
	t.Run("a path given again takes the new source", func(t *testing.T) {
		got := Merge([]Dir{{Path: a, Source: SourceFlag}}, []Dir{{Path: a, Writable: true, Source: SourceAsk}})
		assert.Eq(t, []Dir{{Path: a, Writable: true, Source: SourceAsk}}, got)
	})
	t.Run("equal sets do not need a rewrite", func(t *testing.T) {
		stored := []Dir{{Path: a}, {Path: b, Writable: true}}
		assert.True(t, Equal(stored, Merge(stored, nil)))
		assert.False(t, Equal(stored, Merge(stored, []Dir{{Path: b}})))
	})
	t.Run("a changed source needs a rewrite", func(t *testing.T) {
		assert.False(t, Equal([]Dir{{Path: a, Source: SourceFlag}}, []Dir{{Path: a, Source: SourceSession}}))
	})
}

func TestEffective(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	dirs := []Dir{{Path: a}, {Path: b, Writable: true}}

	t.Run("a writable run keeps the modes", func(t *testing.T) {
		assert.Eq(t, dirs, Effective(dirs, false))
	})
	t.Run("a read-only run reports every directory read-only", func(t *testing.T) {
		assert.Eq(t, []Dir{{Path: a}, {Path: b}}, Effective(dirs, true))
	})
}

func TestMeta(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	dirs := []Dir{{Path: a}, {Path: b, Writable: true}}

	t.Run("a round trip through the store keeps the set", func(t *testing.T) {
		raw, err := json.Marshal(ToMeta(dirs))
		assert.NoErr(t, err)
		var stored any
		assert.NoErr(t, json.Unmarshal(raw, &stored))
		got, err := FromMeta(stored)
		assert.NoErr(t, err)
		assert.Eq(t, dirs, got)
	})
	t.Run("an absent value is an empty set", func(t *testing.T) {
		got, err := FromMeta(nil)
		assert.NoErr(t, err)
		assert.Empty(t, got)
	})
	t.Run("a malformed value is an error", func(t *testing.T) {
		_, err := FromMeta("not a list")
		assert.ErrMsgContains(t, err, MetaKey+": expected a list")
		for _, value := range []any{
			[]any{"not an object"},
			[]any{map[string]any{"path": "relative"}},
			[]any{map[string]any{"path": a, "mode": "read-write"}},
		} {
			_, err := FromMeta(value)
			assert.ErrMsgContains(t, err, MetaKey+"[0]:", value)
		}
	})
	t.Run("a missing mode is read-only", func(t *testing.T) {
		got, err := FromMeta([]any{map[string]any{"path": a}})
		assert.NoErr(t, err)
		assert.Eq(t, []Dir{{Path: a}}, got)
	})
	t.Run("the source is kept", func(t *testing.T) {
		raw, err := json.Marshal(ToMeta([]Dir{{Path: a, Source: SourceSession}, {Path: b, Writable: true, Source: SourceAsk}}))
		assert.NoErr(t, err)
		var stored any
		assert.NoErr(t, json.Unmarshal(raw, &stored))
		got, err := FromMeta(stored)
		assert.NoErr(t, err)
		assert.Eq(t, []Dir{{Path: a, Source: SourceSession}, {Path: b, Writable: true, Source: SourceAsk}}, got)
	})
	t.Run("an unknown source is dropped", func(t *testing.T) {
		got, err := FromMeta([]any{map[string]any{"path": a, "mode": "rw", "source": "guess"}})
		assert.NoErr(t, err)
		assert.Eq(t, []Dir{{Path: a, Writable: true}}, got)
	})
	t.Run("an entry without a source stays unknown", func(t *testing.T) {
		raw, err := json.Marshal(ToMeta([]Dir{{Path: a}}))
		assert.NoErr(t, err)
		assert.StrNotContains(t, string(raw), "source")
	})
}

func TestWithout(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()

	t.Run("the path is dropped and the order kept", func(t *testing.T) {
		got := Without([]Dir{{Path: a}, {Path: b, Writable: true}}, a)
		assert.Eq(t, []Dir{{Path: b, Writable: true}}, got)
	})
	t.Run("an absent path changes nothing", func(t *testing.T) {
		dirs := []Dir{{Path: a}}
		assert.Eq(t, dirs, Without(dirs, b))
	})
	t.Run("has reports membership", func(t *testing.T) {
		dirs := []Dir{{Path: a}}
		assert.True(t, Has(dirs, filepath.Clean(a)))
		assert.False(t, Has(dirs, b))
	})
}
