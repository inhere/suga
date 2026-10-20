package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/gookit/goutil/cflag/capp"
)

// The ledger maintenance jobs that used to be switches of `suga data gc` are their
// own subcommands since v0.36: each one has a single job, its own help and no
// mutually exclusive flags. The work itself stays in runGCVacuum (gc_maintenance.go)
// and runGCCompressSpill (gc_spill.go).

// newVacuumCommand builds `suga data vacuum`: rewrite the database file to
// return the space a deletion freed.
func newVacuumCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	cmd := capp.NewCmd("vacuum", "Rewrite the database file to return freed space (needs free disk space and no other suga process)", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga data vacuum")
		}
		return runGCVacuum(ctx, stdout)
	})
	cmd.SetOutput(stderr)
	return cmd
}

// newCompressSpillCommand builds `suga data compress-spill`: gzip the idle
// spill files (dry run unless --apply).
func newCompressSpillCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var apply bool
	cmd := capp.NewCmd("compress-spill", "Compress spill outputs older than tools.spill.compress_after (report only unless --apply)", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga data compress-spill [--apply]")
		}
		return runGCCompressSpill(ctx, stdout, apply)
	})
	cmd.SetOutput(stderr)
	cmd.BoolVar(&apply, "apply", false, "rewrite the idle spill files as .gz for real (default: only report)")
	return cmd
}
