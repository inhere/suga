package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
)

const (
	checkpointsCmd     = ":checkpoints"
	undoCmd            = ":undo"
	undoForceArg       = "force"
	noUndoHint         = "没有可回滚的快照（本会话没有写工具改动，或快照已按 tools.snapshots 清理）。"
	undoConflictReason = "冲突：快照之后被改动过"
	undoBusyHint       = "回合进行中，等它结束再执行本地命令"
	undoConfirm        = "执行？[y] 回滚 [N] 取消："
)

// undoUsageHint answers a :undo line whose arguments do not parse; the text is
// the :undo entry of the command table (chat_usage.go).
var undoUsageHint = commandUsageText(undoCmd)

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
		return nil, nil, fmt.Errorf("最多只能回滚最近 %d 个回合", snapshots.Limits().KeepTurns)
	}
	rows, err := s.runner.SelectUndoCheckpoints(s.ctx, snapshots, sessionsDir, s.sessionID, command.n)
	return rows, snapshots, err
}

// runCheckpoints lists the write snapshots of this session; arg is the optional
// number of turns to show (:checkpoints [N]).
func (s *chatState) runCheckpoints(arg string) {
	limit, ok := checkpointLimit(arg)
	if !ok {
		fmt.Fprintln(s.errWriter(), commandUsageText(checkpointsCmd))
		return
	}
	rows, err := s.checkpoints(limit)
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(checkpointsCmd, err))
		return
	}
	fmt.Fprintln(s.source, BuildCheckpoints(rows))
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
		fmt.Fprintln(s.errWriter(), formatCommandError(undoCmd, err))
		return
	}
	fmt.Fprintln(s.source, BuildUndoPlan(rows, command))
	if !hasUndoSnapshots(rows) {
		return
	}
	if appconfig.ChatUndoConfirmEnabled() {
		if s.source.terminal == nil {
			fmt.Fprintln(s.source, "已取消（当前输入不是终端，回滚需要确认）")
			return
		}
		answer, askErr := s.source.Ask(s.ctx, "", undoConfirm)
		if askErr != nil || !undoConfirmed(answer) {
			fmt.Fprintln(s.source, "已取消")
			return
		}
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(undoCmd, err))
		return
	}
	result, err := s.runner.Undo(s.ctx, snapshots, sessionsDir, s.sessionID, command.n, command.force)
	if err != nil {
		fmt.Fprintln(s.errWriter(), formatCommandError(undoCmd, err))
		return
	}
	fmt.Fprintln(s.source, BuildUndoResult(result, rows))
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
func BuildCheckpoints(rows []personal.Checkpoint) string {
	if len(rows) == 0 {
		return noUndoHint
	}
	var b strings.Builder
	b.WriteString("快照点（最近的回合在前；快照在写操作执行前记录）：\n")
	totalFiles, totalBytes := 0, int64(0)
	for _, row := range rows {
		fmt.Fprintf(&b, "  %s  %s  %d 次写调用 / %d 个文件 / %s", cleanText(row.TurnID), row.StartedAt.Local().Format("01-02 15:04"), row.Calls, row.Files, formatBytes(row.Bytes))
		if row.State == personal.SnapshotStateUndone {
			b.WriteString("  [已回滚]")
		} else if row.State == personal.SnapshotStatePartial {
			b.WriteString("  [部分回滚]")
		}
		if row.ShellOnly {
			b.WriteString("  [仅 shell_exec，未纳入回滚]")
		}
		b.WriteByte('\n')
		for _, item := range row.Items {
			fmt.Fprintf(&b, "    %s %s%s%s\n", actionLabel(item.Action), cleanText(item.Meta.Rel), sizeSuffix(item.Meta.Size), reasonSuffix(item.Reason))
		}
		for _, shell := range row.Shell {
			fmt.Fprintf(&b, "    shell_exec：%s%s（改动不在快照范围内）\n", cleanText(shell.Command), taskSuffix(shell.TaskID))
		}
		totalFiles += row.Files
		totalBytes += row.Bytes
	}
	fmt.Fprintf(&b, "合计 %d 个文件 / %s；保留最近 %d 个回合或 %d 天（tools.snapshots）", totalFiles, formatBytes(totalBytes), personal.DefaultSnapshotKeepTurns, personal.DefaultSnapshotKeepDays)
	return b.String()
}

func BuildUndoPlan(rows []personal.Checkpoint, command undoCommand) string {
	if !hasUndoSnapshots(rows) {
		return noUndoHint + renderShellRows(rows)
	}
	var b strings.Builder
	for _, row := range rows {
		if row.HasSnapshots() {
			continue
		}
		for _, shell := range row.Shell {
			fmt.Fprintf(&b, "shell_exec：%s%s（改动不在快照范围内）\n", cleanText(shell.Command), taskSuffix(shell.TaskID))
		}
	}
	first := true
	for _, row := range rows {
		if !row.HasSnapshots() {
			continue
		}
		if first {
			fmt.Fprintf(&b, "将回滚最近 %d 个回合（从 %s 开始%s）：\n", command.n, cleanText(row.TurnID), forceSuffix(command.force))
			first = false
		}
		for _, item := range row.Items {
			action, reason := item.Action, item.Reason
			if item.Conflict {
				// Undo skips a target changed after the snapshot unless forced
				// (personal.contentConflict); force covers it and says so.
				reason = undoConflictReason
				if !command.force {
					action = personal.CheckpointActionSkip
				}
			}
			fmt.Fprintf(&b, "  %s %s%s%s\n", actionLabel(action), cleanText(item.Meta.Rel), sizeSuffix(item.Meta.Size), reasonSuffix(reason))
		}
		for _, shell := range row.Shell {
			fmt.Fprintf(&b, "  shell_exec：%s%s（改动不在快照范围内）\n", cleanText(shell.Command), taskSuffix(shell.TaskID))
		}
	}
	if first {
		return noUndoHint + renderShellRows(rows)
	}
	return strings.TrimRight(b.String(), "\n")
}

func BuildUndoResult(result personal.UndoResult, rows []personal.Checkpoint) string {
	parts := []string{}
	if result.Restored > 0 {
		parts = append(parts, fmt.Sprintf("已回滚 %d 个文件", result.Restored))
	}
	if result.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("删除 %d 个新建文件", result.Deleted))
	}
	if result.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("跳过 %d 个冲突", result.Skipped))
	}
	if result.Unrecoverable > 0 {
		parts = append(parts, fmt.Sprintf("%d 个文件无法恢复", result.Unrecoverable))
	}
	if len(parts) == 0 {
		parts = append(parts, "没有文件被恢复")
	}
	text := strings.Join(parts, "、") + "。"
	text += renderShellRows(rows)
	if hasGitRoot(rows) {
		text += "\n工作区是 git 仓库：可用 git -C <workspace> status --short / git diff 核对其余改动。"
	}
	return text
}

func renderShellRows(rows []personal.Checkpoint) string {
	var b strings.Builder
	for _, row := range rows {
		for _, shell := range row.Shell {
			fmt.Fprintf(&b, "\n本回合还运行了 shell_exec：%s%s；改动不在快照范围内：suga inspect trace <session> --turn %s", cleanText(shell.Command), taskSuffix(shell.TaskID), cleanText(row.TurnID))
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

func actionLabel(action personal.CheckpointAction) string {
	switch action {
	case personal.CheckpointActionRestore:
		return "恢复"
	case personal.CheckpointActionDelete:
		return "删除"
	case personal.CheckpointActionUnrecoverable:
		return "无法恢复"
	default:
		return "跳过"
	}
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

func taskSuffix(taskID string) string {
	if taskID == "" {
		return ""
	}
	return "（task " + cleanText(taskID) + "）"
}

func forceSuffix(force bool) string {
	if force {
		return "，涵盖内容冲突"
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
