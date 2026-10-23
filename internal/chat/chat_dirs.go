package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/adddir"
)

// SessionDirs is the session's added-directory handle (--add-dir): chat's
// :add-dir, :dirs and :rm-dir commands and the outside-directory authorization
// both go through it, so the session metadata (add_dirs) and the set the tools
// read stay in step. The CLI owns the implementation (wiring.SessionAddDirs);
// nil (run, ACP, a test without one) leaves the commands out.
type SessionDirs interface {
	// Workspace is the workspace root the file tools are confined to; List
	// returns it as its first entry.
	Workspace() string
	// List returns the directories the session may reach: the workspace first,
	// then the added ones in order, each with the mode in effect and where it
	// came from (an adddir.Source* value; "" when unknown).
	List(ctx context.Context) ([]adddir.Dir, error)
	// Add authorizes one value ("<absolute directory>[:ro|:rw]", default :ro)
	// for this session and reports the entry that took effect; source says who
	// asked (adddir.SourceSession for a typed command).
	Add(ctx context.Context, value, source string) (adddir.Dir, error)
	// Remove revokes a directory (a trailing :ro/:rw, as :dirs prints it, is
	// accepted and ignored) and reports whether it was in the set.
	Remove(ctx context.Context, value string) (bool, error)
}

// The local chat commands that show or change the session's added directories.
// Only a line typed at this terminal runs them (see Run): text delivered by
// `suga send` or a job runner is always an ordinary message, so a delivered
// ":add-dir /x" can never widen what the session may reach.
const (
	addDirCmd = ":add-dir"
	dirsCmd   = ":dirs"
	rmDirCmd  = ":rm-dir"
)

// isDirCommand reports whether line names one of the added-directory commands.
func isDirCommand(line string) bool {
	name := line
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		name = line[:i]
	}
	switch name {
	case addDirCmd, dirsCmd, rmDirCmd:
		return true
	}
	return false
}

// runDirCommand runs one added-directory command of the local keyboard: the
// path of :add-dir is authorized for the session at once (the tools read the
// changed set on their next call) and :rm-dir revokes one. Everything is
// printed as the answer to the typed line; the command never starts a turn.
func (s *chatState) runDirCommand(line string) {
	name, arg := splitCommand(line)
	switch name {
	case addDirCmd:
		if arg == "" {
			fmt.Fprintln(s.stderr, dirCommandHelp)
			return
		}
		dir, err := s.dirs.Add(s.ctx, arg, adddir.SourceSession)
		if err != nil {
			fmt.Fprintln(s.stderr, formatCommandError(addDirCmd, err))
			fmt.Fprintln(s.stderr, dirCommandHelp)
			return
		}
		fmt.Fprintf(s.source, "已授权：%s（%s）· 本会话立即生效，续接沿用\n", dir.Path, modeLabel(dir.Writable))
	case rmDirCmd:
		if arg == "" {
			fmt.Fprintln(s.stderr, dirCommandHelp)
			return
		}
		removed, err := s.dirs.Remove(s.ctx, arg)
		if err != nil {
			fmt.Fprintln(s.stderr, formatCommandError(rmDirCmd, err))
			return
		}
		if !removed {
			fmt.Fprintf(s.stderr, "%s：%s 不在附加目录中（:dirs 可查看）\n", rmDirCmd, adddir.Expand(arg))
			return
		}
		fmt.Fprintf(s.source, "已撤销：%s\n", adddir.Expand(arg))
	case dirsCmd:
		if arg != "" {
			fmt.Fprintln(s.stderr, dirCommandHelp)
			return
		}
		s.printDirs()
	}
}

// splitCommand splits a typed command line into its name and the argument after
// it (spaces and tabs).
func splitCommand(line string) (name, arg string) {
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i], strings.TrimSpace(line[i+1:])
	}
	return line, ""
}

// printDirs writes the session's reach: the workspace and every added directory
// with its mode and where it came from (--add-dir, SUGA_ADD_DIRS, typed in chat,
// authorized at a question, or kept from an earlier process).
func (s *chatState) printDirs() {
	dirs, err := s.dirs.List(s.ctx)
	if err != nil {
		fmt.Fprintln(s.stderr, formatCommandError(dirsCmd, err))
		return
	}
	workspace, added := s.dirs.Workspace(), 0
	for _, dir := range dirs {
		if dir.Path == workspace {
			fmt.Fprintf(s.source, "工作区：%s（%s）\n", dir.Path, modeLabel(dir.Writable))
			continue
		}
		added++
		fmt.Fprintf(s.source, "  %s（%s · %s）\n", dir.Path, modeLabel(dir.Writable), sourceLabel(dir.Source))
	}
	if added == 0 {
		fmt.Fprintf(s.source, "附加目录：无（%s）\n", dirCommandHelp)
		return
	}
	fmt.Fprintf(s.source, "共 %d 个附加目录（%s）\n", added, dirCommandHelp)
}

// modeLabel names a directory mode as :dirs reads it.
func modeLabel(writable bool) string {
	if writable {
		return "读写"
	}
	return "只读"
}

// sourceLabel names where an added directory came from, for :dirs. An entry of
// an older session (or one a plain --add-dir run wrote before this version kept
// the origin) reads as 会话沿用.
func sourceLabel(source string) string {
	switch source {
	case adddir.SourceFlag:
		return "启动参数"
	case adddir.SourceEnv:
		return "环境变量"
	case adddir.SourceSession:
		return "会话中追加"
	case adddir.SourceAsk:
		return "越界授权"
	}
	return "会话沿用"
}
