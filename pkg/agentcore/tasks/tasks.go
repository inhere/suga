// Package tasks runs work in the background for an agent session: start a
// command (or a function: a watch loop, a hook, later a sub-agent) and get an
// id at once, read its output while it runs, wait for it with a timeout, kill
// it. Output goes line by line (cleaned by the caller, e.g. decoded and
// redacted) to a file the host names per session. A finished task leaves a
// notice the host hands to the model before its next request (TakeNotices),
// unless the model already saw the end through Wait or Kill; a running
// function task may leave notices too (Reporter.Notify). A host that took
// notices for a turn the model never got them in hands them back with
// ReturnNotices. The whole process
// tree of every command task is killed by Kill, by the limits and by Close;
// a function task's context is cancelled. Nothing outlives the manager.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/internal/proctree"
)

// Status is where a task is in its life.
type Status string

const (
	Running  Status = "running"
	Exited   Status = "exited"    // the command ended by itself (any exit code)
	Killed   Status = "killed"    // Kill or Close ended it
	TimedOut Status = "timed_out" // it ran past Limits.MaxDuration
	Failed   Status = "failed"    // it could not be waited for (rare)
)

// Limits bound the tasks of a manager.
type Limits struct {
	// MaxRunning is how many tasks of one session may run at once.
	MaxRunning int
	// MaxDuration ends a task that runs longer.
	MaxDuration time.Duration
	// MaxOutputBytes stops writing a task's output file past this size.
	MaxOutputBytes int64
}

// DefaultLimits: 4 running per session, 2 hours each, 64 MiB of output.
var DefaultLimits = Limits{MaxRunning: 4, MaxDuration: 2 * time.Hour, MaxOutputBytes: 64 << 20}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits
	if l.MaxRunning > 0 {
		d.MaxRunning = l.MaxRunning
	}
	if l.MaxDuration > 0 {
		d.MaxDuration = l.MaxDuration
	}
	if l.MaxOutputBytes > 0 {
		d.MaxOutputBytes = l.MaxOutputBytes
	}
	return d
}

// Config sets up a Manager.
type Config struct {
	Limits Limits
	// Output names the output file of a task and its ref (a handle the
	// model can pass to other tools, e.g. "out:<session>:<hex>"). Required.
	Output func(sessionID, taskID string) (path, ref string, err error)
	// Session returns the session a call belongs to; nil uses
	// api.SessionIDFrom.
	Session func(ctx context.Context) string
	// OnNotice, when set, is called for every notice left pending: a task
	// that ended unseen, or a running task's Reporter.Notify (e.g. to wake an
	// idle chat). It runs in the task's goroutine and must not block.
	OnNotice func(Info)
}

// Reporter is what a function task (Spec.Run) tells the manager while it runs.
// Progress also returns a Wait on the task (WaitUpdated), taking a notice
// left before it: report a step as Count, Notify, then Progress.
type Reporter interface {
	// Notify leaves a notice for the model now, while the task keeps running
	// (e.g. a watch whose result changed). A newer notice of the same task
	// replaces one the host has not taken yet.
	Notify(text string)
	// Progress sets a short state shown with the task (e.g. "run 5, exit 0").
	Progress(text string)
	// Count sets a named counter reported with the task (Info.Counters).
	Count(name string, n int)
	// Label sets a named text reported with the task (Info.Labels), e.g. the
	// session a sub-agent ran in. Unlike Progress it returns no Wait.
	Label(name, value string)
}

// RunFunc is a function task: it writes its output to out (line by line,
// cleaned like a command's) and returns its exit code. ctx is cancelled by
// Kill, by Limits.MaxDuration and by Close; a non-nil error marks it failed.
type RunFunc func(ctx context.Context, out io.Writer, r Reporter) (exitCode int, err error)

// Spec is one task to start: a command (Cmd) or a function (Run).
type Spec struct {
	Kind    string // "shell", "watch", "hook"
	Summary string // what it runs, shown in lists and notices
	// Cmd is ready to Start (path, args, dir, env); its Stdout, Stderr and
	// SysProcAttr are set by the manager.
	Cmd *exec.Cmd
	// Limits caps the memory, process count and CPU time of Cmd and its whole
	// process tree (tools.shell_limits); the zero value is no limit.
	Limits proctree.Limits
	// LimitNotice, when set, is asked for the line to write to the task output
	// when the command ended after a limit: it gets whether the system ended a
	// process of the tree, the exit code and the last lines of the output (what a
	// refused allocation printed). An empty answer writes nothing.
	LimitNotice func(terminated bool, exitCode int, output string) string
	// Run is a function task, instead of Cmd.
	Run RunFunc
	// NoEndNotice: the task reports what matters through Reporter.Notify,
	// so its end leaves no notice (a Stop hook run).
	NoEndNotice bool
	// Clean turns raw output into text for the file and the tail, one line
	// at a time (no trailing newline); nil keeps the bytes as they are.
	Clean func(line []byte) string
}

// Info is a snapshot of a task.
type Info struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	Status    Status    `json:"status"`
	ExitCode  int       `json:"exit_code"`
	Started   time.Time `json:"started"`
	Ended     time.Time `json:"ended,omitzero"`
	// Bytes and Lines count what was written to the output file.
	Bytes     int64  `json:"bytes"`
	Lines     int    `json:"lines"`
	Truncated bool   `json:"truncated,omitempty"` // output past MaxOutputBytes was dropped
	Path      string `json:"path"`
	Ref       string `json:"ref"`
	// Tail is the last lines of the output (up to tailLines / tailBytes).
	Tail []string `json:"tail,omitempty"`
	// Error is why a function task failed.
	Error string `json:"error,omitempty"`
	// Progress and Counters are what a function task reported (Reporter).
	Progress string            `json:"progress,omitempty"`
	Counters map[string]int    `json:"counters,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	// Notice is set on a notice of a running task (TakeNotices): its text.
	// Empty on an end notice.
	Notice string `json:"notice,omitempty"`
}

// Duration is how long the task ran (so far, while running).
func (i Info) Duration() time.Duration {
	if i.Ended.IsZero() {
		return time.Since(i.Started)
	}
	return i.Ended.Sub(i.Started)
}

// ErrUnknownTask is returned for an id the session does not have.
var ErrUnknownTask = errors.New("unknown task")

// ProcessTree owns a command's process group or Windows job object. It is
// passed when a foreground command is adopted so ownership of the already
// started process tree moves to the manager without opening a kill window.
type ProcessTree interface {
	Kill() error
	Close()
	// Started assigns a start suspended process to the tree (Windows) and
	// resumes it; it returns an error if the process could not join, and the
	// caller then ends it and reports a failed command.
	Started() error
	// Exceeded reports whether a resource limit ended a process of the tree.
	Exceeded() bool
}

// RunningCommand is a command that has already been started by its caller.
// AttachOutput must atomically switch the command's output to the supplied
// writer, including bytes written before the handoff.
type RunningCommand struct {
	Cmd          *exec.Cmd
	Tree         ProcessTree
	AttachOutput func(io.Writer)
	Wait         <-chan error
	Started      time.Time
	// MaxDuration is measured from Started; zero uses the manager limit.
	MaxDuration time.Duration
}

// Manager owns the background tasks of one host process.
type Manager struct {
	cfg    Config
	limits Limits

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
	// onEnd is told of every task end (SetOnEnd).
	onEnd atomic.Pointer[func(Info)]
}

// SetOnEnd sets a function told (in the task's goroutine) of every task that
// ends, seen or not, e.g. to record it in the trace; the latest call wins.
func (m *Manager) SetOnEnd(fn func(Info)) { m.onEnd.Store(&fn) }

type session struct {
	seq     int
	tasks   map[string]*task
	order   []string
	notices []pending // notices not taken yet, in order
}

// pending is one notice: a task's end (text "") or a running task's Notify.
type pending struct {
	id   string
	text string
}

// NewManager returns a Manager; cfg.Output is required.
func NewManager(cfg Config) *Manager {
	if cfg.Session == nil {
		cfg.Session = api.SessionIDFrom
	}
	return &Manager{cfg: cfg, limits: cfg.Limits.withDefaults(), sessions: map[string]*session{}}
}

// Limits returns the effective limits.
func (m *Manager) Limits() Limits { return m.limits }

// SessionOf returns the session of ctx (Config.Session).
func (m *Manager) SessionOf(ctx context.Context) string { return m.cfg.Session(ctx) }

func (m *Manager) sessionLocked(id string) *session {
	s := m.sessions[id]
	if s == nil {
		s = &session{tasks: map[string]*task{}}
		m.sessions[id] = s
	}
	return s
}

// Start runs spec in the background for the session of ctx and returns its
// snapshot at once.
func (m *Manager) Start(ctx context.Context, spec Spec) (Info, error) {
	sid := m.cfg.Session(ctx)
	if sid == "" {
		return Info{}, errors.New("background tasks need a session")
	}
	if (spec.Cmd == nil) == (spec.Run == nil) {
		return Info{}, errors.New("a task needs a command or a function")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Info{}, errors.New("background tasks are shut down")
	}
	s := m.sessionLocked(sid)
	running := 0
	for _, t := range s.tasks {
		if t.status() == Running {
			running++
		}
	}
	if running >= m.limits.MaxRunning {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("%d background tasks are already running (the limit): wait for or kill one first", running)
	}
	s.seq++
	id := "t" + strconv.Itoa(s.seq)
	m.mu.Unlock()

	path, ref, err := m.cfg.Output(sid, id)
	if err != nil {
		return Info{}, fmt.Errorf("task output: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Info{}, fmt.Errorf("task output: %w", err)
	}
	t := &task{
		info: Info{ID: id, SessionID: sid, Kind: spec.Kind, Summary: spec.Summary, Status: Running,
			ExitCode: -1, Started: time.Now(), Path: path, Ref: ref},
		done:    make(chan struct{}),
		updated: make(chan struct{}),
		seen:    spec.NoEndNotice,
	}
	t.out = newLineWriter(file, spec.Clean, m.limits.MaxOutputBytes)
	if spec.Run != nil {
		ctx, cancel := context.WithCancel(context.Background())
		t.cancel = cancel
		m.register(s, t)
		go m.runFunc(s, t, ctx, spec.Run)
		return t.snapshot(), nil
	}
	cmd := spec.Cmd
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = t.out, t.out
	// A leftover child holding the pipe must not keep the task "running"
	// after the command itself ended.
	cmd.WaitDelay = 2 * time.Second
	t.tree = proctree.NewLimited(cmd, spec.Limits)
	t.limitNotice = spec.LimitNotice
	if err := cmd.Start(); err != nil {
		_ = file.Close()
		return Info{}, fmt.Errorf("start: %w", err)
	}
	// The task is not registered yet: if the shell could not join its job (its
	// still suspended process is terminated by Started), it becomes an error of
	// Start instead of a task that instantly failed.
	if serr := t.tree.Started(); serr != nil {
		_ = cmd.Wait()
		t.tree.Close()
		_ = file.Close()
		return Info{}, fmt.Errorf("start: %w", serr)
	}
	m.register(s, t)
	go m.wait(s, t, cmd)
	return t.snapshot(), nil
}

// Adopt takes ownership of a command that is already running. It is used when
// a foreground shell reaches its handoff threshold; the caller's process tree
// and output stream are transferred before the manager starts waiting for it.
func (m *Manager) Adopt(ctx context.Context, spec Spec, running RunningCommand) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if running.Cmd == nil || running.Cmd.Process == nil {
		return Info{}, errors.New("a running command is required")
	}
	if running.Tree == nil || running.AttachOutput == nil || running.Wait == nil {
		return Info{}, errors.New("a running command needs its tree, output attachment and waiter")
	}
	sid := m.cfg.Session(ctx)
	if sid == "" {
		return Info{}, errors.New("background tasks need a session")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Info{}, errors.New("background tasks are shut down")
	}
	s := m.sessionLocked(sid)
	runningCount := 0
	for _, task := range s.tasks {
		if task.status() == Running {
			runningCount++
		}
	}
	if runningCount >= m.limits.MaxRunning {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("%d background tasks are already running (the limit): wait for or kill one first", runningCount)
	}
	s.seq++
	id := "t" + strconv.Itoa(s.seq)
	m.mu.Unlock()

	path, ref, err := m.cfg.Output(sid, id)
	if err != nil {
		return Info{}, fmt.Errorf("task output: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Info{}, fmt.Errorf("task output: %w", err)
	}
	started := running.Started
	if started.IsZero() {
		started = time.Now()
	}
	t := &task{
		info: Info{ID: id, SessionID: sid, Kind: spec.Kind, Summary: spec.Summary, Status: Running,
			ExitCode: -1, Started: started, Path: path, Ref: ref},
		done:    make(chan struct{}),
		updated: make(chan struct{}),
		seen:    spec.NoEndNotice,
		tree:    running.Tree,
	}
	t.out = newLineWriter(file, spec.Clean, m.limits.MaxOutputBytes)
	// Attach before registration: once visible in :tasks, every byte must be
	// owned by the task and the foreground caller must no longer close the tree.
	running.AttachOutput(t.out)
	m.registerWithDuration(s, t, started, running.MaxDuration)
	go m.waitResult(s, t, running.Cmd, running.Wait)
	return t.snapshot(), nil
}

// register adds a started task to its session and arms its time limit.
func (m *Manager) register(s *session, t *task) {
	m.registerWithDuration(s, t, t.info.Started, 0)
}

// registerWithDuration uses the shorter of the manager limit and an adopted
// command's hard deadline. The latter is measured from the original start.
func (m *Manager) registerWithDuration(s *session, t *task, started time.Time, maxDuration time.Duration) {
	m.mu.Lock()
	s.tasks[t.info.ID] = t
	s.order = append(s.order, t.info.ID)
	m.mu.Unlock()
	duration := m.limits.MaxDuration
	if maxDuration > 0 {
		remaining := time.Until(started.Add(maxDuration))
		if remaining < duration {
			duration = remaining
		}
	}
	if duration <= 0 {
		duration = time.Nanosecond
	}
	t.timer = time.AfterFunc(duration, func() { t.end(TimedOut) })
}

// wait reaps the command and records how it ended.
func (m *Manager) wait(s *session, t *task, cmd *exec.Cmd) {
	m.waitResult(s, t, cmd, waitCommand(cmd))
}

func waitCommand(cmd *exec.Cmd) <-chan error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done
}

// waitResult consumes the one and only Wait result. Adopted commands already
// have a waiter from their foreground caller, so calling cmd.Wait again would
// lose the exit result and leave the task in a failed state.
func (m *Manager) waitResult(s *session, t *task, cmd *exec.Cmd, done <-chan error) {
	err := <-done
	t.tree.Close() // also ends children left running in the background
	// A limit that ended a process of the tree, or that made the command fail its
	// allocation, is named in the output: the exit code alone would read like the
	// command's own failure.
	code := cmd.ProcessState.ExitCode()
	if t.limitNotice != nil {
		_, _, _, tail := t.out.stats()
		if note := t.limitNotice(t.tree.Exceeded(), code, strings.Join(tail, "\n")); note != "" {
			fmt.Fprintf(t.out, "=== %s ===\n", note)
		}
	}
	status := Exited
	if err != nil && code < 0 {
		status = Failed
	}
	m.finish(s, t, status, code, "")
}

// runFunc runs a function task and records how it ended.
func (m *Manager) runFunc(s *session, t *task, ctx context.Context, run RunFunc) {
	code, err := run(ctx, t.out, reporter{m: m, s: s, t: t})
	t.cancel()
	status, msg := Exited, ""
	if err != nil {
		status, msg = Failed, err.Error()
	}
	m.finish(s, t, status, code, msg)
}

// finish records a task's end (status unless Kill or the time limit ended
// it), tells the host and leaves the end notice unless the model saw it.
func (m *Manager) finish(s *session, t *task, status Status, code int, errMsg string) {
	t.timer.Stop()
	t.out.finish()
	t.mu.Lock()
	t.info.Ended = time.Now()
	t.info.ExitCode = code
	t.info.Status = status
	if t.ending != "" {
		t.info.Status = t.ending
	} else if status == Failed {
		t.info.Error = errMsg
	}
	t.mu.Unlock()
	// The end is recorded before Wait and Kill return: a host that exits
	// right after Kill (run) still gets it into its store.
	if fn := m.onEnd.Load(); fn != nil && *fn != nil {
		(*fn)(t.snapshot())
	}
	// The end notice is queued before done is closed: Kill and Wait return on
	// done and then drop it (markSeen), so it must already be there, or it
	// would be queued after they dropped it and tell the model twice.
	m.mu.Lock()
	t.mu.Lock()
	seen := t.seen
	t.mu.Unlock()
	if !seen {
		s.notices = append(s.notices, pending{id: t.info.ID})
	}
	m.mu.Unlock()
	close(t.done)
	if !seen && m.cfg.OnNotice != nil {
		m.cfg.OnNotice(t.snapshot())
	}
}

// reporter is the Reporter of one function task.
type reporter struct {
	m *Manager
	s *session
	t *task
}

func (r reporter) Notify(text string) {
	if text == "" {
		return
	}
	r.m.mu.Lock()
	// A newer notice replaces the task's pending one (not its end notice).
	r.s.notices = slices.DeleteFunc(r.s.notices, func(p pending) bool { return p.id == r.t.info.ID && p.text != "" })
	r.s.notices = append(r.s.notices, pending{id: r.t.info.ID, text: text})
	r.m.mu.Unlock()
	if r.m.cfg.OnNotice != nil {
		info := r.t.snapshot()
		info.Notice = text
		r.m.cfg.OnNotice(info)
	}
}

func (r reporter) Progress(text string) {
	r.t.mu.Lock()
	r.t.info.Progress = text
	r.t.mu.Unlock()
	r.t.signal()
}

// signal wakes the Waits of the task (it reported).
func (t *task) signal() {
	t.mu.Lock()
	close(t.updated)
	t.updated = make(chan struct{})
	t.mu.Unlock()
}

func (r reporter) Label(name, value string) {
	r.t.mu.Lock()
	if r.t.info.Labels == nil {
		r.t.info.Labels = map[string]string{}
	}
	r.t.info.Labels[name] = value
	r.t.mu.Unlock()
}

func (r reporter) Count(name string, n int) {
	r.t.mu.Lock()
	if r.t.info.Counters == nil {
		r.t.info.Counters = map[string]int{}
	}
	r.t.info.Counters[name] = n
	r.t.mu.Unlock()
}

// List returns the session's tasks, in start order.
func (m *Manager) List(sessionID string) []Info {
	m.mu.Lock()
	s := m.sessions[sessionID]
	var tasks []*task
	if s != nil {
		for _, id := range s.order {
			tasks = append(tasks, s.tasks[id])
		}
	}
	m.mu.Unlock()
	out := make([]Info, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.snapshot())
	}
	return out
}

func (m *Manager) get(sessionID, id string) (*task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[sessionID]; s != nil {
		if t := s.tasks[id]; t != nil {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%w %q", ErrUnknownTask, id)
}

// Get returns one task's snapshot.
func (m *Manager) Get(sessionID, id string) (Info, error) {
	t, err := m.get(sessionID, id)
	if err != nil {
		return Info{}, err
	}
	return t.snapshot(), nil
}

// WaitOutcome is how a Wait returned.
type WaitOutcome int

const (
	WaitTimedOut WaitOutcome = iota // still running after the timeout
	WaitEnded                       // the task ended; its notices are taken (Info.Notice: its last report)
	// WaitUpdated: a running function task reported progress (a watch
	// finished a run); a notice it left is taken into Info.Notice.
	WaitUpdated
)

// Wait waits up to timeout for the task to end or, for a function task, to
// report progress (Reporter.Progress): a watch never ends by itself, so
// waiting for it means waiting for its next run. What the caller saw is not
// noticed again.
func (m *Manager) Wait(ctx context.Context, sessionID, id string, timeout time.Duration) (Info, WaitOutcome, error) {
	t, err := m.get(sessionID, id)
	if err != nil {
		return Info{}, WaitTimedOut, err
	}
	t.mu.Lock()
	updated := t.updated
	t.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.done:
		m.markSeen(sessionID, t)
		info := t.snapshot()
		info.Notice = m.takeRunningNotice(sessionID, t.info.ID) // e.g. an agent task's report
		return info, WaitEnded, nil
	case <-updated:
		info := t.snapshot()
		info.Notice = m.takeRunningNotice(sessionID, t.info.ID)
		return info, WaitUpdated, nil
	case <-timer.C:
		return t.snapshot(), WaitTimedOut, nil
	case <-ctx.Done():
		return t.snapshot(), WaitTimedOut, ctx.Err()
	}
}

// takeRunningNotice drops the task's pending running notice and returns it.
func (m *Manager) takeRunningNotice(sessionID, id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	if s == nil {
		return ""
	}
	text := ""
	s.notices = slices.DeleteFunc(s.notices, func(p pending) bool {
		if p.id == id && p.text != "" {
			text = p.text
			return true
		}
		return false
	})
	return text
}

// Kill ends the task's process tree and waits (briefly) for it to be reaped.
func (m *Manager) Kill(sessionID, id string) (Info, error) {
	t, err := m.get(sessionID, id)
	if err != nil {
		return Info{}, err
	}
	t.end(Killed)
	select {
	case <-t.done:
	case <-time.After(5 * time.Second):
	}
	m.markSeen(sessionID, t)
	return t.snapshot(), nil
}

// markSeen drops the pending notice of an ended task.
func (m *Manager) markSeen(sessionID string, t *task) {
	t.mu.Lock()
	t.seen = true
	t.mu.Unlock()
	m.mu.Lock()
	if s := m.sessions[sessionID]; s != nil {
		s.notices = slices.DeleteFunc(s.notices, func(p pending) bool { return p.id == t.info.ID && p.text == "" })
	}
	m.mu.Unlock()
}

// TakeNotices returns the session's ended tasks whose notice is pending and
// clears them.
func (m *Manager) TakeNotices(sessionID string) []Info {
	m.mu.Lock()
	s := m.sessions[sessionID]
	var taken []pending
	if s != nil {
		taken, s.notices = s.notices, nil
	}
	tasks := make([]*task, len(taken))
	for i, p := range taken {
		tasks[i] = s.tasks[p.id]
	}
	m.mu.Unlock()
	out := make([]Info, 0, len(taken))
	for i, t := range tasks {
		if taken[i].text == "" { // an end notice: the model sees the end
			t.mu.Lock()
			t.seen = true
			t.mu.Unlock()
		}
		info := t.snapshot()
		info.Notice = taken[i].text
		out = append(out, info)
	}
	return out
}

// ReturnNotices puts notices back into the session's pending queue (see
// TakeNotices): a host that took them for a turn which then did not deliver
// them to the model - a turn that ends cancelled or failed, whose entries only
// replay up to its last complete tool exchange - hands them back here instead of
// losing them. They keep their original order and go before the notices that
// arrived in the meantime, so the next TakeNotices returns each of them once
// more. A notice of a task that left a newer notice meanwhile (a running task
// reporting again, or ending) is dropped: the same "a newer notice replaces the
// older one" rule Reporter.Notify applies.
func (m *Manager) ReturnNotices(sessionID string, infos []Info) {
	if len(infos) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	if s == nil {
		return
	}
	back := make([]pending, 0, len(infos))
	for _, info := range infos {
		if _, ok := s.tasks[info.ID]; !ok {
			continue // the session no longer knows the task
		}
		if info.Notice != "" && hasNoticeLocked(s, info.ID) {
			continue // a newer notice of the task is queued already
		}
		back = append(back, pending{id: info.ID, text: info.Notice})
	}
	if len(back) == 0 {
		return
	}
	s.notices = append(back, s.notices...)
}

// hasNoticeLocked reports whether the session has a pending notice of id.
func hasNoticeLocked(s *session, id string) bool {
	for _, p := range s.notices {
		if p.id == id {
			return true
		}
	}
	return false
}

// HasNotices reports whether the session has pending notices.
func (m *Manager) HasNotices(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	return s != nil && len(s.notices) > 0
}

// Running returns the session's running tasks.
func (m *Manager) Running(sessionID string) []Info {
	var out []Info
	for _, i := range m.List(sessionID) {
		if i.Status == Running {
			out = append(out, i)
		}
	}
	return out
}

// KillSession kills the session's running tasks and returns them.
func (m *Manager) KillSession(sessionID string) []Info {
	var out []Info
	for _, i := range m.Running(sessionID) {
		if info, err := m.Kill(sessionID, i.ID); err == nil {
			out = append(out, info)
		}
	}
	return out
}

// Close kills every running task and refuses new ones.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	var ids []string
	for sid := range m.sessions {
		ids = append(ids, sid)
	}
	m.mu.Unlock()
	for _, sid := range ids {
		m.KillSession(sid)
	}
}

// task is one background command or function.
type task struct {
	mu     sync.Mutex
	info   Info
	ending Status             // set by end before the kill: how the end is reported
	seen   bool               // the model saw the end (Wait, Kill or a notice)
	tree   ProcessTree        // a command's process tree
	cancel context.CancelFunc // a function's context
	// limitNotice is asked for the line a limit leaves in the output.
	limitNotice func(terminated bool, exitCode int, output string) string
	// updated is closed (and replaced) when a function task reports.
	updated chan struct{}
	out     *lineWriter
	timer   *time.Timer
	done    chan struct{}
}

func (t *task) status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info.Status
}

// end kills the task, reporting it as how (unless it already ended).
func (t *task) end(how Status) {
	t.mu.Lock()
	if t.info.Status != Running || t.ending != "" {
		t.mu.Unlock()
		return
	}
	t.ending = how
	t.mu.Unlock()
	if t.tree != nil {
		_ = t.tree.Kill()
	} else {
		t.cancel()
	}
}

func (t *task) snapshot() Info {
	t.mu.Lock()
	info := t.info
	info.Counters = maps.Clone(t.info.Counters)
	info.Labels = maps.Clone(t.info.Labels)
	t.mu.Unlock()
	info.Bytes, info.Lines, info.Truncated, info.Tail = t.out.stats()
	return info
}
