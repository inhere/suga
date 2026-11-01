package personal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// rewindFix is a Runner over the file-rewind fixture: every turn has a typed
// request, so the turns are valid rewind targets.
type rewindFix struct {
	*rewindFilesFixture
	r     *Runner
	tasks *fakeNotices
}

func newRewindFix(t *testing.T) *rewindFix {
	t.Helper()
	base := newRewindFilesFixture(t)
	tk := &fakeNotices{}
	r, err := New(Config{Repository: base.repo, Provider: mock.NewProvider(), Model: "m", ProviderName: "p", Tasks: tk})
	assert.Require(t, assert.NoErr(t, err))
	return &rewindFix{rewindFilesFixture: base, r: r, tasks: tk}
}

// userTurn creates a turn with a typed request and an answer.
func (f *rewindFix) userTurn(t *testing.T, text string) string {
	t.Helper()
	id := f.turn(t)
	for _, m := range []agentcore.Message{{Role: "user", Content: text}, {Role: "assistant", Content: "ok " + text}} {
		raw, err := json.Marshal(m)
		assert.Require(t, assert.NoErr(t, err))
		typ := store.EntryUser
		if m.Role == "assistant" {
			typ = store.EntryAssistant
		}
		_, err = f.repo.AppendEntries(f.ctx, f.sid, store.NewEntry{TurnID: id, Type: typ, JSON: raw})
		assert.Require(t, assert.NoErr(t, err))
	}
	return id
}

// threeWrites: a.txt zero -> one (t1) -> two (t2, also new b.txt) -> three (t3).
func (f *rewindFix) threeWrites(t *testing.T) (t1, t2, t3 string) {
	t.Helper()
	assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("zero"), 0o640))
	t1, t2, t3 = f.userTurn(t, "req one"), f.userTurn(t, "req two"), f.userTurn(t, "req three")
	f.write(t, t1, "a.txt", "one")
	f.write(t, t2, "a.txt", "two")
	f.write(t, t2, "b.txt", "new")
	f.write(t, t3, "a.txt", "three")
	return
}

func (f *rewindFix) plan(t *testing.T, target string, scope RewindScope) RewindPlan {
	t.Helper()
	p, err := f.r.RewindPlan(f.ctx, f.snapshots, t.TempDir(), f.sid, target, scope)
	assert.Require(t, assert.NoErr(t, err))
	return p
}

func (f *rewindFix) apply(t *testing.T, p RewindPlan) (RewindResult, error) {
	return f.r.ApplyRewind(f.ctx, f.snapshots, t.TempDir(), p)
}

func (f *rewindFix) queue(t *testing.T) []pendingUndoNotice {
	t.Helper()
	s, err := f.repo.GetSession(f.ctx, f.sid)
	assert.Require(t, assert.NoErr(t, err))
	q, err := decodeUndoNotices(s.Meta[undoNoticeQueueKey])
	assert.Require(t, assert.NoErr(t, err))
	return q
}

func (f *rewindFix) records(t *testing.T) []store.RewindRecord {
	t.Helper()
	recs, err := f.repo.RewindRecords(f.ctx, f.sid)
	assert.Require(t, assert.NoErr(t, err))
	return recs
}

// deliver injects the next queued notice into a fresh turn like Run does.
func (f *rewindFix) deliver(t *testing.T) (string, bool) {
	t.Helper()
	id := f.turn(t)
	item, ok, err := f.r.injectUndoNotice(f.ctx, f.sid, id)
	assert.Require(t, assert.NoErr(t, err))
	if !ok {
		return "", false
	}
	return item.msg.Content, true
}

func (f *rewindFix) failInserts(t *testing.T, name, where string) {
	t.Helper()
	_, err := f.repo.DB().Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON entries WHEN %s BEGIN SELECT RAISE(ABORT, 'injected failure'); END`, name, where))
	assert.Require(t, assert.NoErr(t, err))
}

func (f *rewindFix) dropTrigger(t *testing.T, name string) {
	t.Helper()
	_, err := f.repo.DB().Exec(`DROP TRIGGER ` + name)
	assert.NoErr(t, err)
}

func TestRewindScopes(t *testing.T) {
	t.Run("both restores files, writes the record, resets fold and prefix, queues the facts", func(t *testing.T) {
		f := newRewindFix(t)
		_, t2, _ := f.threeWrites(t)
		assert.NoErr(t, f.repo.PatchSessionMeta(f.ctx, f.sid, map[string]any{metaFoldBoundary: 7, metaFoldActive: true, "loaded_tools": []string{"x"}}))
		f.r.rec.prefix.requests = map[string]*agentcore.GenerateRequest{f.sid: {}}
		f.tasks.set(tasks.Info{ID: "tk1"})

		p := f.plan(t, "2", RewindScopeBoth)
		assert.Eq(t, t2, p.TargetTurn)
		assert.Len(t, p.Files, 3)
		res, err := f.apply(t, p)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.Conversation)
		assert.Eq(t, "one", f.read(t, "a.txt"))
		_, statErr := os.Stat(filepath.Join(f.root, "b.txt"))
		assert.True(t, os.IsNotExist(statErr))

		recs := f.records(t)
		assert.Require(t, assert.Len(t, recs, 1))
		assert.Eq(t, t2, recs[0].TargetTurn)
		assert.Eq(t, p.CutSeq, recs[0].CutSeq)
		assert.Eq(t, "both", recs[0].Scope)

		s, _ := f.repo.GetSession(f.ctx, f.sid)
		assert.Eq(t, float64(0), s.Meta[metaFoldBoundary])
		assert.Eq(t, false, s.Meta[metaFoldActive])
		assert.NotNil(t, s.Meta["loaded_tools"])
		assert.Nil(t, f.r.rec.prefix.requests[f.sid])
		assert.Len(t, res.DroppedTasks, 1)
		assert.Len(t, f.tasks.pending(), 0)

		q := f.queue(t)
		assert.Require(t, assert.Len(t, q, 1))
		assert.Eq(t, noticeKindRewind, q[0].Kind)
		assert.Eq(t, res.Notice, q[0].Text)
		// F45: the restored and deleted facts are in the text, with paths, and
		// nothing says the disk was not rolled back.
		assert.StrContains(t, q[0].Text, "已恢复 2 个文件")
		assert.StrContains(t, q[0].Text, filepath.Join(f.root, "a.txt"))
		assert.StrContains(t, q[0].Text, "已删除 1 个新建文件")
		assert.StrContains(t, q[0].Text, filepath.Join(f.root, "b.txt"))
		assert.StrContains(t, q[0].Text, "第 2 轮")
		assert.StrContains(t, q[0].Text, "1 条已结束但未送达")
		assert.False(t, strings.Contains(q[0].Text, "磁盘文件没有回退"))
	})

	t.Run("conversation leaves the files and says so", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		res, err := f.apply(t, f.plan(t, "2", RewindScopeConversation))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.Conversation)
		assert.Len(t, res.Files, 0)
		assert.Eq(t, "three", f.read(t, "a.txt"))
		assert.StrContains(t, res.Notice, "磁盘文件没有回退")
		assert.Len(t, f.records(t), 1)
	})

	t.Run("files scope writes no record, keeps meta, tasks and the next request history", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		assert.NoErr(t, f.repo.PatchSessionMeta(f.ctx, f.sid, map[string]any{metaFoldBoundary: 7, metaFoldActive: true}))
		f.r.rec.prefix.requests = map[string]*agentcore.GenerateRequest{f.sid: {}}
		f.tasks.set(tasks.Info{ID: "tk1"})
		before, err := f.r.loadHistory(f.ctx, f.sid)
		assert.Require(t, assert.NoErr(t, err))

		res, err := f.apply(t, f.plan(t, "2", RewindScopeFiles))
		assert.Require(t, assert.NoErr(t, err))
		assert.False(t, res.Conversation)
		assert.Eq(t, "one", f.read(t, "a.txt"))
		assert.Len(t, f.records(t), 0)
		s, _ := f.repo.GetSession(f.ctx, f.sid)
		assert.Eq(t, float64(7), s.Meta[metaFoldBoundary])
		assert.Eq(t, true, s.Meta[metaFoldActive])
		assert.NotNil(t, f.r.rec.prefix.requests[f.sid])
		assert.Len(t, f.tasks.pending(), 1)
		assert.Len(t, res.DroppedTasks, 0)
		after, err := f.r.loadHistory(f.ctx, f.sid)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, itemSeqs(before), itemSeqs(after))

		// An :undo-style notice: kind empty, EventUndo, the existing wording.
		q := f.queue(t)
		assert.Require(t, assert.Len(t, q, 1))
		assert.Eq(t, "", q[0].Kind)
		assert.StrContains(t, q[0].Text, "已恢复 2 个文件")
		assert.Len(t, eventsOf(t, f.repo, f.sid, store.EventUndo), 1)
		assert.Len(t, eventsOf(t, f.repo, f.sid, store.EventSession), 0)
	})

	t.Run("an unknown scope is refused", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		_, err := f.r.RewindPlan(f.ctx, f.snapshots, t.TempDir(), f.sid, "1", "everything")
		assert.Err(t, err)
	})
}

func TestRewindStale(t *testing.T) {
	cases := map[string]func(t *testing.T, f *rewindFix, t3 string){
		"an entry appended after the preview": func(t *testing.T, f *rewindFix, _ string) {
			raw, _ := json.Marshal(agentcore.Message{Role: "user", Content: "late"})
			_, err := f.repo.AppendEntries(f.ctx, f.sid, store.NewEntry{TurnID: "t3", Type: store.EntryUser, JSON: raw})
			assert.NoErr(t, err)
		},
		"a snapshot directory removed after the preview": func(t *testing.T, f *rewindFix, t3 string) {
			assert.NoErr(t, os.RemoveAll(filepath.Join(f.snapshots.dir, f.sid, "snapshots", t3)))
		},
		"the cwd changed after the preview": func(t *testing.T, f *rewindFix, _ string) {
			s, err := f.repo.GetSession(f.ctx, f.sid)
			assert.Require(t, assert.NoErr(t, err))
			s.Cwd = t.TempDir()
			assert.NoErr(t, f.repo.UpdateSession(f.ctx, s))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRewindFix(t)
			_, _, t3 := f.threeWrites(t)
			p := f.plan(t, "2", RewindScopeBoth)
			mutate(t, f, t3)
			res, err := f.apply(t, p)
			assert.True(t, errors.Is(err, ErrRewindStale))
			assert.Len(t, res.Files, 0)
			assert.Eq(t, "three", f.read(t, "a.txt"))
			assert.Len(t, f.records(t), 0)
		})
	}
}

func TestRewindBusy(t *testing.T) {
	t.Run("a running turn returns busy without waiting", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		p := f.plan(t, "2", RewindScopeBoth)
		release, err := f.r.gate(f.sid).Acquire(f.ctx)
		assert.Require(t, assert.NoErr(t, err))
		defer release()
		start := time.Now()
		_, err = f.apply(t, p)
		assert.True(t, errors.Is(err, ErrRewindBusy))
		assert.True(t, time.Since(start) < time.Second)
		assert.Eq(t, "three", f.read(t, "a.txt"))
	})
	t.Run("an idle consolidation is preempted and the rewind succeeds", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		p := f.plan(t, "2", RewindScopeBoth)
		held, preempted, err := f.r.gate(f.sid).AcquirePreemptible(f.ctx)
		assert.Require(t, assert.NoErr(t, err))
		go func() { <-preempted; held() }()
		res, err := f.apply(t, p)
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.Conversation)
	})
	t.Run("a running background task refuses", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		p := f.plan(t, "2", RewindScopeBoth)
		f.tasks.setTasks(tasks.Info{ID: "run", Status: tasks.Running})
		_, err := f.apply(t, p)
		assert.True(t, errors.Is(err, ErrRewindTasksRunning))
		assert.Len(t, f.records(t), 0)
	})
}

func TestRewindNoticeQueue(t *testing.T) {
	t.Run("undo then rewind: rewind first, then the undo, event ids filled", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		_, err := f.r.UndoCore(f.snapshots, t.TempDir()).Undo(f.ctx, f.sid, 1, false) // undoes t3
		assert.Require(t, assert.NoErr(t, err))
		res, err := f.apply(t, f.plan(t, "2", RewindScopeBoth))
		assert.Require(t, assert.NoErr(t, err))
		q := f.queue(t)
		assert.Require(t, assert.Len(t, q, 2))
		assert.Eq(t, noticeKindRewind, q[0].Kind)
		assert.Eq(t, "", q[1].Kind)

		first, ok := f.deliver(t)
		assert.True(t, ok)
		assert.Eq(t, res.Notice, first)
		second, ok := f.deliver(t)
		assert.True(t, ok)
		assert.StrContains(t, second, "回滚了文件改动")
		_, ok = f.deliver(t)
		assert.False(t, ok)

		for _, e := range append(eventsOf(t, f.repo, f.sid, store.EventUndo), eventsOf(t, f.repo, f.sid, store.EventSession)...) {
			assert.Gt(t, e.Attrs["entry_seq"].(float64), float64(0))
		}
	})

	t.Run("two rewinds in a row: one injection with both, in order, earlier event not filled", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		first, err := f.apply(t, f.plan(t, "3", RewindScopeBoth)) // restores t3's a.txt
		assert.Require(t, assert.NoErr(t, err))
		second, err := f.apply(t, f.plan(t, "2", RewindScopeConversation))
		assert.Require(t, assert.NoErr(t, err))
		q := f.queue(t)
		assert.Require(t, assert.Len(t, q, 1))
		assert.Eq(t, second.Notice, q[0].Text)
		i, j := strings.Index(q[0].Text, "先前一次回退："), strings.Index(q[0].Text, "本次回退：")
		assert.True(t, i == 0 && j > i)
		assert.True(t, strings.Contains(q[0].Text, first.Notice))
		assert.True(t, strings.Index(q[0].Text, first.Notice) < j)
		assert.StrContains(t, q[0].Text, "已恢复 1 个文件") // the first operation's fact is visible
		assert.StrContains(t, q[0].Text, "磁盘文件没有回退")
		assert.Eq(t, 0, q[0].Restored) // counts are the newest item's only

		text, ok := f.deliver(t)
		assert.True(t, ok)
		assert.Eq(t, q[0].Text, text)
		_, ok = f.deliver(t)
		assert.False(t, ok)
		evs := eventsOf(t, f.repo, f.sid, store.EventSession)
		assert.Require(t, assert.Len(t, evs, 2))
		assert.Eq(t, float64(0), evs[0].Attrs["entry_seq"])
		assert.Gt(t, evs[1].Attrs["entry_seq"].(float64), float64(0))
	})

	t.Run("an injection failure puts a rewind notice back at the head", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		_, err := f.r.UndoCore(f.snapshots, t.TempDir()).Undo(f.ctx, f.sid, 1, false)
		assert.Require(t, assert.NoErr(t, err))
		_, err = f.apply(t, f.plan(t, "2", RewindScopeBoth))
		assert.Require(t, assert.NoErr(t, err))

		f.failInserts(t, "no_user", "NEW.type = 'user'")
		id := f.turn(t)
		_, ok, err := f.r.injectUndoNotice(f.ctx, f.sid, id)
		assert.Err(t, err)
		assert.False(t, ok)
		q := f.queue(t)
		assert.Require(t, assert.Len(t, q, 2))
		assert.Eq(t, noticeKindRewind, q[0].Kind)
		f.dropTrigger(t, "no_user")
		text, ok := f.deliver(t)
		assert.True(t, ok)
		assert.StrContains(t, text, "第 2 轮")
	})

	t.Run("an undo notice that fails injection still goes to the tail", func(t *testing.T) {
		f := newRewindFix(t)
		f.userTurn(t, "x")
		assert.NoErr(t, appendUndoNotice(f.ctx, f.repo, f.sid, pendingUndoNotice{Text: "u1"}))
		assert.NoErr(t, appendUndoNotice(f.ctx, f.repo, f.sid, pendingUndoNotice{Text: "u2"}))
		f.failInserts(t, "no_user", "NEW.type = 'user'")
		_, _, err := f.r.injectUndoNotice(f.ctx, f.sid, f.turn(t))
		assert.Err(t, err)
		q := f.queue(t)
		assert.Eq(t, []string{"u2", "u1"}, []string{q[0].Text, q[1].Text})
	})

	t.Run("the pending undo notices are kept, not dropped", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		assert.NoErr(t, appendUndoNotice(f.ctx, f.repo, f.sid, pendingUndoNotice{Text: "keep me", EventID: 0}))
		_, err := f.apply(t, f.plan(t, "2", RewindScopeConversation))
		assert.Require(t, assert.NoErr(t, err))
		q := f.queue(t)
		assert.Require(t, assert.Len(t, q, 2))
		assert.Eq(t, "keep me", q[1].Text)
	})
}

func TestRewindMergeTexts(t *testing.T) {
	a := mergeRewindTexts(nil, []string{"[one]"}, "[two]")
	assert.Eq(t, "先前一次回退：\n[one]\n\n本次回退：\n[two]", a)
	b := mergeRewindTexts(nil, []string{a}, "[three]")
	assert.Eq(t, "先前一次回退：\n[one]\n\n先前一次回退：\n[two]\n\n本次回退：\n[three]", b)
	assert.Eq(t, "[solo]", mergeRewindTexts(nil, nil, "[solo]"))
}

func TestRewindFilesOnlyOutcomes(t *testing.T) {
	t.Run("unrecoverable files are counted and noted like an undo", func(t *testing.T) {
		f := newRewindFix(t)
		t1 := f.userTurn(t, "req one")
		other := t.TempDir()
		f.writeAt(t, t1, other, "a.txt", "one")
		f.write(t, t1, "ok.txt", "x")
		res, err := f.apply(t, f.plan(t, "1", RewindScopeFiles))
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, res.Notice, "1 个文件无法恢复")
		evs := eventsOf(t, f.repo, f.sid, store.EventUndo)
		assert.Require(t, assert.Len(t, evs, 1))
		assert.Eq(t, float64(1), evs[0].Attrs["unrecoverable"])
		assert.Eq(t, float64(1), evs[0].Attrs["deleted"])
	})
	t.Run("all conflicts still count the turn, queue the notice and write EventUndo", func(t *testing.T) {
		f := newRewindFix(t)
		t1 := f.userTurn(t, "req one")
		f.write(t, t1, "a.txt", "one")
		assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("someone else"), 0o640))
		res, err := f.apply(t, f.plan(t, "1", RewindScopeFiles))
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, res.Files, 1)
		assert.Eq(t, RewindFileConflict, res.Files[0].Action)
		assert.StrContains(t, res.Notice, "跳过 1 个冲突")
		evs := eventsOf(t, f.repo, f.sid, store.EventUndo)
		assert.Require(t, assert.Len(t, evs, 1))
		assert.Eq(t, float64(1), evs[0].Attrs["turns"])
		assert.Eq(t, float64(1), evs[0].Attrs["skipped"])
		assert.Len(t, f.queue(t), 1)
	})
	t.Run("no snapshot at all: one session event, nothing queued", func(t *testing.T) {
		f := newRewindFix(t)
		f.userTurn(t, "req one")
		res, err := f.apply(t, f.plan(t, "1", RewindScopeFiles))
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "", res.Notice)
		assert.Len(t, f.queue(t), 0)
		assert.Len(t, eventsOf(t, f.repo, f.sid, store.EventUndo), 0)
		evs := eventsOf(t, f.repo, f.sid, store.EventSession)
		assert.Require(t, assert.Len(t, evs, 1))
		assert.Eq(t, "rewind", evs[0].Name)
		assert.Eq(t, "files", evs[0].Attrs["scope"])
	})
}

func TestRewindFailureBranches(t *testing.T) {
	t.Run("file part errors: the conversation part does not run, restored files are noted", func(t *testing.T) {
		f := newRewindFix(t)
		t1, t2, _ := f.threeWrites(t)
		_ = t1
		// Remove the snapshot blob of t2's b.txt step (the plan only reads
		// meta): restoring it fails after t3 was already rolled back.
		var removed bool
		_ = filepath.WalkDir(filepath.Join(f.snapshots.dir, f.sid, "snapshots", t2), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "before" && !removed {
				removed = true
				return os.Remove(path)
			}
			return nil
		})
		p := f.plan(t, "2", RewindScopeBoth)
		res, err := f.apply(t, p)
		if !removed {
			t.Skip("no before blob to remove")
		}
		assert.Err(t, err)
		assert.False(t, res.Conversation)
		assert.Len(t, f.records(t), 0)
		assert.NotEmpty(t, res.Files)
		q := f.queue(t)
		assert.Require(t, assert.Len(t, q, 1))
		assert.Eq(t, "", q[0].Kind) // the :undo-style note of the restored files
	})
	t.Run("skipped files do not stop the conversation part", func(t *testing.T) {
		f := newRewindFix(t)
		t1 := f.userTurn(t, "req one")
		f.write(t, t1, "a.txt", "one")
		assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "a.txt"), []byte("someone else"), 0o640))
		res, err := f.apply(t, f.plan(t, "1", RewindScopeBoth))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.Conversation)
		assert.StrContains(t, res.Notice, "跳过 1 个冲突")
	})
	t.Run("record write fails after files were restored", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		p := f.plan(t, "2", RewindScopeBoth)
		f.failInserts(t, "no_rewind", "NEW.type = 'rewind'")
		res, err := f.apply(t, p)
		assert.Err(t, err)
		assert.False(t, res.Conversation)
		assert.Eq(t, "one", f.read(t, "a.txt"))
		q := f.queue(t)
		assert.Require(t, assert.Len(t, q, 1))
		assert.Eq(t, "", q[0].Kind)
		assert.StrContains(t, q[0].Text, "已恢复 2 个文件")
		assert.Len(t, f.records(t), 0)
	})
	t.Run("meta reset fails after the record: reported by Warn, the rewind stands", func(t *testing.T) {
		f := newRewindFix(t)
		f.threeWrites(t)
		assert.NoErr(t, f.repo.PatchSessionMeta(f.ctx, f.sid, map[string]any{metaFoldBoundary: 7}))
		_, err := f.repo.DB().Exec(`CREATE TRIGGER no_meta BEFORE UPDATE ON sessions WHEN NEW.meta_json LIKE '%"fold_boundary":0%' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
		assert.Require(t, assert.NoErr(t, err))
		res, err := f.apply(t, f.plan(t, "2", RewindScopeBoth))
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, res.Conversation)
		assert.Len(t, f.records(t), 1)
		assert.Len(t, f.queue(t), 1)
	})
}

func TestRewindEventAttrs(t *testing.T) {
	f := newRewindFix(t)
	_, t2, _ := f.threeWrites(t)
	_, err := f.apply(t, f.plan(t, "2", RewindScopeBoth))
	assert.Require(t, assert.NoErr(t, err))
	evs := eventsOf(t, f.repo, f.sid, store.EventSession)
	assert.Require(t, assert.Len(t, evs, 1))
	e := evs[0]
	assert.Eq(t, "rewind", e.Name)
	assert.Eq(t, t2, e.Attrs["target_turn"])
	assert.Eq(t, "both", e.Attrs["scope"])
	assert.Eq(t, float64(2), e.Attrs["restored"])
	assert.Eq(t, float64(1), e.Attrs["deleted"])
	assert.Eq(t, float64(0), e.Attrs["entry_seq"])
	assert.Len(t, eventsOf(t, f.repo, f.sid, store.EventUndo), 0)
}

func TestRewindNoticeEnglish(t *testing.T) {
	tr, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	st := rewindFileStat{restored: []string{"/r/a"}, deleted: []string{"/r/b"}}
	text := rewindNoticeText(tr, RewindScopeBoth, 2, st, 1)
	assert.StrContains(t, text, "1 files restored: /r/a\n1 newly created files deleted: /r/b")
	assert.StrContains(t, text, "before turn 2")
	assert.False(t, strings.Contains(text, "were not rewound"))
	merged := mergeRewindTexts(tr, []string{"[one]"}, "[two]")
	assert.Eq(t, "Earlier rewind:\n[one]\n\nThis rewind:\n[two]", merged)
}
