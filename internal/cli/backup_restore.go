package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/backup"
)

// newBackupCommand registers the archive command. Archive implementation and
// validation live in personal/backup so non-CLI callers use the same format.
func newBackupCommand(ctx context.Context, stdout, stderr io.Writer, version string) *capp.Cmd {
	tr := cliTranslator()
	var output string
	var includeSpill bool
	cmd := capp.NewCmd("backup", "Create a verified suga data backup archive", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) != 0 || strings.TrimSpace(output) == "" {
			return fmt.Errorf("usage: suga data backup --output FILE.suga-backup.zip [--include-spill]")
		}
		home, err := appconfig.ResolveSugaHome()
		if err != nil {
			return err
		}
		// A backup is an online snapshot (VACUUM INTO): chats and runs may keep
		// writing. It only takes the maintenance lock, so it never overlaps a
		// VACUUM, a restore or a GC apply.
		lock, err := acquireMaintenanceLock(home)
		if err != nil {
			return err
		}
		defer lock.Close()
		manifest, err := backup.Create(ctx, backup.Options{Home: home, Output: output, IncludeSpill: includeSpill, SugaVersion: version})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "%s\n", cliText(tr, "cli.backup.done", map[string]any{"files": len(manifest.Files), "tool_files": manifest.ToolBodies.FileCount, "spill": manifest.Spill.Policy, "archive": output}))
		return err
	})
	cmd.SetOutput(stderr)
	cmd.StringVar(&output, "output", "", "write a .suga-backup.zip archive;;o")
	cmd.BoolVar(&includeSpill, "include-spill", false, "include externalised tool output files")
	return cmd
}

func newRestoreCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, version string) *capp.Cmd {
	tr := cliTranslator()
	var dryRun, noAuto bool
	cmd := capp.NewCmd("restore", "Restore a verified suga data backup archive", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) != 0 {
			return fmt.Errorf("usage: suga data restore FILE.suga-backup.zip [--dry-run] [--no-auto-backup]")
		}
		archive := c.Arg("archive").String()
		home, err := appconfig.ResolveSugaHome()
		if err != nil {
			return err
		}
		if dryRun {
			manifest, err := backup.Validate(archive, home)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(stdout, "%s\n", cliText(tr, "cli.backup.valid", map[string]any{"files": len(manifest.Files), "spill": manifest.Spill.Policy}))
			return err
		}
		if noAuto {
			if _, err := fmt.Fprintln(stderr, cliText(tr, "cli.backup.no_auto")); err != nil {
				return err
			}
			line, readErr := bufio.NewReader(stdin).ReadString('\n')
			if readErr != nil && len(line) == 0 {
				return readErr
			}
			if strings.TrimSpace(line) != "YES" {
				return fmt.Errorf("%s", cliText(tr, "cli.backup.cancelled"))
			}
		}
		// Restore replaces the ledger: the same exclusive window as `gc --vacuum`
		// (maintenance lock, no other suga process, every session lock held).
		window, err := acquireMaintenanceWindow(filepath.Join(home, "run"))
		if err != nil {
			return err
		}
		defer window.Release()
		auto := func(path string) error {
			_, err := backup.Create(ctx, backup.Options{Home: home, Output: path, IncludeSpill: true, SugaVersion: version})
			return err
		}
		when, err := backup.Restore(ctx, archive, home, noAuto, auto)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "%s\n", cliText(tr, "cli.backup.restore_done", map[string]any{"when": when})); err != nil {
			return err
		}
		// The session files hold the only copy of a moved tool body, so verify them
		// against the restored ledger before the command reports success.
		report, err := backup.ToolBodyCheck(ctx, home)
		if err != nil {
			return fmt.Errorf("%s", cliText(tr, "cli.backup.done_check", map[string]any{"error": err}))
		}
		if _, err := fmt.Fprintf(stdout, "%s\n", cliText(tr, "cli.backup.check", map[string]any{"checked": report.Checked, "missing": report.Missing, "orphans": report.OrphanRecords, "bytes": report.OrphanBytes})); err != nil {
			if err != nil {
				return err
			}
		}
		if !report.OK() {
			for _, problem := range report.Problems {
				if _, err := fmt.Fprintf(stderr, "%s\n", cliText(tr, "cli.backup.missing", map[string]any{"problem": problem})); err != nil {
					return err
				}
			}
		}
		return nil
	})
	cmd.SetOutput(stderr)
	cmd.AddArg("archive", "the .suga-backup.zip archive", true)
	cmd.BoolVar(&dryRun, "dry-run", false, "validate without writing anything")
	cmd.BoolVar(&noAuto, "no-auto-backup", false, "skip automatic pre-restore backup after explicit YES confirmation")
	return cmd
}
