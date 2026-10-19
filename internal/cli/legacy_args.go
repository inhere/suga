package cli

import "strings"

// This file keeps the command line compatible across the v0.36 command-group
// move (design 2026-10-19-cli-command-groups-design.md). Two rewrites happen
// before the command tree parses anything:
//
//  1. a top-level command that moved into the `inspect` or `data` group keeps
//     working for two more minor versions and says so once on stderr;
//  2. a first argument that is an option (not -h/--help/-V/--version) means
//     `suga chat …`, so `suga --resume <id>` behaves like `suga chat --resume <id>`.
//
// The rewrite lives in internal/cli (not in cmd/suga) so tests can call it, and
// cmd/suga runs it before bootstrapExempt, which reads the first argument.

// legacyCommandRemoval is the version the rewritten forms are dropped in. It is
// part of the notice the user sees, so keep it in sync with the design.
const legacyCommandRemoval = "v0.38"

// legacyCommands maps a top-level command that moved into a group to its new
// group name. The whole first argument is replaced, the rest stays as typed.
var legacyCommands = map[string]string{
	"trace":    "inspect",
	"stats":    "inspect",
	"export":   "inspect",
	"manifest": "inspect",
	"backup":   "data",
	"restore":  "data",
}

// legacyGCForms maps the `gc` maintenance switch that selects a subcommand to
// that subcommand. The switch is dropped from the arguments: the subcommand
// carries it. The order of the checks is the order the old `suga data gc` used to
// pick its action (shrink-fts, vacuum, compress-spill, then the plain form).
var legacyGCForms = []struct {
	flag string
	sub  string
}{
	{"--shrink-fts", "shrink-fts"},
	{"--vacuum", "vacuum"},
	{"--compress-spill", "compress-spill"},
}

// legacySilentCommands are moved commands whose rewrite says nothing: `trace`
// is used a lot (including inside chat hints), so the compatibility notice would
// only be noise (design Q2).
var legacySilentCommands = map[string]bool{"trace": true}

// rootOnlyFlags are the options the root command itself owns. A first argument
// that is one of them is not a chat option, so no `chat` is inserted.
var rootOnlyFlags = map[string]bool{
	"-h": true, "--help": true, "-V": true, "--version": true,
}

// RewriteLegacyArgs rewrites the arguments of a suga invocation to the v0.36
// command tree. It returns the arguments to run and, when an old top-level
// command was used, the one-line notice to print on stderr (stdout is left
// alone, so scripts that parse it are unaffected). `suga inspect trace …` is rewritten
// silently (design Q2).
func RewriteLegacyArgs(args []string) (out []string, notice string) {
	if len(args) == 0 {
		return args, ""
	}
	if rewritten, note, ok := rewriteTopLevelCommand(args); ok {
		return rewritten, note
	}
	// No subcommand: an option in front is a chat option (design D6).
	if strings.HasPrefix(args[0], "-") && !rootOnlyFlags[args[0]] {
		return append([]string{"chat"}, args...), ""
	}
	return args, ""
}

// rewriteTopLevelCommand handles the commands that moved into a group; ok is
// false when the first argument is not one of them.
func rewriteTopLevelCommand(args []string) (out []string, notice string, ok bool) {
	old := args[0]
	if old == "gc" {
		return rewriteGCArgs(args)
	}
	group, moved := legacyCommands[old]
	if !moved {
		return nil, "", false
	}
	out = append([]string{group, old}, args[1:]...)
	if legacySilentCommands[old] {
		return out, "", true
	}
	return out, noticeFor(old, group+" "+old), true
}

// rewriteGCArgs splits the old multi-purpose `suga data gc` into `suga data gc` and
// the maintenance subcommands that own the flags it used to carry.
func rewriteGCArgs(args []string) (out []string, notice string, ok bool) {
	for _, form := range legacyGCForms {
		if !containsArg(args[1:], form.flag) {
			continue
		}
		// Drop the switch that selected the action; every other flag and value
		// keeps its place, so `gc --shrink-fts --batch 100` becomes
		// `data shrink-fts --batch 100`.
		out = append([]string{"data", form.sub}, dropArg(args[1:], form.flag)...)
		return out, noticeFor("gc "+form.flag, "data "+form.sub), true
	}
	out = append([]string{"data", "gc"}, args[1:]...)
	return out, noticeFor("gc", "data gc"), true
}

// noticeFor formats the transition notice shown for an old command form.
func noticeFor(old, new string) string {
	return "提示：suga " + old + " 已改为 suga " + new + "，旧写法将在 " + legacyCommandRemoval + " 移除"
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

// dropArg returns the arguments without the first exact match of flag.
func dropArg(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	dropped := false
	for _, arg := range args {
		if !dropped && arg == flag {
			dropped = true
			continue
		}
		out = append(out, arg)
	}
	return out
}
