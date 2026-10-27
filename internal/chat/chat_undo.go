package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
)

const (
	checkpointsCmd = ":checkpoints"
	undoCmd        = ":undo"
	undoForceArg   = "force"
)

// Catalog keys of the :undo answers; the checkpoint listing and the undo plan
// and result are shared with the CLI "suga session undo" display.
const (
	undoNoHintKey    = "cmd.undo.none"
	undoConflictKey  = "cmd.undo.conflict"
	undoConfirmKey   = "cmd.undo.confirm"
	undoCancelledKey = "cmd.undo.cancelled"
)

type undoCommand struct {
	n     int
	force bool
}

func isCheckpointCommand(line string) bool {
	name, _ := splitCommand(line)
	return name == checkpointsCmd
}

func parseUndoCommand(line string) (undoCommand, bool) {
	name, arg := splitCommand(line)
	if name != undoCmd {
		return undoCommand{}, false
	}
	command := undoCommand{n: 1}
	if arg == "" {
		return command, true
	}
	seenN, seenForce := false, false
	for _, field := range strings.Fields(arg) {
		switch {
		case field == undoForceArg && !seenForce:
			command.force, seenForce = true, true
		case !seenN:
			n, err := strconv.Atoi(field)
			if err != nil || n < 1 {
				return undoCommand{}, false
			}
			command.n, seenN = n, true
		default:
			return undoCommand{}, false
		}
	}
	return command, true
}

func isUndoCommand(line string) bool {
	name, _ := splitCommand(line)
	return name == undoCmd
}

// undoStore creates the reader used by chat's undo commands. It deliberately
// uses the same sessions root and limits as wiring; the runner still owns the
// session gate and cross-process lock.
func (s *chatState) undoStore() (*personal.SessionSnapshots, string, error) {
	settings, err := appconfig.ReadSnapshotSettings()
	if err != nil {
		return nil, "", err
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return nil, "", err
	}
	return personal.NewSessionSnapshotsWithLimits(sessionsDir, personal.SnapshotLimits{
		MaxFileBytes: settings.MaxFileBytes, MaxSessionBytes: settings.MaxSessionBytes,
		MaxGlobalBytes: settings.MaxGlobalBytes, KeepTurns: settings.KeepTurns, KeepDays: settings.KeepDays,
	}), sessionsDir, nil
}

func (s *chatState) checkpoints(limit int) ([]personal.Checkpoint, error) {
	snapshots, sessionsDir, err := s.undoStore()
	if err != nil {
		return nil, err
	}
	return s.runner.Checkpoints(s.ctx, snapshots, sessionsDir, s.sessionID, limit)
}

func (s *chatState) selectUndo(command undoCommand) ([]personal.Checkpoint, *personal.SessionSnapshots, error) {
	snapshots, sessionsDir, err := s.undoStore()
	if err != nil {
		return nil, nil, err
	}
	if command.n > snapshots.Limits().KeepTurns {
		return nil, nil, fmt.Errorf("%s", s.tr().T("cmd.undo.max", map[string]any{"count": snapshots.Limits().KeepTurns}))
	}
	rows, err := s.runner.SelectUndoCheckpoints(s.ctx, snapshots, sessionsDir, s.sessionID, command.n)
	return rows, snapshots, err
}

// runCheckpoints lists the write snapshots of this session; arg is the optional
// number of turns to show (:checkpoints [N]).
func (s *chatState) runCheckpoints(arg string) {
	limit, ok := checkpointLimit(arg)
	if !ok {
		fmt.Fprintln(s.errWriter(), commandUsageText(s.tr(), checkpointsCmd))
		return
	}
	rows, err := s.checkpoints(limit)
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(s.tr(), checkpointsCmd, err))
		return
	}
	fmt.Fprintln(s.source, BuildCheckpoints(s.tr(), rows))
}

// checkpointLimit reads the :checkpoints argument: no argument lists every turn,
// N the last N turns of the listing.
func checkpointLimit(arg string) (int, bool) {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return 0, true
	}
	if len(fields) > 1 {
		return 0, false
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func (s *chatState) runUndo(command undoCommand) {
	rows, snapshots, err := s.selectUndo(command)
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(s.tr(), undoCmd, err))
		return
	}
	fmt.Fprintln(s.source, BuildUndoPlan(s.tr(), rows, command))
	if !hasUndoSnapshots(rows) {
		return
	}
	if appconfig.ChatUndoConfirmEnabled() {
		if s.source.terminal == nil {
			fmt.Fprintln(s.source, s.tr().T("cmd.undo.cancelled.notty"))
			return
		}
		answer, askErr := s.source.Ask(s.ctx, "", s.tr().T(undoConfirmKey))
		if askErr != nil || !undoConfirmed(answer) {
			fmt.Fprintln(s.source, s.tr().T(undoCancelledKey))
			return
		}
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(s.tr(), undoCmd, err))
		return
	}
	result, err := s.runner.Undo(s.ctx, snapshots, sessionsDir, s.sessionID, command.n, command.force)
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(s.tr(), undoCmd, err))
		return
	}
	fmt.Fprintln(s.source, BuildUndoResult(s.tr(), result, rows))
}

func undoConfirmed(answer string) bool {
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func hasUndoSnapshots(rows []personal.Checkpoint) bool {
	for _, row := range rows {
		if row.HasSnapshots() {
			return true
		}
	}
	return false
}

// BuildCheckpoints renders the read-only checkpoint list. Every value originating
// from a path, event, or model call is cleaned before it can reach the terminal.
func BuildCheckpoints(tr i18n.Translator, rows []personal.Checkpoint) string {
	if len(rows) == 0 {
		return tr.T(undoNoHintKey)
	}
	var b strings.Builder
	b.WriteString(tr.T("cmd.undo.checkpoints.header") + "\n")
	totalFiles, totalBytes := 0, int64(0)
	for _, row := range rows {
		b.WriteString("  " + tr.T("cmd.undo.checkpoints.row", map[string]any{
			"turn":    cleanText(row.TurnID),
			"started": row.StartedAt.Local().Format("01-02 15:04"),
			"calls":   row.Calls,
			"files":   row.Files,
			"bytes":   formatBytes(row.Bytes),
		}))
		if row.State == personal.SnapshotStateUndone {
			b.WriteString("  " + tr.T("cmd.undo.state.undone"))
		} else if row.State == personal.SnapshotStatePartial {
			b.WriteString("  " + tr.T("cmd.undo.state.partial"))
		}
		if row.ShellOnly {
			b.WriteString("  " + tr.T("cmd.undo.state.shell_only"))
		}
		b.WriteByte('\n')
		for _, item := range row.Items {
			fmt.Fprintf(&b, "    %s %s%s%s\n", actionLabel(tr, item.Action), cleanText(item.Meta.Rel), sizeSuffix(item.Meta.Size), reasonSuffix(item.Reason))
		}
		for _, shell := range row.Shell {
			b.WriteString("    " + shellRow(tr, shell.Command, shell.TaskID) + "\n")
		}
		totalFiles += row.Files
		totalBytes += row.Bytes
	}
	b.WriteString(tr.T("cmd.undo.checkpoints.total", map[string]any{
		"files": totalFiles,
		"bytes": formatBytes(totalBytes),
		"turns": personal.DefaultSnapshotKeepTurns,
		"days":  personal.DefaultSnapshotKeepDays,
	}))
	return b.String()
}

func BuildUndoPlan(tr i18n.Translator, rows []personal.Checkpoint, command undoCommand) string {
	if !hasUndoSnapshots(rows) {
		return tr.T(undoNoHintKey) + renderShellRows(tr, rows)
	}
	var b strings.Builder
	for _, row := range rows {
		if row.HasSnapshots() {
			continue
		}
		for _, shell := range row.Shell {
			b.WriteString(shellRow(tr, shell.Command, shell.TaskID) + "\n")
		}
	}
	first := true
	for _, row := range rows {
		if !row.HasSnapshots() {
			continue
		}
		if first {
			b.WriteString(tr.T("cmd.undo.plan.header", map[string]any{
				"count": command.n,
				"turn":  cleanText(row.TurnID),
				"force": forceSuffix(tr, command.force),
			}) + "\n")
			first = false
		}
		for _, item := range row.Items {
			action, reason := item.Action, item.Reason
			if item.Conflict {
				// Undo skips a target changed after the snapshot unless forced
				// (personal.contentConflict); force covers it and says so.
				reason = tr.T(undoConflictKey)
				if !command.force {
					action = personal.CheckpointActionSkip
				}
			}
			fmt.Fprintf(&b, "  %s %s%s%s\n", actionLabel(tr, action), cleanText(item.Meta.Rel), sizeSuffix(item.Meta.Size), reasonSuffix(reason))
		}
		for _, shell := range row.Shell {
			b.WriteString("  " + shellRow(tr, shell.Command, shell.TaskID) + "\n")
		}
	}
	if first {
		return tr.T(undoNoHintKey) + renderShellRows(tr, rows)
	}
	return strings.TrimRight(b.String(), "\n")
}

func BuildUndoResult(tr i18n.Translator, result personal.UndoResult, rows []personal.Checkpoint) string {
	parts := []string{}
	if result.Restored > 0 {
		parts = append(parts, tr.T("cmd.undo.result.restored", map[string]any{"count": result.Restored}))
	}
	if result.Deleted > 0 {
		parts = append(parts, tr.T("cmd.undo.result.deleted", map[string]any{"count": result.Deleted}))
	}
	if result.Skipped > 0 {
		parts = append(parts, tr.T("cmd.undo.result.skipped", map[string]any{"count": result.Skipped}))
	}
	if result.Unrecoverable > 0 {
		parts = append(parts, tr.T("cmd.undo.result.unrecoverable", map[string]any{"count": result.Unrecoverable}))
	}
	if len(parts) == 0 {
		parts = append(parts, tr.T("cmd.undo.result.nothing"))
	}
	text := strings.Join(parts, "、") + "。"
	text += renderShellRows(tr, rows)
	if hasGitRoot(rows) {
		text += "\n" + tr.T("cmd.undo.result.git")
	}
	return text
}

func renderShellRows(tr i18n.Translator, rows []personal.Checkpoint) string {
	var b strings.Builder
	for _, row := range rows {
		for _, shell := range row.Shell {
			b.WriteString("\n" + tr.T("cmd.undo.shell.note", map[string]any{
				"command": cleanText(shell.Command),
				"task":    taskSuffix(tr, shell.TaskID),
				"turn":    cleanText(row.TurnID),
			}))
		}
	}
	return b.String()
}

func hasGitRoot(rows []personal.Checkpoint) bool {
	for _, row := range rows {
		for _, item := range row.Items {
			if _, err := os.Stat(filepath.Join(item.Meta.Root, ".git")); err == nil {
				return true
			}
		}
	}
	return false
}

func actionLabel(tr i18n.Translator, action personal.CheckpointAction) string {
	switch action {
	case personal.CheckpointActionRestore:
		return tr.T("cmd.undo.action.restore")
	case personal.CheckpointActionDelete:
		return tr.T("cmd.undo.action.delete")
	case personal.CheckpointActionUnrecoverable:
		return tr.T("cmd.undo.action.unrecoverable")
	default:
		return tr.T("cmd.undo.action.skip")
	}
}

// shellRow is one shell_exec line outside the snapshots; the caller writes the
// indent, since the listing and the plan indent it differently.
func shellRow(tr i18n.Translator, command, taskID string) string {
	return tr.T("cmd.undo.shell.line", map[string]any{"command": cleanText(command), "task": taskSuffix(tr, taskID)})
}

func cleanText(value string) string { return strings.Join(strings.Fields(termtext.Clean(value)), " ") }

func sizeSuffix(size int64) string {
	if size <= 0 {
		return ""
	}
	return "  " + formatBytes(size)
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return "  " + cleanText(reason)
}

func taskSuffix(tr i18n.Translator, taskID string) string {
	if taskID == "" {
		return ""
	}
	return tr.T("cmd.undo.task_suffix", map[string]any{"id": cleanText(taskID)})
}

func forceSuffix(tr i18n.Translator, force bool) string {
	if force {
		return tr.T("cmd.undo.plan.force")
	}
	return ""
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}
