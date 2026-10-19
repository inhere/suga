package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

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

// runShrinkFTS runs `suga gc --shrink-fts`: --status only reports, --abort
// drops the checkpoint, and the plain form rewrites the historical tool rows
// of entries_fts in batches.
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
		fmt.Fprintln(out, "已清除 FTS 瘦身断点；下次 --shrink-fts 会从头开始")
		return nil
	}

	before, err := repo.FTSShrinkStatus(ctx)
	if err != nil {
		return err
	}
	if !before.Remaining() {
		fmt.Fprintf(out, "FTS 无需瘦身：tool 输出索引中没有超过 %d 字符的行\n", store.FTSToolMaxChars)
		return nil
	}
	fmt.Fprintf(out, "开始瘦身：%d 行 tool 输出待处理（%d 字符文本），预计可回收约 %s；Ctrl-C 可中断，重跑续上\n",
		before.PendingRows, before.PendingChars, humanBytes(before.EstimatedReclaimBytes))
	report, err := repo.ShrinkFTSToolOutputs(ctx, store.FTSShrinkOptions{
		Batch: batch,
		OnBatch: func(p store.FTSShrinkProgress) {
			fmt.Fprintf(out, "  已扫到 rowid %d/%d，重写 %d 行\n", p.ScannedRowID, p.HighWater, p.Rows)
		},
	})
	if err != nil {
		// The checkpoint of the last committed batch stands: say what is done
		// and how to continue instead of only reporting the interruption.
		fmt.Fprintf(out, "中断于 rowid %d/%d（已重写 %d 行）；重跑 `suga gc --shrink-fts` 从这里续上：%v\n",
			report.ScannedRowID, report.HighWater, report.Rows, err)
		return err
	}
	fmt.Fprintf(out, "瘦身完成：重写 %d 行，%d 个写事务，merge %d 次；entries_fts_data %s → %s\n",
		report.Rows, report.Batches, report.Merges, humanBytes(report.BytesBefore), humanBytes(report.BytesAfter))
	after, err := repo.FTSShrinkStatus(ctx)
	if err != nil {
		return err
	}
	if after.Remaining() {
		fmt.Fprintf(out, "仍有 %d 行待处理（并发写入或 --batch 之外的边界）；可再跑一次\n", after.PendingRows)
	}
	fmt.Fprintln(out, "文件大小要等 `suga gc --vacuum` 才回落")
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
	fmt.Fprintf(out, "待处理 tool 行：%d（%d 字符文本），预计可回收约 %s\n",
		report.PendingRows, report.PendingChars, humanBytes(report.EstimatedReclaimBytes))
	return nil
}

// runGCVacuum runs `suga gc --vacuum`: it refuses to start without the free
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
	lock, held, err := acquireMaintenanceWindow(home)
	if err != nil {
		return err
	}
	defer lock.Close()
	defer func() {
		for _, sessionLock := range held {
			_ = sessionLock.Close()
		}
	}()

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

// acquireMaintenanceWindow is the shared gate for destructive maintenance:
// it owns the maintenance lock and verifies registered processes and turn locks.
func acquireMaintenanceWindow(home string) (*personalprocess.SessionLock, []*personalprocess.SessionLock, error) {
	runDir := filepath.Join(home, "run")
	lock, err := personalprocess.AcquireSessionLock(runDir, "maintenance job", 0)
	if err != nil {
		return nil, nil, fmt.Errorf("another maintenance job is running: %w", err)
	}
	records, err := personalprocess.List(runDir)
	if err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	if len(records) > 0 {
		ids := make([]string, 0, len(records))
		for _, record := range records {
			ids = append(ids, fmt.Sprintf("%s (pid %d, %s)", record.SessionID, record.PID, record.State))
		}
		sort.Strings(ids)
		_ = lock.Close()
		return nil, nil, fmt.Errorf("this ledger is in use, stop these suga processes first: %s", strings.Join(ids, ", "))
	}
	held, busy, err := holdRunLocks(filepath.Join(runDir, "locks"))
	if err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	if len(busy) > 0 {
		for _, item := range held {
			_ = item.Close()
		}
		_ = lock.Close()
		return nil, nil, fmt.Errorf("a turn is running (session lock held): %s", strings.Join(busy, ", "))
	}
	return lock, held, nil
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
