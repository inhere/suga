package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// Session rewind and fork, target side (session rewind design 0.2, plan T3):
// which turns a person can pick, how a target argument resolves to one, where
// the conversation is cut for it, and the two refusals rewind and fork share.

// Errors of target resolution and of the preconditions. They are sentinels (the
// returned errors wrap them with the offending argument or count), so the chat
// command can pick the message with errors.Is (plan T7).
var (
	// ErrRewindTargetNotFound: the ordinal is out of range, or the turn id is not
	// a turn of this session (a turn of another session is reported the same way,
	// so the id does not leak its existence).
	ErrRewindTargetNotFound = errors.New("rewind target not found in this session")
	// ErrRewindTargetMasked: the turn was rewound away earlier; it is no longer part
	// of the conversation.
	ErrRewindTargetMasked = errors.New("rewind target was already rewound away")
	// ErrRewindTargetNotUserTurn: the turn exists but a person did not start it (an
	// injected prompt, a nudge, a delivery) or it has no request entry.
	ErrRewindTargetNotUserTurn = errors.New("rewind target is not a user turn")
	// ErrRewindSessionArchived: rewind and fork refuse an archived session.
	ErrRewindSessionArchived = errors.New("session is archived")
	// ErrRewindTasksRunning: a background task of the session still runs; rewinding
	// the conversation under it would leave it reporting into a history it is not in.
	ErrRewindTasksRunning = errors.New("session has running background tasks")
)

// userTurn is a turn a person started and the conversation still contains.
type userTurn struct {
	Turn store.Turn
	// Ordinal is the 1-based position among the session's user turns, oldest first
	// (what `:rewind 3` means).
	Ordinal int
	// FirstSeq is the seq of the turn's first entry of any type: a context_update
	// the turn wrote at its start comes before the request and is rewound with it.
	FirstSeq int64
}

// cutFor is the cut_seq of a rewind to the turn: the last entry before it (the
// masked interval is (cut, record seq), so the turn's first entry is the first
// masked one).
func cutFor(t userTurn) int64 { return t.FirstSeq - 1 }

// userTurns lists the session's user turns, oldest first. A turn is left out
// when its request entry is injected (suga.injected: a Stop hook reply, a nudge,
// a `suga send` delivery - those carry Hops>0 - a task-notice turn, a steer
// turn nobody typed), when it has no request entry (no entries at all, or only
// context updates and injected items), or when a rewind masked it.
func (r *Runner) userTurns(ctx context.Context, sessionID string) ([]userTurn, error) {
	all, _, err := r.classifyTurns(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var out []userTurn
	for _, c := range all {
		if c.err == nil {
			out = append(out, c.userTurn)
		}
	}
	return out, nil
}

// turnClass is one turn with the reason it is not a user turn (nil = it is one;
// err wraps ErrRewindTargetMasked or ErrRewindTargetNotUserTurn).
type turnClass struct {
	userTurn
	err error
}

// classifyTurns reads the session once and sorts every turn; the second result
// indexes the turns by id. Ordinals count the user turns only.
func (r *Runner) classifyTurns(ctx context.Context, sessionID string) ([]turnClass, map[string]int, error) {
	repo := r.cfg.Repository
	turns, err := repo.ListTurns(ctx, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("list turns: %w", err)
	}
	masked, err := r.maskedRanges(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	refs, err := repo.ListEntryRefs(ctx, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("list entry refs: %w", err)
	}
	first := make(map[string]int64, len(turns)) // refs are oldest first
	for _, ref := range refs {
		if ref.TurnID == "" {
			continue
		}
		if _, ok := first[ref.TurnID]; !ok {
			first[ref.TurnID] = ref.Seq
		}
	}
	users, err := repo.ListEntries(ctx, sessionID, store.EntryFilter{Types: []string{store.EntryUser}})
	if err != nil {
		return nil, nil, fmt.Errorf("list user entries: %w", err)
	}
	// request: the turn's request entry is its first user entry that is not a
	// context update, a segment context or an item injected into the running turn.
	request := make(map[string]agentcore.Message, len(turns))
	for _, e := range users {
		if _, seen := request[e.TurnID]; seen {
			continue
		}
		var m agentcore.Message
		if err := json.Unmarshal(e.JSON, &m); err != nil || m.Role != "user" || isContextUpdate(m) || isSegmentContext(m) || isInjectedItem(m) {
			continue
		}
		request[e.TurnID] = m
	}
	out := make([]turnClass, 0, len(turns))
	byID := make(map[string]int, len(turns))
	ordinal := 0
	for _, t := range turns {
		c := turnClass{userTurn: userTurn{Turn: t, FirstSeq: first[t.ID]}}
		m, hasRequest := request[t.ID]
		injected, _ := m.Metadata[metaInjected].(bool)
		switch {
		case c.FirstSeq > 0 && masked.Hidden(c.FirstSeq):
			c.err = ErrRewindTargetMasked
		case !hasRequest || c.FirstSeq == 0 || injected:
			c.err = ErrRewindTargetNotUserTurn
		default:
			ordinal++
			c.Ordinal = ordinal
		}
		byID[t.ID] = len(out)
		out = append(out, c)
	}
	return out, byID, nil
}

// resolveTarget turns a `:rewind` / `:fork` argument into a user turn: a 1-based
// ordinal among userTurns, or a turn id. A turn id has to be a turn of this
// session (SR1301: an id of another session never resolves), outside the masked
// intervals, and a user turn; each failure has its own sentinel.
func (r *Runner) resolveTarget(ctx context.Context, sessionID, arg string) (userTurn, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return userTurn{}, fmt.Errorf("%w: empty target", ErrRewindTargetNotFound)
	}
	all, byID, err := r.classifyTurns(ctx, sessionID)
	if err != nil {
		return userTurn{}, err
	}
	if n, err := strconv.Atoi(arg); err == nil {
		for _, c := range all {
			if c.err == nil && c.Ordinal == n {
				return c.userTurn, nil
			}
		}
		return userTurn{}, fmt.Errorf("%w: no user turn number %d", ErrRewindTargetNotFound, n)
	}
	// byID only holds this session's turns: the ownership check.
	i, ok := byID[arg]
	if !ok {
		return userTurn{}, fmt.Errorf("%w: turn %q", ErrRewindTargetNotFound, arg)
	}
	if c := all[i]; c.err != nil {
		return userTurn{}, fmt.Errorf("%w: turn %q", c.err, arg)
	}
	return all[i].userTurn, nil
}

// rewindPreconditions is the refusal rewind and fork share, called inside
// withSessionUnlessBusy's fn (a running turn was already refused there with
// ErrRewindBusy). It refuses an archived session and a session with a running
// background task. Without a task manager (Config.Tasks nil: sugasdk, a CLI
// that never started one) there are no tasks to wait for.
func (r *Runner) rewindPreconditions(ctx context.Context, sessionID string) error {
	sess, err := r.cfg.Repository.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.Status == store.SessionArchived {
		return fmt.Errorf("%w: %s", ErrRewindSessionArchived, sessionID)
	}
	if r.cfg.Tasks == nil {
		return nil
	}
	running := 0
	for _, t := range r.cfg.Tasks.List(sessionID) {
		if t.Status == tasks.Running {
			running++
		}
	}
	if running > 0 {
		return fmt.Errorf("%w: %d running", ErrRewindTasksRunning, running)
	}
	return nil
}
