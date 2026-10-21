// Command eval runs the suga evaluation tasks in scripts/eval/testdata/tasks
// in isolated temporary workspaces and SUGA_HOMEs. See README.md.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/goccy/go-yaml"
)

type options struct {
	bin, task, model, configFrom, out string
	agent, ompBin, claudeBin          string
	claudeConfigFrom, claudeArgs      string
	fake                              bool
	timeout                           time.Duration
	tasksDir                          string
	probe                             bool
	probeRepeat                       int
	probeGap                          time.Duration
	sugaArgs, ompArgs                 string
	sugaSet                           setFlags // --suga-set key=value config overrides
	variants                          variantFlags
	repeat                            int
}

// setFlags collects repeated key=value flags.
type setFlags []string

func (s *setFlags) String() string     { return strings.Join(*s, ",") }
func (s *setFlags) Set(v string) error { *s = append(*s, v); return nil }

// Result is one results.jsonl line.
type Result struct {
	Task        string  `json:"task"`
	Round       int     `json:"round"`
	Agent       string  `json:"agent"`
	AgentArgs   string  `json:"agent_args,omitempty"`
	Passed      bool    `json:"passed"`
	Error       string  `json:"error,omitempty"`
	SessionID   string  `json:"session_id"`
	Mode        string  `json:"mode"`
	Model       string  `json:"model"`
	Fake        bool    `json:"fake"`
	DurationMs  int64   `json:"duration_ms"`
	Calls       int     `json:"calls"`
	InputTokens int64   `json:"input_tokens"`
	OutputToks  int64   `json:"output_tokens"`
	CacheRead   int64   `json:"cache_read_tokens"`
	CacheWrite  int64   `json:"cache_write_tokens"`
	CacheHit    float64 `json:"cache_hit_rate"`
	// FirstUncached is the uncached input of the first main-loop model call
	// (suga: input - cache_read - cache_write from the trace; omp: usage.input).
	// nil when unavailable (no usable model event).
	FirstUncached *int64         `json:"first_call_uncached"`
	CostUSD       float64        `json:"cost_usd"`
	ToolCalls     int            `json:"tool_calls"`
	ToolCounts    map[string]int `json:"tool_counts,omitempty"`
	RawBytes      *int64         `json:"tool_raw_bytes"`
	InlineBytes   *int64         `json:"tool_inline_bytes"`
	Spilled       int            `json:"tool_spilled"`
	Answer        string         `json:"answer"`
	Checks        []CheckResult  `json:"checks"`
	// Variant names the --variant dir this run belongs to ("" = no --variant).
	Variant string `json:"variant,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

func run() error {
	var o options
	flag.StringVar(&o.bin, "bin", filepath.Join("tmp", "smoke", "suga"), "suga executable (.exe is added on Windows)")
	flag.StringVar(&o.task, "task", "*", "glob of task names")
	flag.StringVar(&o.model, "model", "", "model passed to `suga run --model`")
	flag.BoolVar(&o.fake, "fake", false, "use scripts/ptysmoke/fakellm.py (pipeline check only)")
	flag.StringVar(&o.configFrom, "config-from", "", "directory whose config.yaml and .env are copied (read-only) into the temp SUGA_HOME")
	flag.StringVar(&o.out, "out", "", "output directory (default tmp/eval/<run-id>)")
	flag.DurationVar(&o.timeout, "timeout", 10*time.Minute, "per-task timeout (a task's own timeout field overrides it)")
	flag.StringVar(&o.agent, "agent", "suga", "agent under test: suga, omp (oh-my-pi) or claude (Claude Code); same model via --model")
	flag.StringVar(&o.ompBin, "omp-bin", "omp", "omp executable for --agent omp")
	flag.StringVar(&o.claudeBin, "claude-bin", "claude", "Claude Code executable for --agent claude")
	flag.StringVar(&o.claudeConfigFrom, "claude-config-from", "", "directory whose settings.json env (gateway URL, token, model mapping) the temp CLAUDE_CONFIG_DIR gets (default ~/.claude)")
	flag.StringVar(&o.claudeArgs, "claude-args", "", "extra `claude -p` arguments placed before the prompt (split on whitespace)")
	flag.StringVar(&o.tasksDir, "tasks-dir", filepath.Join("scripts", "eval", "testdata", "tasks"), "tasks directory")
	flag.BoolVar(&o.probe, "probe-prefix", false, "run the prefix-cache probe (exclusive with tasks; suga only)")
	flag.IntVar(&o.probeRepeat, "probe-repeat", 3, "probe: repetitions per group")
	flag.DurationVar(&o.probeGap, "probe-gap", 5*time.Second, "probe: pause between two requests")
	flag.Var(&o.sugaSet, "suga-set", "set a key in the temp suga config.yaml, as dotted.key=value (YAML scalar); repeatable")
	flag.StringVar(&o.sugaArgs, "suga-args", "", "extra `suga run` arguments appended after the built-in ones (split on whitespace)")
	flag.StringVar(&o.ompArgs, "omp-args", "", "extra `omp -p` arguments placed before the prompt (split on whitespace)")
	flag.Var(&o.variants, "variant", "a variant directory (config.yaml overlay + env + workspace files); repeatable (suga only)")
	flag.IntVar(&o.repeat, "repeat", 1, "rounds per task (outputs go to <out>/<task>-r<i>/ when > 1)")
	flag.Parse()
	if o.repeat < 1 {
		return errors.New("--repeat must be >= 1")
	}
	taskSet := false
	flag.Visit(func(f *flag.Flag) { taskSet = taskSet || f.Name == "task" || f.Name == "tasks-dir" })
	if o.probe && (taskSet || o.agent != "suga") {
		return errors.New("--probe-prefix is exclusive with --task/--tasks-dir and needs --agent suga")
	}
	if o.probe && o.probeRepeat < 1 {
		return errors.New("--probe-repeat must be >= 1")
	}
	if len(o.variants) > 0 && (o.probe || o.agent != "suga") {
		return errors.New("--variant works only with --agent suga (and is exclusive with --probe-prefix)")
	}

	if runtime.GOOS == "windows" && !strings.HasSuffix(o.bin, ".exe") {
		o.bin += ".exe"
	}
	bin, err := filepath.Abs(o.bin)
	if err != nil {
		return err
	}
	switch o.agent {
	case "suga":
		if _, err := os.Stat(bin); err != nil {
			return fmt.Errorf("suga binary: %w", err)
		}
		if !o.fake && o.configFrom == "" {
			return errors.New("need --fake or --config-from <dir>")
		}
	case "omp", "claude":
		if o.fake {
			return errors.New("--fake works only with --agent suga")
		}
	default:
		return fmt.Errorf("unknown --agent %q (suga|omp|claude)", o.agent)
	}
	runID := time.Now().Format("20060102-150405")
	if o.out == "" {
		o.out = filepath.Join("tmp", "eval", runID)
	}
	if o.out, err = filepath.Abs(o.out); err != nil {
		return err
	}
	if o.probe {
		return runProbe(&o, bin, runID)
	}
	tasks, err := loadTasks(o.tasksDir, o.task)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return fmt.Errorf("no task matches %q", o.task)
	}
	variants, err := loadVariants(o.variants)
	if err != nil {
		return err
	}
	for _, v := range variants { // fail fast on duplicate variant names: their output dirs would collide
		for _, w := range variants {
			if w != v && w.Name == v.Name {
				return fmt.Errorf("duplicate --variant name %q", v.Name)
			}
		}
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	// The output usually lies inside the suga module (tmp/eval/...): an
	// empty go.mod makes it a module of its own, so the Go files a run
	// leaves outside its workspace (the trash of file_remove under home/)
	// are not packages of suga's "go test ./...".
	if err := writeOutGoMod(o.out); err != nil {
		return err
	}
	jf, err := os.Create(filepath.Join(o.out, "results.jsonl"))
	if err != nil {
		return err
	}
	defer jf.Close()

	return runTaskSet(&o, bin, runID, tasks, variants, jf)
}

// runTaskSet runs every task once per variant (a run without --variant has a
// single nil "variant" and behaves exactly as before), appending one
// results.jsonl line per run; with variants it also writes <out>/compare.md.
func runTaskSet(o *options, bin, runID string, tasks []*Task, variants []*Variant, jf io.Writer) error {
	enc := json.NewEncoder(jf)

	var results []Result
	for _, t := range tasks {
		for _, v := range variantSet(variants) {
			for round := 1; round <= o.repeat; round++ {
				r, err := runTask(o, bin, runID, t, round, v)
				if err != nil { // runner-side failure of this task: record and go on
					r.Error = err.Error()
				}
				results = append(results, r)
				if err := enc.Encode(r); err != nil {
					return err
				}
			}
		}
	}
	printTable(os.Stdout, results, o.repeat > 1)
	if len(variants) > 0 {
		if err := writeCompare(o.out, variants, results); err != nil {
			return err
		}
	}
	fmt.Printf("\nresults: %s\n", filepath.Join(o.out, "results.jsonl"))
	return nil
}

func runTask(o *options, bin, runID string, t *Task, round int, v *Variant) (Result, error) {
	name := t.Name
	if o.repeat > 1 {
		name = fmt.Sprintf("%s-r%d", t.Name, round)
	}
	root := filepath.Join(o.out, name)
	suffix := ""
	if v != nil { // a variant run lives under <out>/<variant>/ and gets its own session id prefix
		root = filepath.Join(o.out, v.Name, name)
		suffix = "-" + v.Name
	}
	sid := "eval-" + runID + suffix + "-" + name
	r := Result{Task: t.Name, Round: round, Agent: o.agent, SessionID: sid, Mode: t.Mode, Model: o.model, Fake: o.fake, Variant: variantName(v)}
	switch o.agent {
	case "omp":
		r.AgentArgs = strings.Join(splitArgs(o.ompArgs), " ")
	case "claude":
		r.AgentArgs = strings.Join(splitArgs(o.claudeArgs), " ")
	default:
		r.AgentArgs = strings.Join(append(splitArgs(o.sugaArgs), o.sugaSet...), " ")
	}
	ws, home := filepath.Join(root, "ws"), filepath.Join(root, "home")
	timeout := t.effectiveTimeout(o.timeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := t.prepareWorkspace(ctx, ws); err != nil {
		return r, err
	}
	if err := applyVariantWorkspace(ws, v); err != nil {
		return r, err
	}
	if o.agent == "omp" {
		r.SessionID = ""
		runOmp(ctx, o, t, timeout, ws, filepath.Join(root, "omp-sessions"), &r)
		return r, nil
	}
	if o.agent == "claude" {
		r.SessionID = ""
		runClaude(ctx, o, t, timeout, ws, filepath.Join(root, "claude-home"), &r)
		return r, nil
	}
	model, stop, err := setupSugaHome(ctx, o, home, t.FakeMode, v)
	if err != nil {
		return r, err
	}
	defer stop()

	env := withVariantEnv(isolatedEnv(home), v)
	// A multi-turn task sends its prompts in order to the same session; the
	// run stops at the first failing turn and checks judge the last answer.
	start := time.Now()
	var stdout []byte
	var runErr error
	for _, prompt := range t.Turns() {
		stdout, runErr = runCmd(ctx, bin, sugaRunArgs(sid, prompt, model, t.Mode == "allow-write", splitArgs(o.sugaArgs)), ws, env)
		if runErr != nil {
			break
		}
	}
	r.DurationMs = time.Since(start).Milliseconds()
	if runErr != nil {
		r.Error = runErr.Error()
	}
	r.Answer = finalAnswer(stdout)
	t.finish(ws, r.Answer, &r)
	r.Passed = runErr == nil && allPassed(r.Checks)

	sout, err := runCmd(context.Background(), bin, []string{"inspect", "stats", "--session", sid, "--json"}, ws, env)
	if err != nil {
		if r.Error == "" {
			r.Error = "stats: " + err.Error()
		}
	} else {
		parseStats(sout, &r)
	}
	if tout, err := runCmd(context.Background(), bin, []string{"inspect", "trace", sid, "--json"}, ws, env); err == nil {
		if c, ok := firstMainCall(tout); ok {
			v := c.uncached()
			r.FirstUncached = &v
		}
	}
	return r, nil
}

// sugaRunArgs builds the `suga run` arguments of one turn.
// extra (from --suga-args) goes last: `suga run` takes only flags, so their
// position relative to --prompt does not matter.
func sugaRunArgs(sid, prompt, model string, allowWrite bool, extra []string) []string {
	args := []string{"run", "--session-id", sid, "--output-format", "stream-json", "--prompt", prompt}
	if model != "" {
		args = append(args, "--model", model)
	}
	if allowWrite {
		args = append(args, "--allow-write")
	}
	return append(args, extra...)
}

// setupSugaHome creates home with an isolated config (fake provider with a
// fresh fakellm, or a copy of --config-from, then the variant overlay) and
// returns the model to pass (empty = config default) and a stop func for the
// fake server.
func setupSugaHome(ctx context.Context, o *options, home, fakeMode string, v *Variant) (string, func(), error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", nil, err
	}
	if !o.fake {
		model, stop, err := o.model, func() {}, writeConfig(home, o.configFrom, 0, o.sugaSet)
		return model, stop, firstErr(err, applyVariantConfig(home, v))
	}
	stop, port, err := startFake(ctx, fakeMode)
	if err != nil {
		return "", nil, err
	}
	if err := writeConfig(home, "", port, o.sugaSet); err != nil {
		stop()
		return "", nil, err
	}
	if err := applyVariantConfig(home, v); err != nil {
		stop()
		return "", nil, err
	}
	model := o.model
	if model == "" {
		model = "fake/m1"
	}
	return model, stop, nil
}

// firstErr returns the first non-nil error in errs (nil when all pass).
func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// runCmd runs bin in dir with env and returns its stdout. A non-zero exit
// includes the tail of stderr in the error.
func runCmd(ctx context.Context, bin string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.WaitDelay = 5 * time.Second
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	if err != nil {
		tail := strings.TrimSpace(se.String())
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return so.Bytes(), fmt.Errorf("%s %s: %v: %s", filepath.Base(bin), args[0], err, tail)
	}
	return so.Bytes(), nil
}

// isolatedEnv builds the environment of a task's child process: the caller's
// environment minus the variables that would point the agent at a real home or
// config instead of the task's temp ones.
func isolatedEnv(home string) []string {
	drop := map[string]bool{"SUGA_HOME": true, "SUGA_CONFIG": true, "SUGA_MODEL": true, "HOME": true, "USERPROFILE": true}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[strings.ToUpper(k)] {
			env = append(env, kv)
		}
	}
	env = append(env, "SUGA_HOME="+home, "HOME="+home)
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home)
	}
	return env
}

// finalAnswer returns the "result" field of the last stream-json result line.
func finalAnswer(stdout []byte) string {
	var answer string
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Type   string `json:"type"`
			Result string `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Type == "result" {
			answer = line.Result
		}
	}
	return answer
}

func parseStats(raw []byte, r *Result) {
	var rep struct {
		Models []struct {
			Calls      int     `json:"calls"`
			In         int64   `json:"input_tokens"`
			Out        int64   `json:"output_tokens"`
			CacheRead  int64   `json:"cache_read_tokens"`
			CacheWrite int64   `json:"cache_write_tokens"`
			Cost       float64 `json:"cost_usd"`
		} `json:"models"`
		Tools []struct {
			Name        string `json:"name"`
			Calls       int    `json:"calls"`
			RawBytes    *int64 `json:"raw_bytes"`
			InlineBytes *int64 `json:"inline_bytes"`
			Spilled     int    `json:"spilled"`
		} `json:"tools"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &rep) != nil {
		return
	}
	for _, m := range rep.Models {
		r.Calls += m.Calls
		r.InputTokens += m.In
		r.OutputToks += m.Out
		r.CacheRead += m.CacheRead
		r.CacheWrite += m.CacheWrite
		r.CostUSD += m.Cost
	}
	if total := r.InputTokens + r.CacheRead + r.CacheWrite; total > 0 {
		r.CacheHit = float64(r.CacheRead) / float64(total)
	}
	for _, t := range rep.Tools {
		r.ToolCalls += t.Calls
		if t.Name != "" && t.Calls > 0 {
			if r.ToolCounts == nil {
				r.ToolCounts = map[string]int{}
			}
			r.ToolCounts[t.Name] += t.Calls
		}
		r.RawBytes = addPtr(r.RawBytes, t.RawBytes)
		r.InlineBytes = addPtr(r.InlineBytes, t.InlineBytes)
		r.Spilled += t.Spilled
	}
}

func addPtr(a, b *int64) *int64 {
	if b == nil {
		return a
	}
	v := *b
	if a != nil {
		v += *a
	}
	return &v
}

// writeConfig writes <home>/config.yaml (copying fromDir's config.yaml and
// .env when fromDir is set, else a fake-provider config), forces the files
// memory backends and applies the dotted.key=value overrides in sets.
func writeConfig(home, fromDir string, fakePort int, sets []string) error {
	cfg := map[string]any{}
	if fromDir != "" {
		raw, err := os.ReadFile(filepath.Join(fromDir, "config.yaml"))
		if err != nil {
			return fmt.Errorf("--config-from: %w", err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("--config-from config.yaml: %w", err)
		}
		if env, err := os.ReadFile(filepath.Join(fromDir, ".env")); err == nil {
			if err := os.WriteFile(filepath.Join(home, ".env"), env, 0o600); err != nil {
				return err
			}
		}
	} else {
		cfg["model"] = "fake/m1"
		cfg["providers"] = map[string]any{"fake": map[string]any{
			"type": "openai", "base_url": fmt.Sprintf("http://127.0.0.1:%d/v1", fakePort), "api_key": "k"}}
	}
	mem, _ := cfg["memory"].(map[string]any)
	if mem == nil {
		mem = map[string]any{}
	}
	for _, scope := range []string{"user", "project"} {
		sm, _ := mem[scope].(map[string]any)
		if sm == nil {
			sm = map[string]any{}
		}
		sm["backend"] = "files"
		mem[scope] = sm
	}
	cfg["memory"] = mem
	for _, kv := range sets {
		if err := setConfigKey(cfg, kv); err != nil {
			return err
		}
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "config.yaml"), out, 0o600)
}

// setConfigKey applies one dotted.key=value override to cfg, creating
// intermediate maps; the value is parsed as a YAML scalar (true, 3, text).
func setConfigKey(cfg map[string]any, kv string) error {
	key, value, ok := strings.Cut(kv, "=")
	if !ok || key == "" {
		return fmt.Errorf("--suga-set %q: want dotted.key=value", kv)
	}
	var v any
	if err := yaml.Unmarshal([]byte(value), &v); err != nil || v == nil {
		v = value
	}
	parts := strings.Split(key, ".")
	node := cfg
	for _, part := range parts[:len(parts)-1] {
		next, _ := node[part].(map[string]any)
		if next == nil {
			next = map[string]any{}
			node[part] = next
		}
		node = next
	}
	node[parts[len(parts)-1]] = v
	return nil
}

// startFake launches fakellm.py on a free port and returns a stop func.
func startFake(ctx context.Context, mode string) (func(), int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	script := filepath.Join("scripts", "ptysmoke", "fakellm.py")
	var lastErr error
	for _, py := range []string{"python3", "python"} {
		cmd := exec.CommandContext(ctx, py, "-I", script, "--port", fmt.Sprint(port), "--mode", mode, "--delay", "0.05")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, 0, err
		}
		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		if !strings.Contains(line, "listening") {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			lastErr = fmt.Errorf("%s: fake model did not start: %q", py, line)
			continue
		}
		go io.Copy(io.Discard, stdout) //nolint:errcheck // keep the pipe drained
		return func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }, port, nil
	}
	return nil, 0, lastErr
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func printTable(w io.Writer, rs []Result, repeated bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	// PROMPT is every prompt token sent (uncached + cache read/write), the
	// fair basis across agents; TOKENS(in/out) keeps uncached input / output.
	// FIRST is the uncached input of the first main-loop call ("-" if unknown).
	fmt.Fprintln(tw, "TASK\tAGENT\tRESULT\tCALLS\tTOOLS\tSPILL\tFIRST\tPROMPT\tTOKENS(in/out)\tHIT%\tCOST\tTIME\tARGS")
	for i, r := range rs {
		status := "FAIL"
		if r.Passed {
			status = "PASS"
		}
		prompt := r.InputTokens + r.CacheRead + r.CacheWrite
		first := "-"
		if r.FirstUncached != nil {
			first = fmt.Sprint(*r.FirstUncached)
		}
		label := r.Task
		if repeated {
			label = fmt.Sprintf("%s-r%d", r.Task, r.Round)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%d\t%d/%d\t%.1f\t$%.4f\t%.1fs\t%s\n", label, r.Agent, status, r.Calls, r.ToolCalls, r.Spilled,
			first, prompt, r.InputTokens, r.OutputToks, r.CacheHit*100, r.CostUSD, float64(r.DurationMs)/1000, truncArgs(r.AgentArgs, 24))
		if repeated && (i == len(rs)-1 || rs[i+1].Task != r.Task) {
			j := i
			for j > 0 && rs[j-1].Task == r.Task {
				j--
			}
			a := averageRounds(rs[j : i+1])
			fmt.Fprintf(tw, "%s\t%s\t%d/%d\t%.1f\t%.1f\t-\t-\t%.0f\t%.0f/%.0f\t-\t-\t%.1fs\t%s\n", r.Task+" (avg)", r.Agent, a.Passed, a.Rounds,
				a.Calls, a.ToolCalls, a.Prompt, a.Uncached, a.Out, a.Seconds, truncArgs(r.AgentArgs, 24))
		}
	}
	_ = tw.Flush()
}

// Average is the per-task mean over the rounds of a --repeat run.
type Average struct {
	Rounds, Passed int
	Calls          float64
	ToolCalls      float64
	Prompt         float64 // all prompt tokens (uncached + cache read/write)
	Uncached       float64 // uncached input tokens
	Out            float64
	Seconds        float64
}

// averageRounds averages the rounds of one task.
func averageRounds(rs []Result) Average {
	a := Average{Rounds: len(rs)}
	if len(rs) == 0 {
		return a
	}
	for _, r := range rs {
		if r.Passed {
			a.Passed++
		}
		a.Calls += float64(r.Calls)
		a.ToolCalls += float64(r.ToolCalls)
		a.Prompt += float64(r.InputTokens + r.CacheRead + r.CacheWrite)
		a.Uncached += float64(r.InputTokens)
		a.Out += float64(r.OutputToks)
		a.Seconds += float64(r.DurationMs) / 1000
	}
	n := float64(len(rs))
	a.Calls, a.ToolCalls, a.Prompt, a.Uncached, a.Out, a.Seconds = a.Calls/n, a.ToolCalls/n, a.Prompt/n, a.Uncached/n, a.Out/n, a.Seconds/n
	return a
}

func truncArgs(s string, n int) string {
	if s == "" {
		return "-"
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "~"
	}
	return s
}

// writeOutGoMod writes <out>/go.mod (module evalout) unless one exists.
func writeOutGoMod(out string) error {
	path := filepath.Join(out, "go.mod")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte("module evalout\n"), 0o644)
}
