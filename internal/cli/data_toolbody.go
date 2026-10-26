package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/gookit/goutil/cflag/capp"

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
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()

	if f.abort {
		if err := repo.ResetToolBodyState(ctx); err != nil {
			return err
		}
		fmt.Fprintln(out, "已清除正文文件断点；下次 --move / --undo 会从头开始")
		return nil
	}
	if f.check {
		report, err := repo.ToolBodyCheck(ctx)
		if err != nil {
			return err
		}
		writeToolBodyCheck(out, report)
		return nil
	}
	before, err := repo.ToolBodyStatus(ctx)
	if err != nil {
		return err
	}
	switch {
	case f.status:
		writeToolBodyStatus(out, before)
		return nil
	case f.undo:
		if err := runUndoToolBodies(ctx, out, repo, before, f); err != nil {
			return err
		}
		return nil
	case f.move:
		// Tool and assistant rows keep independent checkpoints: a ledger whose tool
		// rows moved (that pass is complete) still has its assistant rows to move.
		if err := runMoveToolBodies(ctx, out, repo, before, f.batch); err != nil {
			return err
		}
		return runMoveAssistantBodies(ctx, out, repo, before, f.batch)
	}
	return fmt.Errorf("usage: suga data bodies [--status|--move|--undo|--check|--abort]")
}

// runMoveToolBodies moves the historical tool bodies out in batches.
func runMoveToolBodies(ctx context.Context, out io.Writer, repo *store.Store, before store.ToolBodyStatus, batch int) error {
	tr := cliTranslator()
	if before.PendingRows == 0 {
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.none_tool"))
		return nil
	}
	fmt.Fprintf(out, "开始移出：%d 条 tool 结果（%s）将写入会话文件，Ctrl-C 可中断，重跑续上\n",
		before.PendingRows, humanBytes(before.PendingBytes))
	report, err := repo.MoveToolBodies(ctx, store.ToolBodyMaintOptions{
		Batch: batch,
		OnBatch: func(p store.ToolBodyMaintProgress) {
			fmt.Fprintf(out, "  已扫到 %s/%d，移出 %d 条（%s）\n", p.SessionID, p.Seq, p.Rows, humanBytes(p.Bytes))
		},
	})
	if err != nil {
		fmt.Fprintf(out, "中断于 %s/%d（已移出 %d 条）；重跑 `suga data bodies --move` 从这里续上：%v\n",
			report.CursorSession, report.CursorSeq, report.Rows, err)
		return err
	}
	fmt.Fprintf(out, "移出完成：%d 条 tool 结果（%s）写入会话文件，%d 个写事务\n", report.Rows, humanBytes(report.Bytes), report.Batches)
	return nil
}

// runMoveAssistantBodies moves the historical assistant bodies out. It has its
// own checkpoint (move_assistant), so it runs even when the tool pass of the same
// database finished already.
func runMoveAssistantBodies(ctx context.Context, out io.Writer, repo *store.Store, before store.ToolBodyStatus, batch int) error {
	tr := cliTranslator()
	if before.AssistantPendingRows == 0 {
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.none_assistant"))
		fmt.Fprintln(out, cliText(tr, "cli.data.bodies.files_note"))
		return nil
	}
	fmt.Fprintf(out, "开始移出：%d 条 assistant 条目（库内 %s）将写入会话文件，Ctrl-C 可中断，重跑续上\n",
		before.AssistantPendingRows, humanBytes(before.AssistantPending))
	report, err := repo.MoveAssistantBodies(ctx, store.ToolBodyMaintOptions{
		Batch: batch,
		OnBatch: func(p store.ToolBodyMaintProgress) {
			fmt.Fprintf(out, "  已扫到 %s/%d，移出 %d 条（%s）\n", p.SessionID, p.Seq, p.Rows, humanBytes(p.Bytes))
		},
	})
	if err != nil {
		fmt.Fprintf(out, "中断于 %s/%d（已移出 %d 条）；重跑 `suga data bodies --move` 从这里续上：%v\n",
			report.CursorSession, report.CursorSeq, report.Rows, err)
		return err
	}
	fmt.Fprintf(out, "移出完成：%d 条 assistant 条目（%s）写入会话文件，%d 个写事务\n", report.Rows, humanBytes(report.Bytes), report.Batches)
	fmt.Fprintln(out, cliText(tr, "cli.data.bodies.files_note"))
	return nil
}

// runUndoToolBodies restores every moved body and, with --remove-files, deletes
// the files of sessions that keep none.
func runUndoToolBodies(ctx context.Context, out io.Writer, repo *store.Store, before store.ToolBodyStatus, f toolBodiesFlags) error {
	if before.MovedRows+before.AssistantMovedRows == 0 {
		fmt.Fprintln(out, "已还原：没有移出到会话文件的正文")
	} else {
		fmt.Fprintf(out, "开始还原：%d 条 tool 结果与 %d 条 assistant 条目将写回库内（先关闭「新写入移出」开关），Ctrl-C 可中断，重跑续上\n",
			before.MovedRows, before.AssistantMovedRows)
		report, err := repo.UndoToolBodies(ctx, store.ToolBodyMaintOptions{
			Batch: f.batch,
			OnBatch: func(p store.ToolBodyMaintProgress) {
				fmt.Fprintf(out, "  已扫到 %s/%d，还原 %d 条（%s）\n", p.SessionID, p.Seq, p.Rows, humanBytes(p.Bytes))
			},
		})
		if err != nil {
			fmt.Fprintf(out, "中断于 %s/%d（已还原 %d 条）；重跑 `suga data bodies --undo` 从这里续上：%v\n",
				report.CursorSession, report.CursorSeq, report.Rows, err)
			return err
		}
		fmt.Fprintf(out, "还原完成：%d 条正文写回库内，%d 个写事务\n", report.Rows, report.Batches)
	}
	if !f.removeFiles {
		fmt.Fprintln(out, "新写入已不再移出（`--move` 可重新打开）；会话文件仍保留，可加 --remove-files 删除")
		return nil
	}
	removed, err := repo.RemoveToolBodyFiles(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "已删除 %d 个不再需要的会话文件\n", removed)
	return nil
}

// writeToolBodyStatus prints what --status found, one line per entry type.
func writeToolBodyStatus(out io.Writer, st store.ToolBodyStatus) {
	switchState := "已打开"
	if !st.MoveEnabled {
		switchState = "已关闭（--undo 之后）"
	}
	fmt.Fprintf(out, "正文文件：阈值 %s，新写入移出开关%s\n", humanBytes(st.InlineBytes), switchState)
	fileNameState := fmt.Sprintf("仍是旧名（%s）；v0.40 的 --rename-files 可改名", store.LegacyBodyFileName)
	if st.BodyFileNameEnabled {
		fileNameState = fmt.Sprintf("已改用 %s", store.BodyFileName)
	}
	fmt.Fprintf(out, "文件名：%s；旧名文件 %d 个\n", fileNameState, st.LegacyNameFiles)
	fmt.Fprintf(out, "tool：待移出 %d 条（%s）；已移出 %d 条\n",
		st.PendingRows, humanBytes(st.PendingBytes), st.MovedRows)
	fmt.Fprintf(out, "assistant：可移出 %d 条（库内 %s，含推理内容与工具调用参数）；移出不省空间 %d 条（库内 %s，不处理）；已移出 %d 条\n",
		st.AssistantPendingRows, humanBytes(st.AssistantPending), st.AssistantSkippedRows, humanBytes(st.AssistantSkipped), st.AssistantMovedRows)
	fmt.Fprintf(out, "会话文件 %d 个（%s）\n", st.Files, humanBytes(st.FileBytes))
	fmt.Fprintf(out, "--move 状态：tool %s（断点 %s/%d，已移出 %d 条）\n",
		passState(st.MoveStarted, st.MoveComplete), st.MoveSession, st.MoveSeq, st.MoveRows)
	fmt.Fprintf(out, "--move 状态：assistant %s（断点 %s/%d，已移出 %d 条）\n",
		passState(st.AssistantMoveStarted, st.AssistantMoveComplete),
		st.AssistantMoveSession, st.AssistantMoveSeq, st.AssistantMoveRows)
	fmt.Fprintf(out, "--undo 状态：%s（已还原 %d 条）\n", passState(st.UndoStarted, st.UndoComplete), st.UndoRows)
}

// passState names the state of one maintenance checkpoint.
func passState(started, complete bool) string {
	switch {
	case complete:
		return "已完成"
	case started:
		return "进行中"
	}
	return "未开始"
}

// writeToolBodyCheck prints the verification result.
func writeToolBodyCheck(out io.Writer, report store.ToolBodyCheckReport) {
	fmt.Fprintf(out, "校验了 %d 条移出的正文（tool 与 assistant）：%d 条读不回正文\n", report.Checked, report.Missing)
	if report.OrphanRecords > 0 {
		fmt.Fprintf(out, "孤儿记录 %d 条（%s）：没有桩指向它们，是提交前中断留下的，可忽略；需要清理时请先备份\n",
			report.OrphanRecords, humanBytes(report.OrphanBytes))
	}
	for _, problem := range report.Problems {
		fmt.Fprintf(out, "  %s\n", problem)
	}
	if report.OK() {
		fmt.Fprintln(out, "结论：库内桩与会话文件一致")
		return
	}
	fmt.Fprintln(out, "结论：有正文读不回，先别回退版本；可从备份恢复会话文件，或用 `data bodies --undo` 还原仍可读的部分")
}
