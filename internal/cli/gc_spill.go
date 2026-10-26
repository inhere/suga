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

// runGCCompressSpill runs `suga data compress-spill`: it reports the spill
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
		fmt.Fprintln(out, cliText(cliTranslator(), "cli.data.spill.off"))
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
	tr := cliTranslator()
	quota := cliText(tr, "cli.data.spill.quota")
	if usage.MaxBytes > 0 {
		quota = cliText(tr, "cli.data.spill.quota_set", map[string]any{"bytes": humanBytes(usage.MaxBytes)})
		if usage.OverQuota() {
			quota += cliText(tr, "cli.data.spill.over", map[string]any{"bytes": humanBytes(usage.LogicalBytes - usage.MaxBytes)})
		} else {
			quota += cliText(tr, "cli.data.spill.under")
		}
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.spill.usage", map[string]any{"sessions": usage.Sessions, "files": usage.Files, "compressed": usage.Compressed, "logical": humanBytes(usage.LogicalBytes), "physical": humanBytes(usage.PhysicalBytes), "quota": quota}))
	if usage.OverQuota() {
		fmt.Fprintf(out, "  %s\n", cliText(tr, "cli.data.spill.over_note", map[string]any{"policy": settings.OnQuota}))
	}
}

// writeSpillCompressReport prints what a compression pass planned and did: one
// line per file (refs stay the same, so the list is the audit trail of the
// rewrite) and a summary of the bytes saved.
func writeSpillCompressReport(out io.Writer, report personal.SpillCompressReport, settings appconfig.SpillSettings, apply bool) {
	tr := cliTranslator()
	const listed = 50
	for i, entry := range report.Planned {
		if i == listed {
			fmt.Fprintf(out, "  %s\n", cliText(tr, "cli.data.spill.more", map[string]any{"count": len(report.Planned) - listed}))
			break
		}
		verb := cliText(tr, "cli.data.spill.will")
		if apply {
			verb = cliText(tr, "cli.data.spill.did")
		}
		fmt.Fprintf(out, "  %s\n", cliText(tr, "cli.data.spill.file", map[string]any{"verb": verb, "ref": entry.File.Ref(),
			"before": humanBytes(entry.Size), "after": humanBytes(entry.CompressedSize)}))
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.spill.available", map[string]any{"age": settings.CompressAfter.String(), "count": len(report.Planned), "before": humanBytes(report.BytesBefore), "after": humanBytes(report.BytesAfter)}))
	if report.SkippedYoung > 0 || report.SkippedActive > 0 {
		fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.spill.skipped", map[string]any{"young": report.SkippedYoung, "active": report.SkippedActive}))
	}
	if !apply {
		fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.spill.dry", map[string]any{"before": humanBytes(report.Usage.PhysicalBytes), "after": humanBytes(report.AfterUsage.PhysicalBytes)}))
		return
	}
	fmt.Fprintf(out, "%s\n", cliText(tr, "cli.data.spill.complete", map[string]any{"count": len(report.Planned), "before": humanBytes(report.Usage.PhysicalBytes), "after": humanBytes(report.AfterUsage.PhysicalBytes), "logical": humanBytes(report.Usage.LogicalBytes)}))
}
