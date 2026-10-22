package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
)

// Task is one evaluation task loaded from testdata/tasks/<name>/task.yaml.
type Task struct {
	Name     string     `yaml:"name"`
	Prompt   string     `yaml:"prompt"`
	Prompts  []string   `yaml:"prompts"` // multi-turn: one session, sent in order; exclusive with prompt
	Mode     string     `yaml:"mode"`    // read-only | allow-write
	FakeMode string     `yaml:"fake_mode"`
	Generate []Generate `yaml:"generate"`
	Checks   []Check    `yaml:"checks"`
	// Timeout overrides the global --timeout for this task (e.g. "20m").
	Timeout string `yaml:"timeout"`
	// Acceptance names a subdirectory of the task directory whose files are
	// copied into the workspace (overwriting) after the agent finishes and
	// before the checks run: tests the agent never saw.
	Acceptance string `yaml:"acceptance"`

	// AllowWeb turns the agent's network tools back on for this task; without
	// it the run keeps web_fetch/web_search off (see the README's "禁网").
	AllowWeb bool `yaml:"allow_web"`
	// KeepTests leaves the test files the agent wrote in the workspace, for a
	// task whose checks judge that test file on purpose (default: judging only
	// sees the task's own and the injected acceptance tests).
	KeepTests bool `yaml:"keep_tests"`
	// CompactAfter lists 1-based turns after which the runner forces a context
	// compaction (a new segment) of the session, so a long-session task can
	// measure whether a constraint, a fact or a conclusion survives it. The
	// break is `suga compact <session>`, the CLI form of chat's :compact.
	CompactAfter []int `yaml:"compact_after"`
	// FakeReply is the text the fake model answers with (fakellm --reply); it
	// makes a --fake run of the task deterministic. Ignored on a real run.
	FakeReply string `yaml:"fake_reply"`

	// Repo makes the workspace a snapshot of a git ref of the repository the
	// eval is started in (exclusive with fixture/). See repo.go.
	Repo *RepoSpec `yaml:"repo"`

	dir     string        // task directory (holds fixture/)
	timeout time.Duration // parsed Timeout; 0 = use the global one
}

// Generate describes a file produced at run time inside the workspace, so big
// fixtures need not be committed. Each of Count lines is Line with {i}
// replaced by the 1-based line number; Inserts replace line At verbatim.
type Generate struct {
	Path    string   `yaml:"path"`
	Line    string   `yaml:"line"`
	Count   int      `yaml:"count"`
	Inserts []Insert `yaml:"inserts"`
}

// Insert replaces the generated line number At (1-based) with Text.
type Insert struct {
	At   int    `yaml:"at"`
	Text string `yaml:"text"`
}

// Check is one pass/fail assertion. Types: file_contains, file_equals,
// file_not_contains, file_matches, file_not_matches (path+text; the last two take
// a regular expression, so a line-anchored rule uses `(?m)^...$`), answer_contains,
// answer_not_contains, answer_matches, answer_not_matches (text only; the last two
// take a regular expression) and command (args [+ timeout]: exec in the workspace
// without a shell, exit 0 passes).
type Check struct {
	Type    string   `yaml:"type"`
	Path    string   `yaml:"path"`
	Text    string   `yaml:"text"`
	Args    []string `yaml:"args"`
	Timeout string   `yaml:"timeout"` // command only; default 5m
	// Turn judges the answer of this 1-based turn of a multi-turn task; 0 (the
	// default) judges the final answer.
	Turn int `yaml:"turn"`
}

// CheckResult is the recorded outcome of one Check.
type CheckResult struct {
	Type   string `json:"type"`
	Path   string `json:"path,omitempty"`
	Text   string `json:"text"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
	// Turn is the turn whose answer an answer check judged (0 for the file and
	// command checks and for a single-turn task).
	Turn int `json:"turn,omitempty"`
	// command checks: exit code (-1 when not started / timed out) and the
	// tail of the combined output.
	ExitCode *int   `json:"exit_code,omitempty"`
	Output   string `json:"output,omitempty"`
}

// TurnRun is one turn of a multi-turn task: its answer, how long it took and
// the forced compaction that ran after it (compact_after), if any.
type TurnRun struct {
	Turn       int             `json:"turn"`
	Answer     string          `json:"answer"`
	DurationMs int64           `json:"duration_ms"`
	Compacted  *TurnCompaction `json:"compacted,omitempty"`
}

// TurnCompaction is the segment break the runner forced after a turn
// (`suga compact <session> --json`).
type TurnCompaction struct {
	TokensBefore int `json:"tokens_before"`
	TokensAfter  int `json:"tokens_after"`
	Segment      int `json:"segment"`
}

const (
	defaultCommandTimeout = 5 * time.Minute
	maxCheckOutput        = 4000
)

func loadTask(dir string) (*Task, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "task.yaml"))
	if err != nil {
		return nil, err
	}
	var t Task
	if err := yaml.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if t.Name == "" || (t.Prompt == "") == (len(t.Prompts) == 0) {
		return nil, fmt.Errorf("%s: name and exactly one of prompt / prompts are required", dir)
	}
	for _, p := range t.Prompts {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("%s: empty entry in prompts", dir)
		}
	}
	switch t.Mode {
	case "":
		t.Mode = "read-only"
	case "read-only", "allow-write":
	default:
		return nil, fmt.Errorf("%s: unknown mode %q", dir, t.Mode)
	}
	if t.FakeMode == "" {
		t.FakeMode = "text"
	}
	if t.Timeout != "" {
		d, err := time.ParseDuration(t.Timeout)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("%s: bad timeout %q", dir, t.Timeout)
		}
		t.timeout = d
	}
	if t.Acceptance != "" {
		if _, err := safeJoin(dir, t.Acceptance); err != nil {
			return nil, fmt.Errorf("%s: acceptance: %w", dir, err)
		}
	}
	for _, c := range t.Checks {
		if c.Turn < 0 || c.Turn > len(t.Turns()) {
			return nil, fmt.Errorf("%s: check turn %d is outside 1..%d", dir, c.Turn, len(t.Turns()))
		}
		if isRegexCheck(c.Type) {
			if c.Text == "" {
				return nil, fmt.Errorf("%s: %s needs a pattern in text", dir, c.Type)
			}
			if _, err := regexp.Compile(c.Text); err != nil {
				return nil, fmt.Errorf("%s: %s pattern %q: %w", dir, c.Type, c.Text, err)
			}
		}
		if c.Type != "command" {
			continue
		}
		if len(c.Args) == 0 {
			return nil, fmt.Errorf("%s: command check needs args", dir)
		}
		if c.Timeout != "" {
			if d, err := time.ParseDuration(c.Timeout); err != nil || d <= 0 {
				return nil, fmt.Errorf("%s: bad command check timeout %q", dir, c.Timeout)
			}
		}
	}
	if t.Repo != nil {
		if strings.TrimSpace(t.Repo.Ref) == "" {
			return nil, fmt.Errorf("%s: repo.ref is required", dir)
		}
		if _, err := os.Stat(filepath.Join(dir, "fixture")); err == nil {
			return nil, fmt.Errorf("%s: repo and fixture/ are mutually exclusive", dir)
		}
	}
	// compact_after: a 1-based turn index each, in range and without repeats.
	seen := map[int]bool{}
	for _, turn := range t.CompactAfter {
		if turn < 1 || turn > len(t.Turns()) {
			return nil, fmt.Errorf("%s: compact_after turn %d is outside 1..%d", dir, turn, len(t.Turns()))
		}
		if seen[turn] {
			return nil, fmt.Errorf("%s: compact_after repeats turn %d", dir, turn)
		}
		seen[turn] = true
	}
	t.dir = dir
	return &t, nil
}

// compactsAfter reports whether a forced compaction runs after 1-based turn.
func (t *Task) compactsAfter(turn int) bool {
	for _, after := range t.CompactAfter {
		if after == turn {
			return true
		}
	}
	return false
}

// effectiveTimeout is the task timeout, or def when the task sets none.
func (t *Task) effectiveTimeout(def time.Duration) time.Duration {
	if t.timeout > 0 {
		return t.timeout
	}
	return def
}

// injectAcceptance copies the task's acceptance directory into ws.
func (t *Task) injectAcceptance(ws string) error {
	if t.Acceptance == "" {
		return nil
	}
	src, err := safeJoin(t.dir, t.Acceptance)
	if err != nil {
		return err
	}
	return copyDir(src, ws)
}

// finish injects the acceptance files, drops the tests the agent wrote itself
// (judging runs the task's own and the injected ones only) and evaluates the
// checks. before is the workspace file set from before the agent ran and
// answers holds each turn's answer (the last one is the final answer).
func (t *Task) finish(ws string, before map[string]bool, answers []string, r *Result) {
	accErr := t.injectAcceptance(ws)
	if !t.KeepTests {
		r.RemovedTests = removeForeignTests(ws, before, t.injectedFiles(ws))
	}
	r.Checks = runChecks(ws, answers, t.Checks)
	if accErr != nil { // a task that cannot be judged must not pass
		r.Checks = append(r.Checks, CheckResult{Type: "acceptance", Text: t.Acceptance, Detail: accErr.Error()})
	}
}

// Turns returns the prompts to send in order (a single-prompt task has one).
// answer_contains checks apply to the answer of the last turn.
func (t *Task) Turns() []string {
	if len(t.Prompts) > 0 {
		return t.Prompts
	}
	return []string{t.Prompt}
}

func loadTasks(root, pattern string) ([]*Task, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var tasks []*Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if ok, err := filepath.Match(pattern, e.Name()); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		t, err := loadTask(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// safeJoin joins a task-relative path under ws and rejects escapes.
func safeJoin(ws, rel string) (string, error) {
	p := filepath.Join(ws, filepath.FromSlash(rel))
	r, err := filepath.Rel(ws, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", rel)
	}
	return p, nil
}

func (g Generate) write(ws string) error {
	p, err := safeJoin(ws, g.Path)
	if err != nil {
		return err
	}
	over := map[int]string{}
	for _, in := range g.Inserts {
		over[in.At] = in.Text
	}
	var b strings.Builder
	for i := 1; i <= g.Count; i++ {
		if s, ok := over[i]; ok {
			b.WriteString(s)
		} else {
			b.WriteString(strings.ReplaceAll(g.Line, "{i}", fmt.Sprint(i)))
		}
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

// runChecks evaluates every check against the workspace and the answers. An
// answer check judges the answer of its turn, or the final one when it names
// no turn (single-turn tasks and the last answer of a multi-turn task).
func runChecks(ws string, answers []string, checks []Check) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		r := CheckResult{Type: c.Type, Path: c.Path, Text: c.Text}
		if isAnswerCheck(c.Type) {
			answer, ok := answerOf(answers, c.Turn)
			r.Turn = c.Turn
			if r.Turn == 0 {
				r.Turn = len(answers)
			}
			if !ok {
				r.Detail = fmt.Sprintf("no answer for turn %d", c.Turn)
				out = append(out, r)
				continue
			}
			r.Passed = answerCheckPassed(c, answer)
			out = append(out, r)
			continue
		}
		if c.Type == "command" {
			out = append(out, runCommandCheck(ws, c))
			continue
		}
		p, err := safeJoin(ws, c.Path)
		var data []byte
		if err == nil {
			data, err = os.ReadFile(p)
		}
		if err != nil {
			r.Detail = err.Error()
			out = append(out, r)
			continue
		}
		s := string(data)
		switch c.Type {
		case "file_contains":
			r.Passed = strings.Contains(s, c.Text)
		case "file_not_contains":
			r.Passed = !strings.Contains(s, c.Text)
		case "file_equals":
			r.Passed = strings.TrimRight(s, "\r\n") == strings.TrimRight(c.Text, "\r\n")
		case "file_matches", "file_not_matches":
			re, err := regexp.Compile(c.Text)
			if err != nil {
				r.Detail = err.Error()
				break
			}
			r.Passed = re.MatchString(s) == (c.Type == "file_matches")
		default:
			r.Detail = "unknown check type"
		}
		out = append(out, r)
	}
	return out
}

func allPassed(rs []CheckResult) bool {
	for _, r := range rs {
		if !r.Passed {
			return false
		}
	}
	return true
}

// isRegexCheck reports whether a check type reads its text as a regular
// expression (the matcher itself is applied by runChecks / answerCheckPassed).
func isRegexCheck(typ string) bool {
	switch typ {
	case "answer_matches", "answer_not_matches", "file_matches", "file_not_matches":
		return true
	}
	return false
}

// isAnswerCheck reports whether a check type judges an answer instead of a file.
func isAnswerCheck(typ string) bool {
	switch typ {
	case "answer_contains", "answer_not_contains", "answer_matches", "answer_not_matches":
		return true
	}
	return false
}

// answerOf returns the answer a check judges: the 1-based turn's own, or the
// final answer (the last one) for turn 0. ok is false when the turn has no
// answer (the run stopped before it).
func answerOf(answers []string, turn int) (string, bool) {
	if len(answers) == 0 {
		return "", false
	}
	if turn <= 0 {
		return answers[len(answers)-1], true
	}
	if turn > len(answers) {
		return "", false
	}
	return answers[turn-1], true
}

// answerCheckPassed applies one answer check to its answer.
func answerCheckPassed(c Check, answer string) bool {
	switch c.Type {
	case "answer_contains":
		return strings.Contains(answer, c.Text)
	case "answer_not_contains":
		return !strings.Contains(answer, c.Text)
	case "answer_matches", "answer_not_matches":
		re, err := regexp.Compile(c.Text)
		if err != nil {
			return false
		}
		return re.MatchString(answer) == (c.Type == "answer_matches")
	}
	return false
}

// runCommandCheck execs c.Args in ws directly (no shell, so it works on
// Windows); exit code 0 passes.
func runCommandCheck(ws string, c Check) CheckResult {
	r := CheckResult{Type: c.Type, Text: strings.Join(c.Args, " ")}
	timeout := defaultCommandTimeout
	if c.Timeout != "" {
		if d, err := time.ParseDuration(c.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	code := -1
	r.ExitCode = &code
	if len(c.Args) == 0 {
		r.Detail = "empty args"
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Args[0], c.Args[1:]...)
	cmd.Dir = ws
	cmd.WaitDelay = 5 * time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	r.Output = tailString(buf.String(), maxCheckOutput)
	switch {
	case err == nil:
		code = 0
		r.Passed = true
	case ctx.Err() != nil:
		r.Detail = fmt.Sprintf("timed out after %s", timeout)
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		r.Detail = err.Error()
	}
	return r
}

// tailString keeps the last n bytes of s (on a rune boundary).
func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return "..." + s
}
