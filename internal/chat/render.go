package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/inhere/suga/internal/i18n"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gookit/cliui/show/symbols"
	"github.com/gookit/color"
	"github.com/inhere/suga/internal/chat/lineedit"
	"github.com/inhere/suga/internal/diffview"
	"github.com/inhere/suga/internal/output"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	"golang.org/x/term"
)

const (
	// resultSummaryLimit caps the one-line tool result summary, in runes.
	resultSummaryLimit = 100
	// callArgsLimit caps the tool_call argument text on screen, in runes.
	callArgsLimit = 120

	promptText        = "❯ "
	promptWaitingText = "❯ (可等待外部回复) "

	// interruptNotice is what chat shows when a turn was interrupted while it
	// ran (Ctrl-C): never the runner's raw "context canceled". cmd/suga still
	// reports the process-wide 已中断 with exit 130, and run, ACP and stream-json
	// keep the raw error text.
	interruptNotice = "已中断本轮"
	// interruptText is that wording for an interrupted command (:compact), which
	// is not a turn.
	interruptText = "已中断"

	// replyTailLines caps how much of a continued session's last reply chat shows
	// above the first prompt: the tail is what the person goes on from, and the
	// whole text stays readable with `suga inspect trace`.
	replyTailLines = 40
)

// rendererOptions configures a Renderer. Color switches ANSI styling for Out
// and ErrColor for Err; both false yields plain text (symbols stay).
type rendererOptions struct {
	Out, Err io.Writer
	// Translator renders the human-facing text; nil uses the Chinese fallback
	// (see trOf in i18n.go).
	Translator      i18n.Translator
	Color, ErrColor bool
	// Provider and Model label the per-turn status line; either may be empty.
	Provider, Model string
	// Thinking and ContextWindow follow the model in the banner when set.
	Thinking      string
	ContextWindow int
	// SessionName is the session's short name ("<workspace directory>-k7"); the
	// banner shows it and it is the address `suga send --session <name>` accepts.
	// It is set after the local session socket is registered, so it stays empty
	// when registration failed.
	SessionName string
	// WriteTools selects the banner's tool line (write and shell on or off).
	WriteTools bool
	// Workspace is the root the file tools work in: the tool lines show a path
	// inside it relative to it (empty: every path as the call wrote it).
	Workspace string
	// Markdown renders the model's text as terminal markdown (config
	// chat.markdown); it only applies to a colored terminal.
	Markdown bool
	// Diff shows what a file tool call changed (config chat.diff, on by default):
	// the unified diff under the tool result, the pending one before an approval
	// question, and the :diff command. DiffLines caps how many lines one preview
	// shows (chat.diff_lines, default 20); :diff shows the whole change. The
	// change itself arrives with the tool_call event (personal.Config.DiffPreview).
	Diff      bool
	DiffLines int
	// CodeHighlight colors a fenced code block by the language of its fence
	// (config chat.code_highlight, on by default), CodeMaxLines folds a block after
	// that many lines (chat.code_max_lines, default 40; 0 folds nothing), naming
	// :code for the rest. Both need the markdown rendering and a colored terminal.
	// Theme is the palette the markdown elements are painted with (chat.theme,
	// "dark" or "light" as resolved; empty keeps the dark default).
	CodeHighlight bool
	CodeMaxLines  int
	Theme         string
	// InputLineUp is true while chat keeps its input line on screen for the whole
	// turn running (chat.steering with a terminal). The output then works around
	// that line (r3 M1): the streamed text leaves only complete lines - the line
	// editor repaints its line after every write, so a partial one would stay next
	// to it - and the waiting line becomes a status line above that input line
	// (Status), which the line editor keeps clear of every write of its own.
	InputLineUp bool
	// Status is where that status line goes (the input source); nil while chat keeps
	// no input line on screen.
	Status statusSetter
	// Spinner shows the "思考中…" waiting line while the model is asked for its
	// next message (config chat.spinner, on by default). Like Markdown it needs
	// a colored terminal: a pipe, a redirected output or NO_COLOR keeps it off.
	Spinner bool
	// AgentProgress controls sub-agent tool-call display: summary, full or off.
	AgentProgress string
	// AgentTasks returns the session's background tasks. The status line counts a
	// running background agent by its task there, so a task that ended, failed or
	// was killed stops being counted whether or not its notice reached the model
	// (nil: a background agent is not shown, nothing could tell that it ended).
	AgentTasks func() []tasks.Info
	// now supplies the local wall clock of the end-of-turn time on the status
	// line (nil: time.Now). Tests inject a fixed clock.
	now func() time.Time
	// HighlightInput paints the messages that come from elsewhere (External) on a
	// background of their own (config chat.highlight_input); the input source
	// paints the line the person submitted with the same switch. Like Markdown it
	// needs a colored terminal.
	HighlightInput bool
	// HighlightWidth returns the column count a highlighted line is filled to, so
	// its background reaches the end of the terminal row (nil, 0 or no terminal:
	// the line's own text only). A func, not a number: the window may be resized
	// while chat runs.
	HighlightWidth func() int
	// spinnerNow and spinnerTicks drive that waiting line in tests (nil: the
	// real clock and a time.Ticker).
	spinnerNow   func() time.Time
	spinnerTicks func(time.Duration) (<-chan time.Time, func())
}

// Renderer owns all terminal output of one chat: prompt, streamed model text,
// tool lines, diff previews, plans, errors and the per-turn status line.
type Renderer struct {
	opts rendererOptions
	live *output.LiveText
	// spin is the waiting line between a model request and its first output.
	spin *thinkingSpinner
	// memory collects this turn's successful memory writes, shown with the
	// status line ("已记住：...").
	memory []string
	// changes are the file changes this chat showed, in order, numbered from 1:
	// the :diff command shows one of them again in full (diff.go).
	changes []*diffview.Change
	// pending is the change the running tool call announced (its tool_call event
	// arrived, its result not yet): the approval question shows it and the result
	// prints it.
	pending *diffview.Change
	// budget is the latest budget snapshot received during this turn.
	budget *personal.BudgetSnapshot
	// budgetNoticed prevents repeated threshold reminders within one turn.
	budgetNoticed bool
	// agentProgress tracks the running agent tool calls of this chat by call ID
	// (the state carries the description for display). It deliberately lives on
	// the renderer, not in the event stream, so concurrent children cannot share
	// counts.
	agentProgress map[string]*agentProgressState
	// agentMu guards agentProgress: a background sub-agent reports on the
	// goroutine of the turn that started it while chat's own main loop (:tasks -k,
	// a task notice) reads and ends entries.
	agentMu sync.Mutex
}

// agentProgressState is one running agent tool call: the call ID is the key and
// the description only labels it. A foreground entry ends with its tool result
// (a failed or cancelled run leaves no summary) or, at the latest, with the
// turn; a background one lives while its task in the tasks manager runs.
type agentProgressState struct {
	description string
	calls       int
	started     time.Time
	background  bool
	// taskID is the background task the call started (empty: a foreground call
	// or one whose result never arrived).
	taskID string
}

// newRenderer returns a renderer that shows no waiting line yet: the first turn
// starts it (chat prints the banner before that).
func newRenderer(opts rendererOptions) *Renderer {
	r := &Renderer{opts: opts, agentProgress: make(map[string]*agentProgressState)}
	r.spin = newThinkingSpinner(spinnerConfig{
		enabled: opts.Spinner && opts.Color,
		out:     opts.Out,
		status:  opts.Status,
		paint:   r.paintDim,
		speed:   spinnerSpeed,
		now:     opts.spinnerNow,
		ticks:   opts.spinnerTicks,
	})
	r.resetLive()
	return r
}

// localNow is the wall clock of the renderer: the injected one in tests, the
// real local time otherwise.
func (r *Renderer) localNow() time.Time {
	if r.opts.now != nil {
		return r.opts.now()
	}
	return time.Now()
}

// paintDim dims a line (gray, like the tool lines) on a colored terminal.
func (r *Renderer) paintDim(s string) string { return r.paint(color.Gray, s) }

// colorEnabledFor reports whether w is a terminal and NO_COLOR is not set.
func colorEnabledFor(w io.Writer) bool {
	file, ok := w.(*os.File)
	return colorEnabled(ok && term.IsTerminal(int(file.Fd())))
}

// colorEnabled applies the NO_COLOR convention (any non-empty value disables).
func colorEnabled(isTTY bool) bool {
	if v, ok := os.LookupEnv("NO_COLOR"); ok && v != "" {
		return false
	}
	return isTTY
}

func (r *Renderer) paint(c color.Color, s string) string {
	// Clean external text before adding our own SGR style, so plain and colored output are safe.
	s = termtext.Clean(s)
	if !r.opts.Color {
		return s
	}
	return c.Text(s)
}

// Prompt returns the input prompt; waiting marks a pending external reply.
func (r *Renderer) Prompt(waiting bool) string {
	if waiting {
		return r.paint(color.Cyan, promptWaitingText)
	}
	return r.paint(color.Cyan, promptText)
}

// BeginTurn resets the per-turn text streaming state and starts the waiting
// line: the model was just asked for a new message.
func (r *Renderer) BeginTurn() {
	r.stopThinking() // nothing of the previous turn may linger on the line
	r.resetLive()
	r.budget = nil
	r.budgetNoticed = false
	r.startThinking()
}

// resetLive resets the per-turn text streaming state.
func (r *Renderer) resetLive() {
	r.live = output.NewPrefixedLiveText(r.opts.Out, r.answerPrefix())
	// Whole lines first: the markdown renderer is built knowing whether it prints the
	// message prefix itself.
	r.live.SetWholeLines(r.opts.InputLineUp)
	// Markdown needs a terminal: Color is exactly "a terminal without NO_COLOR",
	// so a pipe, a redirected output or NO_COLOR keeps the raw text.
	if r.opts.Markdown && r.opts.Color {
		r.live.SetMarkdown(true)
		// The palette the elements are painted with (chat.theme, resolved to dark
		// or light before chat starts).
		r.live.SetMarkdownTheme(r.opts.Theme)
		// Code highlighting and folding, and the terminal width a table must fit
		// in (:code, mdcode.go, mdtable.go).
		r.live.SetCode(r.opts.CodeHighlight, r.opts.CodeMaxLines, r.highlightWidth())
	}
	r.memory = nil
}

// startThinking shows the waiting state: the model is working on its next
// message. A no-op while the spinner is off (chat.spinner: false or no colored
// terminal) or the line is already shown.
func (r *Renderer) startThinking() {
	// The status row and the standalone spinner share one label, so summaries
	// stay in the existing waiting indicator rather than adding another painter.
	r.spin.SetLabel(r.agentStatusLabel())
	r.spin.Start()
}

// agentProgressMode keeps direct renderer tests and callers without config on the
// documented default.
func (r *Renderer) agentProgressMode() string {
	switch r.opts.AgentProgress {
	case "full", "off", "summary":
		return r.opts.AgentProgress
	default:
		return "summary"
	}
}

func (r *Renderer) agentStatusLabel() string {
	if r.agentProgressMode() != "summary" {
		return spinnerLabel
	}
	parts := r.runningAgents()
	if len(parts) == 0 {
		return spinnerLabel
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		texts = append(texts, fmt.Sprintf("%s %d 次调用", truncateLine(part.description, 24), part.calls))
	}
	return truncateLine(fmt.Sprintf("子 agent：%d 个运行中（%s）", len(parts), strings.Join(texts, "、")), 120)
}

// agentStatusPart is one running agent the status line counts.
type agentStatusPart struct {
	id          string
	description string
	calls       int
}

// runningAgents is what the status line counts as running, in display order: the
// foreground entries of this turn and the manager's running agent tasks. The
// tasks manager is the truth for a background agent - its task leaves the
// running set when it ends, fails or is killed, so the count never depends on a
// notice being delivered - and an entry whose task is gone is dropped here.
func (r *Renderer) runningAgents() []agentStatusPart {
	tasksByID := make(map[string]tasks.Info)
	for _, info := range r.agentTasks() {
		if info.Kind == "agent" && info.Status == tasks.Running {
			tasksByID[info.ID] = info
		}
	}
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	parts := make([]agentStatusPart, 0, len(r.agentProgress)+len(tasksByID))
	for id, state := range r.agentProgress {
		if state == nil {
			delete(r.agentProgress, id)
			continue
		}
		if state.taskID == "" {
			parts = append(parts, agentStatusPart{id: id, description: state.description, calls: state.calls})
			continue
		}
		if _, running := tasksByID[state.taskID]; !running {
			delete(r.agentProgress, id)
			continue
		}
		parts = append(parts, agentStatusPart{id: id, description: state.description, calls: state.calls})
		delete(tasksByID, state.taskID)
	}
	// A running agent task no entry knows (its call ended without a result) is
	// still a running sub-agent: name it by its task.
	for id, info := range tasksByID {
		parts = append(parts, agentStatusPart{id: id, description: strings.TrimPrefix(info.Summary, "agent: ")})
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].description != parts[j].description {
			return parts[i].description < parts[j].description
		}
		return parts[i].id < parts[j].id
	})
	return parts
}

// agentTasks is the renderer's view of the session's tasks (nil: none).
func (r *Renderer) agentTasks() []tasks.Info {
	if r.opts.AgentTasks == nil {
		return nil
	}
	return r.opts.AgentTasks()
}

// refreshAgentStatus puts what the line says back in place after a background
// agent changed outside the event stream (its task ended or was killed).
func (r *Renderer) refreshAgentStatus() { r.spin.SetLabel(r.agentStatusLabel()) }

// EndTurnAgents forgets the entries no background task backs: a foreground
// sub-agent ends with its turn (a cancelled turn leaves no tool result, a failed
// one no summary), so the status line of the next turn starts clean.
func (r *Renderer) EndTurnAgents() {
	r.agentMu.Lock()
	for id, state := range r.agentProgress {
		if state == nil || state.taskID == "" {
			delete(r.agentProgress, id)
		}
	}
	r.agentMu.Unlock()
	r.refreshAgentStatus()
}

// beginAgent records the agent tool call of event, so the status line counts
// the sub-agent's tool calls under that call. The call ID is the key and the
// description is only its label: two children with the same description stay
// two entries. A provider that sends no call id leaves only the description as a
// key (the turn's end still clears such an entry).
func (r *Renderer) beginAgent(event personal.Event) {
	if event.ToolName != "agent" {
		return
	}
	description, background := agentCallDetails(event.Text)
	if description == "" {
		return
	}
	r.agentMu.Lock()
	r.agentProgress[agentEntryKey(event.CallID, description)] = &agentProgressState{description: description, started: time.Now(), background: background}
	r.agentMu.Unlock()
}

// agentEntryKey is the entry of an agent call: its call ID, or a key built from
// the description when the provider sent no call id.
func agentEntryKey(callID, description string) string {
	if callID != "" {
		return callID
	}
	return "desc:" + description
}

func agentCallDetails(text string) (string, bool) {
	var args struct {
		Description string `json:"description"`
		Background  bool   `json:"background"`
	}
	if err := json.Unmarshal([]byte(text), &args); err != nil {
		return "", false
	}
	return strings.TrimSpace(args.Description), args.Background
}

// finishAgent ends the entry of an agent tool result. Any end counts - a report,
// an error or a refusal - so an entry never outlives its call. A background call
// keeps its entry: the task it started is what runs, and the manager's task list
// decides when it is gone; its summary line comes with that task's notice
// (AgentTaskSummaries).
func (r *Renderer) finishAgent(event personal.Event) {
	if event.ToolName != "agent" {
		return
	}
	summary := event.AgentSummary
	key := event.CallID
	if key == "" && summary != nil {
		key = agentEntryKey("", summary.Description)
	}
	r.agentMu.Lock()
	state := r.agentProgress[key]
	background := (state != nil && state.background) || (summary != nil && summary.Background)
	calls := 0
	if state != nil {
		calls = state.calls
	}
	switch {
	case background:
		// The task keeps the entry alive (runningAgents); its id is how the
		// manager's task list is matched against it.
		if state != nil && summary != nil {
			state.taskID = summary.TaskID
		}
	case key != "":
		delete(r.agentProgress, key)
	}
	r.agentMu.Unlock()
	if background || summary == nil || summary.Description == "" {
		return
	}
	if summary.ToolCalls > calls {
		calls = summary.ToolCalls
	}
	if r.agentProgressMode() == "summary" {
		fmt.Fprintf(r.opts.Out, "子 agent %s：%d 次工具调用，耗时 %s\n", truncateLine(summary.Description, 40), calls, formatElapsed(summary.Duration))
	}
}

// dropAgentTask forgets the entry of a background agent task that ended.
func (r *Renderer) dropAgentTask(taskID string) {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	for id, state := range r.agentProgress {
		if state != nil && state.taskID == taskID {
			delete(r.agentProgress, id)
		}
	}
}

// AgentTaskSummaries prints completions of background agents before their task
// notice. The task manager owns their counters because they outlive the turn.
func (r *Renderer) AgentTaskSummaries(infos []tasks.Info) {
	if r.agentProgressMode() != "summary" {
		return
	}
	for _, info := range infos {
		if info.Kind != "agent" || !strings.HasPrefix(info.Summary, "agent: ") {
			continue
		}
		name := strings.TrimPrefix(info.Summary, "agent: ")
		calls := info.Counters["tool_calls"]
		fmt.Fprintf(r.opts.Out, "子 agent %s：%d 次工具调用，耗时 %s\n", truncateLine(name, 40), calls, formatElapsed(info.Duration()))
		r.dropAgentTask(info.ID)
	}
	r.refreshAgentStatus()
}
func (r *Renderer) toolRunning(name string) {
	if !r.statusLine() {
		r.spin.Stop()
		return
	}
	r.spin.SetLabel(toolLabel(name))
}

// toolCallArgs is the package's toolCallArgs (tool_summary.go) with the path
// handling of this chat: paths inside the workspace are shown relative to it.
func (r *Renderer) toolCallArgs(name, text string, full map[string]any) string {
	return toolCallArgs(name, text, full, r.opts.Workspace)
}

// stopThinking clears the renderer's own waiting line before a write of this
// renderer, so a frame can never mix into a tool line, a line of the turn's
// status, an answer or a prompt. With the status line above the input line there
// is nothing to do: the line editor keeps that row clear of every write by
// itself, so it stays up for the whole turn (its end takes it down: endThinking).
func (r *Renderer) stopThinking() {
	if r.statusLine() {
		return
	}
	r.spin.Stop()
}

// endThinking ends the turn's waiting state for good: the line of its own is
// erased, and the status line above the input line goes away.
func (r *Renderer) endThinking() { r.spin.Stop() }

// suspendStatus takes the status line off screen while another part of chat owns
// the input line (an approval question asks on it) and resumeStatus brings it
// back with what the turn is waiting for. Both are no-ops without a status line.
func (r *Renderer) suspendStatus() { r.spin.Suspend() }
func (r *Renderer) resumeStatus()  { r.spin.Resume() }

// answerPrefix marks the start of each model message so it stands apart from
// tool lines and the user's input.
func (r *Renderer) answerPrefix() string { return r.paint(color.Green, "▣") + " " }

// Handle renders one runner event: text deltas, tool calls/results, plans and
// budget observations. Events the chat does not render (run_started, usage,
// ...) leave the waiting line alone; budget observations update the final line
// and may emit one threshold reminder per turn.
func (r *Renderer) Handle(event personal.Event) {
	if budget := eventBudget(event); budget != nil {
		r.noteBudget(budget)
	}
	if !r.statusLine() && stopsWaiting(event) {
		r.stopThinking()
	}
	if r.live.Handle(event) {
		return
	}
	switch event.Kind {
	case "tool_call":
		r.live.Finish() // never start a tool line in the middle of streamed text
		r.beginAgent(event)
		line := "⏺ " + event.ToolName
		if args := r.toolCallArgs(event.ToolName, event.Text, event.ToolArgs); args != "" {
			line += " " + args
		}
		fmt.Fprintln(r.opts.Out, r.paint(color.Gray, line))
		r.toolRunning(event.ToolName) // the tool runs now, the model does not
		// A file change was read before the tool ran (personal.Config.DiffPreview);
		// the approval question and result below show it.
		r.noteToolCall(event.Change)
	case "tool_result":
		r.live.Finish()
		change, tool := r.pending, event.ToolName
		r.pending = nil
		summary := truncateLine(redact.Secrets(event.Text), resultSummaryLimit)
		if event.IsError {
			fmt.Fprintln(r.opts.Out, r.paint(color.Red, strings.TrimRight("  ⎿ error: "+summary, " ")))
		} else {
			fmt.Fprintln(r.opts.Out, r.paint(color.Gray, strings.TrimRight("  ⎿ ok: "+summary, " ")))
			r.noteMemory(event)
			r.showToolResult(change, tool)
		}
		r.finishAgent(event)
		r.startThinking() // the model reads the result and thinks on
	case "agent_progress":
		// A sub-agent's tool call, under the agent call that started it. It is
		// counted on that call's entry only: an event of a call the renderer does
		// not know must not create one, or the status line would count an agent
		// nothing ever ends.
		r.agentMu.Lock()
		if state := r.agentProgress[agentEntryKey(event.CallID, event.Agent)]; state != nil {
			state.calls++
		}
		r.agentMu.Unlock()
		if r.agentProgressMode() == "full" {
			r.live.Finish()
			line := "  · " + event.ToolName
			if args := r.toolCallArgs(event.ToolName, event.Text, event.ToolArgs); args != "" {
				line += " " + args
			}
			fmt.Fprintln(r.opts.Out, r.paint(color.Gray, line))
		}
		r.startThinking() // the model waits for the sub-agent
	case "plan":
		if event.Plan != nil {
			r.live.Finish()
			r.renderPlan(event)
			r.startThinking()
		}
	case string(agentcore.StreamEventRetry):
		fmt.Fprintln(r.opts.Out, r.paint(color.Yellow, output.RetryNotice(event)))
		r.startThinking() // the request is retried: still waiting for the model
	case "loop_guard":
		// The loop guard reminds the model not to repeat an identical tool call
		// (internal/personal/loop_guard.go): the person sees one gray line for it.
		r.live.Finish()
		fmt.Fprintln(r.opts.Out, r.paint(color.Gray, event.Text))
		r.startThinking()
	case personal.EventSessionRecovered:
		// The session was left running by a process that died mid-turn and has
		// been taken back to open before this turn (personal/recovery.go): say so
		// on a line of its own, then keep waiting for the model.
		r.stopThinking()
		fmt.Fprintln(r.opts.Out, r.paint(color.Yellow, personal.SessionRecoveredNotice))
		r.startThinking()
	}
}

// eventBudget reads both locations used by the personal event stream: budget
// events carry it at the top level, while run_finished carries it in Usage.
func eventBudget(event personal.Event) *personal.BudgetSnapshot {
	if event.Budget != nil {
		return event.Budget
	}
	if event.Usage != nil {
		return event.Usage.Budget
	}
	return nil
}

func (r *Renderer) noteBudget(snapshot *personal.BudgetSnapshot) {
	r.budget = snapshot
	share, _, ok := budgetShare(snapshot)
	if !ok || r.budgetNoticed || snapshot.NoticeRatio < 0 || float64(share)/100 < snapshot.NoticeRatio {
		return
	}
	r.budgetNoticed = true
	if text := budgetNoticeText(snapshot); text != "" {
		r.Notice(text)
		r.startThinking()
	}
}

// statusLine reports whether what the turn waits for shows on a row above the
// input line (chat.steering keeps that line on screen for the whole turn)
// instead of on a line of its own. The status text only changes there, since
// the line editor clears that row around every write anyway; a line of its own
// has to be taken down before each one.
func (r *Renderer) statusLine() bool { return r.spin.statusRow() }

// stopsWaiting reports whether event ends the "the model is answering" state:
// its text, a tool call it asked for, a plan it published, or the next request
// after a failed one.
func stopsWaiting(event personal.Event) bool {
	switch event.Kind {
	case string(agentcore.StreamEventTextDelta), string(agentcore.StreamEventMessageStop),
		string(agentcore.StreamEventRetry), "tool_call", "tool_result", "agent_progress":
		return true
	case "plan":
		return event.Plan != nil // an empty plan prints nothing
	}
	return false
}

func (r *Renderer) renderPlan(event personal.Event) {
	head := "⏺ plan"
	if event.Plan.Explanation != "" {
		head += " " + truncateLine(event.Plan.Explanation, callArgsLimit)
	}
	fmt.Fprintln(r.opts.Out, r.paint(color.Gray, head))
	for _, step := range event.Plan.Steps {
		switch step.Status {
		case "completed":
			fmt.Fprintln(r.opts.Out, r.paint(color.Green, fmt.Sprintf("  %c %s", symbols.OK, step.Step)))
		case "in_progress":
			fmt.Fprintln(r.opts.Out, r.paint(color.Yellow, "  ▶ "+step.Step))
		default:
			fmt.Fprintln(r.opts.Out, r.paint(color.Gray, "  ○ "+step.Step))
		}
	}
}

// Finish closes the turn's streamed text and reports whether any was shown.
func (r *Renderer) Finish() bool {
	r.endThinking()
	return r.live.Finish()
}

// Answer prints a final answer that was not streamed: the streamed text's own
// renderer does it, so the message prefix, markdown and whole-line handling stay
// exactly the ones used by the streamed case.
func (r *Renderer) Answer(text string) {
	r.endThinking()
	r.live.Answer(text)
}

// SteerWaiting prints the line of a message a person inserted into the running
// turn that no model request has picked up yet: dim, like a tool line, so it is
// not taken for the model's answer.
func (r *Renderer) SteerWaiting(text string) {
	r.stopThinking()
	fmt.Fprintln(r.opts.Out, r.paintDim(steerNotice(text, steerWaitingSuffix)))
}

// SteerLate prints that line when the turn did not take the message (it was
// already ending, or its inbox was full): it becomes the person's next input.
func (r *Renderer) SteerLate(text string) {
	r.stopThinking()
	fmt.Fprintln(r.opts.Out, r.paintDim(steerNotice(text, steerLateSuffix)))
}

// SteerDelivered prints the message again once a model request of the turn
// carried it (its "steer_delivered" event), on the background of the person's
// own input line (chat.highlight_input) - the same style the line submitted at
// the prompt gets, so a delivered message reads as the person's own.
func (r *Renderer) SteerDelivered(text string) {
	r.stopThinking()
	line := steerNotice(text, steerTakenSuffix)
	if r.highlightInput() {
		line = inputHighlightPrefix + lineedit.PadLine(line, r.opts.HighlightWidth()) + highlightSuffix
	}
	fmt.Fprintln(r.opts.Out, line)
}

// SteerWithdrawn prints the message once the person took it back out of the
// running turn's inbox with Up on an empty input line: the waiting line becomes
// the withdrawn one, the same in-place update a delivered message gets (SteerDelivered),
// and the text is on the input line for editing again.
func (r *Renderer) SteerWithdrawn(text string) {
	r.stopThinking()
	fmt.Fprintln(r.opts.Out, r.paintDim(steerNotice(text, steerWithdrawnSuffix)))
}

// FollowUpQueued prints the line a Tab press queued during a turn (followup.go):
// it does not go into the running turn, it waits for the next one.
func (r *Renderer) FollowUpQueued(text string) {
	r.stopThinking()
	fmt.Fprintln(r.opts.Out, r.paintDim(followUpNotice(text, followUpQueuedSuffix)))
}

// FollowUpSent prints that line again when the turn it was queued for starts.
func (r *Renderer) FollowUpSent(text string) {
	r.stopThinking()
	fmt.Fprintln(r.opts.Out, r.paintDim(followUpNotice(text, followUpSentSuffix)))
}

// followUpNotice is the one-line display of a queued message, laid out like
// steerNotice.
func followUpNotice(text, suffix string) string {
	return "[排队] " + truncateLine(text, resultSummaryLimit) + suffix
}

// steerNotice is the one-line display of an inserted message: the marker, the
// message collapsed like a tool line, and its state.
func steerNotice(text, suffix string) string {
	return "[插话] " + truncateLine(text, resultSummaryLimit) + suffix
}

// External prints input that did not come from this keyboard (a reply sent
// from elsewhere through `suga send` or a Stop hook) in its own color, so it
// stands out from the model's answer. With chat.highlight_input on and a colored
// terminal each line is painted on a background of its own instead (the magenta
// family, so it is not taken for the person's own line), reaching the end of the
// terminal row.
func (r *Renderer) External(text string) {
	r.stopThinking()
	text = termtext.Clean(text)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if r.highlightInput() {
			lines[i] = externalHighlightPrefix + lineedit.PadLine(line, r.highlightWidth()) + highlightSuffix
			continue
		}
		lines[i] = r.paint(color.Magenta, line)
	}
	fmt.Fprintln(r.opts.Out, strings.Join(lines, "\n"))
}

// highlightInput reports whether the highlighted input lines are on: config
// chat.highlight_input with a colored terminal (a pipe, a redirected output or
// NO_COLOR keeps the plain colors).
func (r *Renderer) highlightInput() bool { return r.opts.Color && r.opts.HighlightInput }

// highlightWidth is the fill width of a highlighted line (0: fill nothing).
func (r *Renderer) highlightWidth() int {
	if r.opts.HighlightWidth == nil {
		return 0
	}
	return r.opts.HighlightWidth()
}

// ApprovalDetails formats the full key arguments of an approval question, one
// indented item per detail (continuation lines indented further). Nothing is
// truncated: the person must see exactly what will run.
func (r *Renderer) ApprovalDetails(q tools.Question) string {
	r.stopThinking()
	var b strings.Builder
	for _, detail := range q.Details {
		for i, line := range strings.Split(detail, "\n") {
			indent := "  "
			if i > 0 {
				indent = "    "
			}
			b.WriteString(r.paint(color.Yellow, indent+line))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ApprovalPrompt is the line the approval answer is typed on.
func (r *Renderer) ApprovalPrompt(q tools.Question) string {
	r.stopThinking()
	return r.paint(color.Yellow, q.Prompt)
}

// Hint prints a gray one-line hint on the chat output.
func (r *Renderer) Hint(text string) {
	r.stopThinking()
	fmt.Fprintln(r.opts.Out, r.paint(color.Gray, text))
}

// Interrupt reports a turn that was interrupted while it ran (Ctrl-C) on the
// chat output, in place of the runner's cancellation error.
func (r *Renderer) Interrupt() { r.Notice(interruptNotice) }

// Notice prints a plain informational line on the chat output.
func (r *Renderer) Notice(text string) {
	r.endThinking()
	fmt.Fprintln(r.opts.Out, termtext.Clean(text))
}

// Error prints a failed turn on the error stream.
func (r *Renderer) Error(err error) {
	r.endThinking()
	text := termtext.Clean("Error: " + err.Error())
	if r.opts.ErrColor {
		text = color.Red.Text(text)
	}
	fmt.Fprintln(r.opts.Err, text)
}

// memoryVerbs names a memory write on the end-of-turn line.
var memoryVerbs = map[string]string{
	native.MemoryActionAdd:     "已记住",
	native.MemoryActionReplace: "已更新",
	native.MemoryActionRemove:  "已删除",
}

// noteMemory remembers a successful memory write of this turn (parsed from
// the memory tool result) for the end-of-turn line.
func (r *Renderer) noteMemory(event personal.Event) {
	if event.ToolName != "memory" {
		return
	}
	change, ok := native.ParseMemoryResult(event.Text)
	if !ok {
		return
	}
	title := truncateLine(redact.Secrets(change.Title), resultSummaryLimit)
	r.memory = append(r.memory, fmt.Sprintf("%s：%s（%s）", memoryVerbs[change.Action], title, change.Scope))
}

// Status prints the end of a turn on the chat output: the memory lines of the
// turn and the status line (model, token usage, context share, elapsed time,
// local end time, inserted messages). The text and its styles are built by
// BuildTurnStatus (layout.go); this only collects the data.
func (r *Renderer) Status(result personal.RunResult, elapsed time.Duration, steers int) {
	r.endThinking()
	printLayout(r.opts.Out, r.opts.Color, BuildTurnStatus(TurnStatus{
		Memory:   r.memory,
		Provider: r.opts.Provider, Model: r.opts.Model,
		Usage: result.Usage, Budget: r.budget,
		LastInputTokens: result.LastInputTokens, ContextWindow: r.opts.ContextWindow,
		Elapsed: elapsed, EndTime: r.localNow(), Steers: steers,
		// An answer cut off at the output limit is named in the status line.
		Truncated: result.Truncated, OutputLimit: result.OutputLimit,
	}))
	r.memory = nil
}

// LastReply prints the last reply of a session chat continues, above the first
// prompt: the dim label and notes of BuildLastReplyNotices (layout.go) first,
// so it is not taken for a fresh answer, then the reply itself, rendered like
// an answer.
func (r *Renderer) LastReply(text, sessionID string, incomplete bool) {
	r.endThinking()
	body := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	omitted := len(body) - replyTailLines
	if omitted < 0 {
		omitted = 0
	}
	if omitted > 0 {
		body = body[omitted:]
	}
	printLayout(r.opts.Out, r.opts.Color, BuildLastReplyNotices(LastReplyInfo{
		Incomplete: incomplete, Omitted: omitted, SessionID: sessionID,
	}))
	// The same marker and markdown rendering as a streamed answer.
	r.Answer(strings.Join(body, "\n"))
}

// SetModel updates the model label used by the banner and every later status line.
func (r *Renderer) SetModel(provider, model, thinking string, contextWindow int) {
	r.opts.Provider, r.opts.Model = provider, model
	r.opts.Thinking, r.opts.ContextWindow = thinking, contextWindow
}

// SetSessionName records the session's short name for the banner; chat calls it
// once the local session socket is registered (before Banner).
func (r *Renderer) SetSessionName(name string) { r.opts.SessionName = name }

// Banner prints the chat header: the model in use and the commands. The text
// and its styles are built by BuildBanner (layout.go).
func (r *Renderer) Banner() {
	r.endThinking()
	printLayout(r.opts.Out, r.opts.Color, BuildBanner(BannerInfo{
		Provider: r.opts.Provider, Model: r.opts.Model,
		Thinking: r.opts.Thinking, ContextWindow: r.opts.ContextWindow,
		SessionName: r.opts.SessionName, WriteTools: r.opts.WriteTools,
	}))
}

// clockTime renders the end-of-turn time as a 24 hour local HH:MM.
func clockTime(t time.Time) string { return t.Format("15:04") }

// formatTokens renders a token count compactly: 950000 -> "950k",
// 1000000 -> "1M", 1500000 -> "1.5M", 512 -> "512".
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", float64(n)/1e6), "0"), ".") + "M"
	case n >= 1000:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", float64(n)/1e3), "0"), ".") + "k"
	}
	return strconv.Itoa(n)
}

// ModelLabel is "provider/model" (or whichever part is known).
func ModelLabel(provider, model string) string { return modelLabel(provider, model) }

func modelLabel(provider, model string) string {
	switch {
	case provider != "" && model != "":
		return provider + "/" + model
	case model != "":
		return model
	}
	return provider
}

// FormatElapsed renders a duration for status lines (ms, s, then 1m2s).
func FormatElapsed(d time.Duration) string { return formatElapsed(d) }

func formatElapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String() // e.g. 12m34s, 1h2m3s
	}
}

// truncateLine collapses whitespace (one display line), removes terminal controls, and cuts to limit runes.
func truncateLine(s string, limit int) string {
	s = strings.Join(strings.Fields(termtext.Clean(s)), " ")
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit-1]) + "…"
}

// toolArgsText makes the arguments of a tool call (event.Text, the compact JSON
// of personal.toolArgSummary) readable. encoding/json escapes <, > and & as
// \u003c, \u003e and \u0026 for HTML, which turns the command of a shell call
// such as `gofer ready 2>&1 | grep x` into unreadable escapes on the tool line:
// decode the text and encode it again with HTML escaping off. Numbers keep their
// exact form (decoder.UseNumber) and the keys keep the order the summary has
// (both encode a map by sorted key). Anything that is not JSON is shown as it is.
// Only the display changes; the events, the transcript and stream-json keep the
// escaped text.
func toolArgsText(text string) string {
	if text == "" {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return text
	}
	if _, err := dec.Token(); err != io.EOF {
		return text // more than one value: not the summary this is written for
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return text
	}
	// Encoder.Encode ends every value with a newline; the tool line is one line.
	return strings.TrimSuffix(buf.String(), "\n")
}
