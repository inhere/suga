package chat

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/inhere/suga/internal/i18n"
)

// This file is the one place the local chat commands are described: the :help
// listing, the detailed usage that ":<命令> -h" prints (":help <命令>" is the
// same text) and the Tab completion of a command's options all read the table
// below, so a new option or a reworded usage is one edit.
//
// A command answers ":<命令> -h"/"--help" with its Usage, and a command without
// arguments answers any other argument the same way instead of dropping it
// (commandUsageReply).

// chatCommandSpec is one entry of the local command table.
type chatCommandSpec struct {
	// Names are the spellings the command answers to. :quit and :exit share one
	// entry, so :help lists them on one line and both print the same usage.
	Names []string
	// SummaryKey is the catalog key of the one line :help prints.
	SummaryKey string
	// UsageKey is the catalog key of the detailed text ":<命令> -h" prints: what
	// the command does, its arguments and one or two examples. Its catalog value
	// writes the line breaks as the literal \n.
	UsageKey string
	// TakesArgs reports whether the command accepts arguments at all. One that
	// does not answers an argument with its usage rather than ignoring it.
	TakesArgs bool
	// Options are the arguments Tab offers after the command (":queue " + Tab).
	Options []string
}

// chatCommandSpecs lists every local command: its order is the order of :help
// and of the Tab completion candidates.
var chatCommandSpecs = []chatCommandSpec{
	{
		Names:      []string{":session"},
		SummaryKey: "cmd.summary.session",
		UsageKey:   "cmd.usage.session",
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":compact"},
		SummaryKey: "cmd.summary.compact",
		UsageKey:   "cmd.usage.compact",
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":add-dir"},
		SummaryKey: "cmd.summary.add-dir",
		UsageKey:   "cmd.usage.add-dir",
		TakesArgs:  true,
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":dirs"},
		SummaryKey: "cmd.summary.dirs",
		UsageKey:   "cmd.usage.dirs",
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":rm-dir"},
		SummaryKey: "cmd.summary.rm-dir",
		UsageKey:   "cmd.usage.rm-dir",
		TakesArgs:  true,
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":diff"},
		SummaryKey: "cmd.summary.diff",
		UsageKey:   "cmd.usage.diff",
		TakesArgs:  true,
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":code"},
		SummaryKey: "cmd.summary.code",
		UsageKey:   "cmd.usage.code",
		TakesArgs:  true,
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":image"},
		SummaryKey: "cmd.summary.image",
		UsageKey:   "cmd.usage.image",
		TakesArgs:  true,
		Options:    []string{"clear", "-h"},
	},
	{
		Names:      []string{":queue"},
		SummaryKey: "cmd.summary.queue",
		UsageKey:   "cmd.usage.queue",
		TakesArgs:  true,
		Options:    []string{"-d", "clear", "-h"},
	},
	{
		Names:      []string{":checkpoints"},
		SummaryKey: "cmd.summary.checkpoints",
		UsageKey:   "cmd.usage.checkpoints",
		TakesArgs:  true,
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":undo"},
		SummaryKey: "cmd.summary.undo",
		UsageKey:   "cmd.usage.undo",
		TakesArgs:  true,
		Options:    []string{"force", "-h"},
	},
	{
		Names:      []string{":help"},
		SummaryKey: "cmd.summary.help",
		UsageKey:   "cmd.usage.help",
		TakesArgs:  true,
	},
	{
		Names:      []string{":model"},
		SummaryKey: "cmd.summary.model",
		UsageKey:   "cmd.usage.model",
		TakesArgs:  true,
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":usage"},
		SummaryKey: "cmd.summary.usage",
		UsageKey:   "cmd.usage.usage",
		Options:    []string{"-h"},
	},
	{
		Names:      []string{":skills"},
		SummaryKey: "cmd.summary.skills",
		UsageKey:   "cmd.usage.skills",
		TakesArgs:  true,
		Options:    []string{"--only-names", "-n", "-h"},
	},
	{
		Names:      []string{":context"},
		SummaryKey: "cmd.summary.context",
		UsageKey:   "cmd.usage.context",
		TakesArgs:  true,
		Options:    []string{"--files", "-h"},
	},
	{
		Names:      []string{":tasks"},
		SummaryKey: "cmd.summary.tasks",
		UsageKey:   "cmd.usage.tasks",
		TakesArgs:  true,
		Options:    []string{"-a", "-k", "-h"},
	},
	{
		Names:      []string{":quit", ":exit"},
		SummaryKey: "cmd.summary.quit",
		UsageKey:   "cmd.usage.quit",
		Options:    []string{"-h"},
	},
}

// chatHelpKeysKey and chatHelpHintKey are the catalog keys of the fixed tail of
// :help: the "!" prefixes and the keys. The catalog writes the two lines with a
// literal \n, so :help stays one text.
const (
	chatHelpKeysKey = "cmd.help.keys"
	chatHelpHintKey = "cmd.help.hint"
)

// commandLinePattern matches the lines that are commands rather than ordinary
// text: a ":" followed by a lowercase command word and nothing else. It is
// what keeps a colon at the start of a message sendable —
// ":)" (an emoticon), ": 说明" (a colon and a space), ":中文" and URLs such as
// "::1" all fail it and go to the model, only a single "word" token after the
// colon is treated as an attempted command (suga-okbt).
var commandLinePattern = regexp.MustCompile(`^:[a-z][a-z0-9-]*$`)

// unknownCommandSuggestions is how many near misses a hint names.
const unknownCommandSuggestions = 3

// unknownCommandHint is the answer to a line that looks like a command but
// names none (":sessio"): chat prints it instead of sending the line to the
// model, so a typo does not become a message. An empty answer means the line is
// no attempted command (see commandLinePattern) or names one.
func unknownCommandHint(tr i18n.Translator, line string) string {
	if !commandLinePattern.MatchString(line) {
		return ""
	}
	if _, ok := lookupCommand(line); ok {
		return ""
	}
	suggestions := similarCommands(line)
	if len(suggestions) == 0 {
		return tr.T("cmd.unknown.none", map[string]any{"name": line})
	}
	return tr.T("cmd.unknown.suggest", map[string]any{
		"name":   line,
		"first":  suggestions[0],
		"others": strings.Join(suggestions, "、"),
	})
}

// similarCommands ranks the commands of the table against name by edit
// distance (closest first, at most unknownCommandSuggestions). A command whose
// name is this close is what the person most likely meant; nothing within
// reach means the hint only points at :help.
func similarCommands(name string) []string {
	type candidate struct {
		cmd  string
		dist int
	}
	var ranked []candidate
	for _, cmd := range chatCommands {
		dist := editDistance(name, cmd)
		if dist > suggestionDistance(cmd) {
			continue
		}
		ranked = append(ranked, candidate{cmd: cmd, dist: dist})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].dist != ranked[j].dist {
			return ranked[i].dist < ranked[j].dist
		}
		return ranked[i].cmd < ranked[j].cmd
	})
	out := make([]string, 0, unknownCommandSuggestions)
	for _, c := range ranked {
		if len(out) == unknownCommandSuggestions {
			break
		}
		out = append(out, c.cmd)
	}
	return out
}

// suggestionDistance is how far a typo may be from a command name to count as
// one: one edit for short names (":dirs"), two for longer ones ("contxt").
func suggestionDistance(cmd string) int {
	name := strings.TrimPrefix(cmd, ":")
	if len(name) <= 5 {
		return 1
	}
	return 2
}

// editDistance is the Levenshtein distance of a and b over runes.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// chatCommands is every command name in table order (Tab completion).
var chatCommands = chatCommandNames()

// dirCommandHelp is the one-line reminder the added-directory commands print
// inline (a malformed :add-dir/:rm-dir, the :dirs footer); the detailed usage of
// each of them is its table entry.
func dirCommandHelp(tr i18n.Translator) string { return tr.T("cmd.dirs.help") }

// chatCommandNames collects every spelling of the table in order.
func chatCommandNames() []string {
	names := make([]string, 0, len(chatCommandSpecs)+2)
	for _, spec := range chatCommandSpecs {
		names = append(names, spec.Names...)
	}
	return names
}

// lookupCommand finds the table entry of a command name; ":queue" and "queue"
// name the same command.
func lookupCommand(name string) (chatCommandSpec, bool) {
	name = ":" + strings.TrimPrefix(strings.TrimSpace(name), ":")
	for _, spec := range chatCommandSpecs {
		for _, candidate := range spec.Names {
			if candidate == name {
				return spec, true
			}
		}
	}
	return chatCommandSpec{}, false
}

// commandUsageText is the detailed usage of a command. An unknown name says so
// instead of printing nothing, since silence would read as if it had run.
func commandUsageText(tr i18n.Translator, name string) string {
	if spec, ok := lookupCommand(name); ok {
		return tr.T(spec.UsageKey)
	}
	return tr.T("cmd.unknown.command", map[string]any{"name": name})
}

// commandAvailable reports whether chat runs a command in this process: the
// added-directory commands need the session's handle, which run, ACP and a chat
// without --add-dir do not have.
func commandAvailable(name string, dirsAvailable bool) bool {
	switch name {
	case addDirCmd, dirsCmd, rmDirCmd:
		return dirsAvailable
	}
	return true
}

// commandUsageReply is the text chat answers line with instead of running a
// command: a help ask (":queue -h", ":queue --help") shows that command's usage,
// and an argument a command cannot take shows the usage after naming the
// argument. An empty answer means line is a command to run (or no command at
// all), so chat goes on with it.
func commandUsageReply(tr i18n.Translator, line string, dirsAvailable bool) string {
	name, arg := splitCommand(line)
	spec, ok := lookupCommand(name)
	if !ok || !commandAvailable(name, dirsAvailable) {
		return ""
	}
	fields := strings.Fields(arg)
	if len(fields) > 0 && (fields[0] == "-h" || fields[0] == "--help") {
		return tr.T(spec.UsageKey)
	}
	if arg != "" && !spec.TakesArgs {
		return tr.T("cmd.unknown.arg.usage", map[string]any{"arg": arg, "usage": tr.T(spec.UsageKey)})
	}
	return ""
}

// chatHelpFor answers :help: no argument lists every command, a command name
// shows that command's usage and anything else names the unknown command.
func chatHelpFor(tr i18n.Translator, arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return buildChatHelp(tr)
	}
	if _, ok := lookupCommand(arg); ok {
		return commandUsageText(tr, arg)
	}
	return tr.T("cmd.unknown.command", map[string]any{"name": arg})
}

// buildChatHelp renders :help: one line per command of the table, the fixed key
// lines and where the detail of a command is.
func buildChatHelp(tr i18n.Translator) string {
	var b strings.Builder
	for _, spec := range chatCommandSpecs {
		fmt.Fprintf(&b, "%s %s\n", strings.Join(spec.Names, "/"), tr.T(spec.SummaryKey))
	}
	b.WriteString(tr.T(chatHelpKeysKey))
	b.WriteByte('\n')
	b.WriteString(tr.T(chatHelpHintKey))
	return b.String()
}

// commandOptions lists the arguments Tab offers after a command: its own
// options and -h. :help instead completes the names of the commands.
func commandOptions(name string) []string {
	if name == ":help" {
		return chatCommands
	}
	spec, ok := lookupCommand(name)
	if !ok {
		return nil
	}
	return spec.Options
}
