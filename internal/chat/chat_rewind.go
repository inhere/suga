package chat

import (
	"errors"
	"fmt"
	"strings"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
)

// :rewind and :fork (session rewind design 0.2, plan T7). Both work on a user
// turn picked by its ordinal (the listing `:rewind` prints) or by turn id. The
// flow of :rewind mirrors :undo: preview, confirm, execute, then show what was
// really done. :fork never switches this chat to the new session (R7).

const (
	rewindCmd = ":rewind"
	forkCmd   = ":fork"
)

// rewindCommand is a parsed ":rewind" line. An empty target lists the turns.
type rewindCommand struct {
	target string
	scope  personal.RewindScope
}

func isRewindCommand(line string) bool {
	name, _ := splitCommand(line)
	return name == rewindCmd
}

func isForkCommand(line string) bool {
	name, _ := splitCommand(line)
	return name == forkCmd
}

// parseRewindCommand reads ":rewind", ":rewind N" and ":rewind N <scope>"; the
// scope defaults to both. The target is an ordinal or a turn id, so it is not
// validated here: the runner resolves it.
func parseRewindCommand(line string) (rewindCommand, bool) {
	name, arg := splitCommand(line)
	if name != rewindCmd {
		return rewindCommand{}, false
	}
	fields := strings.Fields(arg)
	command := rewindCommand{scope: personal.RewindScopeBoth}
	switch len(fields) {
	case 0:
		return command, true
	case 2:
		switch scope := personal.RewindScope(fields[1]); scope {
		case personal.RewindScopeFiles, personal.RewindScopeConversation, personal.RewindScopeBoth:
			command.scope = scope
		default:
			return rewindCommand{}, false
		}
		fallthrough
	case 1:
		command.target = fields[0]
		return command, true
	}
	return rewindCommand{}, false
}

// parseForkCommand reads ":fork N": exactly one target.
func parseForkCommand(line string) (string, bool) {
	name, arg := splitCommand(line)
	fields := strings.Fields(arg)
	if name != forkCmd || len(fields) != 1 {
		return "", false
	}
	return fields[0], true
}

func scopeHas(scope personal.RewindScope, part personal.RewindScope) bool {
	return scope == personal.RewindScopeBoth || scope == part
}

func (s *chatState) runRewind(command rewindCommand) {
	if command.target == "" {
		s.listRewindTurns()
		return
	}
	snapshots, sessionsDir, err := s.undoStore()
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(s.tr(), rewindCmd, err))
		return
	}
	plan, err := s.runner.RewindPlan(s.ctx, snapshots, sessionsDir, s.sessionID, command.target, command.scope)
	if err != nil {
		fmt.Fprintln(s.errWriter(), rewindCmd+": "+rewindErrorText(s.tr(), err))
		return
	}
	fmt.Fprintln(s.source, BuildRewindPlan(s.tr(), plan, command.target))
	if appconfig.ChatUndoConfirmEnabled() {
		if s.source.terminal == nil {
			fmt.Fprintln(s.source, s.tr().T("cmd.rewind.cancelled.notty"))
			return
		}
		answer, askErr := s.source.Ask(s.ctx, "", s.tr().T("cmd.rewind.confirm"))
		if askErr != nil || !undoConfirmed(answer) {
			fmt.Fprintln(s.source, s.tr().T(undoCancelledKey))
			return
		}
	}
	result, err := s.runner.ApplyRewind(s.ctx, snapshots, sessionsDir, plan)
	if err != nil {
		fmt.Fprintln(s.errWriter(), rewindCmd+": "+rewindErrorText(s.tr(), err))
		return
	}
	fmt.Fprintln(s.source, BuildRewindResult(s.tr(), plan, result))
}

func (s *chatState) listRewindTurns() {
	turns, err := s.runner.RewindTurns(s.ctx, s.sessionID)
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(s.tr(), rewindCmd, err))
		return
	}
	fmt.Fprintln(s.source, BuildRewindTurns(s.tr(), turns))
}

func (s *chatState) runFork(target string) {
	snapshots, sessionsDir, err := s.undoStore()
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(s.tr(), forkCmd, err))
		return
	}
	res, err := s.runner.Fork(s.ctx, snapshots, sessionsDir, s.sessionID, target)
	if err != nil {
		fmt.Fprintln(s.errWriter(), forkCmd+": "+rewindErrorText(s.tr(), err))
		return
	}
	fmt.Fprintln(s.source, s.tr().T("cmd.fork.done", map[string]any{
		"id": cleanText(res.SessionID), "turns": res.Turns, "entries": res.Entries,
	}))
}

// rewindErrorText picks the message of a rewind/fork failure by its sentinel; an
// error without one (storage, lock) is shown as it is.
func rewindErrorText(tr i18n.Translator, err error) string {
	key := ""
	switch {
	case errors.Is(err, personal.ErrRewindStale):
		key = "cmd.rewind.err.stale"
	case errors.Is(err, personal.ErrRewindBusy):
		key = "cmd.rewind.err.busy"
	case errors.Is(err, personal.ErrRewindTargetNotFound):
		key = "cmd.rewind.err.not_found"
	case errors.Is(err, personal.ErrRewindTargetMasked):
		key = "cmd.rewind.err.masked"
	case errors.Is(err, personal.ErrRewindTargetNotUserTurn):
		key = "cmd.rewind.err.not_user_turn"
	case errors.Is(err, personal.ErrRewindSessionArchived):
		key = "cmd.rewind.err.archived"
	case errors.Is(err, personal.ErrRewindTasksRunning):
		key = "cmd.rewind.err.tasks_running"
	default:
		return cleanText(err.Error())
	}
	return tr.T(key)
}

// BuildRewindTurns renders the user turns :rewind / :fork can target.
func BuildRewindTurns(tr i18n.Translator, turns []personal.RewindTurn) string {
	if len(turns) == 0 {
		return tr.T("cmd.rewind.no_turns")
	}
	var b strings.Builder
	b.WriteString(tr.T("cmd.rewind.turns.header") + "\n")
	for _, t := range turns {
		fmt.Fprintf(&b, "  %d  %s  %s  %s\n", t.Ordinal, t.StartedAt.Local().Format("01-02 15:04"),
			cleanText(t.TurnID), truncateRunes(cleanText(t.Summary), 60))
	}
	b.WriteString(tr.T("cmd.rewind.turns.hint"))
	return b.String()
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func rewindActionLabel(tr i18n.Translator, action personal.RewindFileAction) string {
	return tr.T("cmd.rewind.action." + string(action))
}

// rewindReasonCodes are the reason codes of personal/rewind_files*.go with a
// catalog text (cmd.rewind.reason.<code>); anything else is shown as it is.
var rewindReasonCodes = map[string]bool{
	"content_changed": true, "cascade": true, "root_untrusted": true, "cleared": true,
	"no_snapshot": true, "snapshots_off": true, "not_restorable": true, "snapshot_incomplete": true,
	"file_missing": true, "not_regular_file": true, "snapshot_hash_missing": true, "unchanged": true, "unreadable": true,
}

// restoreFailedPrefix starts "restore_failed: <error text>"; the error text is kept.
const restoreFailedPrefix = "restore_failed"

// rewindReasonText turns a rewind reason code into the catalog text. An unknown
// code is returned unchanged; restore_failed keeps the error text after it.
func rewindReasonText(tr i18n.Translator, reason string) string {
	if rewindReasonCodes[reason] {
		return tr.T("cmd.rewind.reason." + reason)
	}
	if rest, ok := strings.CutPrefix(reason, restoreFailedPrefix); ok && (rest == "" || strings.HasPrefix(rest, ": ")) {
		return tr.T("cmd.rewind.reason.restore_failed") + rest
	}
	return reason
}

func rewindFileRows(tr i18n.Translator, items []personal.RewindFileItem) string {
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "  %s %s%s\n", rewindActionLabel(tr, item.Action), cleanText(item.Path), reasonSuffix(rewindReasonText(tr, item.Reason)))
	}
	return b.String()
}

// BuildRewindPlan renders the preview: what the rewind would do to the files and
// the conversation. Tasks that ended unseen are only mentioned generally, since
// the preview does not take them out.
func BuildRewindPlan(tr i18n.Translator, plan personal.RewindPlan, target string) string {
	var b strings.Builder
	b.WriteString(tr.T("cmd.rewind.plan.header", map[string]any{
		"target": cleanText(target), "turn": cleanText(plan.TargetTurn), "scope": tr.T("cmd.rewind.scope." + string(plan.Scope)),
	}) + "\n")
	if scopeHas(plan.Scope, personal.RewindScopeFiles) {
		if len(plan.Files) == 0 {
			b.WriteString("  " + tr.T("cmd.rewind.plan.no_files") + "\n")
		}
		b.WriteString(rewindFileRows(tr, plan.Files))
	}
	if scopeHas(plan.Scope, personal.RewindScopeConversation) {
		b.WriteString(tr.T("cmd.rewind.plan.conversation") + "\n")
		b.WriteString(tr.T("cmd.rewind.plan.tasks"))
		return b.String()
	}
	return strings.TrimRight(b.String(), "\n")
}

// BuildRewindResult renders what the rewind really did, including the dropped
// task notices and the warnings of a rewind that still stands.
func BuildRewindResult(tr i18n.Translator, plan personal.RewindPlan, res personal.RewindResult) string {
	var b strings.Builder
	b.WriteString(tr.T("cmd.rewind.result.header") + "\n")
	if scopeHas(plan.Scope, personal.RewindScopeFiles) {
		// One row per path (the last step of a path decides), like the counts.
		files := personal.DedupeRewindFiles(res.Files)
		if len(files) == 0 {
			b.WriteString("  " + tr.T("cmd.rewind.result.no_files") + "\n")
		}
		b.WriteString(rewindFileRows(tr, files))
	}
	if res.Conversation {
		b.WriteString("  " + tr.T("cmd.rewind.result.conversation") + "\n")
	}
	if scopeHas(plan.Scope, personal.RewindScopeConversation) {
		if len(res.DroppedTasks) == 0 {
			b.WriteString("  " + tr.T("cmd.rewind.result.no_tasks") + "\n")
		}
		for _, task := range res.DroppedTasks {
			b.WriteString("  " + tr.T("cmd.rewind.result.dropped_task", map[string]any{
				"id": cleanText(task.ID), "summary": cleanText(task.Summary),
			}) + "\n")
		}
	}
	for _, w := range res.Warnings {
		b.WriteString(tr.T("cmd.rewind.result.warning", map[string]any{"text": cleanText(w)}) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
