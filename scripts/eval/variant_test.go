package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/gookit/goutil/x/assert"
)

func TestParseEnvFile(t *testing.T) {
	t.Run("valid lines, comments and blanks skipped", func(t *testing.T) {
		env, err := parseEnvFile("v", []byte("# comment\n\nCASEVAR=hello world\nOTHER=1\n"))
		assert.NoErr(t, err)
		assert.Eq(t, map[string]string{"CASEVAR": "hello world", "OTHER": "1"}, env)
	})
	t.Run("line without = is rejected", func(t *testing.T) {
		_, err := parseEnvFile("v", []byte("BROKEN\n"))
		assert.Err(t, err)
	})
	t.Run("isolation variables are rejected", func(t *testing.T) {
		for _, k := range []string{"SUGA_HOME", "HOME", "USERPROFILE", "suga_config"} {
			_, err := parseEnvFile("v", []byte(k+"=x\n"))
			assert.Err(t, err)
		}
	})
}

func TestMergeConfig(t *testing.T) {
	base := map[string]any{
		"model":   "fake/m1",
		"context": map[string]any{"fold_at": 0.5, "keep": true},
	}
	top := map[string]any{
		"context": map[string]any{"fold_at": 0, "new": 1},
		"tools":   map[string]any{"allow_write": true},
	}
	mergeConfig(base, top)
	assert.Eq(t, "fake/m1", base["model"])                       // non-map base keys survive
	assert.Eq(t, true, base["context"].(map[string]any)["keep"]) // sibling keys survive
	assert.Eq(t, 0, base["context"].(map[string]any)["fold_at"])
	assert.Eq(t, 1, base["context"].(map[string]any)["new"])
	assert.Eq(t, true, base["tools"].(map[string]any)["allow_write"])
}

func TestLoadVariant(t *testing.T) {
	t.Run("full variant dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "fold-off")
		assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "workspace"), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("context:\n  fold_at: 0\n"), 0o644))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "env"), []byte("CASEVAR=1\n"), 0o644))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "workspace", "a.txt"), []byte("hi"), 0o644))

		v, err := loadVariant(dir)
		assert.NoErr(t, err)
		assert.Eq(t, "fold-off", v.Name)
		assert.Eq(t, "0", fmt.Sprint(v.Config["context"].(map[string]any)["fold_at"]))
		assert.Eq(t, "1", v.Env["CASEVAR"])
		assert.NotEmpty(t, v.WorkDir)
	})
	t.Run("empty dir gets no config, env or workspace", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "plain")
		assert.NoErr(t, os.MkdirAll(dir, 0o755))
		v, err := loadVariant(dir)
		assert.NoErr(t, err)
		assert.Eq(t, "plain", v.Name)
		assert.Nil(t, v.Config)
		assert.Nil(t, v.Env)
		assert.Eq(t, "", v.WorkDir)
	})
	t.Run("missing dir and dir with bad config fail", func(t *testing.T) {
		_, err := loadVariant(filepath.Join(t.TempDir(), "nope"))
		assert.Err(t, err)
		bad := filepath.Join(t.TempDir(), "bad")
		assert.NoErr(t, os.MkdirAll(bad, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(bad, "config.yaml"), []byte("{not yaml"), 0o644))
		_, err = loadVariant(bad)
		assert.Err(t, err)
	})
	t.Run("workspace file below a subdirectory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wsv")
		assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "workspace", ".agents", "skills"), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "workspace", ".agents", "skills", "s.md"), []byte("skill"), 0o644))
		v, err := loadVariant(dir)
		assert.NoErr(t, err)
		assert.NotEmpty(t, v.WorkDir)
	})
}

func TestWithVariantEnv(t *testing.T) {
	assert.Eq(t, []string{"A=1"}, withVariantEnv([]string{"A=1"}, nil))
	v := &Variant{Env: map[string]string{"B": "2"}}
	assert.Eq(t, []string{"A=1", "B=2"}, withVariantEnv([]string{"A=1"}, v))
}

func TestApplyVariantConfigOverlay(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	assert.NoErr(t, os.MkdirAll(home, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: fake/m1\nmemory:\n  user:\n    backend: files\n"), 0o600))
	v, err := loadVariant(func() string {
		d := filepath.Join(t.TempDir(), "v")
		assert.NoErr(t, os.MkdirAll(d, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(d, "config.yaml"), []byte("model: fake/m2\ncontext:\n  fold_at: 0\n"), 0o644))
		return d
	}())
	assert.NoErr(t, err)
	assert.NoErr(t, applyVariantConfig(home, v))

	var got struct {
		Model   string `yaml:"model"`
		Context struct {
			FoldAt float64 `yaml:"fold_at"`
		} `yaml:"context"`
		Memory map[string]any `yaml:"memory"`
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	assert.NoErr(t, err)
	assert.NoErr(t, yaml.Unmarshal(raw, &got))
	assert.Eq(t, "fake/m2", got.Model) // variant wins
	assert.Eq(t, 0.0, got.Context.FoldAt)
	assert.Eq(t, "files", got.Memory["user"].(map[string]any)["backend"]) // base leaf kept
}

func TestApplyVariantWorkspace(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "v", "workspace")
	assert.NoErr(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(src, "AGENTS.md"), []byte("variant rules"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644))
	ws := filepath.Join(root, "ws")
	assert.NoErr(t, os.MkdirAll(ws, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("task rules"), 0o644))

	assert.NoErr(t, applyVariantWorkspace(ws, nil)) // no variant: no change
	b, err := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	assert.NoErr(t, err)
	assert.Eq(t, "task rules", string(b))

	v := &Variant{WorkDir: src}
	assert.NoErr(t, applyVariantWorkspace(ws, v))
	b, err = os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	assert.NoErr(t, err)
	assert.Eq(t, "variant rules", string(b)) // same-named file overwritten
	_, err = os.Stat(filepath.Join(ws, "sub", "b.txt"))
	assert.NoErr(t, err)
}

// evalStub builds a stand-in suga binary for the variant e2e test: `run`
// answers with a stream-json result line carrying its CASEVAR env value (so
// the test sees the variant env), `inspect stats|trace` reply minimal JSON.
func evalStub(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "stub")
	assert.NoErr(t, os.MkdirAll(src, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(src, "main.go"), []byte(`package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "run" {
		fmt.Printf("{\"type\":\"result\",\"result\":\"run ok CASEVAR=%s\"}\n", os.Getenv("CASEVAR"))
		return
	}
	if len(os.Args) > 0 && os.Args[1] == "inspect" {
		if len(os.Args) > 2 && os.Args[2] == "stats" {
			fmt.Println(`+"`"+`{"models":[{"calls":1,"input_tokens":10,"output_tokens":5}],"tools":[]}`+"`"+`)
		} else {
			fmt.Println("[]")
		}
	}
}
`), 0o644))
	out := filepath.Join(t.TempDir(), "stub-suga.exe") // .exe (also valid on Linux)
	bo, err := exec.Command("go", "build", "-o", out, filepath.Join(src, "main.go")).CombinedOutput()
	if err != nil {
		t.Fatal("build stub:", err, string(bo))
	}
	return out
}

// TestVariantEndToEnd runs two variants through runTaskSet (the real loop of
// scripts/eval) against a stub suga binary: the config overlay lands in each
// variant's temp SUGA_HOME, the workspace files are copied, the variant env
// reaches the agent, results.jsonl carries variant and compare.md is written.
func TestVariantEndToEnd(t *testing.T) {
	stub := evalStub(t)
	base := t.TempDir()

	// Two variants: fold-off (fold_at 0) and fold-on (0.5), each with its own
	// env value and workspace file.
	for i, name := range []string{"fold-off", "fold-on"} {
		dir := filepath.Join(base, "variants", name)
		assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "workspace"), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "config.yaml"), fmt.Appendf(nil, "context:\n  fold_at: %d\n", i), 0o644))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "env"), fmt.Appendf(nil, "CASEVAR=v%denv\n", i+1), 0o644))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, "workspace", "extra.txt"), fmt.Appendf(nil, "mark-%s\n", name), 0o644))
	}

	taskDir := filepath.Join(base, "tasks", "vare2e")
	assert.NoErr(t, os.MkdirAll(filepath.Join(taskDir, "fixture"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(taskDir, "fixture", "go.mod"), []byte("module evalfixture/vare2e\n"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(taskDir, "task.yaml"), []byte("name: vare2e\nprompt: hi\nchecks:\n  - {type: answer_contains, text: \"run ok\"}\n"), 0o644))

	out := filepath.Join(base, "out")
	assert.NoErr(t, os.MkdirAll(out, 0o755))
	variants, err := loadVariants([]string{
		filepath.Join(base, "variants", "fold-off"),
		filepath.Join(base, "variants", "fold-on"),
	})
	assert.NoErr(t, err)
	assert.Eq(t, 2, len(variants))

	tasks, err := loadTasks(filepath.Join(base, "tasks"), "*")
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(tasks))

	jf, err := os.Create(filepath.Join(out, "results.jsonl"))
	assert.NoErr(t, err)
	defer jf.Close()
	o := &options{bin: stub, agent: "suga", out: out, timeout: time.Minute, tasksDir: filepath.Join(base, "tasks"), repeat: 1}
	assert.NoErr(t, runTaskSet(o, stub, "20260101-000000", tasks, variants, jf))

	for i, name := range []string{"fold-off", "fold-on"} {
		root := filepath.Join(out, name, "vare2e")
		t.Run("variant "+name, func(t *testing.T) {
			// Config overlay landed in the temp SUGA_HOME config.
			raw, err := os.ReadFile(filepath.Join(root, "home", "config.yaml"))
			assert.NoErr(t, err)
			assert.True(t, strings.Contains(string(raw), fmt.Sprintf("fold_at: %d", i)))
			// Workspace file copied in.
			wraw, err := os.ReadFile(filepath.Join(root, "ws", "extra.txt"))
			assert.NoErr(t, err)
			assert.Eq(t, fmt.Sprintf("mark-%s\n", name), string(wraw))
		})
	}

	// results.jsonl: one line per variant with the variant field, the stub's
	// answer carries the per-variant CASEVAR value.
	fr, err := os.ReadFile(filepath.Join(out, "results.jsonl"))
	assert.NoErr(t, err)
	lines := strings.Split(strings.TrimSpace(string(fr)), "\n")
	assert.Eq(t, 2, len(lines))
	var res []Result
	for _, l := range lines {
		var r Result
		assert.NoErr(t, json.Unmarshal([]byte(l), &r))
		res = append(res, r)
	}
	assert.Eq(t, "fold-off", res[0].Variant)
	assert.Eq(t, "fold-on", res[1].Variant)
	for i, res1 := range res {
		t.Logf("e2e debug %d: answer=%q error=%q", i, res1.Answer, res1.Error)
	}
	assert.True(t, strings.Contains(res[0].Answer, "CASEVAR=v1env"), res[0].Answer)
	assert.True(t, strings.Contains(res[1].Answer, "CASEVAR=v2env"), res[1].Answer)
	assert.True(t, res[0].Passed)
	assert.True(t, res[1].Passed)

	// compare.md exists and lists both variants and the task.
	cmp, err := os.ReadFile(filepath.Join(out, "compare.md"))
	assert.NoErr(t, err)
	cs := string(cmp)
	assert.True(t, strings.Contains(cs, "fold-off"))
	assert.True(t, strings.Contains(cs, "fold-on"))
	assert.True(t, strings.Contains(cs, "vare2e"))
}

// TestVariantNoVariantUnchanged: without --variant the output structure is
// exactly the old one (<out>/<task>/, no variant field, no compare.md).
func TestVariantNoVariantUnchanged(t *testing.T) {
	stub := evalStub(t)
	base := t.TempDir()
	taskDir := filepath.Join(base, "tasks", "plain")
	assert.NoErr(t, os.MkdirAll(filepath.Join(taskDir, "fixture"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(taskDir, "fixture", "go.mod"), []byte("module evalfixture/plain\n"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(taskDir, "task.yaml"), []byte("name: plain\nprompt: hi\n"), 0o644))

	out := filepath.Join(base, "out")
	assert.NoErr(t, os.MkdirAll(out, 0o755))
	tasks, err := loadTasks(filepath.Join(base, "tasks"), "*")
	assert.NoErr(t, err)
	jf, err := os.Create(filepath.Join(out, "results.jsonl"))
	assert.NoErr(t, err)
	defer jf.Close()
	o := &options{bin: stub, agent: "suga", out: out, timeout: time.Minute, tasksDir: filepath.Join(base, "tasks"), repeat: 1}
	assert.NoErr(t, runTaskSet(o, stub, "20260101-000000", tasks, nil, jf))

	// Still directly under out/, no compare.md, no variant field.
	if _, err := os.Stat(filepath.Join(out, "plain", "ws")); err != nil {
		t.Fatal("expected <out>/plain/ws:", err)
	}
	if _, err := os.Stat(filepath.Join(out, "compare.md")); err == nil {
		t.Fatal("compare.md must not be written without --variant")
	}
	fr, err := os.ReadFile(filepath.Join(out, "results.jsonl"))
	assert.NoErr(t, err)
	assert.True(t, !strings.Contains(string(fr), "variant"), string(fr))
}

func TestWriteCompareMarksDifferences(t *testing.T) {
	rs := []Result{
		{Task: "a", Variant: "v1", Passed: true, Calls: 5, InputTokens: 100, DurationMs: 8000, CacheHit: 0.2},
		{Task: "a", Variant: "v2", Error: "run: context deadline exceeded", Calls: 3, InputTokens: 50, DurationMs: 1000},
	}
	out := t.TempDir()
	assert.NoErr(t, writeCompare(out, []*Variant{{Name: "v1"}, {Name: "v2"}}, rs))
	raw, err := os.ReadFile(filepath.Join(out, "compare.md"))
	assert.NoErr(t, err)
	s := string(raw)
	assert.True(t, strings.Contains(s, "TIMEOUT"))
	assert.True(t, strings.Contains(s, "**是**")) // statuses differ
	assert.True(t, strings.Contains(s, "1/1"))   // variant 总览通过数
}
