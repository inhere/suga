package personal

import (
	"context"
	"errors"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// meta appends a user entry carrying metadata flags.
func (f *maskFixture) meta(sid, turn, text string, keys ...string) int64 {
	f.t.Helper()
	md := map[string]any{}
	for _, k := range keys {
		md[k] = true
	}
	return f.add(sid, turn, store.EntryUser, agentcore.Message{Role: "user", Content: text, Metadata: md})
}

func userTurnIDs(ts []userTurn) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Turn.ID)
	}
	return out
}

// mixedSession is: u1 typed, inj1 (Stop hook / delivery: tagged injected), nudge
// (injected), empty (a turn without entries), u2 typed with a context_update in
// front and a steer entry inside, steerTurn (steer fallback nobody typed),
// u3 typed.
func (f *maskFixture) mixedSession(sid string) {
	f.session(sid)
	f.turn(sid, "u1", store.TurnCompleted)
	f.msg(sid, "u1", "user", "first typed request")
	f.msg(sid, "u1", "assistant", "a1")
	f.turn(sid, "inj1", store.TurnCompleted)
	f.meta(sid, "inj1", "from a delivery", metaInjected)
	f.msg(sid, "inj1", "assistant", "a")
	f.turn(sid, "nudge", store.TurnCompleted)
	f.meta(sid, "nudge", "you may want to remember", metaInjected)
	f.msg(sid, "nudge", "assistant", "a")
	f.turn(sid, "empty", store.TurnCancelled)
	f.turn(sid, "u2", store.TurnCompleted)
	f.meta(sid, "u2", "ctx update", metaContextUpdate)
	f.msg(sid, "u2", "user", "second typed request")
	f.meta(sid, "u2", "steer inside the turn", metaSteer)
	f.msg(sid, "u2", "assistant", "a2")
	f.turn(sid, "steerTurn", store.TurnCompleted)
	f.meta(sid, "steerTurn", "steer nobody typed", metaInjected, metaSteer)
	f.turn(sid, "u3", store.TurnCompleted)
	f.msg(sid, "u3", "user", "third typed request")
	f.msg(sid, "u3", "assistant", "a3")
}

func TestUserTurns(t *testing.T) {
	t.Run("injected, nudge, empty and steer turns are left out", func(t *testing.T) {
		f := newMaskFixture(t)
		f.mixedSession("s")
		got, err := f.runner.userTurns(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, []string{"u1", "u2", "u3"}, userTurnIDs(got))
		assert.Eq(t, []int{1, 2, 3}, []int{got[0].Ordinal, got[1].Ordinal, got[2].Ordinal})
	})
	t.Run("a turn with only injected items has no request", func(t *testing.T) {
		f := newMaskFixture(t)
		f.session("s")
		f.turn("s", "only", store.TurnCompleted)
		f.meta("s", "only", "task ended", metaTaskNotice)
		f.turn("s", "u", store.TurnCompleted)
		f.msg("s", "u", "user", "typed request")
		got, err := f.runner.userTurns(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, []string{"u"}, userTurnIDs(got))
	})
	t.Run("masked turns are left out, later ones keep counting", func(t *testing.T) {
		f := newMaskFixture(t)
		f.threeTurns("s")
		f.rewind("s", 2) // masks t2, t3 (seq 3..6)
		f.turn("s", "t4", store.TurnCompleted)
		f.msg("s", "t4", "user", "request number 4 please")
		got, err := f.runner.userTurns(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, []string{"t1", "t4"}, userTurnIDs(got))
		assert.Eq(t, 2, got[1].Ordinal)
	})
	t.Run("no turns", func(t *testing.T) {
		f := newMaskFixture(t)
		f.session("s")
		got, err := f.runner.userTurns(f.ctx, "s")
		assert.NoErr(t, err)
		assert.Eq(t, 0, len(got))
	})
}

// The runtime tags injected prompts itself: verify against real runs that the
// turns a delivery (Hops>0), a nudge or `suga run --injected` start carry
// suga.injected, so userTurns leaves them out.
func TestUserTurnsRealRunTagging(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &scriptProv{name: "main", normal: func(int, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
		return finalText("ok")
	}}
	runner := compactRunner(t, repo, prov, bigToolRegistry(1, nil), nil)
	ctx := context.Background()
	first, err := runner.Run(ctx, RunRequest{Prompt: "typed by a person"})
	assert.NoErr(t, err)
	sid := first.SessionID
	_, err = runner.Run(ctx, RunRequest{SessionID: sid, Prompt: "reply from another session", Injected: true, Hops: 1})
	assert.NoErr(t, err)
	_, err = runner.Run(ctx, RunRequest{SessionID: sid, Prompt: "nudge prompt", Injected: true})
	assert.NoErr(t, err)
	last, err := runner.Run(ctx, RunRequest{SessionID: sid, Prompt: "typed again"})
	assert.NoErr(t, err)
	got, err := runner.userTurns(ctx, sid)
	assert.NoErr(t, err)
	assert.Eq(t, []string{first.TurnID, last.TurnID}, userTurnIDs(got))
}

func TestCutFor(t *testing.T) {
	f := newMaskFixture(t)
	f.mixedSession("s")
	got, err := f.runner.userTurns(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Require(t, assert.Eq(t, 3, len(got)))
	// u2's first entry is its context_update, not the request.
	refs, err := f.repo.ListEntryRefs(f.ctx, "s")
	assert.NoErr(t, err)
	var firstU2 int64
	for _, r := range refs {
		if r.TurnID == "u2" {
			firstU2 = r.Seq
			break
		}
	}
	assert.Eq(t, firstU2-1, cutFor(got[1]))
	assert.Eq(t, int64(0), cutFor(got[0])) // the first turn: cut before everything
}

func TestResolveTarget(t *testing.T) {
	f := newMaskFixture(t)
	f.mixedSession("s")
	f.threeTurns("other")
	f.rewind("other", 2) // masks other/t2, t3

	t.Run("ordinal and turn id", func(t *testing.T) {
		byNum, err := f.runner.resolveTarget(f.ctx, "s", "2")
		assert.NoErr(t, err)
		assert.Eq(t, "u2", byNum.Turn.ID)
		byID, err := f.runner.resolveTarget(f.ctx, "s", "u3")
		assert.NoErr(t, err)
		assert.Eq(t, 3, byID.Ordinal)
	})
	t.Run("ordinal out of range", func(t *testing.T) {
		for _, arg := range []string{"0", "4", "-1", "", "  "} {
			_, err := f.runner.resolveTarget(f.ctx, "s", arg)
			assert.True(t, errors.Is(err, ErrRewindTargetNotFound), arg)
		}
	})
	t.Run("turn id of another session", func(t *testing.T) {
		_, err := f.runner.resolveTarget(f.ctx, "s", "t1") // a real, unmasked user turn of "other"
		assert.True(t, errors.Is(err, ErrRewindTargetNotFound))
		_, err = f.runner.resolveTarget(f.ctx, "other", "u1")
		assert.True(t, errors.Is(err, ErrRewindTargetNotFound))
	})
	t.Run("masked turn", func(t *testing.T) {
		_, err := f.runner.resolveTarget(f.ctx, "other", "t2")
		assert.True(t, errors.Is(err, ErrRewindTargetMasked))
		_, err = f.runner.resolveTarget(f.ctx, "other", "3") // ordinals count visible user turns only
		assert.True(t, errors.Is(err, ErrRewindTargetNotFound))
	})
	t.Run("turn that is not a user turn", func(t *testing.T) {
		for _, id := range []string{"inj1", "nudge", "empty", "steerTurn"} {
			_, err := f.runner.resolveTarget(f.ctx, "s", id)
			assert.True(t, errors.Is(err, ErrRewindTargetNotUserTurn), id)
		}
	})
}

func TestRewindPreconditions(t *testing.T) {
	setup := func(t *testing.T) (*maskFixture, *fakeNotices) {
		f := newMaskFixture(t)
		f.threeTurns("s")
		notices := &fakeNotices{}
		f.runner.cfg.Tasks = notices
		return f, notices
	}
	running := tasks.Info{ID: "t1", Status: tasks.Running}
	ended := tasks.Info{ID: "t2", Status: tasks.Exited}

	t.Run("idle session passes", func(t *testing.T) {
		f, notices := setup(t)
		notices.setTasks(ended) // an ended task does not refuse
		assert.NoErr(t, f.runner.rewindPreconditions(f.ctx, "s"))
	})
	t.Run("archived session", func(t *testing.T) {
		f, _ := setup(t)
		sess, err := f.repo.GetSession(f.ctx, "s")
		assert.NoErr(t, err)
		sess.Status = store.SessionArchived
		assert.NoErr(t, f.repo.UpdateSession(f.ctx, sess))
		err = f.runner.rewindPreconditions(f.ctx, "s")
		assert.True(t, errors.Is(err, ErrRewindSessionArchived))
	})
	t.Run("running background task", func(t *testing.T) {
		f, notices := setup(t)
		notices.setTasks(ended, running)
		err := f.runner.rewindPreconditions(f.ctx, "s")
		assert.True(t, errors.Is(err, ErrRewindTasksRunning))
	})
	t.Run("no task manager means no tasks", func(t *testing.T) {
		f, _ := setup(t)
		f.runner.cfg.Tasks = nil
		assert.NoErr(t, f.runner.rewindPreconditions(f.ctx, "s"))
	})
	t.Run("unknown session", func(t *testing.T) {
		f, _ := setup(t)
		err := f.runner.rewindPreconditions(f.ctx, "nope")
		assert.True(t, errors.Is(err, store.ErrSessionNotFound))
	})
}
