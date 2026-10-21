package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/gookit/goutil/x/assert"
)

// These tests use the mini exercises in testdata/practice: two hand-written
// exercises with the same layout as an Exercism Go exercise (stub, tests,
// .docs, .meta reference solution), never a copy of a polyglot exercise.

// generatedTask mirrors the fields the converter writes to task.yaml.
type generatedTask struct {
	Name       string `yaml:"name"`
	Mode       string `yaml:"mode"`
	FakeMode   string `yaml:"fake_mode"`
	Prompt     string `yaml:"prompt"`
	Timeout    string `yaml:"timeout"`
	Acceptance string `yaml:"acceptance"`
	Checks     []struct {
		Type string   `yaml:"type"`
		Args []string `yaml:"args"`
	} `yaml:"checks"`
}

func loadGenerated(t *testing.T, out, name string) generatedTask {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(out, name, "task.yaml"))
	assert.NoErr(t, err)
	var g generatedTask
	assert.NoErr(t, yaml.Unmarshal(raw, &g))
	return g
}

// filesOf returns the slash-separated relative paths under dir.
func filesOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	assert.NoErr(t, err)
	return out
}

func TestConvertHiddenFixture(t *testing.T) {
	out := t.TempDir()
	task, err := convertExercise(filepath.Join("testdata", "practice"), "gizmo", out, false)
	assert.NoErr(t, err)
	assert.Eq(t, "polyglot-go-gizmo", task)

	fx := filesOf(t, filepath.Join(out, task, "fixture"))
	assert.Eq(t, []string{".docs/instructions.append.md", ".docs/instructions.md", "gizmo.go", "go.mod"}, fx)

	acc := filesOf(t, filepath.Join(out, task, "acceptance"))
	assert.Eq(t, []string{"cases_test.go", "gizmo_test.go"}, acc)

	g := loadGenerated(t, out, task)
	assert.Eq(t, "polyglot-go-gizmo", g.Name)
	assert.Eq(t, "allow-write", g.Mode)
	assert.Eq(t, "5m", g.Timeout)
	assert.Eq(t, "acceptance", g.Acceptance)
	assert.Eq(t, 1, len(g.Checks))
	assert.Eq(t, "command", g.Checks[0].Type)
	assert.Eq(t, []string{"go", "test", "./..."}, g.Checks[0].Args)
	assert.True(t, strings.Contains(g.Prompt, "instructions.md"))
	assert.True(t, strings.Contains(g.Prompt, "gizmo.go"))
	assert.True(t, strings.Contains(g.Prompt, "注入原始测试"))
	assert.False(t, strings.Contains(g.Prompt, "你可以运行 go test"))
}

func TestConvertVisibleTests(t *testing.T) {
	out := t.TempDir()
	task, err := convertExercise(filepath.Join("testdata", "practice"), "widget", out, true)
	assert.NoErr(t, err)

	fx := filesOf(t, filepath.Join(out, task, "fixture"))
	assert.Eq(t, []string{".docs/instructions.md", "go.mod", "widget.go", "widget_test.go"}, fx)
	// The tests stay in acceptance even when they are visible: the runner
	// injects them again after the run, so edits to them do not count.
	assert.Eq(t, []string{"widget_test.go"}, filesOf(t, filepath.Join(out, task, "acceptance")))

	g := loadGenerated(t, out, task)
	assert.True(t, strings.Contains(g.Prompt, "运行 go test ./..."))
	assert.False(t, strings.Contains(g.Prompt, "注入原始测试"))
}

func TestConvertOnlyGlob(t *testing.T) {
	out := t.TempDir()
	names, err := convertAll(filepath.Join("testdata", "practice"), out, "wid*", false)
	assert.NoErr(t, err)
	assert.Eq(t, []string{"polyglot-go-widget"}, names)
	_, err = os.Stat(filepath.Join(out, "polyglot-go-gizmo"))
	assert.True(t, os.IsNotExist(err))

	_, err = convertAll(filepath.Join("testdata", "practice"), out, "[", false)
	assert.Err(t, err)
}

func TestWriteOutGoMod(t *testing.T) {
	t.Run("creates", func(t *testing.T) {
		out := t.TempDir()
		assert.NoErr(t, writeOutGoMod(out))
		raw, err := os.ReadFile(filepath.Join(out, "go.mod"))
		assert.NoErr(t, err)
		assert.Eq(t, "module polyglotout\n", string(raw))
	})
	t.Run("keeps existing", func(t *testing.T) {
		out := t.TempDir()
		assert.NoErr(t, os.WriteFile(filepath.Join(out, "go.mod"), []byte("module mine\n"), 0o644))
		assert.NoErr(t, writeOutGoMod(out))
		raw, err := os.ReadFile(filepath.Join(out, "go.mod"))
		assert.NoErr(t, err)
		assert.Eq(t, "module mine\n", string(raw))
	})
}

func TestConvertRejectsIncompleteExercise(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(src, "empty"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(src, "empty", "only.go"), []byte("package empty\n"), 0o644))
	_, err := convertExercise(src, "empty", out, false)
	assert.Err(t, err) // no go.mod, no tests
}
