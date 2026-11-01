package personal

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

// richSession is the one session shape the rewind e2e work needs and the other
// fixtures lack (plan T0): several turns plus a manual compaction, a /clear
// record, a steer, an undo notice and an interrupted turn. It builds on the
// maskFixture helpers (turn/msg/meta/add) and adds nothing they already do.
//
//	1  t1 user "request one"            2 t1 assistant
//	3  t2 user "request two"            4 t2 steer "steer text"   5 t2 assistant
//	6  compaction manual, keeps from 3
//	7  t3 undo notice (injected)        8 t3 user "request three"  9 t3 assistant
//	10 compaction clear (segment boundary, keeps from itself)
//	11 t4 user "request four"          12 t4 interrupted note (turn status cancelled)
//	13 t5 user "request five"          14 t5 assistant
func (f *maskFixture) richSession(sid string) {
	f.session(sid)
	f.turn(sid, "t1", store.TurnCompleted)
	f.msg(sid, "t1", "user", "request one")
	f.msg(sid, "t1", "assistant", "answer one")
	f.turn(sid, "t2", store.TurnCompleted)
	f.msg(sid, "t2", "user", "request two")
	f.meta(sid, "t2", "steer text", metaSteer)
	f.msg(sid, "t2", "assistant", "answer two")
	f.add(sid, "", store.EntryCompaction, compactionRecord{Summary: "SUM", FirstKeptSeq: 3, Reason: "manual"})
	f.turn(sid, "t3", store.TurnCompleted)
	f.meta(sid, "t3", "undo happened", metaInjected, metaUndo)
	f.msg(sid, "t3", "user", "request three")
	f.msg(sid, "t3", "assistant", "answer three")
	f.add(sid, "", store.EntryCompaction, compactionRecord{Reason: segmentReasonClear, FirstKeptSeq: 10})
	f.turn(sid, "t4", store.TurnCancelled)
	f.msg(sid, "t4", "user", "request four")
	f.add(sid, "t4", store.EntryUser, agentcore.Message{Role: "user", Content: "interrupted",
		Metadata: map[string]any{metaTurnInterrupted: true, metaInjected: true}})
	f.turn(sid, "t5", store.TurnCompleted)
	f.msg(sid, "t5", "user", "request five")
	f.msg(sid, "t5", "assistant", "answer five")
}

func TestRichSessionFixture(t *testing.T) {
	f := newMaskFixture(t)
	f.richSession("s")
	es, err := f.repo.ListEntries(f.ctx, "s", store.EntryFilter{})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, 14, len(es))
	kinds := map[string]int{}
	for _, e := range es {
		kinds[e.Type]++
	}
	assert.Eq(t, 2, kinds[store.EntryCompaction])
	// The clear record starts a new segment: only the turns after it are visible.
	v, err := f.runner.loadHistory(f.ctx, "s")
	assert.NoErr(t, err)
	assert.Eq(t, []int64{11, 12, 13, 14}, itemSeqs(v))
}
