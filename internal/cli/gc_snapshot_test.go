package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

func TestGCReportIncludesSnapshotBytes(t *testing.T) {
	var out bytes.Buffer
	writeGCReport(&out, store.GCReport{SnapshotBytes: 17}, time.Unix(0, 0))
	assert.StrContains(t, out.String(), "snapshots 17 字节")
}

// TestGCReportIncludesSnapshotRepair checks the repair path `data gc` prints for
// damaged snapshot trees (suga-m870): skipped records and removed partials.
func TestGCReportIncludesSnapshotRepair(t *testing.T) {
	var out bytes.Buffer
	reportSnapshotCleanup(&out, personal.SnapshotCleanupReport{SkippedRecords: 2, RepairedPartials: 1})
	assert.StrContains(t, out.String(), "跳过 2 个缺元数据的记录，清理 1 个半成品目录")

	out.Reset()
	reportSnapshotCleanup(&out, personal.SnapshotCleanupReport{RemovedTurns: 3})
	assert.Eq(t, "", out.String())
}
