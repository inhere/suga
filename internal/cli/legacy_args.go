package cli

import (
	"fmt"
	"strings"

	"github.com/gookit/goutil/cflag/capp"
)

// This file carries what is left of the pre-v0.36 command line (design
// 2026-10-19-cli-command-groups-design.md):
//
//  1. a first argument that is an option (not -h/--help/-V/--version) means
//     `suga chat …`, so `suga --resume <id>` behaves like `suga chat --resume
//     <id>`. That binding is a permanent feature (CHANGELOG v0.36), not a
//     compatibility shim;
//  2. the top-level commands that moved into the `inspect` or `data` group were
//     rewritten for two minor versions and are removed in v0.38. They now fail
//     as ordinary unknown commands; legacyCommandHints only feeds the hint such
//     an error carries, nothing is executed from the old spelling.
//
// NormalizeArgs lives in internal/cli (not in cmd/suga) so tests can call it,
// and cmd/suga runs it before bootstrapExempt, which reads the first argument.

// legacyCommandRemoval is the version the old top-level forms were dropped in.
// It is part of the hint the user sees, so keep it in sync with the design.
const legacyCommandRemoval = "v0.38"

// legacyCommandHints maps an old top-level command to the command path that
// replaces it. The whole first argument is replaced, the rest stays as typed.
// The table is only read to build the hint of the unknown-command error.
var legacyCommandHints = map[string]string{
	"trace":    "inspect trace",
	"stats":    "inspect stats",
	"export":   "inspect export",
	"manifest": "inspect manifest",
	"backup":   "data backup",
	"restore":  "data restore",
}

// dataCommandRemoval is the release the one-time migration forms of `data` were
// dropped in (the notices v0.39.0 wrote promised v0.41.0, user 2026-10-20).
const dataCommandRemoval = "v0.41.0"

// removedDataForms maps a removed `data` form to what replaces it: the command
// that took a renamed form over, "" for the one-time migrations that have no
// successor. Like legacyCommandHints it only feeds the hint of the
// unknown-command error; nothing is executed from the old spelling.
var removedDataForms = map[string]string{
	"tool-bodies":         "suga data bodies",
	"dedupe-tool-results": "",
	"shrink-fts":          "",
}

// removedBodyJobs are the removed one-time flags of `data bodies`.
var removedBodyJobs = []string{"--slim", "--rename-files"}

// legacyGCForms maps the `gc` maintenance switch that used to select a
// subcommand to that subcommand. The order of the checks is the order the old
// `suga data gc` used to pick its action (shrink-fts, vacuum, compress-spill,
// then the plain form).
var legacyGCForms = []struct {
	flag string
	sub  string
}{
	{"--shrink-fts", "data shrink-fts"},
	{"--vacuum", "data vacuum"},
	{"--compress-spill", "data compress-spill"},
}

// rootOnlyFlags are the options the root command itself owns. A first argument
// that is one of them is not a chat option, so no `chat` is inserted.
var rootOnlyFlags = map[string]bool{
	"-h": true, "--help": true, "-V": true, "--version": true,
}

// NormalizeArgs turns an invocation whose first argument is a chat option into
// the explicit `suga chat …` form (design D6). Old top-level commands are left
// alone: they fail as unknown commands, see RunArgs.
func NormalizeArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	// No subcommand: an option in front is a chat option (design D6).
	if strings.HasPrefix(args[0], "-") && !rootOnlyFlags[args[0]] {
		return append([]string{"chat"}, args...)
	}
	return args
}

// RunArgs runs the command tree with args. A pre-v0.36 top-level command form
// (removed in v0.38) fails as an ordinary unknown command; the returned error
// then also names the new spelling, so the user is not left guessing.
func RunArgs(app *capp.App, args []string) error {
	err := app.RunWithArgs(args)
	if err == nil {
		return nil
	}
	hint := legacyCommandHint(args)
	if hint == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, hint)
}

// legacyCommandHint names the new spelling of an old top-level command form, or
// returns "" when the first argument is not one of them.
func legacyCommandHint(args []string) string {
	if len(args) == 0 {
		return ""
	}
	old := args[0]
	if old == "data" {
		return removedDataHint(args)
	}
	if old == "gc" {
		// The switch that selected the action decides which subcommand owns it.
		for _, form := range legacyGCForms {
			if containsArg(args[1:], form.flag) {
				return hintFor("gc "+form.flag, form.sub)
			}
		}
		return hintFor("gc", "data gc")
	}
	moved, ok := legacyCommandHints[old]
	if !ok {
		return ""
	}
	return hintFor(old, moved)
}

// removedDataHint names the successor of a removed `data` form, or "" when the
// arguments do not spell one. A one-time migration has no successor: its hint
// says what it did and that a pre-v0.36 ledger needs the v0.40 release.
func removedDataHint(args []string) string {
	if len(args) < 2 {
		return ""
	}
	form := args[1]
	replacement, ok := removedDataForms[form]
	if !ok && form == "bodies" {
		for _, flag := range removedBodyJobs {
			if containsArg(args[2:], flag) {
				form, ok = "bodies "+flag, true
				break
			}
		}
	}
	if !ok {
		return ""
	}
	hint := "提示：suga data " + form + " 已在 " + dataCommandRemoval + " 移除"
	if replacement != "" {
		return hint + "，改用 " + replacement
	}
	return hint + "；它只用于把 v0.35 及更早的库迁移到当前格式，若需迁移旧库请用 v0.40"
}

// hintFor formats the hint shown when an old command form is typed.
func hintFor(old, new string) string {
	return "提示：suga " + old + " 已改为 suga " + new + "，旧写法已在 " + legacyCommandRemoval + " 移除"
}

// containsArg reports whether the exact flag is among the arguments.
func containsArg(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}
