package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var chatCommands = []string{":session", ":compact", ":add-dir", ":dirs", ":rm-dir", ":diff", ":code", ":image", ":queue", ":checkpoints", ":undo", ":help", ":model", ":usage", ":tasks", ":quit", ":exit"}

// installCompletion wires the line editor's callback to local chat commands and
// directory arguments. Completion only acts at the command line and never on a
// delivered message or an approval answer.
func installCompletion(source *chatInputSource, dirs SessionDirs, modelRefs ...[]string) {
	if source.terminal == nil {
		return
	}
	source.terminal.AutoCompleteCallback = func(line string, pos int, key rune) (string, int, bool) {
		// Tab during a turn queues the line as the turn after it instead of
		// inserting it into the running one (followup.go): it is taken, so the
		// editor drops it from the input line, and chat has shown it as queued.
		if key == '\t' && pos == len(line) && source.tabFollowUp(line) {
			return "", 0, true
		}
		source.mu.Lock()
		asking := source.readMode == modeAsk
		source.mu.Unlock()
		if asking || key != '\t' || pos != len(line) || !strings.HasPrefix(line, ":") {
			return "", 0, false
		}
		name, arg := splitCommand(line)
		if !strings.ContainsAny(line, " \t") {
			var matches []string
			for _, command := range chatCommands {
				if strings.HasPrefix(command, line) {
					matches = append(matches, command)
				}
			}
			return completeCandidates(source, line, matches)
		}
		if name == ":model" {
			var refs []string
			if len(modelRefs) > 0 {
				refs = modelRefs[0]
			}
			matches := make([]string, 0, len(refs))
			for _, ref := range refs {
				candidate := name + " " + ref
				if strings.HasPrefix(candidate, line) {
					matches = append(matches, candidate)
				}
			}
			return completeCandidates(source, line, matches)
		}
		if name == tasksCmd {
			// The :tasks options; a task id is not offered (the manager's ids are
			// the session's own, and :tasks lists them).
			var matches []string
			for _, option := range []string{"-a", "-h", "-k"} {
				candidate := name + " " + option
				if strings.HasPrefix(candidate, line) {
					matches = append(matches, candidate)
				}
			}
			return completeCandidates(source, line, matches)
		}
		if dirs == nil || (name != addDirCmd && name != rmDirCmd) {
			return "", 0, false
		}
		return completePath(source, dirs.Workspace(), line, name, arg)
	}
}

func completeCandidates(source *chatInputSource, line string, matches []string) (string, int, bool) {
	if len(matches) == 0 {
		return "", 0, false
	}
	if len(matches) > 1 {
		_, _ = fmt.Fprintln(source, strings.Join(matches, "  "))
		return line, len(line), true
	}
	return matches[0], len(matches[0]), true
}

func completePath(source *chatInputSource, workspace, line, name, arg string) (string, int, bool) {
	base := arg
	prefix := ""
	if i := strings.LastIndexAny(arg, `/\\`); i >= 0 {
		prefix, base = arg[:i+1], arg[i+1:]
	}
	dir := prefix
	if dir == "" {
		dir = "."
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(workspace, dir)
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, false
	}
	var matches []string
	for _, item := range items {
		if item.IsDir() && strings.HasPrefix(item.Name(), base) {
			matches = append(matches, item.Name())
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "", 0, false
	}
	sep := string(filepath.Separator)
	completedPrefix := prefix
	if !filepath.IsAbs(completedPrefix) {
		completedPrefix = filepath.Join(workspace, completedPrefix)
	}
	if !strings.HasSuffix(completedPrefix, sep) {
		completedPrefix += sep
	}
	if len(matches) > 1 {
		paths := make([]string, len(matches))
		for i, match := range matches {
			paths[i] = completedPrefix + match + sep
		}
		_, _ = fmt.Fprintln(source, strings.Join(paths, "  "))
		return line, len(line), true
	}
	completed := name + " " + completedPrefix + matches[0] + sep
	return completed, len(completed), true
}
