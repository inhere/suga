package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// forkFixture is a maskFixture whose store and runner share a sessions root, so
// attachments have a place to live.
type forkFixture struct {
	*maskFixture
	root string
}

func newForkFixture(t *testing.T) *forkFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sessions")
	repo, err := store.Open(newStorePath(t), store.WithSessionsDir(root))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(), Model: "m", ProviderName: "p", SessionsDir: root})
	assert.Require(t, assert.NoErr(t, err))
	return &forkFixture{maskFixture: &maskFixture{t: t, ctx: context.Background(), repo: repo, runner: runner}, root: root}
}

func (f *forkFixture) fork(sid, target string) (ForkResult, error) {
	return f.runner.Fork(f.ctx, nil, f.root, sid, target)
}

func (f *forkFixture) mustFork(sid, target string) ForkResult {
	f.t.Helper()
	res, err := f.fork(sid, target)
	assert.Require(f.t, assert.NoErr(f.t, err))
	return res
}

// start writes the segment start record every real session begins with.
func (f *forkFixture) start(sid string) int64 {
	return f.add(sid, "", store.EntryCompaction, compactionRecord{Reason: segmentReasonStart, Context: &segmentContext{Rules: "R"}})
}

// view is a history projection reduced to what a model would be sent.
func projection(t *testing.T, f *forkFixture, sid string) []string {
	t.Helper()
	v, err := f.runner.loadHistory(f.ctx, sid)
	assert.Require(t, assert.NoErr(t, err))
	out := []string{"summary:" + v.summary, fmt.Sprintf("segment:%v", v.segment != nil)}
	for _, it := range v.items {
		out = append(out, fmt.Sprintf("%s:%s", it.msg.Role, it.msg.Content))
	}
	return out
}

// rewindAt is the control of the comparison (plan F13): the rewind record a
// conversation rewind to the target would write, appended to the source.
func (f *forkFixture) rewindAt(sid, target string) {
	f.t.Helper()
	tt, err := f.runner.resolveTarget(f.ctx, sid, target)
	assert.Require(f.t, assert.NoErr(f.t, err))
	f.rewind(sid, cutFor(tt))
}

func (f *forkFixture) entriesOf(sid string) []store.Entry {
	f.t.Helper()
	es, err := f.repo.ListEntries(f.ctx, sid, store.EntryFilter{})
	assert.Require(f.t, assert.NoErr(f.t, err))
	return es
}

// base is: start(1), t1(2,3), t2(4,5), t3(6,7) rewound away by a rewind at 8,
// t4(9,10), manual compaction keeping from 9 (11), t5(12,13).
func (f *forkFixture) base(sid string) {
	f.session(sid)
	f.start(sid)
	for i, id := range []string{"t1", "t2"} {
		f.turn(sid, id, store.TurnCompleted)
		f.msg(sid, id, "user", fmt.Sprintf("request %d", i+1))
		f.msg(sid, id, "assistant", fmt.Sprintf("answer %d", i+1))
	}
	f.turn(sid, "t3", store.TurnCompleted)
	f.msg(sid, "t3", "user", "request rewound")
	f.msg(sid, "t3", "assistant", "answer rewound")
	f.rewind(sid, 5)
	f.turn(sid, "t4", store.TurnCompleted)
	f.msg(sid, "t4", "user", "request 4")
	f.msg(sid, "t4", "assistant", "answer 4")
	f.add(sid, "", store.EntryCompaction, compactionRecord{Summary: "SUM", FirstKeptSeq: 9, Reason: "manual"})
	f.turn(sid, "t5", store.TurnCompleted)
	f.msg(sid, "t5", "user", "request 5")
	f.msg(sid, "t5", "assistant", "answer 5")
}

func TestForkProjection(t *testing.T) {
	t.Run("equals the source rewound to the same cut", func(t *testing.T) {
		for _, target := range []string{"1", "2", "3", "4"} {
			f := newForkFixture(t)
			f.base("s")
			res := f.mustFork("s", target)
			got := projection(t, f, res.SessionID)
			f.rewindAt("s", target)
			assert.Eq(t, projection(t, f, "s"), got, "target "+target)
		}
	})
	t.Run("compaction before the cut: first_kept_seq follows the renumbering", func(t *testing.T) {
		f := newForkFixture(t)
		f.base("s")
		res := f.mustFork("s", "4") // cut 11: the compaction is the last copied entry
		es := f.entriesOf(res.SessionID)
		var rec compactionRecord
		assert.NoErr(t, json.Unmarshal(es[len(es)-1].JSON, &rec))
		// 1..5 kept, 6,7 and the rewind record dropped, 9,10,11 -> 6,7,8.
		assert.Eq(t, int64(6), rec.FirstKeptSeq)
		assert.Eq(t, "SUM", rec.Summary)
		assert.Eq(t, []string{"summary:SUM", "segment:false", "user:request 4", "assistant:answer 4"}, projection(t, f, res.SessionID))
	})
	t.Run("compaction across the cut is not copied", func(t *testing.T) {
		f := newForkFixture(t)
		f.base("s")
		res := f.mustFork("s", "3") // cut 8: t4 is the target, the compaction (11) is later
		for _, e := range f.entriesOf(res.SessionID) {
			if e.Type == store.EntryCompaction {
				var rec compactionRecord
				assert.NoErr(t, json.Unmarshal(e.JSON, &rec))
				assert.Eq(t, segmentReasonStart, rec.Reason)
			}
		}
		assert.Eq(t, []string{"summary:", "segment:true", "user:request 1", "assistant:answer 1", "user:request 2", "assistant:answer 2"},
			projection(t, f, res.SessionID))
	})
	t.Run("clear record keeps pointing at itself", func(t *testing.T) {
		f := newForkFixture(t)
		f.session("s")
		f.start("s")
		f.turn("s", "t1", store.TurnCompleted)
		f.msg("s", "t1", "user", "request 1")
		f.msg("s", "t1", "assistant", "answer 1") // 3
		f.turn("s", "tx", store.TurnCompleted)
		f.msg("s", "tx", "user", "request x")
		f.msg("s", "tx", "assistant", "answer x") // 5
		f.rewind("s", 3)                          // 6
		f.turn("s", "t2", store.TurnCompleted)
		f.msg("s", "t2", "user", "request 2")
		f.msg("s", "t2", "assistant", "answer 2")                                                            // 8
		f.add("s", "", store.EntryCompaction, compactionRecord{Reason: segmentReasonClear, FirstKeptSeq: 9}) // 9
		f.turn("s", "t3", store.TurnCompleted)
		f.msg("s", "t3", "user", "request 3")
		f.msg("s", "t3", "assistant", "answer 3")
		res := f.mustFork("s", "3")
		es := f.entriesOf(res.SessionID)
		var rec compactionRecord
		assert.NoErr(t, json.Unmarshal(es[len(es)-1].JSON, &rec))
		assert.Eq(t, segmentReasonClear, rec.Reason)
		assert.Eq(t, es[len(es)-1].Seq, rec.FirstKeptSeq)
		got := projection(t, f, res.SessionID)
		f.rewindAt("s", "3")
		assert.Eq(t, projection(t, f, "s"), got)
	})
	t.Run("interrupted turns keep their status and replay", func(t *testing.T) {
		f := newForkFixture(t)
		f.session("s")
		f.start("s")
		f.turn("s", "t1", store.TurnCompleted)
		f.msg("s", "t1", "user", "request 1")
		f.msg("s", "t1", "assistant", "answer 1")
		f.turn("s", "t2", store.TurnFailed)
		assert.NoErr(t, f.repo.UpdateTurn(f.ctx, store.Turn{ID: "t2", Status: store.TurnFailed, StopReason: "boom"}))
		f.msg("s", "t2", "user", "request 2")
		f.add("s", "t2", store.EntryAssistant, agentcore.Message{Role: "assistant", Content: "partial"})
		f.turn("s", "t3", store.TurnCompleted)
		f.msg("s", "t3", "user", "request 3")
		f.msg("s", "t3", "assistant", "answer 3")
		res := f.mustFork("s", "3")
		turns, err := f.repo.ListTurns(f.ctx, res.SessionID)
		assert.NoErr(t, err)
		assert.Eq(t, 2, len(turns))
		assert.Eq(t, store.TurnFailed, turns[1].Status)
		assert.Eq(t, "boom", turns[1].StopReason)
		got := projection(t, f, res.SessionID)
		f.rewindAt("s", "3")
		want := projection(t, f, "s")
		assert.Eq(t, want, got)
		assert.True(t, len(got) > 4, "the failed turn contributes its prefix")
	})
}

// hashSession digests everything a fork must leave alone in the source.
func hashSession(t *testing.T, f *forkFixture, sid string) string {
	t.Helper()
	h := sha256.New()
	for _, e := range f.entriesOf(sid) {
		fmt.Fprintf(h, "%d|%s|%s|%s|%s\n", e.Seq, e.TurnID, e.Type, e.JSON, e.CreatedAt.Format("20060102150405.000000"))
	}
	turns, err := f.repo.ListTurns(f.ctx, sid)
	assert.NoErr(t, err)
	for _, tr := range turns {
		fmt.Fprintf(h, "%s|%s|%s|%s\n", tr.ID, tr.Status, tr.StopReason, tr.StateJSON)
	}
	sess, err := f.repo.GetSession(f.ctx, sid)
	assert.NoErr(t, err)
	meta, _ := json.Marshal(sess.Meta)
	fmt.Fprintf(h, "%s|%s|%s|%s|%s", sess.Title, sess.Cwd, sess.Status, sess.LastTurnID, meta)
	evs, err := f.repo.ListEvents(f.ctx, store.EventFilter{SessionID: sid})
	assert.NoErr(t, err)
	fmt.Fprintf(h, "events:%d", len(evs))
	return hex.EncodeToString(h.Sum(nil))
}

func TestForkLeavesSourceAlone(t *testing.T) {
	f := newForkFixture(t)
	f.base("s")
	before := hashSession(t, f, "s")
	f.mustFork("s", "2")
	assert.Eq(t, before, hashSession(t, f, "s"))
}

func TestForkMetaWhitelist(t *testing.T) {
	f := newForkFixture(t)
	f.base("s")
	sess, err := f.repo.GetSession(f.ctx, "s")
	assert.NoErr(t, err)
	sess.Title, sess.Cwd = "Plan the work", "/work/proj"
	assert.NoErr(t, f.repo.UpdateSessionFields(f.ctx, sess))
	dirs := []any{map[string]any{"path": "/work/other", "mode": "rw"}}
	assert.NoErr(t, f.repo.PatchSessionMeta(f.ctx, "s", map[string]any{
		"add_dirs": dirs, "model": "mm", "provider": "pp", "loaded_tools": []any{"web_fetch"},
		"origin": store.OriginRun, "used_interactively": true, "pinned": true,
		"kind": "agent", "parent_session_id": "p", "short_name": "sn",
		"suga.undo.pending": []any{"x"}, "fold_boundary": 7, "fold_active": true,
		"cache_prefix_print": "px", "personal_rules_content": "R", "personal_rules_hash": "h",
		"personal_append_prompt": "A", "memory_index_content": "M", "memory_index_hash": "mh",
		"suga_version": "v1", "run_mode": "plan", "zz_future_key": 1,
	}))
	res := f.mustFork("s", "2")
	got, err := f.repo.GetSession(f.ctx, res.SessionID)
	assert.NoErr(t, err)
	keys := []string{}
	for k := range got.Meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.Eq(t, []string{"add_dirs", "forked_at_turn", "forked_from", "loaded_tools", "model", "origin", "provider"}, keys)
	assert.Eq(t, dirs, got.Meta["add_dirs"])
	assert.Eq(t, "mm", got.Meta["model"])
	assert.Eq(t, "pp", got.Meta["provider"])
	assert.Eq(t, []any{"web_fetch"}, got.Meta["loaded_tools"])
	assert.Eq(t, store.OriginChat, got.Meta["origin"])
	assert.Eq(t, "s", got.Meta["forked_from"])
	assert.Eq(t, "t2", got.Meta["forked_at_turn"])
	assert.Eq(t, "/work/proj", got.Cwd)
	assert.Eq(t, "Plan the work（分叉）", got.Title)
	assert.Eq(t, store.SessionOpen, got.Status)
	turns, err := f.repo.ListTurns(f.ctx, res.SessionID)
	assert.NoErr(t, err)
	assert.Eq(t, turns[len(turns)-1].ID, got.LastTurnID)
	evs := eventsOf(t, f.repo, res.SessionID, store.EventSession)
	assert.Eq(t, 1, len(evs))
	assert.Eq(t, "fork", evs[0].Name)
}

func TestForkTaint(t *testing.T) {
	// base: entries are written within the same second, so the cut entry's
	// created_at is "now"; stamps are placed around it.
	cases := []struct {
		name, stamp string
		inherit     bool
	}{
		{"tainted before the cut", "2000-01-01T00:00:00Z", true},
		{"tainted after the cut", "2999-01-01T00:00:00Z", false},
		{"unreadable stamp counts as tainted", "garbage", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newForkFixture(t)
			f.base("s")
			assert.NoErr(t, f.repo.PatchSessionMeta(f.ctx, "s", map[string]any{"tainted_at": c.stamp, "tainted_by": "web_fetch"}))
			res := f.mustFork("s", "2")
			got, err := f.repo.GetSession(f.ctx, res.SessionID)
			assert.NoErr(t, err)
			_, has := got.Meta["tainted_at"]
			assert.Eq(t, c.inherit, has)
			if c.inherit {
				assert.Eq(t, c.stamp, got.Meta["tainted_at"])
				assert.Eq(t, "web_fetch", got.Meta["tainted_by"])
			} else {
				assert.NotContains(t, got.Meta, "tainted_by")
			}
		})
	}
	t.Run("an untainted source stays untainted", func(t *testing.T) {
		f := newForkFixture(t)
		f.base("s")
		res := f.mustFork("s", "2")
		got, err := f.repo.GetSession(f.ctx, res.SessionID)
		assert.NoErr(t, err)
		assert.NotContains(t, got.Meta, "tainted_at")
	})
}

const (
	forkSha   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	forkStem  = "0123456789abcdef"
	forkStem2 = "fedcba9876543210"
)

func TestForkAttachments(t *testing.T) {
	f := newForkFixture(t)
	f.base("s")
	assert.NoErr(t, os.MkdirAll(filepath.Join(f.root, "s", "images"), 0o700))
	assert.NoErr(t, os.MkdirAll(filepath.Join(f.root, "s", "outputs"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "s", "images", forkSha+".png"), []byte("PNGDATA"), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "s", "outputs", forkStem+".txt"), []byte("BIG OUTPUT"), 0o600))
	assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "s", "outputs", forkStem2+".txt.gz"), []byte("GZ"), 0o600))
	// An entry after t2 carries an image and a spill ref; the compaction keeps a ref.
	f.turn("s", "t6", store.TurnCompleted)
	f.add("s", "t6", store.EntryUser, map[string]any{"Role": "user", "Content": "see this",
		"Images": []any{map[string]any{"MediaType": "image/png", "Ref": "images/" + forkSha + ".png", "SHA256": forkSha}}})
	f.add("s", "t6", store.EntryAssistant, agentcore.Message{Role: "assistant", Content: "full output: out:s:" + forkStem + " and out:s:" + forkStem2 + " and out:s:aaaaaaaaaaaaaaaa"})
	f.add("s", "", store.EntryCompaction, compactionRecord{Summary: "SUM out:s:" + forkStem, FirstKeptSeq: 1, Refs: []string{"out:s:" + forkStem, "s/3"}, Reason: "manual"})
	f.turn("s", "t7", store.TurnCompleted)
	f.msg("s", "t7", "user", "request 7")
	f.msg("s", "t7", "assistant", "answer 7")

	res := f.mustFork("s", "6")
	id := res.SessionID
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(f.root, id, rel))
		assert.NoErr(t, err)
		return string(b)
	}
	assert.Eq(t, "PNGDATA", read("images/"+forkSha+".png"))
	assert.Eq(t, "BIG OUTPUT", read("outputs/"+forkStem+".txt"))
	assert.Eq(t, "GZ", read("outputs/"+forkStem2+".txt.gz"))
	rc, err := OpenOutput(f.root, "out:"+id+":"+forkStem)
	assert.Require(t, assert.NoErr(t, err))
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Eq(t, "BIG OUTPUT", string(body))

	var texts []string
	for _, e := range f.entriesOf(id) {
		texts = append(texts, string(e.JSON))
	}
	all := strings.Join(texts, "\n")
	assert.StrContains(t, all, "out:"+id+":"+forkStem+" and out:"+id+":"+forkStem2)
	assert.StrContains(t, all, "out:s:aaaaaaaaaaaaaaaa") // not in the source's files: left as it was
	assert.StrContains(t, all, "images/"+forkSha+".png")
	// The compaction record keeps pointing at the source (R6).
	es := f.entriesOf(id)
	var rec compactionRecord
	for _, e := range es {
		if e.Type == store.EntryCompaction && strings.Contains(string(e.JSON), "SUM") {
			assert.NoErr(t, json.Unmarshal(e.JSON, &rec))
		}
	}
	assert.Eq(t, []string{"out:s:" + forkStem, "s/3"}, rec.Refs)
	assert.Eq(t, "SUM out:s:"+forkStem, rec.Summary)
	// The source keeps its own files.
	_, err = os.Stat(filepath.Join(f.root, "s", "outputs", forkStem+".txt"))
	assert.NoErr(t, err)
}

// sessionDirs lists the entries below the sessions root.
func sessionDirs(t *testing.T, root string) []string {
	t.Helper()
	des, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	assert.NoErr(t, err)
	var out []string
	for _, d := range des {
		out = append(out, d.Name())
	}
	return out
}

func TestForkFailureLeavesNothing(t *testing.T) {
	setup := func(t *testing.T) *forkFixture {
		f := newForkFixture(t)
		f.base("s")
		assert.NoErr(t, os.MkdirAll(filepath.Join(f.root, "s", "images"), 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(f.root, "s", "images", forkSha+".png"), []byte("PNG"), 0o600))
		f.turn("s", "t6", store.TurnCompleted)
		f.add("s", "t6", store.EntryUser, map[string]any{"Role": "user", "Content": "see",
			"Images": []any{map[string]any{"Ref": "images/" + forkSha + ".png"}}})
		f.msg("s", "t6", "assistant", "ok")
		f.turn("s", "t7", store.TurnCompleted)
		f.msg("s", "t7", "user", "next")
		return f
	}
	check := func(t *testing.T, f *forkFixture, before string) {
		t.Helper()
		sessions, err := f.repo.ListSessions(f.ctx)
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(sessions))
		assert.Eq(t, []string{"s"}, sessionDirs(t, f.root))
		assert.Eq(t, before, hashSession(t, f, "s"))
	}
	for _, stage := range []string{"files", "created"} {
		t.Run("failure at "+stage, func(t *testing.T) {
			f := setup(t)
			before := hashSession(t, f, "s")
			forkHook = func(s string) error {
				if s == stage {
					return errors.New("injected")
				}
				return nil
			}
			t.Cleanup(func() { forkHook = nil })
			_, err := f.fork("s", "6")
			assert.ErrSubMsg(t, err, "injected")
			check(t, f, before)
		})
	}
	t.Run("attachment copy failure", func(t *testing.T) {
		f := setup(t)
		// The image path is a directory: copying it fails after earlier files may exist.
		assert.NoErr(t, os.Remove(filepath.Join(f.root, "s", "images", forkSha+".png")))
		assert.NoErr(t, os.Mkdir(filepath.Join(f.root, "s", "images", forkSha+".png"), 0o700))
		before := hashSession(t, f, "s")
		_, err := f.fork("s", "6")
		assert.ErrSubMsg(t, err, "not a regular file")
		check(t, f, before)
	})
}

func TestForkRefusals(t *testing.T) {
	t.Run("unknown target", func(t *testing.T) {
		f := newForkFixture(t)
		f.base("s")
		_, err := f.fork("s", "99")
		assert.True(t, errors.Is(err, ErrRewindTargetNotFound))
		sessions, _ := f.repo.ListSessions(f.ctx)
		assert.Eq(t, 1, len(sessions))
	})
	t.Run("rewound away turn", func(t *testing.T) {
		f := newForkFixture(t)
		f.base("s")
		_, err := f.fork("s", "t3")
		assert.True(t, errors.Is(err, ErrRewindTargetMasked))
	})
	t.Run("archived session", func(t *testing.T) {
		f := newForkFixture(t)
		f.base("s")
		sess, err := f.repo.GetSession(f.ctx, "s")
		assert.NoErr(t, err)
		sess.Status = store.SessionArchived
		assert.NoErr(t, f.repo.UpdateSessionFields(f.ctx, sess))
		_, err = f.fork("s", "1")
		assert.True(t, errors.Is(err, ErrRewindSessionArchived))
	})
	t.Run("a running turn", func(t *testing.T) {
		f := newForkFixture(t)
		f.base("s")
		release, err := f.runner.gate("s").Acquire(f.ctx)
		assert.Require(t, assert.NoErr(t, err))
		defer release()
		_, err = f.fork("s", "1")
		assert.True(t, errors.Is(err, ErrRewindBusy))
	})
}

// A fork of a fork carries its origin forward and copies from its own
// numbering.
func TestForkOfFork(t *testing.T) {
	f := newForkFixture(t)
	f.base("s")
	first := f.mustFork("s", "4")
	second := f.mustFork(first.SessionID, "2")
	got, err := f.repo.GetSession(f.ctx, second.SessionID)
	assert.NoErr(t, err)
	assert.Eq(t, first.SessionID, got.Meta["forked_from"])
	// The first fork holds t1, t2, t4; "2" there is t2, so only t1 is left.
	assert.Eq(t, []string{"user:request 1", "assistant:answer 1"}, projection(t, f, second.SessionID)[2:])
}
