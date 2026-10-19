package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/gookit/goutil/cflag/capp"

	"github.com/inhere/suga/internal/personal/store"
)

// Tool result dedupe maintenance (data governance P2, design 0.4). New tool
// messages are stored once since this command shipped (store.EncodeMessage);
// the rows written before hold the same body twice, and this command rewrites
// them in batches. --undo is the way back for an older suga, whose providers
// read only ToolResult.Content.

// newDedupeToolResultsCommand builds `suga data dedupe-tool-results`.
func newDedupeToolResultsCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var status, abort, undo bool
	var batch int
	cmd := capp.NewCmd("dedupe-tool-results", "Store the body of historical tool messages once (resumable; --undo restores it for older versions)", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga data dedupe-tool-results [--status|--abort|--undo] [--batch N]")
		}
		return runDedupeToolResults(ctx, stdout, status, abort, undo, batch)
	})
	cmd.SetOutput(stderr)
	cmd.BoolVar(&status, "status", false, "report how many tool messages still hold the result twice")
	cmd.BoolVar(&abort, "abort", false, "drop the progress checkpoints so the next run starts over")
	cmd.BoolVar(&undo, "undo", false, "restore ToolResult.Content on every deduped row (do this before running an older suga)")
	cmd.IntVar(&batch, "batch", 0, "rows per write transaction (default 2000)")
	return cmd
}

// runDedupeToolResults runs `suga data dedupe-tool-results`: --status only
// reports, --abort drops the checkpoints, --undo restores the double-stored
// form, and the plain form rewrites the historical rows.
func runDedupeToolResults(ctx context.Context, out io.Writer, status, abort, undo bool, batch int) error {
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()

	if status {
		report, err := repo.ToolDedupeStatus(ctx)
		if err != nil {
			return err
		}
		return writeDedupeStatus(out, report)
	}
	if abort {
		if err := repo.ResetToolDedupe(ctx); err != nil {
			return err
		}
		fmt.Fprintln(out, "已清除 tool 结果去重断点；下次运行会从头开始")
		return nil
	}

	before, err := repo.ToolDedupeStatus(ctx)
	if err != nil {
		return err
	}
	if undo {
		return runUndoToolDedupe(ctx, out, repo, before, batch)
	}
	if !before.Remaining() {
		fmt.Fprintln(out, "tool 结果无需去重：没有 Content 与 ToolResult.Content 相同的 tool 消息")
		return nil
	}
	fmt.Fprintf(out, "开始去重：%d 条 tool 消息双份存储（重复正文约 %s），Ctrl-C 可中断，重跑续上\n",
		before.PendingRows, humanBytes(before.PendingBytes))
	report, err := repo.DedupeToolResults(ctx, store.ToolDedupeOptions{
		Batch: batch,
		OnBatch: func(p store.ToolDedupeProgress) {
			fmt.Fprintf(out, "  已扫到 %s/%d，重写 %d 行\n", p.SessionID, p.Seq, p.Rows)
		},
	})
	if err != nil {
		fmt.Fprintf(out, "中断于 %s/%d（已重写 %d 行）；重跑 `suga data dedupe-tool-results` 从这里续上：%v\n",
			report.CursorSession, report.CursorSeq, report.Rows, err)
		return err
	}
	fmt.Fprintf(out, "去重完成：重写 %d 条 tool 消息，%d 个写事务\n", report.Rows, report.Batches)
	after, err := repo.ToolDedupeStatus(ctx)
	if err != nil {
		return err
	}
	if after.Remaining() {
		fmt.Fprintf(out, "仍有 %d 条待处理（并发写入或 --batch 之外的边界）；可再跑一次\n", after.PendingRows)
	}
	fmt.Fprintln(out, "文件大小要等 `suga data vacuum` 才回落")
	return nil
}

// runUndoToolDedupe restores ToolResult.Content on every marked row, so an
// older suga (which reads only that field) can run on this ledger again.
func runUndoToolDedupe(ctx context.Context, out io.Writer, repo *store.Store, before store.ToolDedupeStatus, batch int) error {
	if before.MarkedRows == 0 {
		fmt.Fprintln(out, "已还原：没有带去重标记的 tool 消息")
		return nil
	}
	fmt.Fprintf(out, "开始还原：%d 条去重后的 tool 消息将写回 ToolResult.Content，Ctrl-C 可中断，重跑续上\n",
		before.MarkedRows)
	report, err := repo.UndoToolDedupe(ctx, store.ToolDedupeOptions{
		Batch: batch,
		OnBatch: func(p store.ToolDedupeProgress) {
			fmt.Fprintf(out, "  已扫到 %s/%d，还原 %d 行\n", p.SessionID, p.Seq, p.Rows)
		},
	})
	if err != nil {
		fmt.Fprintf(out, "中断于 %s/%d（已还原 %d 行）；重跑 `suga data dedupe-tool-results --undo` 从这里续上：%v\n",
			report.CursorSession, report.CursorSeq, report.Rows, err)
		return err
	}
	fmt.Fprintf(out, "还原完成：%d 条 tool 消息重新双份存储，%d 个写事务\n", report.Rows, report.Batches)
	after, err := repo.ToolDedupeStatus(ctx)
	if err != nil {
		return err
	}
	if after.MarkedRows > 0 {
		fmt.Fprintf(out, "仍有 %d 条带标记（还原期间新写入的 tool 消息）；旧版本运行前再跑一次 --undo\n", after.MarkedRows)
	}
	fmt.Fprintln(out, "文件大小要等 `suga data vacuum` 才回落")
	return nil
}

// writeDedupeStatus prints what the dedupe passes did and what is left.
func writeDedupeStatus(out io.Writer, report store.ToolDedupeStatus) error {
	state := "未开始"
	switch {
	case report.Complete:
		state = "已完成"
	case report.Started:
		state = "进行中"
	}
	fmt.Fprintf(out, "tool 结果去重状态：%s（断点 %s/%d，已重写 %d 行）\n",
		state, report.CursorSession, report.CursorSeq, report.Rows)
	fmt.Fprintf(out, "待处理 tool 消息：%d 条（重复正文 %s）\n",
		report.PendingRows, humanBytes(report.PendingBytes))
	undoState := "未开始"
	switch {
	case report.UndoComplete:
		undoState = "已完成"
	case report.UndoStarted:
		undoState = "进行中"
	}
	fmt.Fprintf(out, "带去重标记（--undo 可还原）：%d 条；还原状态：%s（已还原 %d 行）\n",
		report.MarkedRows, undoState, report.UndoRows)
	return nil
}
