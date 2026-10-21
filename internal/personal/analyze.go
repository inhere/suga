package personal

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// TraceAnalysis summarizes a session's events for loop analysis (suga inspect trace
// --analyze): how many steps, which tools, repeated reads and commands, and
// how much verification happened after the tests first passed. It is derived
// from the events alone (tool attrs arg/target/exit_code, model attr
// tool_calls), so older sessions without these attrs give partial numbers.
type TraceAnalysis struct {
	ModelCalls      int   `json:"model_calls"`
	ToolCalls       int   `json:"tool_calls"`
	ParallelSteps   int   `json:"parallel_steps"`     // model calls that issued more than one tool call
	MaxCallsPerStep int   `json:"max_calls_per_step"` // most tool calls one model call issued
	InputTokens     int64 `json:"input_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	// ModelMs and ToolMs are the time spent in main model calls and in tool
	// calls (side calls such as compaction are not counted).
	ModelMs        int64          `json:"model_ms"`
	ToolMs         int64          `json:"tool_ms"`
	ReasoningChars int            `json:"reasoning_chars"`
	TextChars      int            `json:"text_chars"`
	Tools          map[string]int `json:"tools"`
	ToolErrors     map[string]int `json:"tool_errors,omitempty"`
	// Rereads lists files read more than once; ReadsAfterWrite counts reads
	// of a file this session had already written or edited.
	Rereads         []Count `json:"rereads,omitempty"`
	ReadsAfterWrite int     `json:"reads_after_write"`
	EditsPerFile    []Count `json:"edits_per_file,omitempty"`
	// Shell: runs, failures (exit code != 0) and identical commands run again.
	ShellRuns        int     `json:"shell_runs"`
	ShellFailures    int     `json:"shell_failures"`
	RepeatedCommands []Count `json:"repeated_commands,omitempty"`
	// Tests: shell runs that look like test commands, and how many of them
	// came after the first passing one (re-verification).
	TestRuns          int `json:"test_runs"`
	TestRunsAfterPass int `json:"test_runs_after_pass"`
	// Phases, in model calls: before the first write or edit, from that step
	// to the first passing test run, and after it (-1 when the run never got
	// there). A test run passes when its output reports no failure (attr
	// test_failed) and it exited 0; a focused run (-run, -k) does not count.
	ExploreSteps      int `json:"explore_steps"`
	WriteToGreenSteps int `json:"write_to_green_steps"`
	AfterGreenSteps   int `json:"after_green_steps"`
	// EditSteps are model calls that wrote or edited files; SingleEditSteps
	// those whose only call made a single change (one file_edit replacement
	// or one file_write): fixes sent one per step instead of together.
	EditSteps       int `json:"edit_steps"`
	SingleEditSteps int `json:"single_edit_steps"`
	// ShellStderrOK counts shell runs that exited 0 but wrote to stderr (a
	// failing command before ";" hides its exit code).
	ShellStderrOK int `json:"shell_stderr_exit0"`
	Spills        int `json:"spills"`
	Minimized     int `json:"minimized"` // results tidied by the output minimizer
	Folds         int `json:"folds"`
	// Cache attribution from the model attrs prefix*: the input tokens the
	// provider did not serve from its cache, split into content new since
	// the previous request, earlier content sent again after a prefix break
	// (by reason: fold, segment, system, tools, rewrite) and content the
	// prefix should have let the provider serve but it did not (expired or
	// not yet written cache). Estimated from JSON sizes; calls without the
	// attrs (older sessions) count in Uncached only.
	Uncached         int64            `json:"uncached_input_tokens"`
	UncachedFresh    int64            `json:"uncached_fresh"`
	UncachedRebuilt  map[string]int64 `json:"uncached_rebuilt,omitempty"`
	UncachedProvider int64            `json:"uncached_provider"`
	PrefixBreaks     map[string]int   `json:"prefix_breaks,omitempty"`
	Compactions      int              `json:"compactions"`
	// Retries are the model request retries after transient failures (every
	// model call, side calls included) and RetryWaitMs the time waited.
	Retries     int   `json:"retries"`
	RetryWaitMs int64 `json:"retry_wait_ms"`
	// BackgroundTasks / BackgroundMs: background tasks that ended (of every
	// kind) and how long the tasks among them ran in all (time the loop did not
	// have to wait for). A watch or a Stop hook stays for as long as it is
	// wanted, so its time is not waiting saved: they count apart. A background
	// sub-agent counts here like a shell task, its own numbers in AgentMs.
	BackgroundTasks int   `json:"background_tasks"`
	BackgroundMs    int64 `json:"background_ms"`
	// By kind: WatchTasks counts the watch tasks of the session, WatchRuns the
	// times their command ran and WatchChanges the times a run's result
	// differed from the previous one; StopHooks counts the runs of the
	// background Stop hook (hooks.stop_background). These are parts of
	// BackgroundTasks.
	WatchTasks   int `json:"watch_tasks"`
	WatchRuns    int `json:"watch_runs"`
	WatchChanges int `json:"watch_changes"`
	StopHooks    int `json:"stop_hooks"`
	// BackgroundOverlapMs is the part of the commands' time the loop spent in
	// model calls and foreground tool calls anyway: the time the backgrounding saved
	// (spans count once where calls overlap, as in a parallel batch).
	BackgroundOverlapMs int64 `json:"background_overlap_ms"`
	// TaskWaits / TaskWaitMs: calls of the task tool that waited for a task to
	// end (action=wait) and the time they blocked the loop.
	TaskWaits  int   `json:"task_waits"`
	TaskWaitMs int64 `json:"task_wait_ms"`
	// ImmediateWaits counts those waits that came right after the task was
	// started: between the shell_exec that returned the task id and the wait
	// lay no other tool call, only model calls. It finds tasks run as if they
	// were foreground work.
	ImmediateWaits int `json:"immediate_waits"`
	// Slowest are the longest model calls.
	Slowest []SlowCall `json:"slowest,omitempty"`
	// Sub-agents (the agent tool): the calls the session made (background
	// calls included), the model steps and tool calls they ran, the tokens and
	// cost of their own model calls and the time they ran in all (the same
	// numbers its trace line shows). A background call returns a task id at
	// once, so only its call counts here at first; what it spent (steps, tool
	// calls, tokens, cost, stop) arrives with its task's end event. AgentStops
	// is why the calls that reported an end stopped: final, max_steps,
	// max_tokens, timeout, cancelled or failed.
	AgentCalls           int            `json:"agent_calls"`
	AgentSteps           int            `json:"agent_steps"`
	AgentToolCalls       int            `json:"agent_tool_calls"`
	AgentInputTokens     int64          `json:"agent_input_tokens"`
	AgentCacheReadTokens int64          `json:"agent_cache_read_tokens"`
	AgentOutputTokens    int64          `json:"agent_output_tokens"`
	AgentCostUSD         float64        `json:"agent_cost_usd"`
	AgentMs              int64          `json:"agent_ms"`
	AgentStops           map[string]int `json:"agent_stops,omitempty"`
}

// Count is a named count (a path or a command).
type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// SlowCall is one long model call: its step number, duration and output.
type SlowCall struct {
	Step         int   `json:"step"`
	DurationMs   int64 `json:"duration_ms"`
	OutputTokens int64 `json:"output_tokens"`
	ToolCalls    int   `json:"tool_calls"`
}

// AnalyzeTrace derives a TraceAnalysis from a session's events (in order).
func AnalyzeTrace(events []store.Event) TraceAnalysis {
	a := TraceAnalysis{Tools: map[string]int{}, ToolErrors: map[string]int{},
		UncachedRebuilt: map[string]int64{}, PrefixBreaks: map[string]int{}}
	reads, edits, commands := map[string]int{}, map[string]int{}, map[string]int{}
	written := map[string]bool{}
	passed := false
	var slow []SlowCall
	firstWrite, green := -1, -1
	// wroteLater: the run writes or edits a file at some point; a pass
	// before that write is then no "first pass" (see the test runs below).
	wroteLater := false
	for _, e := range events {
		if e.Kind == store.EventTool && (e.Name == "file_write" || e.Name == "file_edit") {
			wroteLater = true
			break
		}
	}
	// Background tasks (see BackgroundOverlapMs, TaskWaits, ImmediateWaits):
	// the spans the tasks ran (from their end events), the spans of foreground
	// work (model and tool calls, without the task wait that blocks the loop)
	// and the tasks started by a call, still without another call after them.
	var bgSpans, fgSpans []span
	fresh := map[string]bool{}
	// the current step's calls: how many, how many edits, changes made
	stepCalls, stepEdits, stepChanges := 0, 0, 0
	closeStep := func() {
		if stepEdits > 0 {
			a.EditSteps++
			if stepCalls == 1 && stepChanges == 1 {
				a.SingleEditSteps++
			}
		}
		stepCalls, stepEdits, stepChanges = 0, 0, 0
	}
	for _, e := range events {
		switch e.Kind {
		case store.EventTask:
			a.BackgroundTasks++
			// The kind a task ran as (tasks.Spec.Kind): a watch ends with its runs
			// and changes counters, a background Stop hook has no counters; only a
			// task the loop would otherwise have waited for (shell, agent, or no
			// kind: older events) is time the backgrounding saved.
			switch kind, _ := e.Attrs["kind"].(string); kind {
			case taskKindWatch:
				a.WatchTasks++
				a.WatchRuns += attrInt(e.Attrs["runs"])
				a.WatchChanges += attrInt(e.Attrs["changes"])
			case taskKindHook:
				a.StopHooks++
			case taskKindAgent:
				// A background sub-agent: its end event carries what its task
				// counted, as a synchronous call's tool event does (agentSpent).
				a.agentSpent(e.Attrs)
				a.AgentMs += e.DurationMs
				a.BackgroundMs += e.DurationMs
				bgSpans = appendSpan(bgSpans, e)
			default:
				a.BackgroundMs += e.DurationMs
				bgSpans = appendSpan(bgSpans, e)
			}
		case store.EventModel:
			a.Retries += attrInt(e.Attrs["retries"])
			a.RetryWaitMs += int64(attrInt(e.Attrs["retry_wait_ms"]))
			// A model call is foreground work like a tool call (side calls such as
			// compaction too): the loop waits for it while a task may run.
			fgSpans = appendSpan(fgSpans, e)
			if p, _ := e.Attrs["purpose"].(string); p != "" {
				continue // compaction and other side calls are not steps
			}
			closeStep()
			a.ModelCalls++
			calls := attrInt(e.Attrs["tool_calls"])
			if calls > 1 {
				a.ParallelSteps++
			}
			a.MaxCallsPerStep = max(a.MaxCallsPerStep, calls)
			a.ModelMs += e.DurationMs
			a.InputTokens += e.InputTokens
			a.CacheReadTokens += e.CacheReadTokens
			a.OutputTokens += e.OutputTokens
			a.attributeCache(e)
			a.ReasoningChars += attrInt(e.Attrs["reasoning_chars"])
			a.TextChars += attrInt(e.Attrs["text_chars"])
			slow = append(slow, SlowCall{Step: a.ModelCalls, DurationMs: e.DurationMs, OutputTokens: e.OutputTokens, ToolCalls: calls})
		case store.EventTool, store.EventDenial:
			a.ToolCalls++
			a.ToolMs += e.DurationMs
			stepCalls++
			a.Tools[e.Name]++
			// A sub-agent call is counted here (a background call too); what the
			// sub-agent spent is added by agentCall.
			if e.Name == "agent" {
				a.agentCall(e)
			}
			// A task wait is no time saved (it blocks the loop until the task
			// ends), so it adds no foreground span; every other call does, and a
			// call that started a background task marks that task as just started
			// while any other call means nothing counts as just started anymore.
			if e.Name == "task" {
				action, id := taskAction(e)
				if action != taskWaitAction {
					fgSpans = appendSpan(fgSpans, e)
				} else {
					a.TaskWaits++
					a.TaskWaitMs += e.DurationMs
					if fresh[id] {
						a.ImmediateWaits++
						delete(fresh, id)
					}
				}
			} else if id, _ := e.Attrs["task_id"].(string); id != "" {
				fgSpans = appendSpan(fgSpans, e)
				fresh[id] = true // the shell_exec that started this background task
			} else {
				fgSpans = appendSpan(fgSpans, e)
				clear(fresh)
			}
			if e.Status != eventOK {
				a.ToolErrors[e.Name]++
			}
			if spilled, _ := e.Attrs["spilled"].(bool); spilled {
				a.Spills++
			}
			if minimized, _ := e.Attrs["minimized"].(bool); minimized {
				a.Minimized++
			}
			targets := attrStrings(e.Attrs["target"])
			switch e.Name {
			case "file_read":
				for _, t := range targets {
					reads[t]++
					if written[t] {
						a.ReadsAfterWrite++
					}
				}
			case "file_write", "file_edit":
				for _, t := range targets {
					edits[t]++
					written[t] = true
				}
				if firstWrite < 0 {
					firstWrite = a.ModelCalls
				}
				stepEdits++
				stepChanges += max(attrInt(e.Attrs["changes"]), 1)
			case "shell_exec":
				a.ShellRuns++
				arg, _ := e.Attrs["arg"].(string)
				commands[arg]++
				code, hasCode := e.Attrs["exit_code"]
				failed := e.Status != eventOK || (hasCode && attrInt(code) != 0)
				if failed {
					a.ShellFailures++
				} else if stderr, _ := e.Attrs["stderr"].(bool); stderr {
					a.ShellStderrOK++
				}
				testFailed, reported := e.Attrs["test_failed"].(bool)
				// A focused run (go test -run X, pytest -k X, maven -Dtest=X)
				// checks one test, often a scratch one written to probe a bug: it
				// is no "green". The patterns come from the language registry
				// (native, one file per language).
				focused := codeintel.FocusedTestPattern().MatchString(arg)
				if reported || codeintel.TestCommandPattern().MatchString(arg) {
					a.TestRuns++
					// After the first pass that followed a change: a test run
					// before any write (a baseline check) does not count.
					if passed {
						a.TestRunsAfterPass++
					}
					if !failed && hasCode && !testFailed && !focused && (firstWrite >= 0 || !wroteLater) {
						passed = true
						if green < 0 && firstWrite >= 0 {
							green = a.ModelCalls
						}
					}
				}
			}
		case store.EventFold:
			a.Folds++
		case store.EventCompaction:
			if e.Name == "compact" {
				a.Compactions++
			}
		}
	}
	closeStep()
	// The background runs that met foreground work: the time the loop spent in
	// model and tool calls while a task ran in the background. Each task adds
	// its own share, so tasks running at the same time count their shared time
	// twice (as their run time does above).
	a.BackgroundOverlapMs = overlapMs(sortSpans(bgSpans), mergeSpans(fgSpans))
	// Steps are 1-based model calls; firstWrite and green name the step a
	// call belonged to.
	a.ExploreSteps, a.WriteToGreenSteps, a.AfterGreenSteps = a.ModelCalls, -1, -1
	if firstWrite > 0 {
		a.ExploreSteps = firstWrite - 1
	}
	if green > 0 {
		a.WriteToGreenSteps = green - firstWrite + 1
		a.AfterGreenSteps = a.ModelCalls - green
	}
	a.Rereads = countsAbove(reads, 1)
	a.EditsPerFile = countsAbove(edits, 0)
	a.RepeatedCommands = countsAbove(commands, 1)
	sort.SliceStable(slow, func(i, j int) bool { return slow[i].DurationMs > slow[j].DurationMs })
	a.Slowest = slow[:min(len(slow), 3)]
	if len(a.ToolErrors) == 0 {
		a.ToolErrors = nil
	}
	if len(a.UncachedRebuilt) == 0 {
		a.UncachedRebuilt = nil
	}
	if len(a.PrefixBreaks) == 0 {
		a.PrefixBreaks = nil
	}
	return a
}

// agentCall adds one sub-agent invocation (an agent tool event: a synchronous
// call, or the start of a background one) to the sub-agent numbers. A
// background call hands back a task id at once, so it has none of the numbers
// itself: they come with its task's end event.
func (a *TraceAnalysis) agentCall(e store.Event) {
	a.AgentCalls++
	a.AgentMs += e.DurationMs
	if _, ok := e.Attrs["steps"]; !ok {
		return
	}
	a.agentSpent(e.Attrs)
}

// agentSpent adds what one sub-agent run spent, from the attrs of its
// synchronous call's tool event or of its background task's end event.
func (a *TraceAnalysis) agentSpent(attrs map[string]any) {
	a.AgentSteps += attrInt(attrs["steps"])
	a.AgentToolCalls += attrInt(attrs["tool_calls"])
	a.AgentInputTokens += int64(attrInt(attrs["input_tokens"]))
	a.AgentCacheReadTokens += int64(attrInt(attrs["cache_read_tokens"]))
	a.AgentOutputTokens += int64(attrInt(attrs["output_tokens"]))
	a.AgentCostUSD += attrFloat(attrs["cost_usd"])
	if stop, _ := attrs["stop"].(string); stop != "" {
		if a.AgentStops == nil {
			a.AgentStops = map[string]int{}
		}
		a.AgentStops[stop]++
	}
}

// attributeCache splits one model call's uncached input by cause (see
// TraceAnalysis.Uncached*): the reported input tokens are spread over the
// request's reused, rebuilt and fresh parts by their JSON sizes; what the
// provider left uncached beyond rebuilt + fresh is a provider-side miss, and
// when it served more than expected the shares shrink in proportion.
func (a *TraceAnalysis) attributeCache(e store.Event) {
	miss := max(e.InputTokens-e.CacheReadTokens, 0)
	a.Uncached += miss
	reason, _ := e.Attrs["prefix"].(string)
	reused, rebuilt, fresh := attrInt(e.Attrs["prefix_reused"]), attrInt(e.Attrs["prefix_rebuilt"]), attrInt(e.Attrs["prefix_fresh"])
	total := reused + rebuilt + fresh
	if reason == "" || total == 0 {
		return
	}
	if reason != prefixAppend && reason != prefixFirst {
		a.PrefixBreaks[reason]++
	}
	perChar := float64(e.InputTokens) / float64(total)
	expFresh, expRebuilt := float64(fresh)*perChar, float64(rebuilt)*perChar
	if expected := expFresh + expRebuilt; expected > float64(miss) && expected > 0 {
		scale := float64(miss) / expected
		expFresh, expRebuilt = expFresh*scale, expRebuilt*scale
	}
	a.UncachedFresh += int64(expFresh)
	if rebuilt > 0 {
		a.UncachedRebuilt[reason] += int64(expRebuilt)
	}
	a.UncachedProvider += max(miss-int64(expFresh)-int64(expRebuilt), 0)
}

// WriteTraceAnalysis prints the analysis as text, or as one JSON object.
func WriteTraceAnalysis(w io.Writer, a TraceAnalysis, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(a)
	}
	var b strings.Builder
	row := func(label, format string, args ...any) {
		fmt.Fprintf(&b, "%-22s %s\n", label, fmt.Sprintf(format, args...))
	}
	row("steps", "%d model calls, %d tool calls, %d parallel steps (max %d calls in one step)",
		a.ModelCalls, a.ToolCalls, a.ParallelSteps, a.MaxCallsPerStep)
	row("tokens", "in %s (cache read %s), out %s; reasoning %s chars, text %s chars",
		human(a.InputTokens), human(a.CacheReadTokens), human(a.OutputTokens), human(int64(a.ReasoningChars)), human(int64(a.TextChars)))
	row("time", "model %.1fs, tools %.1fs", float64(a.ModelMs)/1000, float64(a.ToolMs)/1000)
	row("tools", "%s", joinCounts(sortedCounts(a.Tools), a.ToolErrors))
	row("re-reads", "%s", orNone(joinCounts(a.Rereads, nil)))
	row("reads after own write", "%d", a.ReadsAfterWrite)
	row("edits per file", "%s", orNone(joinCounts(a.EditsPerFile, nil)))
	row("shell", "%d runs, %d failed (exit != 0), %d exited 0 with stderr output", a.ShellRuns, a.ShellFailures, a.ShellStderrOK)
	row("repeated commands", "%s", orNone(joinCounts(a.RepeatedCommands, nil)))
	row("tests", "%d runs, %d after the first pass", a.TestRuns, a.TestRunsAfterPass)
	row("phases", "explore %d, first write to green %s, after green %s (model calls)",
		a.ExploreSteps, phaseSteps(a.WriteToGreenSteps), phaseSteps(a.AfterGreenSteps))
	row("edit steps", "%d, %d with a single change (one file_edit replacement or one file_write)", a.EditSteps, a.SingleEditSteps)
	row("uncached input", "%s", uncachedSummary(a))
	row("spills / folds", "%d / %d (compactions %d, tidied outputs %d)", a.Spills, a.Folds, a.Compactions, a.Minimized)
	row("model retries", "%d (waited %.1fs)", a.Retries, float64(a.RetryWaitMs)/1000)
	row("background tasks", "%d (ran %.1fs in all)", a.BackgroundTasks, float64(a.BackgroundMs)/1000)
	// The overlap and the per-kind rows appear only when the session had
	// tasks of that kind at all.
	if a.BackgroundTasks > 0 {
		row("background overlap", "%.1fs while model/tool calls ran (waiting saved); %d waits (%.1fs), %d right after the start",
			float64(a.BackgroundOverlapMs)/1000, a.TaskWaits, float64(a.TaskWaitMs)/1000, a.ImmediateWaits)
	}
	if a.WatchTasks > 0 {
		row("watch tasks", "%d (%d runs, %d changes)", a.WatchTasks, a.WatchRuns, a.WatchChanges)
	}
	if a.StopHooks > 0 {
		row("stop hooks", "%d background Stop hook runs", a.StopHooks)
	}
	// The sub-agent rows appear only when the session used sub-agents.
	if a.AgentCalls > 0 {
		row("sub-agents", "%d calls, %d steps, %d tool calls, in %s (cache %s), out %s, cost $%.4f, ran %.1fs",
			a.AgentCalls, a.AgentSteps, a.AgentToolCalls, human(a.AgentInputTokens), human(a.AgentCacheReadTokens),
			human(a.AgentOutputTokens), a.AgentCostUSD, float64(a.AgentMs)/1000)
		row("sub-agent stops", "%s", orNone(joinCounts(sortedCounts(a.AgentStops), nil)))
	}
	var slow []string
	for _, s := range a.Slowest {
		slow = append(slow, fmt.Sprintf("step %d %.1fs out=%d calls=%d", s.Step, float64(s.DurationMs)/1000, s.OutputTokens, s.ToolCalls))
	}
	row("slowest model calls", "%s", orNone(strings.Join(slow, "; ")))
	_, err := io.WriteString(w, b.String())
	return err
}

func attrInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// attrFloat reads a numeric attribute (a cost, say); json numbers arrive as
// float64, but a host built in-process may store an int.
func attrFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string: // a task label (a background sub-agent's cost)
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

func attrStrings(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// countsAbove returns the entries with a count above min, most first.
func countsAbove(m map[string]int, min int) []Count {
	var out []Count
	for name, n := range m {
		if n > min && name != "" {
			out = append(out, Count{Name: name, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedCounts(m map[string]int) []Count { return countsAbove(m, 0) }

// joinCounts renders "name xN" items (the first 8), with an error count from
// errs when present.
func joinCounts(counts []Count, errs map[string]int) string {
	var parts []string
	for i, c := range counts {
		if i == 8 {
			parts = append(parts, fmt.Sprintf("... %d more", len(counts)-8))
			break
		}
		item := fmt.Sprintf("%s x%d", c.Name, c.Count)
		if n := errs[c.Name]; n > 0 {
			item += fmt.Sprintf(" (%d failed)", n)
		}
		parts = append(parts, item)
	}
	return strings.Join(parts, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// human renders a count compactly: 1380000 -> "1.38M", 60100 -> "60.1k".
func human(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", float64(n)/1e6), "0"), ".") + "M"
	case n >= 1000:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", float64(n)/1e3), "0"), ".") + "k"
	}
	return fmt.Sprint(n)
}

// uncachedSummary renders the cache attribution row: the total, then the
// estimated parts and the prefix breaks by reason.
func uncachedSummary(a TraceAnalysis) string {
	out := human(a.Uncached)
	if a.UncachedFresh == 0 && len(a.UncachedRebuilt) == 0 && a.UncachedProvider == 0 {
		return out // no prefix attrs (older session)
	}
	var rebuilt []string
	var sum int64
	for _, c := range sortedCounts(intCounts(a.UncachedRebuilt)) {
		rebuilt = append(rebuilt, fmt.Sprintf("%s %s x%d", c.Name, human(a.UncachedRebuilt[c.Name]), a.PrefixBreaks[c.Name]))
		sum += a.UncachedRebuilt[c.Name]
	}
	out += fmt.Sprintf(" ≈ fresh %s + rebuilt %s", human(a.UncachedFresh), human(sum))
	if len(rebuilt) > 0 {
		out += " (" + strings.Join(rebuilt, ", ") + ")"
	}
	return out + fmt.Sprintf(" + provider-side %s", human(a.UncachedProvider))
}

// intCounts narrows token sums to ints for sorting by size.
func intCounts(m map[string]int64) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = int(v)
	}
	return out
}

// phaseSteps renders a phase length, "-" when the run never got there.
func phaseSteps(n int) string {
	if n < 0 {
		return "-"
	}
	return fmt.Sprint(n)
}

// taskWaitAction is the task tool action that waits for a task to end.
const taskWaitAction = "wait"

// Task kinds of a task end event (attrs kind, from tasks.Spec.Kind).
const (
	taskKindWatch = "watch"
	taskKindHook  = "hook"
	taskKindAgent = "agent"
)

// taskAction returns the action of a `task` tool call (list, output, wait,
// kill) and the task id it names. The action is read from the call's argument
// summary (attrs arg, "action=wait id=t1"); an explicit action attr wins when
// a newer recorder writes one.
func taskAction(e store.Event) (action, id string) {
	if a, _ := e.Attrs["action"].(string); a != "" {
		id, _ = e.Attrs["id"].(string)
		return a, id
	}
	arg, _ := e.Attrs["arg"].(string)
	for _, field := range strings.Fields(arg) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "action":
			action = value
		case "id":
			id = value
		}
	}
	return action, id
}

// span is a half-open time interval in milliseconds since the epoch.
type span struct{ start, end int64 }

// appendSpan adds the interval an event occupied: from its start for its
// duration. An event without a start time carries no timing and is left out.
func appendSpan(spans []span, e store.Event) []span {
	if e.StartedAt.IsZero() {
		return spans
	}
	start := e.StartedAt.UnixMilli()
	return append(spans, span{start: start, end: start + max(e.DurationMs, 0)})
}

// mergeSpans sorts spans by start and merges the ones that overlap or touch,
// so every instant is covered by one span only (parallel calls count once).
func mergeSpans(spans []span) []span {
	if len(spans) < 2 {
		return spans
	}
	sortSpans(spans)
	out := make([]span, 1, len(spans))
	out[0] = spans[0]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if s.start <= last.end {
			last.end = max(last.end, s.end)
			continue
		}
		out = append(out, s)
	}
	return out
}

// sortSpans orders spans by start, earliest first.
func sortSpans(spans []span) []span {
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	return spans
}

// overlapMs is the time the spans in a (sorted by start, possibly overlapping
// each other) share with the merged spans in b (also sorted). A span of a
// without any part inside b adds nothing.
func overlapMs(a, b []span) int64 {
	var total int64
	for j, i := 0, 0; i < len(a); i++ {
		for j < len(b) && b[j].end <= a[i].start {
			j++
		}
		for k := j; k < len(b) && b[k].start < a[i].end; k++ {
			if shared := min(a[i].end, b[k].end) - max(a[i].start, b[k].start); shared > 0 {
				total += shared
			}
		}
	}
	return total
}
