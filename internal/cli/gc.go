package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
)

// newGCCommand builds `suga gc`: remove sessions idle for longer than
// --older-than, or by origin class with --retention (only reports by default;
// --apply deletes). The same command owns the ledger maintenance jobs of data
// governance: --shrink-fts rewrites the historical tool rows of entries_fts to
// the truncated form new rows already use, --vacuum returns the freed space to
// the disk, and --compress-spill gzips the idle spill files (dry run unless
// --apply). A deletion no longer VACUUMs by itself: the two results are
// reported separately (design 0.3, P3).
func newGCCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var olderThan string
	var apply, shrinkFTS, status, abort, vacuum, retention, compressSpill bool
	var batch int
	cmd := capp.NewCmd("gc", "Remove sessions idle within --older-than or by origin class with --retention (dry run unless --apply), shrink the tool-output FTS index, compress spill outputs or VACUUM the ledger", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga gc --older-than 30d [--apply] | suga gc --retention [--apply] | suga gc --shrink-fts [--status|--abort] | suga gc --vacuum | suga gc --compress-spill [--apply]")
		}
		if retention && olderThan != "" {
			return fmt.Errorf("--retention and --older-than are two different selection rules; pass one of them")
		}
		if status || abort {
			if !shrinkFTS {
				return fmt.Errorf("--status and --abort belong to --shrink-fts: suga gc --shrink-fts [--status|--abort]")
			}
		}
		if shrinkFTS {
			return runShrinkFTS(ctx, stdout, status, abort, batch)
		}
		if vacuum {
			return runGCVacuum(ctx, stdout)
		}
		if retention {
			return runGCRetention(ctx, stdout, apply)
		}
		if compressSpill {
			return runGCCompressSpill(ctx, stdout, apply)
		}
		if olderThan == "" {
			return fmt.Errorf("usage: suga gc --older-than 30d [--apply]")
		}
		age, err := parseSince(olderThan)
		if err != nil {
			return fmt.Errorf("invalid --older-than %q: want <n>h or <n>d", olderThan)
		}
		return runGC(ctx, stdout, time.Now().Add(-age), apply)
	})
	cmd.SetOutput(stderr)
	cmd.StringVar(&olderThan, "older-than", "", "remove sessions not updated within this age, e.g. 30d or 12h")
	cmd.BoolVar(&apply, "apply", false, "delete for real (default: only list what would be removed)")
	cmd.BoolVar(&retention, "retention", false, "remove by origin class: gc.retention.interactive for chats, gc.retention.noninteractive for run/ACP sessions")
	cmd.BoolVar(&shrinkFTS, "shrink-fts", false, "rewrite old tool-output FTS rows to the truncated index form (resumable, Ctrl-C keeps the progress)")
	cmd.BoolVar(&status, "status", false, "with --shrink-fts: report how much of the index is left to shrink")
	cmd.BoolVar(&abort, "abort", false, "with --shrink-fts: drop the progress checkpoint so the next run starts over")
	cmd.BoolVar(&vacuum, "vacuum", false, "rewrite the database file to return freed space (needs free disk space and no other suga process)")
	cmd.BoolVar(&compressSpill, "compress-spill", false, "compress spill outputs older than tools.spill.compress_after (report only unless --apply)")
	cmd.IntVar(&batch, "batch", 0, "with --shrink-fts: rows per write transaction (default 2000)")
	return cmd
}

func runGC(ctx context.Context, out io.Writer, cutoff time.Time, apply bool) error {
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		return err
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return err
	}
	// An apply is a maintenance job: it holds the same run/maintenance.lock as
	// `gc --vacuum`, so two maintenance jobs never rewrite this ledger at once.
	if apply {
		lock, err := acquireMaintenanceLock(home)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Close() }()
	}
	snapshotSettings, err := appconfig.ReadSnapshotSettings()
	if err != nil {
		return err
	}
	// GC owns the sessions root, so it also performs the global checkpoint
	// cleanup. Existing snapshots are cleaned even when new snapshotting is off.
	snapshots := personal.NewSessionSnapshotsWithLimits(sessionsDir, personal.SnapshotLimits{
		MaxFileBytes: snapshotSettings.MaxFileBytes, MaxSessionBytes: snapshotSettings.MaxSessionBytes,
		MaxGlobalBytes: snapshotSettings.MaxGlobalBytes, KeepTurns: snapshotSettings.KeepTurns, KeepDays: snapshotSettings.KeepDays,
	})
	if _, err := snapshots.CleanupAll(); err != nil {
		if snapshotSettings.OnError == appconfig.SnapshotOnErrorDeny {
			return fmt.Errorf("clean snapshots: %w", err)
		}
		fmt.Fprintf(out, "warning: clean snapshots: %v\n", err)
	}

	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()

	// Never touch a session a live process uses: open chats are listed under
	// run/, and a running turn (chat or `run`) holds run/locks/<id>.lock.
	hold, err := newGCSessionHold(home)
	if err != nil {
		return err
	}
	defer hold.release()
	plan, err := repo.CollectGarbage(ctx, cutoff, hold.keep, false)
	if err != nil {
		return err
	}
	hold.protect(sessionIDsOf(plan.Sessions))

	report, err := repo.CollectGarbage(ctx, cutoff, hold.keep, apply)
	if err != nil {
		return err
	}
	for _, session := range report.Sessions {
		if dir, ok := sessionDir(sessionsDir, session.ID); ok {
			bytes, sizeErr := personal.SnapshotDirectoryBytes(filepath.Join(dir, "snapshots"))
			if sizeErr != nil {
				if snapshotSettings.OnError == appconfig.SnapshotOnErrorDeny {
					return fmt.Errorf("measure snapshots of %s: %w", session.ID, sizeErr)
				}
				fmt.Fprintf(out, "warning: measure snapshots of %s: %v\n", session.ID, sizeErr)
			} else {
				report.SnapshotBytes += bytes
			}
		}
	}
	if report.Applied {
		removeSessionFiles(out, home, sessionsDir, report.Sessions)
	}
	writeGCReport(out, report, cutoff)
	return nil
}

// sessionDir returns sessions/<id> when id is a plain single path element; an
// id that could name another place (separators, "..", absolute) yields false,
// so a stored id can never make gc remove anything outside the sessions root.
func sessionDir(sessionsDir, id string) (string, bool) {
	if id == "" || id == "." || strings.ContainsAny(id, `/\\:`) || !filepath.IsLocal(id) {
		return "", false
	}
	return filepath.Join(sessionsDir, id), true
}

func writeGCReport(out io.Writer, report store.GCReport, cutoff time.Time) {
	for _, session := range report.Sessions {
		title := session.Title
		if title == "" {
			title = "-"
		}
		fmt.Fprintf(out, "%s\t%s\t%s\n", session.ID, session.UpdatedAt.Local().Format("2006-01-02 15:04"), title)
	}
	verb := "将删除"
	if report.Applied {
		verb = "已删除"
	}
	fmt.Fprintf(out, "%s %d 个会话（%s 之前未更新，含其 transcript 文件与会话目录）与 %d 条事件记录，含 snapshots %d 字节",
		verb, len(report.Sessions), cutoff.Local().Format("2006-01-02 15:04"), report.Events, report.SnapshotBytes)
	if !report.Applied {
		fmt.Fprint(out, "；加 --apply 执行")
	} else {
		// Freeing the file back to the disk is a separate job with its own
		// report: apply only says which rows are gone (design 0.3, P3).
		fmt.Fprint(out, "；VACUUM 未执行，要回收磁盘空间再运行 `suga gc --vacuum`")
	}
	fmt.Fprintln(out)
}

// removeSessionFiles removes what lives outside the database: the readable
// per-session transcript (transcripts/<id>.jsonl) is removed with its session
// whatever the current transcripts setting, since an earlier run may have
// written it, and so is the session directory (sessions/<id>/, externalised
// tool outputs and snapshots). A failure is a warning: the rows are gone
// already.
func removeSessionFiles(out io.Writer, home, sessionsDir string, sessions []store.Session) {
	for _, session := range sessions {
		if err := os.Remove(filepath.Join(home, "transcripts", session.ID+".jsonl")); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(out, "warning: remove transcript of %s: %v\n", session.ID, err)
		}
		if dir, ok := sessionDir(sessionsDir, session.ID); ok {
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintf(out, "warning: remove session directory of %s: %v\n", session.ID, err)
			}
		}
	}
}

// gcSessionHold keeps the sessions of a running suga process out of a removal:
// the ones registered under run/ and the ones whose session lock cannot be
// taken (a turn is using them right now). The age-based and the retention-based
// removal protect their candidates the same way.
type gcSessionHold struct {
	keep     map[string]bool
	lockRoot string
	held     []*personalprocess.SessionLock
}

// newGCSessionHold lists what is running right now.
func newGCSessionHold(home string) (*gcSessionHold, error) {
	keep := map[string]bool{}
	records, err := personalprocess.List(filepath.Join(home, "run"))
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		keep[record.SessionID] = true
	}
	return &gcSessionHold{keep: keep, lockRoot: filepath.Join(home, "run", "locks")}, nil
}

// protect takes the session lock of every id and holds it until release, so no
// turn can start on a session this run is about to delete. A lock that cannot
// be taken marks the session kept (busy).
func (h *gcSessionHold) protect(ids []string) {
	for _, id := range ids {
		lock, err := personalprocess.AcquireSessionLock(h.lockRoot, id, 0)
		if err != nil {
			h.keep[id] = true // busy: some process is using it right now
			continue
		}
		h.held = append(h.held, lock)
	}
}

func (h *gcSessionHold) release() {
	for _, lock := range h.held {
		_ = lock.Close()
	}
	h.held = nil
}

// sessionIDsOf lists the ids the store reports for removal.
func sessionIDsOf(sessions []store.Session) []string {
	out := make([]string, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, session.ID)
	}
	return out
}

// runGCRetention runs `suga gc --retention`: it applies the per-class retention
// periods (gc.retention.interactive / gc.retention.noninteractive) instead of
// one shared age. A first pass plans and takes the locks of its candidates, the
// second one applies the same selection; both are dry runs without --apply.
func runGCRetention(ctx context.Context, out io.Writer, apply bool) error {
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		return err
	}
	settings, err := appconfig.ReadGCRetentionSettings()
	if err != nil {
		return err
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return err
	}
	if apply {
		lock, err := acquireMaintenanceLock(home)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Close() }()
	}
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()
	hold, err := newGCSessionHold(home)
	if err != nil {
		return err
	}
	defer hold.release()
	policy := store.GCRetentionPolicy{Interactive: settings.Interactive, NonInteractive: settings.NonInteractive}
	now := time.Now()
	plan, err := repo.CollectGarbageByRetention(ctx, now, policy, hold.keep, false)
	if err != nil {
		return err
	}
	hold.protect(sessionIDsOf(plan.Sessions()))
	report, err := repo.CollectGarbageByRetention(ctx, now, policy, hold.keep, apply)
	if err != nil {
		return err
	}
	if report.Applied {
		removeSessionFiles(out, home, sessionsDir, report.Sessions())
	}
	writeRetentionReport(out, report)
	return nil
}

// writeRetentionReport prints one line per class (sessions, entries, estimated
// bytes) and then what the run removes. The estimate is not a reclaim promise:
// only a VACUUM returns space to the disk.
func writeRetentionReport(out io.Writer, report store.GCRetentionReport) {
	for _, class := range report.Classes() {
		name := "非交互会话"
		if class.Interactive {
			name = "交互会话"
		}
		fmt.Fprintf(out, "%s（保留 %s）：%d 个会话，%d 条条目，预计 %s\n",
			name, humanDuration(class.Retention), len(class.Sessions), class.Entries, humanBytes(class.EstimatedBytes))
	}
	verb := "将删除"
	if report.Applied {
		verb = "已删除"
	}
	fmt.Fprintf(out, "%s %d 个会话（含其 transcript 文件与会话目录），预计释放 %s（实际字节要等 `suga gc --vacuum`）\n",
		verb, len(report.Sessions()),
		humanBytes(report.Interactive.EstimatedBytes+report.NonInteractive.EstimatedBytes))
	if !report.Applied {
		fmt.Fprintln(out, "加 --apply 执行；删除本身不 VACUUM，要回收磁盘空间再运行 `suga gc --vacuum`")
	} else {
		// The deletion and the file rewrite are two separate reports.
		fmt.Fprintln(out, "VACUUM 未执行：要把空间还给磁盘再运行 `suga gc --vacuum`")
	}
}
