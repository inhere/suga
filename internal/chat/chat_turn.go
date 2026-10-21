package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// chatState is what one chat process carries besides the session setup Run
// does: the wiring a turn needs and the state that lives across turns. One turn
// runs as runTurn on it, so turn logic is extended here rather than through
// another parameter of Run.
type chatState struct {
	// Wiring made once by Run.
	ctx    context.Context
	runner *personal.Runner
	hooks  sugahooks.Client
	source *chatInputSource
	render *Renderer
	stderr io.Writer
	// errOut is where what may land while the input line is on screen goes: a
	// steer-aware writer (steering.go), set by Run when chat.steering is on, and a
	// nil field falls back to stderr (the tests build a state directly).
	errOut      io.Writer
	baseEvent   func(name string) sugahooks.Event
	title       *terminalTitle
	idle        *idleConsolidation
	server      *personalprocess.Server
	bgStop      *backgroundStop
	stats       *chatStats
	sessionID   string
	sessionName string

	// waiting is the Stop hook wait of the turn that just ran: runTurn starts
	// it, the next iteration of Run's loop consumes it.
	waiting sugahooks.Wait
	// lostReply holds a web reply that arrived while the user interrupted the Stop wait.
	// It is folded into the next turn's context; if the chat ends first, warn on stderr.
	lostReply string
	// stopHookActive is set while a Stop hook asked to continue: the next turn
	// runs as its follow-up.
	stopHookActive bool
	// pendingContext is the extra context collected for the next prompt (hook
	// AdditionalContext, a setup context, a lost reply).
	pendingContext string
	// createdAtStartup is true while the session has no title yet: the next
	// message names it.
	createdAtStartup bool
	// dirs is the session's added-directory handle (Options.Dirs): the :add-dir,
	// :dirs and :rm-dir commands work through it; nil leaves them out.
	dirs         SessionDirs
	modelOptions []ModelOption
	modelSwitch  ModelSwitch
	modelInfo    ModelOption
	// local is the runner of the local commands (Options.Local): a "!cmd" line
	// typed at the prompt runs through it; nil leaves them ordinary messages.
	local *native.LocalCommand
	// readImage and vision carry the :image wiring (Options.ReadImage/Vision);
	// pendingImages are the pictures waiting to ride with the next message
	// (chat :image / Alt+V, design 0.3).
	readImage     func(string) (agentcore.ImagePart, string, error)
	readClip      func() (agentcore.ImagePart, string, error)
	vision        *tools.VisionGate
	pendingImages []agentcore.ImagePart
	tasks         *tasks.Manager
	notifier      chatNotifier
	// localContext holds what the "!!" commands of this chat collected for the
	// model (local_cmd.go): it is handed over with the next turn's context and
	// dropped when chat ends without a message.
	localContext string
	// steering is true while the input line stays available during a turn
	// (Options.Steering with a terminal): runTurn then reads the turn's input
	// through a turnInput consumer (steering.go).
	steering bool
	// steerSeq names the messages inserted into a turn of this chat process
	// (nextSteerID).
	steerSeq int
	// turnMu guards sendTurn, the turn whose input read is open right now: the
	// session socket inserts `suga send --steer` messages into it (steer_send.go).
	turnMu   sync.Mutex
	sendTurn *turnInput
	// sendMu guards sendSeq and sendSettled: the ids of the messages the session
	// socket inserted, and the senders waiting for their fate (steer_send.go).
	sendMu      sync.Mutex
	sendSeq     int
	sendSettled map[string]chan error
	// steersDropped counts the inserted messages chat did not run because it is
	// exiting; the exit summary warns about them, like queued deliveries.
	steersDropped int
	// sessionMu guards sessionHeld, the session messages that arrived at the
	// automatic-reply limit and wait for the next turn the person starts (design
	// session_message; session_message.go).
	sessionMu   sync.Mutex
	sessionHeld []string
	// turnHops is the automatic-reply depth of the turn runTurn is about to run
	// (personal.RunRequest.Hops): 0 for a turn the person started, h for a turn a
	// hops-h session message started.
	turnHops int
}

// errWriter is where chat writes what must not land on the input line: the
// steer-aware writer Run wired (steering on) or stderr.
func (s *chatState) errWriter() io.Writer {
	if s.errOut != nil {
		return s.errOut
	}
	return s.stderr
}

// localSystemContext is the extra context of one turn: what the person's local
// commands ("!!") produced, then the hook context. Both go to the model as a
// user message right before the turn's own input (personal.RunRequest.SystemContext).
func localSystemContext(local, hook string) string {
	return appendContext(local, hook)
}

// runTurn executes one turn of the chat, and the turns the messages inserted
// into it leave behind (design D3): a completed turn hands the messages it never
// delivered back, and they run at once as a turn of their own, so the answer the
// person already saw stays the result of the turn that produced it.
func (s *chatState) runTurn(line string, injected bool, images []agentcore.ImagePart) error {
	for {
		next, err := s.runOneTurn(line, injected, images)
		if err != nil || next == nil {
			return err
		}
		// The messages the finished turn handed back now run as a turn of their
		// own: a `suga send --steer --wait` sender learns its message is running
		// (design "--wait" 完成点 "作为下一回合运行").
		s.settleSendSteers(next.ids, nil)
		line, injected = next.line, next.injected
		// The pictures of the delivery reached the first turn; a turn made of
		// inserted messages carries only their own (a steer keeps its images).
		images = nil
	}
}

// steerTurn is the input of the turn that follows a completed one because the
// messages inserted into it were never delivered.
type steerTurn struct {
	line string
	// injected is false when the person typed any of those messages here: the
	// turn then runs as the person's own input, and the transcript and the
	// UserPromptSubmit hook record the same flag (design m1).
	injected bool
	// ids are the messages that became this turn, by id: a sender waiting for one
	// of them is told it is running now (steer_send.go).
	ids []string
}

// runOneTurn executes one turn of the chat: from pausing the idle consolidation
// to the Stop hook of the finished turn being started. It returns the error that
// ends the chat plus the input of the turn the messages inserted into this one
// left behind (nil: none). A turn that merely failed, yielded or answered
// reports itself through the renderer and the stats.
func (s *chatState) runOneTurn(line string, injected bool, images []agentcore.ImagePart) (*steerTurn, error) {
	// Session messages that arrived at the automatic-reply limit run with this turn
	// as its context (design session_message D3): the person's own next input is
	// what continues them.
	if held := s.takeHeldSessionMessages(); len(held) > 0 {
		line = appendContext(strings.Join(held, "\n\n"), line)
	}
	// A turn starts: no idle consolidation until it has ended (EndTurn).
	s.idle.Pause()
	// The depth belongs to this turn only: the next one sets its own (0 unless a
	// session message starts it).
	hops := s.turnHops
	s.turnHops = 0
	if s.createdAtStartup {
		if err := s.runner.SetSessionTitle(s.ctx, s.sessionID, line); err != nil {
			return nil, err
		}
		// The first message names the session: the terminal title follows the
		// stored title (same derivation, so both read the same).
		s.title.Set(s.sessionName, personal.SessionTitle(line))
		s.createdAtStartup = false
	}
	if s.hooks != nil {
		submit := s.baseEvent("UserPromptSubmit")
		submit.Prompt = redact.Secrets(line)
		// injected covers every non-keyboard source (Stop block reasons and socket
		// deliveries both flow through nextChatInput), same flag the transcript records.
		submit.Injected = injected
		submitResp := s.hooks.Call(s.ctx, submit)
		showHookNotice(s.errWriter(), submitResp)
		s.pendingContext = appendContext(s.pendingContext, sugahooks.AdditionalContext(submitResp))
	}
	if s.lostReply != "" {
		// A reply received during an interrupt race still reaches the model as next-turn context.
		fmt.Fprintln(s.errWriter(), "Note: delivering hook reply received while interrupting:", sugahooks.TruncateOutput(s.lostReply, 200))
		s.pendingContext = appendContext(s.pendingContext, s.lostReply)
		s.lostReply = ""
	}
	// A background Stop that asked to continue makes this turn its follow-up.
	turnStopActive := s.stopHookActive || s.bgStop.takeBlocked()
	s.stopHookActive = false
	s.source.SetBusy(true)
	if s.server != nil {
		_ = s.server.SetBusy(true)
	}
	s.render.BeginTurn()
	turnStart := time.Now()
	// The turn runs under a context of its own (design D9): Esc or a single
	// Ctrl-C during it cancels this turn only, never the chat. With chat.steering
	// on the input line stays available while the turn runs (steering.go).
	turnCtx, cancelTurn := context.WithCancel(s.ctx)
	turn := s.startTurnInput(cancelTurn)
	result, runErr := s.runner.Run(turnCtx, personal.RunRequest{
		SessionID: s.sessionID,
		Prompt:    line,
		// The pictures queued at the prompt (:image, Alt+V) ride with this
		// message; the store writes their files and keeps the message text clean.
		// The pictures of a `suga send --image` delivery follow them.
		Images: append(s.takePendingImages(), images...),
		// What the person's own local commands ("!!") produced comes first, so the
		// model reads it as context of this message; the hook context follows.
		SystemContext: localSystemContext(s.localContext, s.pendingContext),
		Injected:      injected,
		// The automatic-reply depth of this turn: a session message sent by its
		// tools carries hops+1 (design session_message D3).
		Hops: hops,
		Emit: func(eventCtx context.Context, event personal.Event) error {
			if event.Kind == "steer_delivered" {
				// A model request of this turn carried an inserted message: show it as
				// delivered (the waiting line was printed when it was typed) and tell a
				// `send --steer --wait` sender waiting for it (steer_send.go).
				turn.deliver(event)
				s.deliverSendSteer(event)
			}
			s.render.Handle(event)
			if s.hooks == nil || event.Kind != "tool_result" {
				return nil
			}
			// hooks.post_tool_use decides which tools send PostToolUse and under which name.
			toolName, send := s.hooks.PostToolName(event.ToolName)
			if !send {
				return nil
			}
			postTool := s.baseEvent("PostToolUse")
			postTool.ToolName = toolName
			postTool.ToolOutput = sugahooks.TruncateOutput(redact.Secrets(event.Text), toolHookLimit)
			// The notice goes to stderr while the waiting line sits on stdout: take
			// that line down (the tool result above started a new one) so the two
			// never end up on the same screen line, and put it back afterwards.
			s.render.stopThinking()
			showHookNotice(s.errWriter(), s.hooks.Call(eventCtx, postTool))
			s.render.startThinking()
			return nil
		},
	})
	// The turn's input line ends with it; what the read reported after this stays
	// for the next turn's consumer (steering.go). Whether the turn's own context was
	// cancelled is read before it is released: an aborted turn is reported as such
	// even when the runner's error hides the cancellation.
	turnEnd := turn.end()
	turnCancelled := turnCtx.Err() != nil
	cancelTurn()
	s.source.SetBusy(false)
	if s.server != nil {
		_ = s.server.SetBusy(false)
	}
	s.stats.add(result)
	s.pendingContext = ""
	// The "!!" results went to the model with this turn; the next ones start a
	// new block.
	s.localContext = ""
	// What became of the messages inserted while the turn ran (design D4): a
	// completed turn hands back the ones it never delivered, a cancelled or failed
	// one all of them.
	backs := steerReturns(result.ReturnedSteers, turnEnd.unaccepted())
	completed := runErr == nil && (result.State == personal.RunCompleted || result.State == personal.RunInputRequired)
	// A cancelled turn: the person ended it (Esc, Ctrl-C), an approval question was
	// cancelled, or a model call was. The runner's error may hide the cancellation
	// (a wrapped error, a failed persist of the cancelled turn), so the turn's own
	// state decides too.
	cancelled := turnEnd.aborted || turnCancelled || result.State == personal.RunCanceled ||
		errors.Is(runErr, context.Canceled)
	// Every turn end (success, error or yield) must send Stop: the hook contract defines it as
	// "turn finished, back to waiting for input", otherwise a hook-side session stays "running" forever.
	stopMessage := ""
	if runErr != nil {
		s.render.Finish()
		switch {
		case s.ctx.Err() != nil:
			// Ctrl-C (SIGINT) cut this turn short: chat ends here as before
			// (cmd/suga reports 已中断 with exit 130), and the turn itself is
			// reported as interrupted rather than with the runner's raw
			// "context canceled".
			s.render.Interrupt()
			s.steersDropped += len(backs)
			return nil, s.ctx.Err()
		case errors.Is(runErr, tools.ErrAborted):
			// Ctrl-C/Ctrl-D at an approval question: only this turn ends.
			s.render.Notice("已取消" + steerBackNotice(len(backs)))
		case cancelled:
			// The turn was cancelled: Esc or Ctrl-C during it (the chat itself
			// goes on), or a cancelled model call. The raw error would read
			// "context canceled".
			s.render.Notice(interruptNotice + steerBackNotice(len(backs)))
		default:
			s.render.Error(runErr)
			if n := len(backs); n > 0 {
				s.render.Hint(fmt.Sprintf("%d 条插话已放回输入行", n))
			}
		}
		stopMessage = "Error: " + runErr.Error()
	} else if result.Status == agentcore.RunYielded {
		s.render.Finish()
		s.render.Notice("Waiting for more input.")
		s.render.Status(result, time.Since(turnStart), turnEnd.deliveredCount())
	} else {
		// Streamed text is already on screen; otherwise print the answer now.
		if !s.render.Finish() && result.Output != "" {
			s.render.Answer(result.Output)
		}
		if len(result.DeniedTools) > 0 {
			s.render.Notice("本轮被拒绝的工具：" + strings.Join(result.DeniedTools, ", "))
		}
		s.render.Status(result, time.Since(turnStart), turnEnd.deliveredCount())
		stopMessage = result.Output
	}
	// A long turn tells the person it is over (chat.notify / chat.notify_after):
	// they may have looked away while it ran.
	if elapsed := time.Since(turnStart); s.notifier.shouldNotify(elapsed) {
		s.notifier.notify("suga：回合完成（" + formatElapsed(elapsed) + "）")
	}
	if s.hooks != nil {
		stop := s.baseEvent("Stop")
		stop.LastAssistant = sugahooks.TruncateOutput(redact.Secrets(stopMessage), stopHookLimit)
		stop.StopHookActive = turnStopActive
		if s.bgStop == nil {
			s.waiting = s.hooks.StartStop(s.ctx, stop)
		} else if err := s.bgStop.start(s.ctx, stop); err != nil {
			// No task slot (tasks.max_running): wait at the prompt as before.
			s.render.Notice("Stop hook 未能在后台运行，改为等待：" + err.Error())
			s.waiting = s.hooks.StartStop(s.ctx, stop)
		}
	}
	// The turn is over (Stop sent): the idle clock runs from now.
	s.idle.EndTurn(result)
	if turnEnd.exited {
		// A second Ctrl-C during the turn: leave chat, the way the second Ctrl-C at
		// the prompt does. Nothing of the turn's inserted messages is run.
		s.steersDropped += len(backs)
		return nil, errExitChat
	}
	if len(backs) == 0 {
		return nil, nil
	}
	if !completed {
		// A cancelled or failed turn takes every inserted message back to the
		// input line and does not run it (design D4): ending the turn is meant to
		// stop the agent, and the person decides what happens next. A `send --steer
		// --wait` sender learns its message is discarded (design "--wait" 完成点).
		s.settleSendSteers(steerIDs(backs), personalprocess.ErrDiscarded)
		s.putSteersBack(backs)
		return nil, nil
	}
	// A completed turn's undelivered messages run at once, as the next turn
	// (design D3), before any queued delivery or task notice. A Stop hook wait is
	// cut short like typing at the prompt does: its reply joins that turn.
	if s.waiting != nil {
		s.lostReply = appendContext(s.lostReply, s.waiting.Interrupt(context.Background()))
		s.waiting = nil
	}
	return &steerTurn{line: strings.Join(steerLines(backs), "\n"), injected: !typedHere(backs),
		ids: steerIDs(backs)}, nil
}
