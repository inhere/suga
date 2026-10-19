package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
)

// runGCCompressSpill runs `suga gc --compress-spill`: it reports the spill
// files, the quota of tools.spill.max_bytes and what a compression pass would
// do; with --apply (inside the maintenance window, so no other suga process is
// using this ledger) it rewrites the idle .txt files as .txt.gz. A dry run
// changes nothing: it only measures.
func runGCCompressSpill(ctx context.Context, out io.Writer, apply bool) error {
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		return err
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return err
	}
	settings, err := appconfig.ReadSpillSettings()
	if err != nil {
		return err
	}
	usage, _, err := personal.ScanSpill(sessionsDir)
	if err != nil {
		return err
	}
	usage.MaxBytes = settings.MaxBytes
	writeSpillUsage(out, usage, settings)

	if settings.Compression == appconfig.SpillCompressionOff {
		fmt.Fprintln(out, "tools.spill.compression 为 off：不压缩任何文件，ref 与读取方式保持不变")
		return nil
	}

	// A dry run needs no window: it only reads. --apply takes the same
	// maintenance window as `gc --vacuum`, so no turn writes spill files while
	// the pass rewrites them.
	active := map[string]bool{}
	var window *maintenanceWindow
	if apply {
		if window, err = acquireMaintenanceWindow(filepath.Join(home, "run")); err != nil {
			return err
		}
		defer window.Release()
	} else {
		records, err := personalprocess.List(filepath.Join(home, "run"))
		if err != nil {
			return err
		}
		for _, record := range records {
			active[record.SessionID] = true
		}
	}
	report, err := personal.CompressSpillOutputs(ctx, sessionsDir, personal.SpillCompressOptions{
		OlderThan: settings.CompressAfter, Active: active, Apply: apply,
	})
	if err != nil {
		return err
	}
	writeSpillCompressReport(out, report, settings, apply)
	return nil
}

// writeSpillUsage reports the spill picture of the ledger: how many files,
// their logical and physical size, and whether the quota is exceeded.
func writeSpillUsage(out io.Writer, usage personal.SpillUsage, settings appconfig.SpillSettings) {
	quota := "未设上限（tools.spill.max_bytes=0）"
	if usage.MaxBytes > 0 {
		quota = fmt.Sprintf("上限 %s", humanBytes(usage.MaxBytes))
		if usage.OverQuota() {
			quota += fmt.Sprintf("，已超额 %s", humanBytes(usage.LogicalBytes-usage.MaxBytes))
		} else {
			quota += "，未超额"
		}
	}
	fmt.Fprintf(out, "spill：%d 个会话、%d 个文件（其中已压缩 %d 个）；逻辑 %s，占用 %s；%s\n",
		usage.Sessions, usage.Files, usage.Compressed,
		humanBytes(usage.LogicalBytes), humanBytes(usage.PhysicalBytes), quota)
	if usage.OverQuota() {
		fmt.Fprintf(out, "  超过 tools.spill.max_bytes 时新的 spill 会被拒绝（tools.spill.on_quota=%s），"+
			"输出层只保留头尾；不会自动删除旧输出\n", settings.OnQuota)
	}
}

// writeSpillCompressReport prints what a compression pass planned and did: one
// line per file (refs stay the same, so the list is the audit trail of the
// rewrite) and a summary of the bytes saved.
func writeSpillCompressReport(out io.Writer, report personal.SpillCompressReport, settings appconfig.SpillSettings, apply bool) {
	const listed = 50
	for i, entry := range report.Planned {
		if i == listed {
			fmt.Fprintf(out, "  …另有 %d 个文件未列出\n", len(report.Planned)-listed)
			break
		}
		verb := "将压缩"
		if apply {
			verb = "已压缩"
		}
		fmt.Fprintf(out, "  %s %s（%s → %s）\n", verb, entry.File.Ref(),
			humanBytes(entry.Size), humanBytes(entry.CompressedSize))
	}
	fmt.Fprintf(out, "可压缩（mtime 早于 %s 且会话不活跃）：%d 个，占用 %s → 压缩后约 %s\n",
		settings.CompressAfter, len(report.Planned), humanBytes(report.BytesBefore), humanBytes(report.BytesAfter))
	if report.SkippedYoung > 0 || report.SkippedActive > 0 {
		fmt.Fprintf(out, "跳过：mtime 未超过 compress_after 的 %d 个，会话仍活跃的 %d 个\n",
			report.SkippedYoung, report.SkippedActive)
	}
	if !apply {
		fmt.Fprintf(out, "dry-run；加 --apply 执行压缩（需停止其他 suga 进程，占用 %s → %s，逻辑大小与 ref 不变）\n",
			humanBytes(report.Usage.PhysicalBytes), humanBytes(report.AfterUsage.PhysicalBytes))
		return
	}
	fmt.Fprintf(out, "压缩完成：%d 个文件，占用 %s → %s（逻辑 %s 不变，ref 不变）\n",
		len(report.Planned), humanBytes(report.Usage.PhysicalBytes), humanBytes(report.AfterUsage.PhysicalBytes),
		humanBytes(report.Usage.LogicalBytes))
}
