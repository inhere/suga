// Command polyglot converts the Go exercises of the Aider polyglot benchmark
// (a subset of Exercism's Go track) into scripts/eval task directories.
//
// Each exercise becomes one task: the stub code, go.mod and .docs/ go into the
// fixture, the exercise's *_test.go go into the acceptance directory (injected
// after the agent runs), and the check is `go test ./...`. The reference
// solution under .meta/ is never copied. See scripts/eval/README.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "polyglot:", err)
		os.Exit(1)
	}
}

// options holds the command line flags.
type options struct {
	src          string // exercises/practice directory of the polyglot repo
	out          string // directory that receives the task directories
	visibleTests bool   // also put the tests into the fixture
	only         string // glob of exercise names (default all)
}

func run() error {
	var o options
	flag.StringVar(&o.src, "src", "", "path to polyglot-benchmark/go/exercises/practice")
	flag.StringVar(&o.out, "out", "", "output directory for the generated task directories")
	flag.BoolVar(&o.visibleTests, "visible-tests", false, "also put the exercise tests into the fixture (the agent may run them; they are still injected as acceptance)")
	flag.StringVar(&o.only, "only", "*", "glob of exercise names to convert")
	flag.Parse()
	if o.src == "" || o.out == "" {
		return errors.New("need -src and -out")
	}
	names, err := convertAll(o.src, o.out, o.only, o.visibleTests)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no exercise matches %q", o.only)
	}
	// The output usually lies inside the suga module (tmp/eval-tasks/...): an
	// empty go.mod makes it a module of its own, so the generated fixtures and
	// acceptance tests are not packages of suga's `go vet ./...` and
	// `go test ./...` (their stubs do not compile on their own).
	if err := writeOutGoMod(o.out); err != nil {
		return err
	}
	fmt.Printf("converted %d exercises into %s:\n", len(names), o.out)
	for _, n := range names {
		fmt.Println(" ", n)
	}
	return nil
}

// convertAll converts every exercise directory under src that matches the
// name glob and returns the generated task names.
func convertAll(src, out, only string, visibleTests bool) ([]string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ok, err := filepath.Match(only, e.Name())
		if err != nil {
			return nil, fmt.Errorf("-only %q: %w", only, err)
		}
		if !ok {
			continue
		}
		task, err := convertExercise(src, e.Name(), out, visibleTests)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		names = append(names, task)
	}
	return names, nil
}

// convertExercise writes the task directory of one exercise and returns its
// task name.
func convertExercise(srcRoot, exercise, out string, visibleTests bool) (string, error) {
	src := filepath.Join(srcRoot, exercise)
	fx := exerciseFiles{}
	if err := fx.collect(src); err != nil {
		return "", err
	}
	if len(fx.stubs) == 0 {
		return "", errors.New("no stub .go file at the exercise root")
	}
	if len(fx.tests) == 0 {
		return "", errors.New("no *_test.go file at the exercise root")
	}
	stubs, err := copyFixture(src, exercise, out, &fx, visibleTests)
	if err != nil {
		return "", err
	}
	if err := copyAcceptance(src, exercise, out, fx.tests); err != nil {
		return "", err
	}
	return writeTaskYAML(out, exercise, stubs, visibleTests)
}

// exerciseFiles splits the files of one exercise into the fixture files and
// the tests, ignoring everything that belongs to the reference solution or to
// the website's extra content.
type exerciseFiles struct {
	files []string // slash-separated paths relative to the exercise root
	stubs []string // top-level non-test .go files (what the agent must complete)
	tests []string // top-level *_test.go files (acceptance)
}

// collect walks the exercise root. Hidden directories other than .docs
// (.meta holds the reference solution, .approaches/.articles are website
// content for humans) are skipped, as are files in subdirectories (an
// exercise is a single flat package).
func (f *exerciseFiles) collect(src string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if name != ".docs" {
				continue
			}
			docs, err := filepath.Glob(filepath.Join(src, name, "*"))
			if err != nil {
				return err
			}
			for _, d := range docs {
				if isMarkdown(d) {
					f.files = append(f.files, name+"/"+filepath.Base(d))
				}
			}
			continue
		}
		switch {
		case strings.HasSuffix(name, "_test.go"):
			f.tests = append(f.tests, name)
		case strings.HasSuffix(name, ".go"):
			f.files = append(f.files, name)
			f.stubs = append(f.stubs, name)
		case name == "go.mod":
			f.files = append(f.files, name)
		}
	}
	if len(f.files) == 0 || !contains(f.files, "go.mod") {
		return errors.New("exercise has no go.mod")
	}
	sort.Strings(f.files)
	sort.Strings(f.stubs)
	sort.Strings(f.tests)
	return nil
}

func isMarkdown(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".md" || ext == ".txt"
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// copyFixture writes the fixture: stub code, go.mod, .docs, and in visible
// mode the tests too. It returns the stub file names for the prompt.
func copyFixture(src, exercise, out string, fx *exerciseFiles, visibleTests bool) ([]string, error) {
	dst := filepath.Join(out, taskName(exercise), "fixture")
	files := fx.files
	if visibleTests {
		files = append(append([]string{}, files...), fx.tests...)
	}
	for _, rel := range files {
		if err := copyFile(filepath.Join(src, filepath.FromSlash(rel)), filepath.Join(dst, filepath.FromSlash(rel))); err != nil {
			return nil, err
		}
	}
	return fx.stubs, nil
}

// copyAcceptance writes the tests into acceptance/, which the runner copies
// into the workspace after the agent runs (overwriting same-named files).
func copyAcceptance(src, exercise, out string, tests []string) error {
	for _, rel := range tests {
		if err := copyFile(filepath.Join(src, rel), filepath.Join(out, taskName(exercise), "acceptance", rel)); err != nil {
			return err
		}
	}
	return nil
}

// taskFile mirrors the fields of scripts/eval's task.yaml that the converter
// sets (the runner's own struct is in package main of scripts/eval).
type taskFile struct {
	Name       string  `yaml:"name"`
	Mode       string  `yaml:"mode"`
	FakeMode   string  `yaml:"fake_mode"`
	Prompt     string  `yaml:"prompt"`
	Timeout    string  `yaml:"timeout"`
	Acceptance string  `yaml:"acceptance"`
	Checks     []check `yaml:"checks"`
}

type check struct {
	Type string   `yaml:"type"`
	Args []string `yaml:"args"`
}

// writeTaskYAML writes task.yaml and returns the task name.
func writeTaskYAML(out, exercise string, stubs []string, visibleTests bool) (string, error) {
	name := taskName(exercise)
	t := taskFile{
		Name:       name,
		Mode:       "allow-write",
		FakeMode:   "edit",
		Prompt:     promptFor(stubs, visibleTests),
		Timeout:    "5m",
		Acceptance: "acceptance",
		Checks:     []check{{Type: "command", Args: []string{"go", "test", "./..."}}},
	}
	raw, err := yaml.Marshal(t)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(out, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "task.yaml"), raw, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// promptFor builds the Chinese prompt of one exercise. The tests are either
// visible in the fixture or injected only after the run.
func promptFor(stubs []string, visibleTests bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "按 .docs/instructions.md（若有 instructions.append.md 也一并遵循）实现 %s 里的函数与类型。", strings.Join(stubs, "、"))
	b.WriteString("这是 Exercism 的 Go 练习：只修改这些桩文件，不要修改或删除测试文件，不要改函数名与类型名；桩代码里未定义的类型或占位签名按说明补全。只使用标准库。")
	if visibleTests {
		b.WriteString("本次测试文件也在工作区里，你可以运行 go test ./... 验证，直到测试通过。")
	} else {
		b.WriteString("本次不提供测试文件：完成后你可以自己写测试验证行为（不要把自写测试放进仓库以外的地方），评测会在你的改动结束后注入原始测试并运行 go test ./...。")
	}
	return b.String()
}

// taskName maps an exercise name to its eval task name.
func taskName(exercise string) string { return "polyglot-go-" + exercise }

// writeOutGoMod writes <out>/go.mod (module polyglotout) unless one exists.
func writeOutGoMod(out string) error {
	path := filepath.Join(out, "go.mod")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte("module polyglotout\n"), 0o644)
}

// copyFile copies one file, creating parent directories.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
