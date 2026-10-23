package personal

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/inhere/suga/pkg/agentcore"
)

// Config.DeadlineEnv gives the time awareness of a run with a job deadline
// (suga-aj08): gofer injects the job's deadline as RFC3339 into an env var
// (GOFER_JOB_DEADLINE by default), and run.deadline_env names that variable.
// Before every model request the runner attaches a transient user message
// telling the model the current time, the deadline and the remaining time -
// without one, a model cannot know how long its task still has (a 60 minute
// job ended after 12 minutes with a claimed "time is up").
//
// The message is request-only, like the runtime's wrap-up instruction: it is
// never stored (state.Messages and the session storage stay free of it) and
// never part of the stable system prompt prefix, so the provider prefix cache
// is unaffected. The hook runs after the prefix tracker on purpose: the
// appended trailing message must not change the recorded request fingerprint.
//
// A provider that binds a thinking block's signature to the request prefix it
// was created under (Anthropic 2026) adds one more constraint: inside a turn
// the hint must keep the same position and the same bytes on every request,
// or the replayed thinking blocks of an earlier step stop validating
// ("Invalid signature in thinking block. The block is bound to a different
// conversation", 2026-10-23). deadlineHook therefore freezes the text on a
// turn's first request and inserts the message always right after the last
// plain user message (the turn's own prompt, or a request-only wrap-up
// instruction, which the runtime appends at the end) instead of at the very
// end: messages the runtime stores for later steps of the turn (tool calls
// and results) are appended after that point, but must not precede the hint.

// nearDeadlineLeft is the absolute remaining time below which the hint asks
// for an immediate wrap-up (in addition to the window share).
const nearDeadlineLeft = 5 * time.Minute

// deadlineState holds the parsed job deadline of this run and the earliest
// moment the process observed the clock, the reference for the "remaining
// 10%" share (the env var carries no start time). One runner has one state;
// run, chat and ACP sessions of the process share it through their runners.
type deadlineState struct {
	// envName is Config.DeadlineEnv ("" = the hint is disabled, no logging).
	envName string
	// deadline is the parsed env value; zero = unavailable (missing env var,
	// unparsable, already disabled) and the hint stays off.
	deadline time.Time
	// now is the injected clock (Runner.clock): the hint text and the
	// remaining share are computed from it, so tests can pin both.
	now func() time.Time
	// start is the earliest now this state observed; startMu guards it: ACP
	// serves prompts concurrently.
	startMu sync.Mutex
	start   time.Time
}

// newDeadlineState resolves one run's deadline: it parses the env var named
// by DeadlineEnv once, and unavailable values only leave a debug line (a
// missing or bad variable is a configuration choice, not an error).
func newDeadlineState(envName string, now func() time.Time) *deadlineState {
	if now == nil {
		now = time.Now
	}
	state := &deadlineState{envName: envName, now: now}
	if envName == "" {
		return state
	}
	raw, ok := os.LookupEnv(envName)
	if !ok || strings.TrimSpace(raw) == "" {
		slog.Debug("deadline hint: environment variable not set", "env", envName)
		return state
	}
	deadline, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		slog.Debug("deadline hint: environment variable is not RFC3339", "env", envName, "err", err)
		return state
	}
	state.deadline = deadline
	return state
}

// observeStart keeps the earliest clock reading as the start reference of the
// remaining share. Callers pass the reading they have in hand.
func (s *deadlineState) observeStart(now time.Time) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.start.IsZero() || now.Before(s.start) {
		s.start = now
	}
}

// windowStart is the start reference in effect: the earliest observed moment,
// or the reading at hand for the first request (whose share is 1 anyway).
func (s *deadlineState) windowStart(now time.Time) time.Time {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.start.IsZero() || now.Before(s.start) {
		return now
	}
	return s.start
}

// hint renders the transient deadline message ("" = attach nothing). The
// wording follows the configured thresholds: past the deadline it says the
// task has overrun; inside the last 10% of the window or the last five
// minutes it asks for a wrap-up; otherwise it states the remaining minutes.
func (s *deadlineState) hint() string {
	if s == nil || s.deadline.IsZero() {
		return ""
	}
	// now comes from the injected clock so tests pin the wording; windowStart
	// records it as the start reference of the share, first request first.
	now := s.now()
	s.observeStart(now)
	remaining := s.deadline.Sub(now)
	window := s.deadline.Sub(s.windowStart(now))
	// Both times render in the local zone of the reading: the model answers
	// about "now", in one zone, whatever offset the deadline carries.
	location := now.Location()
	zone := now.Format("-07:00")
	nowText := now.Format("15:04")
	deadlineText := s.deadline.In(location).Format("15:04")
	prefix := fmt.Sprintf("[时间] 现在 %s（%s），", nowText, zone)
	switch {
	case remaining <= 0:
		return prefix + fmt.Sprintf("已超过本任务截止 %s，本任务已超时：请立即停止新的工作，直接汇报已完成与未完成的部分。", deadlineText)
	case remaining < nearDeadlineLeft || (window > 0 && remaining < window/10):
		return prefix + fmt.Sprintf("本任务截止 %s，即将到达时限：请尽快提交已完成的工作并汇报未完成项。", deadlineText)
	default:
		minutes := int(math.Round(remaining.Minutes()))
		return prefix + fmt.Sprintf("本任务截止 %s，剩余约 %d 分钟。", deadlineText, minutes)
	}
}

// deadlineHook attaches the transient deadline message to every model
// request. Appending to event.Request (the runtime hands each hook a clone)
// touches neither the stored state nor the stable prompt prefix; it runs
// after the prefix tracker (see turnHooks) so the recorded fingerprint keeps
// byte-identical prefixes.
//
// The hook instance lives for one turn (turnHooks assembles the hooks per
// run), so turnHint freezes the hint text on the turn's first request: the
// remaining minutes change between steps, and a provider that binds thinking
// signatures to the request prefix (Anthropic 2026) rejects the replay of an
// earlier step's thinking when the prefix bytes differ. The message rides
// right after the last plain user message instead of at the very end, so
// messages stored for later steps of the turn never move in front of it.
type deadlineHook struct {
	agentcore.NopHook
	state *deadlineState
	// turnHint is the frozen text of the current turn, "" until the turn's
	// first model request and also when the deadline hint is disabled.
	turnHint string
}

func (h *deadlineHook) BeforeModel(_ context.Context, event *agentcore.ModelEvent, _ *agentcore.Control) error {
	if h.state == nil || event == nil || event.Request == nil {
		return nil
	}
	if h.turnHint == "" {
		// One computation per turn: the wording ("剩余约 N 分钟") would drift
		// between requests and invalidate replayed thinking signatures.
		h.turnHint = h.state.hint()
	}
	if h.turnHint == "" {
		return nil
	}
	msgs := event.Request.Messages
	// Insert after the last plain user message (no tool call and no result on
	// it): that is the turn's own prompt, or the runtime's request-only wrap-up
	// instruction when one is there. Without one the hint is appended, the
	// first request's shape.
	at := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && msgs[i].ToolCall == nil && msgs[i].ToolResult == nil {
			at = i + 1
			break
		}
	}
	hint := agentcore.Message{Role: "user", Content: h.turnHint}
	if at == len(msgs) {
		event.Request.Messages = append(msgs, hint)
		return nil
	}
	// Shift one message right and drop the hint into the gap. The assignment
	// goes through the grown slice: append may have moved the elements to a
	// new array, and msgs still names the old one.
	grown := append(msgs[:at+1], msgs[at:]...)
	grown[at] = hint
	event.Request.Messages = grown
	return nil
}
