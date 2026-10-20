// Command suga runs the personal agent through CLI or ACP stdio.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/inhere/suga/internal/cli"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/wiring"
)

var (
	Version   = "dev"
	GitCommit string
	BuildDate string
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"chat"}
	}
	// Accept chat options without "chat" before anything reads the first
	// argument (bootstrapExempt does). An old top-level command name is no
	// longer rewritten: it fails as unknown and the error names the new form.
	args = cli.NormalizeArgs(args)
	// Version and help are pure queries: neither may create the user's home.
	if !bootstrapExempt(args) && !isInitCommand(args) {
		result, err := appconfig.EnsureHome(appconfig.BootstrapOptions{})
		if err != nil {
			fmt.Fprintln(os.Stderr, "initialize suga home:", err)
			os.Exit(cli.ExitCode(err))
		}
		if err := appconfig.WriteBootstrapNotice(os.Stderr, result); err != nil {
			fmt.Fprintln(os.Stderr, "write initialization notice:", err)
			os.Exit(1)
		}
		if err := appconfig.Load(); err != nil {
			fmt.Fprintln(os.Stderr, "load config:", err)
			os.Exit(1)
		}
		// A deprecated key the config file still sets is reported once, on stderr.
		for _, notice := range appconfig.DeprecatedKeyNotices() {
			fmt.Fprintln(os.Stderr, notice)
		}
	}
	if !bootstrapExempt(args) && !isInitCommand(args) {
		wiring.SetupLogging()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	app := cli.New(ctx, os.Stdin, os.Stdout, os.Stderr, cli.Build{Version: Version, VersionLine: formatVersion(Version, GitCommit)})
	if err := cli.RunArgs(app, args); err != nil {
		msg, code := exitReport(ctx, err)
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(code)
	}
}

func bootstrapExempt(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	// A command group without a subcommand only prints its help (suga config).
	if len(args) == 1 && args[0] == "config" {
		return true
	}
	return args[0] == "help" || args[0] == "version" || args[0] == "--version" || args[0] == "-V"
}

func isInitCommand(args []string) bool {
	return len(args) > 0 && args[0] == "init"
}

// exitReport turns the command error into the stderr line and exit code. An
// error caused by Ctrl-C/SIGTERM (ctx canceled by the signal) reads "已中断"
// instead of "context canceled" and exits 130, the shell convention for SIGINT.
func exitReport(ctx context.Context, err error) (string, int) {
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return "已中断", 130
	}
	return err.Error(), cli.ExitCode(err)
}

func formatVersion(version, commit string) string {
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		commit = "dev"
	}
	return "suga " + version + " (" + commit + ")"
}
