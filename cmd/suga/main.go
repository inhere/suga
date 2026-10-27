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
	readOnlyInspect := readOnlyInspectCommand(args)
	// Version and help are pure queries: neither may create the user's home.
	if readOnlyInspect {
		// A read-only inspect command (manifest) reads the configuration in place
		// instead of bootstrapping the home: a missing .env reads as empty and
		// nothing is written under SUGA_HOME (no config bootstrap, no log file).
		if err := appconfig.Load(); err != nil {
			fmt.Fprintln(os.Stderr, "load config:", err)
			os.Exit(1)
		}
	} else if !bootstrapExempt(args) && !isInitCommand(args) {
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
	if !readOnlyInspect && !bootstrapExempt(args) && !isInitCommand(args) {
		if err := wiring.SetupLogging(); err != nil {
			fmt.Fprintln(os.Stderr, "load log config:", err)
			os.Exit(1)
		}
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

// bootstrapExempt reports the pure-query invocations that must not create the
// user's home: help and version commands, plus a command group without a
// subcommand, which only prints its help (suga config, suga inspect, suga session).
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
	// A command group without a subcommand only prints its help (suga config,
	// suga inspect, suga session).
	if len(args) == 1 && (args[0] == "config" || args[0] == "inspect" || args[0] == "session") {
		return true
	}
	return args[0] == "help" || args[0] == "version" || args[0] == "--version" || args[0] == "-V"
}

// readOnlyInspectCommand reports `suga inspect manifest` (alias "info") and
// `suga inspect rules <approval|output|nudge|lang>`: pure queries that must not
// write anything under SUGA_HOME, so they skip the config bootstrap and the log
// file and read the configuration in place (a missing .env reads as empty; the
// built-in skills are enumerated from the embedded resources, see
// buildReadOnlySkillRegistry).
func readOnlyInspectCommand(args []string) bool {
	if len(args) <= 1 || args[0] != "inspect" {
		return false
	}
	if args[1] == "manifest" || args[1] == "info" {
		return true
	}
	// The rule inspector needs no model or sessions either: a bare kind
	// argument is enough to know it only reads the rule files.
	switch {
	case len(args) > 2 && args[1] == "rules":
		switch args[2] {
		case "approval", "output", "nudge", "lang":
			return true
		}
	}
	return false
}

func isInitCommand(args []string) bool {
	return len(args) > 0 && args[0] == "init"
}

// exitReport turns the command error into the stderr line and exit code. An
// error caused by Ctrl-C/SIGTERM (ctx canceled by the signal) reads "已中断"
// instead of "context canceled" and exits 130, the shell convention for SIGINT.
// The line goes through cli.ErrorText: an error that knows the process language
// renders its human text there, while machine lines keep err.Error().
func exitReport(ctx context.Context, err error) (string, int) {
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return "已中断", 130
	}
	return cli.ErrorText(err, cli.Translator()), cli.ExitCode(err)
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
