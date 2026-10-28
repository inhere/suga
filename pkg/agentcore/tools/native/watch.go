package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/internal/proctree"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// A watch is a background shell_exec with every and/or watch_paths: the
// command runs once at the start (the baseline) and again on every tick and
// whenever the watched files change; the model is told only when a run's exit
// code or output differs from the previous run's (timings and timestamps
// aside). It is a function task of the session's task manager, so the task
// tool lists, reads and kills it like any background command.
const (
	// maxWatchFiles caps the files one watch tracks.
	maxWatchFiles = 5000
	// watchNoticeLines are the last output lines shown with a change.
	watchNoticeLines = 5
	// maxWatchCompareBytes caps the output of a run kept for the comparison.
	maxWatchCompareBytes = 1 << 20
)

// minWatchEvery is the shortest interval between timed runs (a var so tests
// can shorten it).
var minWatchEvery = 10 * time.Second

// watchPollInterval is how often watched files are checked; a change runs the
// command after one more poll without further changes (debounce). A var so
// tests can shorten it.
var watchPollInterval = 2 * time.Second

// watchSpec is what makes a background call a watch.
type watchSpec struct {
	every   time.Duration
	paths   []string
	timeout time.Duration // of one run
}

// parseWatch reads every and watch_paths; ok is false when neither is set.
func parseWatch(args map[string]any) (spec watchSpec, ok bool, err error) {
	if secs, set := numberArg(args["every"]); set && secs != 0 {
		if d, valid := agentcore.DurationFromSeconds(secs, 0); valid {
			spec.every = d
		}
		if spec.every < minWatchEvery {
			return spec, false, fmt.Errorf("every must be at least %d seconds", int(minWatchEvery.Seconds()))
		}
	}
	switch v := args["watch_paths"].(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			spec.paths = []string{v}
		}
	case []any:
		for _, item := range v {
			if s, _ := item.(string); strings.TrimSpace(s) != "" {
				spec.paths = append(spec.paths, s)
			}
		}
	}
	for i, p := range spec.paths {
		p = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./")
		if path.IsAbs(p) || filepath.IsAbs(p) || slices.Contains(strings.Split(p, "/"), "..") {
			return spec, false, fmt.Errorf("watch_paths must be globs relative to the workspace, got %q", p)
		}
		spec.paths[i] = p
	}
	return spec, spec.every > 0 || len(spec.paths) > 0, nil
}

// describe says when the watch runs: "every 30s", "files *.go", or both.
func (s watchSpec) describe() string {
	var parts []string
	if s.every > 0 {
		parts = append(parts, "every "+s.every.String())
	}
	if len(s.paths) > 0 {
		parts = append(parts, "on changes of "+strings.Join(s.paths, ", "))
	}
	return strings.Join(parts, " and ")
}

// startWatch starts command as a watch task of the session.
func (t *ShellExecTool) startWatch(ctx context.Context, call api.ToolCall, command string, spec watchSpec) (*api.ToolResult, error) {
	spec.timeout = t.callTimeout(call.Arguments)
	w := &watcher{tool: t, command: command, spec: spec}
	info, err := t.tasks.Start(ctx, tasks.Spec{Kind: "watch", Summary: taskSummary(command) + " (" + spec.describe() + ")",
		Run: w.run, Clean: cleanTaskLine})
	if err != nil {
		return nil, fmt.Errorf("shell_exec: watch: %w", err)
	}
	return &api.ToolResult{
		CallID: call.CallID,
		Name:   t.Name(),
		Content: fmt.Sprintf("[watch task %s started] The command runs now and again %s (each run up to %s). "+
			"You are told only when a run's exit code or output differs from the previous run (timings ignored; "+
			"the first run only when it fails). Output of every run: %s (task output reads it). task kill stops the watch.",
			info.ID, spec.describe(), spec.timeout, info.Ref),
		Metadata: map[string]any{"task_id": info.ID, "background": true, "watch": true},
	}, nil
}

// watcher runs one watch.
type watcher struct {
	tool    *ShellExecTool
	command string
	spec    watchSpec
}

// watchRun is the outcome of one run.
type watchRun struct {
	exit  int
	key   string   // exit code and normalized output: what a change compares
	lines []string // cleaned output lines
}

// run is the watch loop (a tasks.RunFunc): a baseline run, then a run per
// tick or settled file change, until the task is killed or times out. Runs
// never overlap: a tick during a run waits for it (several fold into one).
func (w *watcher) run(ctx context.Context, out io.Writer, r tasks.Reporter) (int, error) {
	runs, changes, lastExit := 0, 0, -1
	var prev *watchRun
	var files map[string]fileStamp
	if len(w.spec.paths) > 0 {
		files = w.snapshot(ctx)
	}
	runOnce := func(trigger string) {
		runs++
		res, ok := w.once(ctx, out, runs, trigger)
		if !ok {
			return // the watch is ending
		}
		if files != nil {
			files = w.snapshot(ctx) // what the run itself wrote is no change
		}
		// The notice of this run, if its result differs from the last one.
		notice := ""
		switch {
		case prev == nil:
			if res.exit != 0 {
				notice = fmt.Sprintf("run 1 (%s) failed: exit %d%s", trigger, res.exit, lastLines(res.lines))
			}
		case res.key != prev.key:
			changes++
			what := fmt.Sprintf("exit %d -> %d", prev.exit, res.exit)
			if res.exit == prev.exit {
				what = fmt.Sprintf("output changed (exit %d)", res.exit)
			}
			notice = fmt.Sprintf("run %d (%s): %s%s", runs, trigger, what, lastLines(res.lines))
		}
		prev, lastExit = &res, res.exit
		// Progress last: it returns a task wait, which takes the notice.
		r.Count("runs", runs)
		r.Count("changes", changes)
		if notice != "" {
			r.Notify(notice)
		}
		r.Progress(fmt.Sprintf("run %d, last exit %d, %d changes", runs, res.exit, changes))
	}

	runOnce("start")
	var tick, poll <-chan time.Time
	if w.spec.every > 0 {
		ticker := time.NewTicker(w.spec.every)
		defer ticker.Stop()
		tick = ticker.C
	}
	if files != nil {
		ticker := time.NewTicker(watchPollInterval)
		defer ticker.Stop()
		poll = ticker.C
	}
	var changed []string // files changed since the last run, waiting to settle
	for {
		select {
		case <-ctx.Done():
			return lastExit, nil
		case <-tick:
			runOnce("every " + w.spec.every.String())
			changed = nil
		case <-poll:
			now := w.snapshot(ctx)
			diff := changedFiles(files, now)
			files = now
			if len(diff) > 0 { // still changing: wait for a quiet poll
				for _, f := range diff {
					if !slices.Contains(changed, f) {
						changed = append(changed, f)
					}
				}
				continue
			}
			if len(changed) > 0 {
				runOnce("files: " + shortList(changed, 3))
				changed = nil
			}
		}
	}
}

// once runs the command one time, writing its output to out between a
// header and an exit line. ok is false when the watch itself is ending.
func (w *watcher) once(ctx context.Context, out io.Writer, n int, trigger string) (res watchRun, ok bool) {
	fmt.Fprintf(out, "=== run %d (%s) %s ===\n", n, trigger, time.Now().Format("15:04:05"))
	runCtx, cancel := context.WithTimeout(ctx, w.spec.timeout)
	defer cancel()
	argv := w.tool.shell.argv(w.command)
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = w.tool.workspace, shellEnv(ctx, w.tool.extraEnv)
	var buf bytes.Buffer
	both := io.MultiWriter(out, &capWriter{buf: &buf, max: maxWatchCompareBytes})
	cmd.Stdout, cmd.Stderr = both, both // one value: exec uses a single pipe
	tree := proctree.NewLimited(cmd, w.tool.limits.tree())
	cmd.Cancel = tree.Kill
	cmd.WaitDelay = shellWaitDelay
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "=== start failed: %v ===\n", err)
		return watchRun{exit: -1, key: "start failed"}, ctx.Err() == nil
	}
	if serr := tree.Started(); serr != nil {
		// Started kills the suspended shell when it cannot join the job: reap it
		// and write the reason as this run's end line, like a start failure.
		_ = cmd.Wait()
		tree.Close()
		fmt.Fprintf(out, "=== start failed: %v ===\n", serr)
		return watchRun{exit: -1, key: "start failed"}, ctx.Err() == nil
	}
	err := cmd.Wait()
	tree.Close()
	if ctx.Err() != nil {
		return res, false
	}
	res.exit = cmd.ProcessState.ExitCode()
	status := fmt.Sprintf("exit %d", res.exit)
	switch {
	case runCtx.Err() == context.DeadlineExceeded:
		status = "timeout after " + w.spec.timeout.String()
	case errors.Is(err, exec.ErrWaitDelay):
		status += "; a background process kept the output open and was killed"
	}
	// A run the limits stopped says so in the run's header line and therefore
	// in the change comparison as well.
	if w.tool.limits.limitHit(tree.Exceeded(), res.exit, buf.String()) {
		status += "; " + w.tool.limits.notice()
	}
	fmt.Fprintf(out, "=== %s ===\n", status)
	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		res.lines = append(res.lines, cleanTaskLine([]byte(strings.TrimRight(line, "\r"))))
	}
	res.key = status + "\n" + normalizeWatchOutput(res.lines)
	return res, true
}

// capWriter keeps the first max bytes written to it.
type capWriter struct {
	buf *bytes.Buffer
	max int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

var (
	// watchDuration and watchClock match what changes from run to run of the
	// same result: durations ("0.42s", "(12ms)") and timestamps.
	watchDuration = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h)\b`)
	watchClock    = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?|\b\d{2}:\d{2}:\d{2}(?:\.\d+)?\b`)
)

// normalizeWatchOutput is the output without the parts that differ between
// runs of the same result (timings, timestamps), for the change comparison.
func normalizeWatchOutput(lines []string) string {
	text := strings.Join(lines, "\n")
	text = watchClock.ReplaceAllString(text, "<time>")
	return watchDuration.ReplaceAllString(text, "<dur>")
}

// lastLines is the last non-empty output lines of a run, for a notice.
func lastLines(lines []string) string {
	var keep []string
	for i := len(lines) - 1; i >= 0 && len(keep) < watchNoticeLines; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			keep = append([]string{lines[i]}, keep...)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	return "\nlast lines:\n" + strings.Join(keep, "\n")
}

// fileStamp is what a poll compares of a file.
type fileStamp struct {
	mod  int64
	size int64
}

// snapshot stamps the workspace files matching the watch paths (ignored
// files and .git skipped, at most maxWatchFiles). A missing start directory
// is an empty snapshot: files created there later are a change.
func (w *watcher) snapshot(ctx context.Context) map[string]fileStamp {
	out := map[string]fileStamp{}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := filepath.Join(w.tool.workspace, filepath.FromSlash(literalPrefix(w.spec.paths)))
	_, _ = walkWorkspace(wctx, w.tool.workspace, start, false, func(_ *os.Root, rel string, d fs.DirEntry) error {
		for _, p := range w.spec.paths {
			if !globMatch(p, rel) {
				continue
			}
			if info, err := d.Info(); err == nil {
				out[rel] = fileStamp{mod: info.ModTime().UnixNano(), size: info.Size()}
			}
			if len(out) >= maxWatchFiles {
				return errSearchFull
			}
			break
		}
		return nil
	})
	return out
}

// literalPrefix is the directory all patterns share before their first
// wildcard segment ("" = the workspace), so a poll walks only that subtree.
func literalPrefix(patterns []string) string {
	var common []string
	for i, p := range patterns {
		segs := strings.Split(p, "/")
		if len(segs) == 1 { // a base-name pattern matches at any depth
			return ""
		}
		var lit []string
		for _, s := range segs[:len(segs)-1] {
			if strings.ContainsAny(s, "*?[") {
				break
			}
			lit = append(lit, s)
		}
		if i == 0 {
			common = lit
			continue
		}
		n := 0
		for n < len(common) && n < len(lit) && common[n] == lit[n] {
			n++
		}
		common = common[:n]
	}
	return strings.Join(common, "/")
}

// changedFiles lists the files added, removed or modified from a to b.
func changedFiles(a, b map[string]fileStamp) []string {
	var out []string
	for f, s := range b {
		if old, ok := a[f]; !ok || old != s {
			out = append(out, f)
		}
	}
	for f := range a {
		if _, ok := b[f]; !ok {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// shortList is up to n names, then "+k more".
func shortList(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(" +%d more", len(names)-n)
}
