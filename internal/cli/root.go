// Package cli defines the suga command tree; cmd/suga only assembles and runs it.
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/gookit/goutil/cflag"
	"github.com/gookit/goutil/cflag/capp"
	"github.com/inhere/suga/internal/wiring"
)

// Build carries the binary's version metadata injected by cmd/suga.
type Build struct {
	// Version is the semantic version shown by the root app.
	Version string
	// VersionLine is the full "suga <version> (<commit>)" line printed by
	// `suga version` and --version.
	VersionLine string
}

// New builds the root command with all subcommands registered.
func New(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, build Build) *capp.App {
	var showVersion bool
	wiring.SetBuildVersion(build.Version) // shown by the manifest
	root := capp.NewApp()
	root.Name = "suga"
	root.Desc = "Personal agent with CLI and ACP interfaces"
	root.Version = build.Version
	root.SetOutput(stdout)
	root.BoolVar(&showVersion, "version", false, "show version then exit;false;V")
	root.LongHelp = `Examples:
  suga                         Start interactive chat
  suga run --prompt "hello"    Run one turn
  suga acp                     Serve ACP over stdio
  suga init                    Create the onboarding config
  suga config get model        Show the effective model and source
  suga models --json           List configured models offline
  suga undo <session> --list   List file checkpoints
  suga trace <session> --json  Show the event timeline of a session
`

	root.AfterFlagParse = func(c *cflag.CFlags) bool {
		if showVersion {
			fmt.Fprintln(c.Output(), build.VersionLine)
			return false
		}
		return true
	}

	root.Add(
		NewPersonalCommand(ctx, "run", stdin, stdout, stderr),
		NewPersonalCommand(ctx, "chat", stdin, stdout, stderr),
		newACPCommand(ctx, stdin, stdout, stderr),
		newInitCommand(stdout, stderr),
		newConfigCommand(stdout, stderr),
		newModelsCommand(stdout, stderr),
		newVersionCommand(stdout, stderr, build),
		newPSCommand(stdout, stderr),
		newSendCommand(stdin, stderr),
		newTraceCommand(ctx, stdout, stderr),
		newSessionsCommand(ctx, stdout, stderr),
		newStatsCommand(ctx, stdout, stderr),
		newManifestCommand(ctx, stdout, stderr),
		newGCCommand(ctx, stdout, stderr),
		newBackupCommand(ctx, stdout, stderr, build.Version),
		newRestoreCommand(ctx, stdin, stdout, stderr, build.Version),
		newUndoCommand(ctx, stdin, stdout, stderr),
		newExportCommand(ctx, stdout, stderr),
	)
	return root
}
