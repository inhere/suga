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
	root.LongHelp = `<ylw>Options for chat</>:
  --resume, -r <session>              Resume a chat
  --model, -r <model_id>              Custom a model for the chat

<info>Examples</>:
  suga                                 Start interactive chat
  suga --resume <session>              Resume a chat (chat options work without "chat")
  suga run --prompt "hello"            Run one turn
  suga config get model                Show the effective model and source
  suga models --json                   List configured models offline
  suga undo <session> --list           List file checkpoints
  suga inspect trace <session> --json  Show the event timeline of a session
  suga data gc --older-than 30d        Remove idle sessions (dry run)
`

	// 绑定 chat/run 的公共参数到 app

	root.AfterFlagParse = func(c *cflag.CFlags) bool {
		if showVersion {
			fmt.Fprintln(c.Output(), build.VersionLine)
			return false
		}
		return true
	}

	root.Add(
		NewPersonalCommand(ctx, "chat", stdin, stdout, stderr),
		NewPersonalCommand(ctx, "run", stdin, stdout, stderr),
		newSendCommand(stdin, stderr),
		newPSCommand(stdout, stderr),
		newSessionsCommand(ctx, stdout, stderr),
		newUndoCommand(ctx, stdin, stdout, stderr),
		newConfigCommand(stdout, stderr),
		newModelsCommand(stdout, stderr),
		newInitCommand(stdout, stderr),
		newVersionCommand(stdout, stderr, build),
		newACPCommand(ctx, stdin, stdout, stderr),
		newInspectCommand(ctx, stdout, stderr),
		newMemoryCommand(ctx, stdout, stderr),
		newDataCommand(ctx, stdin, stdout, stderr, build.Version),
	)
	return root
}

// newInspectCommand builds the `inspect` group: the session diagnosis commands
// (trace, stats, export, manifest) that used to sit at the top level. The group
// only holds subcommands, the leaves keep their own flags.
func newInspectCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	inspect := capp.NewCmd("inspect", "Inspect sessions: trace / stats / export / manifest")
	inspect.SetOutput(stderr)
	inspect.Add(
		newTraceCommand(ctx, stdout, stderr),
		newStatsCommand(ctx, stdout, stderr),
		newExportCommand(ctx, stdout, stderr),
		newManifestCommand(ctx, stdout, stderr),
	)
	return inspect
}

// newDataCommand builds the `data` group: the data maintenance commands (gc,
// bodies, vacuum, compress-spill, backup, restore) that used to sit at the top
// level or as switches of `gc`, plus the read-only `usage` report.
func newDataCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, version string) *capp.Cmd {
	data := capp.NewCmd("data", "Maintain local data: usage / gc / bodies / vacuum / compress-spill / backup / restore")
	data.SetOutput(stderr)
	data.Add(
		newDataGCCommand(ctx, stdout, stderr),
		newToolBodiesCommand(ctx, stdout, stderr),
		newVacuumCommand(ctx, stdout, stderr),
		newCompressSpillCommand(ctx, stdout, stderr),
		newDataUsageCommand(ctx, stdout, stderr),
		newBackupCommand(ctx, stdout, stderr, version),
		newRestoreCommand(ctx, stdin, stdout, stderr, version),
	)
	return data
}
