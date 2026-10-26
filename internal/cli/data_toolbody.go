package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/gookit/goutil/cflag/capp"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal/store"
)

// Tool body files (design 2026-10-19, tool-output-files 0.3; design 2026-10-20,
// assistant-bodies). Since those versions an entry above store.body_inline_bytes
// lives in sessions/<id>/bodies.jsonl instead of the ledger (a tool result, or an
// assistant reply with its reasoning and call arguments); ListEntries hands it
// back, so only the storage changed. This command migrates the historical rows
// and is the way back for an older suga.

// legacyToolBodiesName is the v0.36 name of `data bodies`. It was an alias until
// v0.41.0, which removed it: typing it now fails with a hint (legacy_args.go).

// newToolBodiesCommand builds `suga data bodies`.
func newToolBodiesCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var status, move, undo, check, removeFiles, abort bool
	var batch int
	cmd := capp.NewCmd("bodies", "Move tool results and assistant replies out of the ledger into sessions/<id>/bodies.jsonl (--move), restore them (--undo), verify (--check)", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga data bodies [--status|--move|--undo|--check|--abort] [--remove-files] [--batch N]")
		}
		modes := 0
		for _, on := range []bool{status, move, undo, check, abort} {
			if on {
				modes++
			}
		}
		if modes > 1 {
			return fmt.Errorf("--status, --move, --undo, --check and --abort are different jobs; pass one of them")
		}
		if removeFiles && !undo {
			return fmt.Errorf("--remove-files only applies to --undo")
		}
		return runToolBodies(ctx, stdout, toolBodiesFlags{status: status, move: move, undo: undo, check: check, abort: abort,
			removeFiles: removeFiles, batch: batch})
	})
	cmd.SetOutput(stderr)
	cmd.BoolVar(&status, "status", false, "report what would move, what moved and how large the files are")
	cmd.BoolVar(&move, "move", false, "move the historical tool results and assistant replies into the session files (resumable)")
	cmd.BoolVar(&undo, "undo", false, "restore every moved body into the ledger and keep new ones inline (do this before running an older suga)")
	cmd.BoolVar(&check, "check", false, "verify every stub against its record and report orphans (report only)")
	cmd.BoolVar(&abort, "abort", false, "drop the progress checkpoints so the next pass starts over")
	cmd.BoolVar(&removeFiles, "remove-files", false, "with --undo: delete the session files of sessions that keep no moved body")
	cmd.IntVar(&batch, "batch", 0, "rows per write transaction (default 100)")
	return cmd
}

// toolBodiesFlags is what the flags asked for.
type toolBodiesFlags struct {
	status, move, undo, check, abort, removeFiles bool
	batch                                         int
}

// runToolBodies runs one job of `suga data bodies`.
func runToolBodies(ctx context.Context, out io.Writer, f toolBodiesFlags) error {
	tr := cliTranslator()
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()

	if f.abort {
		if err := repo.ResetToolBodyState(ctx); err != nil {
			return err
		}
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.abort_done"))
		return nil
	}
	if f.check {
		report, err := repo.ToolBodyCheck(ctx)
		if err != nil {
			return err
		}
		writeToolBodyCheck(out, report, tr)
		return nil
	}
	before, err := repo.ToolBodyStatus(ctx)
	if err != nil {
		return err
	}
	switch {
	case f.status:
		writeToolBodyStatus(out, before, tr)
		return nil
	case f.undo:
		if err := runUndoToolBodies(ctx, out, repo, before, f, tr); err != nil {
			return err
		}
		return nil
	case f.move:
		// Tool and assistant rows keep independent checkpoints: a ledger whose tool
		// rows moved (that pass is complete) still has its assistant rows to move.
		if err := runMoveToolBodies(ctx, out, repo, before, f.batch, tr); err != nil {
			return err
		}
		return runMoveAssistantBodies(ctx, out, repo, before, f.batch, tr)
	}
	return fmt.Errorf("usage: suga data bodies [--status|--move|--undo|--check|--abort]")
}

// runMoveToolBodies moves the historical tool bodies out in batches.
func runMoveToolBodies(ctx context.Context, out io.Writer, repo *store.Store, before store.ToolBodyStatus, batch int, tr i18n.Translator) error {
	if before.PendingRows == 0 {
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.none_tool"))
		return nil
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.move_start_tool", map[string]any{
		"rows": before.PendingRows, "bytes": humanBytes(before.PendingBytes)}))
	report, err := repo.MoveToolBodies(ctx, store.ToolBodyMaintOptions{
		Batch: batch,
		OnBatch: func(p store.ToolBodyMaintProgress) {
			fmt.Fprintf(out, "  %s\n", cliText(tr, "cli.data.bodies.progress_move", map[string]any{
				"session": p.SessionID, "seq": p.Seq, "rows": p.Rows, "bytes": humanBytes(p.Bytes)}))
		},
	})
	if err != nil {
		fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.interrupted_move", map[string]any{
			"session": report.CursorSession, "seq": report.CursorSeq, "rows": report.Rows, "error": err}))
		return err
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.move_done_tool", map[string]any{
		"rows": report.Rows, "bytes": humanBytes(report.Bytes), "batches": report.Batches}))
	return nil
}

// runMoveAssistantBodies moves the historical assistant bodies out. It has its
// own checkpoint (move_assistant), so it runs even when the tool pass of the same
// database finished already.
func runMoveAssistantBodies(ctx context.Context, out io.Writer, repo *store.Store, before store.ToolBodyStatus, batch int, tr i18n.Translator) error {
	if before.AssistantPendingRows == 0 {
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.none_assistant"))
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.files_note"))
		return nil
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.move_start_assistant", map[string]any{
		"rows": before.AssistantPendingRows, "bytes": humanBytes(before.AssistantPending)}))
	report, err := repo.MoveAssistantBodies(ctx, store.ToolBodyMaintOptions{
		Batch: batch,
		OnBatch: func(p store.ToolBodyMaintProgress) {
			fmt.Fprintf(out, "  %s\n", cliText(tr, "cli.data.bodies.progress_move", map[string]any{
				"session": p.SessionID, "seq": p.Seq, "rows": p.Rows, "bytes": humanBytes(p.Bytes)}))
		},
	})
	if err != nil {
		fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.interrupted_move", map[string]any{
			"session": report.CursorSession, "seq": report.CursorSeq, "rows": report.Rows, "error": err}))
		return err
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.move_done_assistant", map[string]any{
		"rows": report.Rows, "bytes": humanBytes(report.Bytes), "batches": report.Batches}))
	fmt.Fprintln(out, cliText(tr, "cli.data.bodies.files_note"))
	return nil
}

// runUndoToolBodies restores every moved body and, with --remove-files, deletes
// the files of sessions that keep none.
func runUndoToolBodies(ctx context.Context, out io.Writer, repo *store.Store, before store.ToolBodyStatus, f toolBodiesFlags, tr i18n.Translator) error {
	if before.MovedRows+before.AssistantMovedRows == 0 {
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.undo_none"))
	} else {
		fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.undo_start", map[string]any{
			"tools": before.MovedRows, "assistant": before.AssistantMovedRows}))
		report, err := repo.UndoToolBodies(ctx, store.ToolBodyMaintOptions{
			Batch: f.batch,
			OnBatch: func(p store.ToolBodyMaintProgress) {
				fmt.Fprintf(out, "  %s\n", cliText(tr, "cli.data.bodies.progress_undo", map[string]any{
					"session": p.SessionID, "seq": p.Seq, "rows": p.Rows, "bytes": humanBytes(p.Bytes)}))
			},
		})
		if err != nil {
			fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.interrupted_undo", map[string]any{
				"session": report.CursorSession, "seq": report.CursorSeq, "rows": report.Rows, "error": err}))
			return err
		}
		fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.undo_done", map[string]any{
			"rows": report.Rows, "batches": report.Batches}))
	}
	if !f.removeFiles {
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.undo_kept_files"))
		return nil
	}
	removed, err := repo.RemoveToolBodyFiles(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.removed_files", map[string]any{"count": removed}))
	return nil
}

// writeToolBodyStatus prints what --status found, one line per entry type.
func writeToolBodyStatus(out io.Writer, st store.ToolBodyStatus, tr i18n.Translator) {
	switchState := cliText(tr, "cli.data.bodies.status.switch_on")
	if !st.MoveEnabled {
		switchState = cliText(tr, "cli.data.bodies.status.switch_off")
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.status.header", map[string]any{
		"threshold": humanBytes(st.InlineBytes), "state": switchState}))
	fileNameState := cliText(tr, "cli.data.bodies.status.name_legacy", map[string]any{"name": store.LegacyBodyFileName})
	if st.BodyFileNameEnabled {
		fileNameState = cliText(tr, "cli.data.bodies.status.name_current", map[string]any{"name": store.BodyFileName})
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.status.names", map[string]any{
		"state": fileNameState, "count": st.LegacyNameFiles}))
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.status.tool", map[string]any{
		"pending": st.PendingRows, "bytes": humanBytes(st.PendingBytes), "moved": st.MovedRows}))
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.status.assistant", map[string]any{
		"pending": st.AssistantPendingRows, "bytes": humanBytes(st.AssistantPending),
		"skipped": st.AssistantSkippedRows, "skipped_bytes": humanBytes(st.AssistantSkipped),
		"moved": st.AssistantMovedRows}))
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.status.files", map[string]any{
		"count": st.Files, "bytes": humanBytes(st.FileBytes)}))
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.status.move_tool", map[string]any{
		"state": passState(st.MoveStarted, st.MoveComplete, tr), "session": st.MoveSession, "seq": st.MoveSeq, "rows": st.MoveRows}))
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.status.move_assistant", map[string]any{
		"state":   passState(st.AssistantMoveStarted, st.AssistantMoveComplete, tr),
		"session": st.AssistantMoveSession, "seq": st.AssistantMoveSeq, "rows": st.AssistantMoveRows}))
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.status.undo", map[string]any{
		"state": passState(st.UndoStarted, st.UndoComplete, tr), "rows": st.UndoRows}))
}

// passState names the state of one maintenance checkpoint.
func passState(started, complete bool, tr i18n.Translator) string {
	switch {
	case complete:
		return cliText(tr, "cli.data.bodies.pass.complete")
	case started:
		return cliText(tr, "cli.data.bodies.pass.running")
	}
	return cliText(tr, "cli.data.bodies.pass.none")
}

// writeToolBodyCheck prints the verification result.
func writeToolBodyCheck(out io.Writer, report store.ToolBodyCheckReport, tr i18n.Translator) {
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.check.header", map[string]any{
		"checked": report.Checked, "missing": report.Missing}))
	if report.OrphanRecords > 0 {
		fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.bodies.check.orphans", map[string]any{
			"count": report.OrphanRecords, "bytes": humanBytes(report.OrphanBytes)}))
	}
	for _, problem := range report.Problems {
		fmt.Fprintf(out, "  %s\n", problem)
	}
	if report.OK() {
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.check.ok"))
		return
	}
	fmt.Fprintln(out, cliText(tr, "cli.data.bodies.check.broken"))
}
