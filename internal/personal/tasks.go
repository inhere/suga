package personal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// TaskNotices hands over the session's ended background tasks the model has
// not seen yet (tasks.Manager) and takes them back when the turn that took them
// did not deliver them.
type TaskNotices interface {
	TakeNotices(sessionID string) []tasks.Info
	// ReturnNotices puts notices a turn took but did not deliver back into the
	// pending queue (a cancelled or failed turn: its entries do not replay the
	// notice, see replayablePrefix).
	ReturnNotices(sessionID string, infos []tasks.Info)
}

// turnNotices is the task notices one turn took from the manager, kept so finish
// can put them back when the turn ends cancelled or failed (issue suga-o26e).
// The turn's own goroutine is the only user (BuildMessages and finish run in
// it), like the steering inbox.
type turnNotices struct {
	taken []tasks.Info
}

// add records the notices a model request took.
func (n *turnNotices) add(infos []tasks.Info) { n.taken = append(n.taken, infos...) }

// returned empties the sink and returns the notices the next turn has to take
// again.
func (n *turnNotices) returned() []tasks.Info {
	out := n.taken
	n.taken = nil
	return out
}

// turnNoticesKey carries the turn's notice sink to finish, like steerInboxKey
// carries the steering inbox.
type turnNoticesKey struct{}

func withTurnNotices(ctx context.Context, n *turnNotices) context.Context {
	return context.WithValue(ctx, turnNoticesKey{}, n)
}

func turnNoticesOf(ctx context.Context) *turnNotices {
	n, _ := ctx.Value(turnNoticesKey{}).(*turnNotices)
	return n
}

// metaTaskNotice marks the user message carrying background task notices.
const metaTaskNotice = "suga.task_notice"

// noticeTailLines are shown per task in a notice.
const noticeTailLines = 5

// TaskNoticeText is the text of a notice for ended tasks and for what
// running tasks reported (a watch whose result changed).
func TaskNoticeText(infos []tasks.Info) string {
	var b strings.Builder
	head := "These background tasks ended"
	for _, info := range infos {
		if info.Notice != "" {
			head = "Background task updates"
			break
		}
	}
	b.WriteString("[background task notice] " + head + " (read more with the task tool or recall the ref):")
	for _, info := range infos {
		if info.Notice != "" { // the report of a task (sent while it ran)
			b.WriteString("\n- " + info.ID + " (" + info.Kind + ") reports: " + strings.ReplaceAll(info.Notice, "\n", "\n  "))
			continue
		}
		b.WriteString("\n- " + tasks.Describe(info))
		if tail := tasks.TailText(info, noticeTailLines); tail != "" {
			b.WriteString("\n  last lines:\n  " + strings.ReplaceAll(tail, "\n", "\n  "))
		}
	}
	return b.String()
}

// TaskNoticeMessage is the user message of a notice.
func TaskNoticeMessage(infos []tasks.Info) agentcore.Message {
	return agentcore.Message{Role: "user", Content: TaskNoticeText(infos), Metadata: map[string]any{metaTaskNotice: true}}
}

// turnInjected is an entry injected into the running turn - a background task
// notice or a steer - and where it goes: after the first at messages of
// state.Messages. seq is the store's entry seq of the entry (0 when it could
// not be stored: it then stays in the view for the rest of the turn and never
// takes part in a cut).
type turnInjected struct {
	at  int
	seq int64
	msg agentcore.Message
}

// takeTaskNotices moves the session's pending task notices into this turn
// (before the coming model request) and stores them as a user entry, so
// later turns replay them in place. The notices themselves are recorded in the
// turn's sink: a turn that ends without completing the delivery (cancelled or
// failed) hands them back to the manager (returnTaskNotices).
func (b *contextBuilder) takeTaskNotices(ctx context.Context, state *agentcore.RunState) {
	src := b.r.cfg.Tasks
	if src == nil || state == nil {
		return
	}
	infos := src.TakeNotices(b.sessionID)
	if len(infos) == 0 {
		return
	}
	if n := b.notices; n != nil {
		n.add(infos)
	}
	msg := TaskNoticeMessage(infos)
	notice := turnInjected{at: len(state.Messages), msg: msg}
	// EncodeMessage as everywhere entries are written (store.EncodeMessage).
	raw, err := store.EncodeMessage(msg)
	if err == nil {
		var entries []store.Entry
		entries, err = b.r.cfg.Repository.AppendEntries(context.WithoutCancel(ctx), b.sessionID,
			store.NewEntry{TurnID: b.turnID, Type: store.EntryUser, JSON: raw})
		if err == nil && len(entries) > 0 {
			notice.seq = entries[0].Seq
		}
	}
	if err != nil {
		slog.Warn("store task notice", "session_id", b.sessionID, "err", err)
	}
	b.injected = append(b.injected, notice)
	// The turn's stored entries changed: the cached view of items() is stale.
	b.turnItemsCache = nil
}

// isTaskNoticeItem reports whether m is a background task notice the runtime
// injected into a running turn.
func isTaskNoticeItem(m agentcore.Message) bool {
	v, _ := m.Metadata[metaTaskNotice].(bool)
	return v
}

// returnTaskNotices puts the notices this turn took back into the manager's
// pending queue when the turn ends without delivering them to the model
// (cancelled or failed): such a turn replays only up to its last complete tool
// exchange and replayablePrefix skips its notices, so the TakeNotices that
// handed them out would be the model's last chance to see the task end. A
// completed turn keeps them - its entries, the notice among them, replay - and
// the reason mirrors the one that returns the steers of the same turn.
func (r *Runner) returnTaskNotices(ctx context.Context, sessionID, turnStatus string) {
	if turnStatus == store.TurnCompleted {
		return
	}
	sink := turnNoticesOf(ctx)
	if sink == nil || r.cfg.Tasks == nil {
		return
	}
	infos := sink.returned()
	if len(infos) == 0 {
		return
	}
	r.cfg.Tasks.ReturnNotices(sessionID, infos)
}

// RecordTaskEnd records a background task's end in the trace (a task event
// spanning its whole run). It is called from the task's own goroutine.
func (r *Runner) RecordTaskEnd(info tasks.Info) {
	r.rec.record(context.Background(), taskEndEvent(info))
}

// RecordTaskEndIn records a task end straight into repo, for hosts whose
// tasks outlive the runner (ACP opens a runner per prompt, so a task may end
// between turns).
func RecordTaskEndIn(repo *store.Store, info tasks.Info) {
	(&recorder{sink: repo}).record(context.Background(), taskEndEvent(info))
}

// taskEndEvent is the task event of an ended task.
func taskEndEvent(info tasks.Info) store.Event {
	attrs := map[string]any{"arg": info.Summary, "kind": info.Kind, "exit_code": info.ExitCode,
		"bytes": info.Bytes, "lines": info.Lines, "ref": info.Ref}
	if info.Truncated {
		attrs["truncated"] = true
	}
	// What a function task reported: a watch's runs and changes, its state.
	for name, n := range info.Counters {
		attrs[name] = n
	}
	for name, value := range info.Labels {
		attrs[name] = value
	}
	if info.Progress != "" {
		attrs["progress"] = info.Progress
	}
	status := eventOK
	if info.Status != tasks.Exited || info.ExitCode != 0 {
		status = string(info.Status)
	}
	return store.Event{Kind: store.EventTask, Name: info.ID, SessionID: info.SessionID,
		Status: status, StartedAt: info.Started, DurationMs: info.Duration().Milliseconds(), Attrs: attrs,
		Error: errorOfTask(info)}
}

func errorOfTask(info tasks.Info) string {
	if info.Status == tasks.Exited && info.ExitCode != 0 {
		return fmt.Sprintf("exit code %d", info.ExitCode)
	}
	if info.Status == tasks.TimedOut {
		return "ran past tasks.max_duration " + time.Duration(info.Duration()).Round(time.Second).String()
	}
	return info.Error // a function task that failed
}
