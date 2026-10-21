package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inhere/suga/internal/chat/lineedit"
	appconfig "github.com/inhere/suga/internal/config"
	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/schedule"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

const (
	toolHookLimit = 8 * 1024
	stopHookLimit = 16 * 1024
)

// Options selects which session a chat opens: a new one, an existing one, or a
// handoff child of another session.
// ModelOption is one configured model shown by :model and offered to completion.
type ModelOption struct {
	Provider        string
	Model           string
	Thinking        string
	ContextWindow   int
	MaxOutputTokens int
}

func (m ModelOption) Ref() string { return m.Provider + "/" + m.Model }

// ModelSwitch activates a model for the next turn and returns its effective settings.
type ModelSwitch func(ctx context.Context, sessionID, ref string) (ModelOption, error)

type Options struct {
	SessionID string
	ResumeID  string
	// FromID starts a NEW session that inherits state from this session.
	FromID string
	// Asker is where the chat approver asks before running a tool. Run binds
	// the terminal input to it only when stdin is a terminal; left unbound,
	// every tool that needs a confirmation is refused. May be nil.
	Asker *tools.BindableAsker
	// WriteTools tells the banner whether file_write/shell_exec are available.
	WriteTools bool
	// Markdown renders the model's text as terminal markdown (config
	// chat.markdown, on by default). Only a colored terminal uses it: a pipe,
	// a redirected output or NO_COLOR keeps the raw text.
	Markdown bool
	// Spinner shows the "思考中…" waiting line while the model is asked for its
	// next message (config chat.spinner, on by default). Like Markdown it needs
	// a colored terminal: a pipe, a redirected output or NO_COLOR keeps it off.
	Spinner bool
	// AgentProgress controls sub-agent tool-call display: summary (default), full or off.
	AgentProgress string
	// HighlightInput paints the line the person submitted at the prompt, and the
	// messages delivered from elsewhere, on a background of their own (config
	// chat.highlight_input, on by default). Like Markdown it needs a colored
	// terminal: a pipe, a redirected output or NO_COLOR keeps the plain text.
	HighlightInput bool
	// Steering keeps the input line available while a turn runs (config
	// chat.steering, on by default): Enter inserts a message into the running
	// turn, Esc or a single Ctrl-C ends this turn only, a second Ctrl-C within
	// three seconds leaves chat. It needs a terminal: with a pipe or a redirected
	// input a turn keeps the plain behaviour (nothing is read while it runs).
	Steering bool
	// TerminalTitle sets the terminal's window/tab title to the session, "suga ·
	// <short name> · <session title>" (config chat.terminal_title, on by
	// default). Like Markdown it needs a colored terminal: a pipe, a redirected
	// output or NO_COLOR leaves the title alone.
	TerminalTitle bool
	// Diff shows what a file tool call changed (config chat.diff, on by default):
	// the unified diff under the tool result, the pending one before an approval
	// question, and the :diff command. DiffLines caps how many lines one preview
	// shows (chat.diff_lines, default 20); :diff shows the whole change. The change
	// itself arrives on the runner's tool_call events, which the CLI turns on for
	// chat (personal.Config.DiffPreview).
	Diff      bool
	DiffLines int
	// CodeHighlight colors a fenced code block by the language of its fence
	// (config chat.code_highlight, on by default) and CodeMaxLines folds a block
	// after that many lines (chat.code_max_lines, default 40; 0 folds nothing), the
	// rest being shown by the :code command. Like Markdown they need a colored
	// terminal.
	CodeHighlight bool
	CodeMaxLines  int
	// terminalTitleOut is where the title sequences go (nil: the chat output);
	// tests point it at a buffer to check when the title changes without a
	// terminal.
	terminalTitleOut io.Writer
	// Thinking (the effective thinking level, "" = provider default) and
	// ContextWindow (tokens, 0 = unknown) are shown in the banner.
	Thinking      string
	ContextWindow int
	// IdleAfter and IdleMinRatio configure the idle consolidation
	// (context.idle_after, context.idle_min_ratio); IdleAfter 0 turns it off.
	IdleAfter    time.Duration
	IdleMinRatio float64
	// Tasks are the background tasks of this chat (nil: none); TaskWake
	// signals a task end that should start a turn when chat is idle
	// (tasks.wake_on_exit; nil: never).
	Tasks    *tasks.Manager
	TaskWake <-chan struct{}
	// Notify controls terminal completion/question notifications.
	Notify      string
	NotifyAfter time.Duration
	// StopBackground runs each Stop hook as a background task of Tasks
	// (hooks.stop_background): typing does not interrupt it and a block
	// reply arrives as a task notice. Needs Tasks.
	StopBackground bool
	// scheduleOptions are extra scheduler options (tests inject a fake clock);
	// scheduleObserver sees every scheduler event synchronously (tests).
	scheduleOptions  []schedule.Option
	scheduleObserver func(schedule.Event)
	// SetupSession, when set, runs once the session is open and before the first
	// turn (the CLI publishes the session's added directories there); a failure
	// stops the chat.
	SetupSession func(ctx context.Context, sessionID string) error
	// Dirs is the session's added-directory handle (--add-dir): the :add-dir,
	// :dirs and :rm-dir commands work through it and nil leaves them out. The
	// session it belongs to is opened by SetupSession, so it is only used from
	// the loop on.
	Dirs SessionDirs
	// ReadImage loads a picture for the :image command and the Alt+V paste with
	// the view_image rules (design 0.3); the CLI builds it over the session's
	// workspace. ReadImage nil leaves both unavailable.
	ReadImage func(path string) (agentcore.ImagePart, string, error)
	// PasteImage reads one picture from the system clipboard for Alt+V (the
	// same queue as :image); the CLI builds it per-platform with the view_image
	// pipeline. PasteImage nil leaves the binding out.
	PasteImage func() (agentcore.ImagePart, string, error)
	// Vision is the live "the current model can see images" gate (nil = can
	// see), the same one the runtime moves on a :model switch.
	Vision *tools.VisionGate
	// ModelOptions and ModelSwitch back the local :model command. Keeping the
	// callback here avoids making chat depend on runtime wiring.
	ModelOptions []ModelOption
	ModelSwitch  ModelSwitch

	// Local runs a line the person typed at the prompt with a leading "!"
	// ("!cmd": run it in the local shell; "!!cmd": run it and hand its result to
	// the model with the next message). The CLI builds it with the same shell,
	// workspace and output limits shell_exec uses; nil leaves the prefix an
	// ordinary message (run, ACP, a chat without one).
	Local *native.LocalCommand
	// ContextNotices are the resident context warnings of this chat (rules
	// truncated, a soft threshold crossed, skipped or shadowed skills). Run
	// prints each once, on stderr, when the session is open; they never change
	// what the model receives. A nil slice prints nothing.
	ContextNotices []string
}

// markInteractivelyUsed records that this chat continued a session a
// non-interactive entry point created, which moves that session into the
// interactive retention class of `suga data gc --retention`. A failure is reported
// and never fatal: the session is usable either way.
func markInteractivelyUsed(stderr io.Writer, runner *personal.Runner, sessionID string) {
	if err := runner.MarkSessionUsedInteractively(context.WithoutCancel(context.Background()), sessionID); err != nil {
		fmt.Fprintf(stderr, "warning: record interactive use of %s: %v\n", sessionID, err)
	}
}

// Run drives the interactive chat REPL on top of runner; hooks may be nil.
func Run(ctx context.Context, runner *personal.Runner, opts Options, stdin io.Reader, stdout, stderr io.Writer, hooks sugahooks.Client) error {
	hooks = recordHooks(hooks, runner)
	sessionID := ""
	// startupTitle is the stored title of a session this chat continues ("" for a
	// new one, which gets its title from the first message); it names the terminal
	// title from the start.
	startupTitle := ""
	source := "startup"
	createdAtStartup := false
	if opts.FromID != "" {
		model, err := runner.Handoff(ctx, personal.HandoffRequest{SourceID: opts.FromID, NewID: opts.SessionID, Title: "New chat", Origin: personal.OriginChat})
		if err != nil {
			return personal.SessionNotFound(err, opts.FromID)
		}
		sessionID, createdAtStartup = model.ID, true
	} else if opts.ResumeID != "" {
		source = "resume"
		model, err := runner.LoadSession(ctx, opts.ResumeID)
		if err != nil {
			return personal.SessionNotFound(err, opts.ResumeID)
		}
		sessionID, startupTitle = model.ID, model.Title
		markInteractivelyUsed(stderr, runner, model.ID)
	} else if opts.SessionID != "" {
		model, err := runner.LoadSession(ctx, opts.SessionID)
		if err == nil {
			// Opening an existing session by --session-id continues it: report resume, like claude.
			sessionID, startupTitle = model.ID, model.Title
			source = "resume"
			markInteractivelyUsed(stderr, runner, model.ID)
		} else if errors.Is(err, personal.ErrSessionNotFound) {
			model, err = runner.NewSessionFor(ctx, opts.SessionID, "New chat", personal.OriginChat)
			if err != nil {
				return err
			}
			sessionID, createdAtStartup = model.ID, true
		} else {
			return err
		}
	} else {
		model, err := runner.NewSessionFor(ctx, "", "New chat", personal.OriginChat)
		if err != nil {
			return err
		}
		sessionID, createdAtStartup = model.ID, true
	}
	fmt.Fprintln(stderr, "session id:", sessionID)
	// Resident context warnings (rules truncated, soft thresholds crossed,
	// skipped or shadowed skills) are said once per session start. run and ACP
	// only log them, so this stays off their stdout/ACP output.
	for _, notice := range opts.ContextNotices {
		fmt.Fprintln(stderr, notice)
	}
	if opts.SetupSession != nil {
		if err := opts.SetupSession(ctx, sessionID); err != nil {
			return err
		}
	}
	// Registered first so it runs last, after the terminal is restored: how to
	// continue this session and what this chat process used.
	chatStart := time.Now()
	var stats chatStats
	// sessionName is the short name the session registers under; it is known once
	// the local session socket is registered, below.
	var sessionName string
	defer func() { stats.print(stderr, sessionID, sessionName, time.Since(chatStart)) }()
	var releaseSessionLock func() error
	var processServer *personalprocess.Server
	defer func() {
		if processServer != nil {
			_ = processServer.Close()
		}
		if releaseSessionLock != nil {
			_ = releaseSessionLock()
		}
	}()
	if sessionID != "" {
		var lockErr error
		releaseSessionLock, lockErr = runner.HoldSessionLock(sessionID)
		if lockErr != nil {
			return lockErr
		}
	}
	cwd, _ := os.Getwd()
	transcriptPath := func(id string) string {
		// With transcripts disabled no file is written, so do not advertise a path.
		if id == "" || !appconfig.TranscriptsEnabled() {
			return ""
		}
		home, err := appconfig.ResolveSugaHome()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "transcripts", id+".jsonl")
	}
	baseEvent := func(name string) sugahooks.Event {
		return sugahooks.Event{Name: name, SessionID: sessionID, CWD: cwd, TranscriptPath: transcriptPath(sessionID)}
	}
	inputSource, err := newChatInputSource(ctx, stdin, stdout)
	if err == nil {
		size, sizeErr := appconfig.ChatHistorySize()
		if sizeErr != nil {
			return sizeErr
		}
		inputHistory, historyErr := newInputHistory(cwd, appconfig.ChatHistoryEnabled(), size)
		if historyErr != nil {
			return historyErr
		}
		if inputSource.terminal != nil {
			inputSource.terminal.SetHistory(inputHistory.lines())
			installCompletion(inputSource, opts.Dirs, modelRefs(opts.ModelOptions))
		}
		// Only lines received from the local keyboard are recorded; deliveries and
		// hook/task messages must never become personal input history.
		inputSource.history = inputHistory
	}
	var render *Renderer
	if err == nil && opts.Tasks != nil {
		inputSource.taskWake = opts.TaskWake
		inputSource.taskNotice = func() string {
			infos := opts.Tasks.TakeNotices(sessionID)
			if len(infos) == 0 {
				return ""
			}
			if render != nil {
				render.AgentTaskSummaries(infos)
			}
			return personal.TaskNoticeText(infos)
		}
	}
	if err != nil {
		return err
	}
	defer inputSource.Close()
	// The turn-time input line (chat.steering): a turn then reads it instead of
	// letting the tty go cooked, so the person can steer or end the turn.
	inputSource.SetSteering(opts.Steering)
	// steering is true while a turn keeps reading the input line (chat.steering with
	// a terminal): the turn's output then has to keep that line out of its way.
	steering := opts.Steering && inputSource.terminal != nil
	// Output that may land while the input line is on screen (hook notices, errors)
	// goes through the input source: it is printed above the line and the line is
	// repainted, instead of sitting on top of it (design N4). The process log is not
	// among them any more: it goes to <SUGA_HOME>/logs/suga-<date>.log and never to
	// the terminal (internal/wiring/logging.go), so nothing has to be rerouted for it.
	errOut := steerOut{source: inputSource, fallback: stderr}
	// All conversation output goes through the renderer (and the input source,
	// which keeps the line editor's screen state consistent).
	color := colorEnabledFor(stdout)
	if opts.HighlightInput && color {
		// The line just submitted is repainted where it is, its background reaching
		// the end of every row it occupies: the editor fills to its own layout width,
		// which is the terminal's and follows a resized window.
		inputSource.SetInputHighlight(lineedit.InputStyle{Prefix: inputHighlightPrefix, Suffix: highlightSuffix})
	}
	// The status line above the input line exists only while that line stays up
	// for the whole turn (chat.steering); without it the waiting line keeps a row of
	// its own (a typed nil would count as a status line, so it stays unset).
	var status statusSetter
	if steering {
		status = inputSource
	}
	render = newRenderer(rendererOptions{
		Out: inputSource, Err: errOut,
		Color: color, ErrColor: colorEnabledFor(stderr),
		Provider: runner.ProviderName(), Model: runner.Model(),
		Thinking: opts.Thinking, ContextWindow: opts.ContextWindow,
		WriteTools: opts.WriteTools, Markdown: opts.Markdown, Spinner: opts.Spinner,
		AgentProgress: opts.AgentProgress,
		Diff:          opts.Diff, DiffLines: opts.DiffLines,
		CodeHighlight: opts.CodeHighlight, CodeMaxLines: opts.CodeMaxLines,
		HighlightInput: opts.HighlightInput, HighlightWidth: inputSource.highlightWidth,
		// The input line stays on screen while a turn runs: the streamed text then
		// leaves only complete lines, and the waiting line becomes a status line above
		// that input line (r3 M1, design suga-54cl).
		InputLineUp: steering, Status: status,
	})
	// The terminal's window/tab title (chat.terminal_title): set below, once the
	// session's short name and title are known, and given back on the way out.
	title := newTerminalTitle(opts.terminalTitleWriter(inputSource), stdout, opts.terminalTitleEnabled(color))
	defer title.Restore()
	// Background idle consolidation; closed before the input source, so its
	// last status line still goes through it.
	idle := newIdleConsolidation(runner, sessionID, opts.IdleAfter, opts.IdleMinRatio, render, opts.scheduleOptions,
		opts.scheduleObserver)
	defer idle.Close()
	if opts.Asker != nil && inputSource.terminal != nil {
		opts.Asker.Bind(chatAsker{source: inputSource, render: render, notifier: newChatNotifier(opts.Notify, opts.NotifyAfter, stdout, inputSource.terminal != nil)})
		defer opts.Asker.Bind(nil)
	}
	// state carries the wiring a turn needs and the state that outlives a turn; a
	// turn runs as state.runTurn (chat_turn.go), so Run itself keeps the session
	// setup, the main loop and the exit handling only. It is built before the
	// session socket is registered, because the socket's delivery endpoint
	// (`suga send`, `--steer`) is a method of it.
	provider, model, contextWindow, maxOutputTokens := runner.ModelSettings()
	state := &chatState{
		ctx: ctx, runner: runner, hooks: hooks, source: inputSource, render: render,
		stderr: stderr, errOut: errOut, baseEvent: baseEvent, title: title, idle: idle,
		stats: &stats, sessionID: sessionID,
		createdAtStartup: createdAtStartup, dirs: opts.Dirs,
		modelOptions: opts.ModelOptions, modelSwitch: opts.ModelSwitch,
		modelInfo: ModelOption{Provider: provider, Model: model, Thinking: opts.Thinking, ContextWindow: contextWindow, MaxOutputTokens: maxOutputTokens},
		readImage: opts.ReadImage, vision: opts.Vision, readClip: opts.PasteImage,
		steering: steering, local: opts.Local, tasks: opts.Tasks, notifier: newChatNotifier(opts.Notify, opts.NotifyAfter, stdout, inputSource.terminal != nil),
	}
	if inputSource.terminal != nil && opts.PasteImage != nil {
		// Alt+V reads the clipboard's picture into the pending queue (design 0.3);
		// without a reader the key stays without meaning.
		inputSource.terminal.SetPasteImageCallback(state.attachFromClipboard)
	}
	if runner.ProcessRoot() != "" {
		// The delivery endpoint is the chat state's own method: `suga send --steer`
		// inserts its message into the turn running right now (steer_send.go).
		processServer, sessionName, err = registerChatProcess(sessionID, runner, state.deliverMessage, stderr)
		if err != nil {
			// Registration only adds the local session socket (`suga send`, gofer delivery);
			// losing it must not end the chat. The common cause is a SUGA_HOME deep enough
			// that <root>/<pid>.sock exceeds the platform's socket path limit.
			processServer, sessionName = nil, ""
			fmt.Fprintf(stderr, "提示：本会话未注册本地会话 socket（%v），因此收不到 suga send 与 gofer 投递；建议缩短 SUGA_HOME 路径。聊天继续运行。\n", err)
		}
		state.server, state.sessionName = processServer, sessionName
	}
	// The banner names the session; the short name is only known after registration.
	render.SetSessionName(sessionName)
	// The same two names label the terminal: a resumed session starts with its
	// stored title, a new one gets the first message's title below.
	title.Set(sessionName, startupTitle)
	if hooks != nil && sessionID != "" {
		ensureTranscriptFile(transcriptPath(sessionID), sessionID)
		start := baseEvent("SessionStart")
		start.Source = source
		startResp := hooks.Call(ctx, start)
		showHookNotice(stderr, startResp)
		state.pendingContext = sugahooks.AdditionalContext(startResp)
	}
	if hooks != nil && opts.StopBackground && opts.Tasks != nil && sessionID != "" {
		state.bgStop = &backgroundStop{hooks: hooks, tasks: opts.Tasks, sessionID: sessionID}
	}
	defer func() {
		// Messages queued by `suga send` that never became a turn: fail their senders
		// (exit 3 for `send --wait` style callers) and tell the person how many were dropped.
		if n := inputSource.DiscardQueued(); n > 0 {
			fmt.Fprintf(stderr, "Warning: %d queued message(s) were discarded on exit and not run.\n", n)
		}
		// Session messages held at the automatic-reply limit never ran either: the
		// person learns how many were still waiting when chat ends (design
		// session_message).
		if n := state.heldSessionCount(); n > 0 {
			fmt.Fprintf(stderr, "Warning: %d session message(s) were still waiting for your next input on exit.\n", n)
		}
		// Messages queued with Tab during a turn that never got their turn either
		// (chat ended first): they are not persisted, so they go now (followup.go).
		if n := len(state.followUps.pending()); n > 0 {
			fmt.Fprintf(stderr, "Warning: %d queued follow-up message(s) were not run on exit.\n", n)
		}
		// Messages `suga send --steer` inserted into the last turn: they will not run
		// either, and their senders are told here (steer_send.go) rather than by a
		// dropped connection.
		state.discardPendingSendSteers()
		if n := state.steersDropped; n > 0 {
			// Messages inserted into the last turn that chat did not run (it was
			// leaving, or the turn was): the person sees how many are gone.
			fmt.Fprintf(stderr, "Warning: %d inserted message(s) were not run on exit.\n", n)
		}
		if state.localContext != "" {
			// "!!" results collected for the next message that chat never sent:
			// they are dropped with the session (the commands themselves ran).
			fmt.Fprintln(stderr, "Warning: local command results (!!) were not sent to the model and were discarded.")
		}
		if state.waiting != nil {
			state.lostReply = appendContext(state.lostReply, state.waiting.Interrupt(context.Background()))
		}
		if state.lostReply != "" {
			fmt.Fprintln(stderr, "Warning: hook reply was not delivered:", termtext.Clean(sugahooks.TruncateOutput(state.lostReply, 200)))
		}
		if hooks != nil && sessionID != "" {
			hooks.Call(context.Background(), baseEvent("SessionEnd"))
		}
	}()

	render.Banner()
	// Continuing an existing session: show the model's last reply above the first
	// prompt, so the person can go on from it (read-only: no model call, no entry
	// written, no hook). A handoff (--from) starts a new session and shows nothing.
	if source == "resume" {
		showLastReply(ctx, runner, render, sessionID, stderr)
	}
	// Registered after every other defer, so it runs first on the way out: the
	// waiting line is down before the exit warnings, the SessionEnd hook and the
	// usage summary print, and no frame goroutine is left behind.
	defer render.endThinking()
	for {
		in, err := nextChatInput(ctx, inputSource, render, state.waiting, &state.lostReply)
		line, hasLine, stopDone, injected, interrupted := in.line, in.hasLine, in.stopDone, in.injected, in.interrupted
		if interrupted {
			state.waiting = nil
		} else if state.waiting != nil && stopDone {
			state.waiting = nil
			if injected {
				state.stopHookActive = true
			}
		}
		if stopDone && !injected {
			continue
		}
		if err != nil {
			// Ctrl-C at the prompt exits only when pressed twice in a row
			// within exitConfirmWindow, so a stray one does not end the chat.
			// Ctrl-D (EOF) still exits at once. The clock belongs to the input
			// source, shared with the reads during a turn (m7).
			if errors.Is(err, lineedit.ErrInterrupt) && !inputSource.takeInterrupt() {
				if inputSource.terminal != nil {
					inputSource.terminal.AbortLine()
				}
				render.Hint("再按一次 Ctrl+C 退出（Ctrl+D 直接退出）")
				continue
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		inputSource.clearInterrupt()
		if !hasLine {
			return nil
		}
		// The local commands ("!cmd"/"!!cmd") are read from the line as it was
		// typed: a leading space before the "!" makes it an ordinary message (the
		// trim below), and only a line typed here is ever one (see the injected
		// case below).
		local, isLocal := parseLocalCommand(line)
		line = strings.TrimSpace(line)
		// Any input (keyboard, delivery, Stop hook reply) is activity: the idle
		// clock starts over. A running consolidation is preempted by the
		// Run/Compact below through the session gate.
		idle.Touch()
		switch {
		case line == "":
			continue
		case injected:
			// Text from elsewhere (a `suga send` delivery, a Stop hook reply, a task
			// notice) is never a command: it is always an ordinary message, so
			// delivered text can neither change the session's permissions nor end it,
			// nor run a local command.
		case isLocal && state.local != nil:
			// A command typed at this keyboard: it runs in the local shell and
			// starts no turn (local_cmd.go).
			state.runLocalCommand(local)
			continue
		case line == ":quit", line == ":exit":
			// Both spellings leave chat; :exit is the familiar one from other REPLs.
			return nil
		case line == ":session":
			fmt.Fprintln(inputSource, sessionLine(sessionID, sessionName))
			continue
		case line == ":compact":
			// Compress the session context now; token figures are estimates.
			idle.Pause()
			if out, err := runner.Compact(ctx, sessionID); err != nil {
				fmt.Fprintln(stderr, formatCommandError("Compact", err))
			} else {
				idle.Compacted()
				fmt.Fprintf(inputSource, "Compacted context: ~%d -> ~%d tokens (estimate)\n", out.TokensBefore, out.TokensAfter)
			}
			idle.Resume()
			continue
		case isDiffCommand(line):
			// Show a file change of this session again, in full (:diff [N]).
			render.ShowDiff(strings.TrimPrefix(line, diffCmd))
			continue
		case isCodeCommand(line):
			// Show a code block of the last answer in full (:code [N]).
			render.ShowCode(strings.TrimPrefix(line, codeCmd))
			continue
		case isImageCommand(line), isModelCommand(line), isQueueCommand(line), line == ":help", line == ":usage", line == ":tasks":
			// The reference, this session's usage and its background tasks
			// (chat_commands.go); like every : command, keyboard only.
			state.runChatCommand(line)
			continue
		case state.dirs != nil && isDirCommand(line):
			// The session's added directories (:add-dir/:dirs/:rm-dir).
			state.runDirCommand(line)
			continue
		}
		// The turn itself, from the idle pause to its Stop hook (chat_turn.go).
		state.turnHops = in.hops
		if err := state.runTurn(line, injected, in.images); err != nil {
			if errors.Is(err, errExitChat) {
				// A second Ctrl-C during a turn: leave chat like the second one at
				// the prompt does, with the usual summary on the way out.
				return nil
			}
			return err
		}
	}
}

// showLastReply prints the last model reply of a session chat continues, under
// the banner and before the first prompt. A session without a reply (new, or
// handed off with --from) shows nothing; failing to read the reply only warns
// on stderr, since chat itself must still start.
func showLastReply(ctx context.Context, runner *personal.Runner, render *Renderer, sessionID string, stderr io.Writer) {
	text, incomplete, err := runner.LastReply(ctx, sessionID)
	if err != nil {
		fmt.Fprintln(stderr, "读取上一次回复失败：", err)
		return
	}
	if text == "" {
		return
	}
	render.LastReply(text, sessionID, incomplete)
}

func ensureTranscriptFile(path, sessionID string) {
	if path == "" || !appconfig.TranscriptsEnabled() {
		return
	}
	transcript, err := personal.OpenTranscript(filepath.Dir(path), sessionID)
	if err != nil {
		return
	}
	_ = transcript.Close()
}

// chatInput is one result of nextChatInput: a line to run (hasLine), or the
// end of a Stop hook wait (stopDone; injected when the hook supplied the next
// prompt). interrupted reports that typing or a delivery cut a Stop wait short.
type chatInput struct {
	line                                     string
	hasLine, stopDone, injected, interrupted bool
	// images are the pictures of a `suga send --image` delivery: they ride with the
	// turn this input starts.
	images []agentcore.ImagePart
	// hops is the automatic-reply depth of the delivery that produced this input
	// (design session_message): a turn started by a hops-h session message runs at
	// h, every other turn at 0.
	hops int
}

func nextChatInput(ctx context.Context, source *chatInputSource, render *Renderer, waiting sugahooks.Wait, lost *string) (chatInput, error) {
	if !source.Request(render.Prompt(waiting != nil)) {
		return chatInput{}, ctx.Err()
	}
	var stopDone <-chan sugahooks.Result
	if waiting != nil {
		stopDone = waiting.Done()
	}
	interrupted := false
	for {
		// Prefer a byte already observed by the reader over a simultaneous Stop result.
		select {
		case event := <-source.Next():
			in, again, err := processChatInputEvent(source, event, waiting, lost)
			if again {
				interrupted = true
				waiting = nil
				stopDone = nil
				continue
			}
			in.interrupted = interrupted
			return in, err
		case <-source.taskWake:
			// A background task ended while chat waits: its notice starts a turn.
			text := source.taskNotice()
			if text == "" {
				continue
			}
			if waiting != nil {
				*lost = appendContext(*lost, waiting.Interrupt(context.Background()))
				interrupted = true
			}
			render.External(text)
			return chatInput{line: text, hasLine: true, injected: true, interrupted: interrupted}, nil
		case delivery := <-source.deliveries:
			if waiting != nil {
				*lost = appendContext(*lost, waiting.Interrupt(context.Background()))
				interrupted = true
			}
			render.External(delivery.line)
			delivery.settle(nil)
			return chatInput{line: delivery.line, hasLine: true, injected: true, interrupted: interrupted, images: delivery.images, hops: delivery.hops}, nil
		default:
		}
		select {
		case event := <-source.Next():
			in, again, err := processChatInputEvent(source, event, waiting, lost)
			if again {
				interrupted = true
				waiting = nil
				stopDone = nil
				continue
			}
			in.interrupted = interrupted
			return in, err
		case result, ok := <-stopDone:
			if ok && result.Response.Notice != "" {
				fmt.Fprintln(source, termtext.Clean(result.Response.Notice))
			}
			if !ok || result.Err != nil || result.Response.Decision != "block" || result.Response.Reason == "" {
				fmt.Fprintln(source)
				return chatInput{stopDone: true, interrupted: interrupted}, nil
			}
			render.External(result.Response.Reason)
			return chatInput{line: result.Response.Reason, hasLine: true, stopDone: true, injected: true, interrupted: interrupted}, nil
		case <-source.taskWake:
			// A background task ended while chat waits: its notice starts a turn.
			text := source.taskNotice()
			if text == "" {
				continue
			}
			if waiting != nil {
				*lost = appendContext(*lost, waiting.Interrupt(context.Background()))
				interrupted = true
			}
			render.External(text)
			return chatInput{line: text, hasLine: true, injected: true, interrupted: interrupted}, nil
		case delivery := <-source.deliveries:
			if waiting != nil {
				*lost = appendContext(*lost, waiting.Interrupt(context.Background()))
				interrupted = true
			}
			render.External(delivery.line)
			delivery.settle(nil)
			return chatInput{line: delivery.line, hasLine: true, injected: delivery.injected, interrupted: interrupted, images: delivery.images, hops: delivery.hops}, nil
		case <-ctx.Done():
			return chatInput{interrupted: interrupted}, ctx.Err()
		}
	}
}

// processChatInputEvent turns one reader event into input. again reports an
// event that only cut a Stop wait short (typing, or Ctrl-C/EOF while waiting):
// the caller keeps reading instead of returning it.
func processChatInputEvent(source *chatInputSource, event chatInputEvent, waiting sugahooks.Wait, lost *string) (in chatInput, again bool, err error) {
	if event.activity {
		if waiting != nil {
			*lost = appendContext(*lost, waiting.Interrupt(context.Background()))
		}
		source.AckActivity()
		return chatInput{}, true, nil
	}
	if event.err != nil && waiting != nil {
		*lost = appendContext(*lost, waiting.Interrupt(context.Background()))
		return chatInput{line: event.line}, true, event.err
	}
	source.CompleteLine()
	return chatInput{line: event.line, hasLine: event.err == nil}, false, event.err
}

// formatCommandError is the line chat prints for a failed :command. An
// interrupted one reads 已中断 instead of the runner's raw "context canceled"
// (the display only: run, ACP and stream-json keep the raw error text).
func formatCommandError(name string, err error) string {
	if errors.Is(err, context.Canceled) {
		return name + ": " + interruptText
	}
	return name + ": " + err.Error()
}

func appendContext(current, next string) string {
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	return current + "\n\n" + next
}

// showHookNotice prints the server-provided notice (if any) for the person at the terminal.
// It always goes to stderr so it never mixes into the conversation output.
func showHookNotice(w io.Writer, response sugahooks.Response) {
	if response.Notice != "" {
		fmt.Fprintln(w, termtext.Clean(response.Notice))
	}
}

// exitConfirmWindow is how soon a second Ctrl-C at the prompt must follow the
// first to exit chat.
const exitConfirmWindow = 3 * time.Second

// chatStats accumulates what one chat process used, for the exit summary.
type chatStats struct {
	turns   int
	usage   agentcore.Usage
	session agentcore.Usage // session-cumulative usage after the last turn
}

func (s *chatStats) add(result personal.RunResult) {
	s.turns++
	s.usage = s.usage.Add(result.Usage)
	if result.SessionUsage.TotalTokens > 0 || result.SessionUsage.InputTokens > 0 {
		s.session = result.SessionUsage
	}
}

// print writes the exit summary built by BuildExitSummary (layout.go): the
// resume command (behind the session's short name, the address `suga send
// --session <name>` accepts), this process's turns and tokens (plus the session
// total when known) and how long chat ran. It has no colors: the resume command
// is meant to be copied as it is.
func (s chatStats) print(w io.Writer, sessionID, shortName string, elapsed time.Duration) {
	printLayout(w, false, BuildExitSummary(ExitSummary{
		SessionID: sessionID, ShortName: shortName,
		Turns: s.turns, Usage: s.usage, SessionUsage: s.session, Elapsed: elapsed,
	}))
}
