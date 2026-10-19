package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appconfig "github.com/inhere/suga/internal/config"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
)

// Ledger maintenance jobs of the data governance P1 design (0.3): the FTS
// shrink and the VACUUM. Both are explicit commands, never startup work, and
// both take the maintenance lock under run/ so that two of them never rewrite
// the same file at once. The shrink itself does not need the lock (it writes
// short batches and coexists with chat), but taking it uniformly keeps the
// "one maintenance job at a time" rule simple.

// runShrinkFTS runs `suga data shrink-fts`: --status only reports, --abort
// drops the checkpoint, and the plain form deletes the historical tool rows of
// entries_fts in batches (tool results are no longer indexed).
func runShrinkFTS(ctx context.Context, out io.Writer, status, abort bool, batch int) error {
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()

	if status {
		report, err := repo.FTSShrinkStatus(ctx)
		if err != nil {
			return err
		}
		return writeShrinkStatus(out, report)
	}
	if abort {
		if err := repo.ResetFTSShrink(ctx); err != nil {
			return err
		}
		fmt.Fprintln(out, "已清除 FTS 瘦身断点；下次 `suga data shrink-fts` 会从头开始")
		return nil
	}

	before, err := repo.FTSShrinkStatus(ctx)
	if err != nil {
		return err
	}
	if !before.Remaining() {
		fmt.Fprintln(out, "FTS 无需瘦身：索引中没有 tool 输出行")
		return nil
	}
	fmt.Fprintf(out, "开始瘦身：删除 %d 行 tool 输出索引（%d 字符文本），预计可回收约 %s；Ctrl-C 可中断，重跑续上\n",
		before.PendingRows, before.PendingChars, humanBytes(before.EstimatedReclaimBytes))
	report, err := repo.ShrinkFTSToolOutputs(ctx, store.FTSShrinkOptions{
		Batch: batch,
		OnBatch: func(p store.FTSShrinkProgress) {
			fmt.Fprintf(out, "  已扫到 rowid %d/%d，删除 %d 行\n", p.ScannedRowID, p.HighWater, p.Rows)
		},
	})
	if err != nil {
		// The checkpoint of the last committed batch stands: say what is done
		// and how to continue instead of only reporting the interruption.
		fmt.Fprintf(out, "中断于 rowid %d/%d（已删除 %d 行）；重跑 `suga data shrink-fts` 从这里续上：%v\n",
			report.ScannedRowID, report.HighWater, report.Rows, err)
		return err
	}
	optimized := "无需 optimize"
	if report.Optimized {
		optimized = "optimize 1 次"
	}
	fmt.Fprintf(out, "瘦身完成：删除 %d 行，%d 个写事务，%s；entries_fts_data %s → %s\n",
		report.Rows, report.Batches, optimized, humanBytes(report.BytesBefore), humanBytes(report.BytesAfter))
	after, err := repo.FTSShrinkStatus(ctx)
	if err != nil {
		return err
	}
	if after.Remaining() {
		fmt.Fprintf(out, "仍有 %d 行 tool 输出在索引中（并发写入已不再产生新的）；可再跑一次\n", after.PendingRows)
	}
	fmt.Fprintln(out, "文件大小要等 `suga data vacuum` 才回落")
	return nil
}

// writeShrinkStatus prints what a shrink pass still has to do.
func writeShrinkStatus(out io.Writer, report store.FTSShrinkStatus) error {
	state := "未开始"
	switch {
	case report.Complete:
		state = "已完成"
	case report.Started:
		state = "进行中"
	}
	fmt.Fprintf(out, "FTS 瘦身状态：%s（断点 rowid=%d）\n", state, report.ScannedRowID)
	fmt.Fprintf(out, "剩余 tool 行：%d（%d 字符文本），预计可回收约 %s\n",
		report.PendingRows, report.PendingChars, humanBytes(report.EstimatedReclaimBytes))
	return nil
}

// runGCVacuum runs `suga data vacuum`: it refuses to start without the free
// space a VACUUM needs or while another suga process uses this ledger, then
// rewrites the file and truncates the WAL.
func runGCVacuum(ctx context.Context, out io.Writer) error {
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		return err
	}
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return err
	}
	runDir := filepath.Join(home, "run")
	// The maintenance lock is one of the session locks, under a name no session
	// can have (session ids never contain a space).
	window, err := acquireMaintenanceWindow(runDir)
	if err != nil {
		return err
	}
	defer window.Release()

	size := store.DatabaseFileBytes(dbPath)
	free, err := store.FreeDiskSpace(dbPath)
	if err != nil {
		return fmt.Errorf("read free disk space: %w", err)
	}
	if err := requireVacuumSpace(size, free); err != nil {
		return err
	}
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()
	result, err := repo.Vacuum(ctx)
	if err != nil {
		// A failed VACUUM leaves the original database in place (SQLite only
		// swaps the new file in at the end); on Windows a held handle or an
		// antivirus scan is the usual cause.
		return fmt.Errorf("vacuum (original database unchanged, retry after closing whatever holds it): %w", err)
	}
	fmt.Fprintf(out, "VACUUM 完成：sessions.db %s → %s（WAL %s → %s）\n",
		humanBytes(result.DBBytesBefore), humanBytes(result.DBBytesAfter),
		humanBytes(result.WALBytesBefore), humanBytes(result.WALBytesAfter))
	if result.BytesAfter < result.BytesBefore {
		fmt.Fprintf(out, "库文件总体 %s → %s，回收 %s\n", humanBytes(result.BytesBefore),
			humanBytes(result.BytesAfter), humanBytes(result.BytesBefore-result.BytesAfter))
	} else {
		fmt.Fprintf(out, "库文件总体 %s → %s（本次没有可回收的页）\n",
			humanBytes(result.BytesBefore), humanBytes(result.BytesAfter))
	}
	return nil
}

// requireVacuumSpace is the free-space gate of `gc --vacuum`: a VACUUM writes
// a new file next to the old one, so the peak usage is about twice the database
// size (design 0.3, VACUUM). It is a helper so that the refusal is testable
// without a database of that size.
func requireVacuumSpace(dbBytes int64, free uint64) error {
	if need := 2 * uint64(dbBytes); free < need {
		return fmt.Errorf("not enough free disk space for VACUUM: need about %s, %s available",
			humanBytes(int64(need)), humanBytes(int64(free)))
	}
	return nil
}

// maintenanceWindow is the exclusive window of one ledger maintenance job: the
// global maintenance lock under run/ (one of the session locks, under a name no
// session can have) plus a refusal while any other suga process uses this
// ledger, and every existing session lock held, so no turn starts inside the
// window. It is shared by `gc --vacuum` (design 0.3) and the spill compression
// so both use the same rule; running both at once would rewrite the same files.
type maintenanceWindow struct {
	lock *personalprocess.SessionLock
	held []*personalprocess.SessionLock
}

// Release gives every lock of the window back. It is safe to call twice.
func (w *maintenanceWindow) Release() {
	if w == nil {
		return
	}
	for _, lock := range w.held {
		_ = lock.Close()
	}
	w.held = nil
	if w.lock != nil {
		_ = w.lock.Close()
		w.lock = nil
	}
}

// acquireMaintenanceWindow takes the maintenance lock, refuses while another
// suga process (chat, ACP, run) uses this ledger or holds a session lock, and
// keeps the session locks for the caller.
func acquireMaintenanceWindow(runDir string) (*maintenanceWindow, error) {
	lock, err := personalprocess.AcquireSessionLock(runDir, "maintenance job", 0)
	if err != nil {
		return nil, fmt.Errorf("another maintenance job is running: %w", err)
	}
	window := &maintenanceWindow{lock: lock}
	records, err := personalprocess.List(runDir)
	if err != nil {
		window.Release()
		return nil, err
	}
	if len(records) > 0 {
		ids := make([]string, 0, len(records))
		for _, record := range records {
			ids = append(ids, fmt.Sprintf("%s (pid %d, %s)", record.SessionID, record.PID, record.State))
		}
		sort.Strings(ids)
		window.Release()
		return nil, fmt.Errorf("this ledger is in use, stop these suga processes first: %s", strings.Join(ids, ", "))
	}
	held, busy, err := holdRunLocks(filepath.Join(runDir, "locks"))
	if err != nil {
		window.Release()
		return nil, err
	}
	if len(busy) > 0 {
		window.Release()
		return nil, fmt.Errorf("a turn is running (session lock held): %s", strings.Join(busy, ", "))
	}
	window.held = held
	return window, nil
}

// holdRunLocks takes every existing session lock of runDir without waiting, so
// the caller learns whether a turn is running. The held locks are returned for
// the caller to release after the maintenance window.
func holdRunLocks(runDir string) (held []*personalprocess.SessionLock, busy []string, err error) {
	entries, err := filepath.Glob(filepath.Join(runDir, "*.lock"))
	if err != nil {
		return nil, nil, err
	}
	for _, path := range entries {
		// The lock file is named after its session with separators replaced, so
		// the stem is the session id for every session id suga creates.
		name := strings.TrimSuffix(filepath.Base(path), ".lock")
		sessionLock, err := personalprocess.AcquireSessionLock(runDir, name, 0)
		if err != nil {
			if errors.Is(err, personalprocess.ErrSessionLocked) {
				busy = append(busy, name)
				continue
			}
			return held, busy, err
		}
		held = append(held, sessionLock)
	}
	sort.Strings(busy)
	return held, busy, nil
}

// acquireMaintenanceLock takes the run/maintenance.lock that every heavy
// maintenance job of data governance shares (gc --apply, gc --vacuum, and the
// backup/restore and spill compaction of the sibling tasks): two of them must
// never rewrite the same ledger at once. The lock is one of the session locks,
// under a name no session can have (session ids never contain a space).
func acquireMaintenanceLock(home string) (*personalprocess.SessionLock, error) {
	lock, err := personalprocess.AcquireSessionLock(filepath.Join(home, "run"), "maintenance job", 0)
	if err != nil {
		return nil, fmt.Errorf("another maintenance job is running: %w", err)
	}
	return lock, nil
}

// humanDuration renders a retention period in days when it is a whole number of
// days, and as a Go duration otherwise.
func humanDuration(d time.Duration) string {
	if d > 0 && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

// humanBytes formats a byte count for the maintenance report.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}
