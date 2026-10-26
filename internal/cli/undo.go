package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
)

// newUndoCommand builds the provider-independent `suga undo` command. The
// command opens the core store directly: undo must remain available when no
// model/provider is configured.
func newUndoCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) *capp.Cmd {
	var list, force, yes, asJSON bool
	var turnID string
	var count int
	cmd := capp.NewCmd("undo", "List or roll back file-tool checkpoints of a session", func(c *capp.Cmd) error {
		if count < 1 {
			return fmt.Errorf("--n must be at least 1")
		}
		if list && (force || yes) {
			return fmt.Errorf("--list cannot be combined with --force or --yes")
		}
		if !list && !yes && !isTerminalInput(stdin) {
			return fmt.Errorf("destructive undo requires --yes when stdin is not a terminal")
		}
		return runUndo(ctx, stdout, stderr, c.Arg("session").String(), personal.UndoOptions{
			TurnID: turnID, Count: count, Force: force,
		}, undoCLIOptions{list: list, yes: yes, asJSON: asJSON, stdin: stdin})
	})
	cmd.SetOutput(stderr)
	cmd.BoolVar(&list, "list", false, "list checkpoints without changing files")
	cmd.StringVar(&turnID, "turn", "", "only undo this turn ID")
	cmd.IntVar(&count, "n", 1, "undo this many recent file-changing turns;;n")
	cmd.BoolVar(&force, "force", false, "overwrite content conflicts (requires --yes or terminal confirmation)")
	cmd.BoolVar(&yes, "yes", false, "skip the destructive-operation confirmation")
	cmd.BoolVar(&asJSON, "json", false, "write checkpoint/result JSON")
	cmd.AddArg("session", "session ID or unique ID prefix", true)
	cmd.Usage = "suga undo <session> [--list] [--turn <id>] [-n <count>] [--force] [--yes] [--json]"
	return cmd
}

type undoCLIOptions struct {
	list   bool
	yes    bool
	asJSON bool
	stdin  io.Reader
}

func runUndo(ctx context.Context, stdout, stderr io.Writer, query string, options personal.UndoOptions, cliOpts undoCLIOptions) error {
	tr := cliTranslator()
	core, repo, err := openUndoCore()
	if err != nil {
		return err
	}
	defer repo.Close()

	if cliOpts.list {
		rows, err := core.Checkpoints(ctx, query, 0)
		if err != nil {
			return mapUndoError(err, query)
		}
		rows = filterUndoTurn(rows, options.TurnID)
		return writeUndoCheckpoints(stdout, rows, cliOpts.asJSON)
	}

	rows, err := undoPlan(ctx, core, query, options)
	if err != nil {
		return mapUndoError(err, query)
	}
	if !cliOpts.asJSON {
		fmt.Fprintln(stdout, renderUndoPlanWithTranslator(rows, options, tr))
	}
	if !hasUndoSnapshots(rows) {
		return processExitError{code: 3, err: errors.New(cliText(tr, "cmd.undo.none"))}
	}
	if !cliOpts.yes {
		if !confirmUndo(cliOpts.stdin, stderr, options.Force) {
			return errors.New("undo cancelled")
		}
	}

	result, err := core.UndoWithOptions(ctx, query, options)
	if err != nil {
		return mapUndoError(err, query)
	}
	if cliOpts.asJSON {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(stdout, renderUndoResultWithTranslator(result, rows, tr))
	}
	if code := undoResultExitCode(result, rows); code != 0 {
		return processExitError{code: code, err: errors.New("undo completed without restoring every selected file")}
	}
	return nil
}

func openUndoCore() (*personal.UndoCore, *store.Store, error) {
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		return nil, nil, err
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return nil, nil, err
	}
	settings, err := appconfig.ReadSnapshotSettings()
	if err != nil {
		return nil, nil, err
	}
	repo, err := openCoreStore()
	if err != nil {
		return nil, nil, err
	}
	snapshots := personal.NewSessionSnapshotsWithLimits(sessionsDir, personal.SnapshotLimits{
		MaxFileBytes: settings.MaxFileBytes, MaxSessionBytes: settings.MaxSessionBytes,
		MaxGlobalBytes: settings.MaxGlobalBytes, KeepTurns: settings.KeepTurns, KeepDays: settings.KeepDays,
	})
	core := personal.NewUndoCore(repo, snapshots, sessionsDir, filepath.Join(home, "run", "locks"))
	return core, repo, nil
}

func undoPlan(ctx context.Context, core *personal.UndoCore, query string, options personal.UndoOptions) ([]personal.Checkpoint, error) {
	if options.TurnID == "" {
		return core.Select(ctx, query, options.Count)
	}
	rows, err := core.Checkpoints(ctx, query, 0)
	if err != nil {
		return nil, err
	}
	return filterUndoTurn(rows, options.TurnID), nil
}

func filterUndoTurn(rows []personal.Checkpoint, turnID string) []personal.Checkpoint {
	if turnID == "" {
		return rows
	}
	for _, row := range rows {
		if row.TurnID == turnID {
			return []personal.Checkpoint{row}
		}
	}
	return nil
}

func mapUndoError(err error, query string) error {
	switch {
	case errors.Is(err, personal.ErrSessionBusy), errors.Is(err, personalprocess.ErrSessionLocked):
		return processExitError{code: 5, err: fmt.Errorf("session busy: %w", err)}
	case errors.Is(err, store.ErrSessionNotFound):
		return personal.SessionNotFound(err, query)
	default:
		return err
	}
}

func confirmUndo(stdin io.Reader, stderr io.Writer, force bool) bool {
	prompt := "确认回滚文件改动？[y/N] "
	if force {
		prompt = "确认强制回滚并覆盖内容冲突？[y/N] "
	}
	fmt.Fprint(stderr, prompt)
	answer, err := readChoice(stdin)
	return err == nil && undoConfirmedCLI(answer)
}

func undoConfirmedCLI(answer string) bool {
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func writeUndoCheckpoints(out io.Writer, rows []personal.Checkpoint, asJSON bool) error {
	if asJSON {
		if rows == nil {
			rows = []personal.Checkpoint{}
		}
		return json.NewEncoder(out).Encode(rows)
	}
	_, err := fmt.Fprintln(out, renderUndoCheckpoints(rows))
	return err
}

func renderUndoCheckpoints(rows []personal.Checkpoint) string {
	return renderUndoCheckpointsWithTranslator(rows, cliTranslator())
}

func renderUndoCheckpointsWithTranslator(rows []personal.Checkpoint, tr i18n.Translator) string {
	if len(rows) == 0 {
		return cliText(tr, "cmd.undo.none")
	}
	var b strings.Builder
	b.WriteString(cliText(tr, "cmd.undo.checkpoints.header") + "\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "  "+cliText(tr, "cmd.undo.checkpoints.row"), cleanUndoValue(row.TurnID), row.StartedAt.Local().Format("01-02 15:04"), row.Calls, row.Files, formatUndoBytes(row.Bytes))
		if row.State == personal.SnapshotStateUndone {
			b.WriteString("  " + cliText(cliTranslator(), "cmd.undo.state.undone"))
		} else if row.State == personal.SnapshotStatePartial {
			b.WriteString("  " + cliText(cliTranslator(), "cmd.undo.state.partial"))
		}
		if row.ShellOnly {
			b.WriteString("  " + cliText(cliTranslator(), "cmd.undo.state.shell_only"))
		}
		b.WriteByte('\n')
		for _, item := range row.Items {
			fmt.Fprintf(&b, "    %s %s%s%s\n", undoActionLabel(item.Action), cleanUndoValue(item.Meta.Rel), undoSizeSuffix(item.Meta.Size), undoReasonSuffix(item.Reason))
		}
		for _, shell := range row.Shell {
			fmt.Fprintln(&b, "    "+cliText(cliTranslator(), "cmd.undo.shell.line", map[string]any{"command": cleanUndoValue(shell.Command), "task": undoTaskSuffix(shell.TaskID)}))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderUndoPlan(rows []personal.Checkpoint, options personal.UndoOptions) string {
	return renderUndoPlanWithTranslator(rows, options, cliTranslator())
}

func renderUndoPlanWithTranslator(rows []personal.Checkpoint, options personal.UndoOptions, tr i18n.Translator) string {
	if !hasUndoSnapshots(rows) {
		return renderUndoCheckpointsWithTranslator(rows, tr)
	}
	var b strings.Builder
	force := ""
	if options.Force {
		force = cliText(tr, "cmd.undo.plan.force")
	}
	if options.TurnID != "" {
		fmt.Fprintln(&b, cliText(tr, "cmd.undo.plan.header", map[string]any{"count": options.Count, "turn": cleanUndoValue(options.TurnID), "force": force}))
	} else {
		fmt.Fprintln(&b, cliText(tr, "cmd.undo.plan.header", map[string]any{"count": options.Count, "turn": "", "force": force}))
	}
	for _, row := range rows {
		for _, item := range row.Items {
			action, reason := item.Action, item.Reason
			if item.Conflict {
				// Undo skips a target changed after its snapshot unless forced
				// (personal.contentConflict), the same plan chat shows.
				reason = cliText(cliTranslator(), "cmd.undo.conflict")
				if !options.Force {
					action = personal.CheckpointActionSkip
				}
			}
			fmt.Fprintf(&b, "  %s %s%s%s\n", undoActionLabel(action), cleanUndoValue(item.Meta.Rel), undoSizeSuffix(item.Meta.Size), undoReasonSuffix(reason))
		}
		for _, shell := range row.Shell {
			fmt.Fprintln(&b, "  "+cliText(cliTranslator(), "cmd.undo.shell.line", map[string]any{"command": cleanUndoValue(shell.Command), "task": undoTaskSuffix(shell.TaskID)}))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderUndoResultWithTranslator(result personal.UndoResult, rows []personal.Checkpoint, tr i18n.Translator) string {
	parts := []string{}
	if result.Restored > 0 {
		parts = append(parts, cliText(tr, "cmd.undo.result.restored", map[string]any{"count": result.Restored}))
	}
	if result.Deleted > 0 {
		parts = append(parts, cliText(tr, "cmd.undo.result.deleted", map[string]any{"count": result.Deleted}))
	}
	if result.Skipped > 0 {
		parts = append(parts, cliText(tr, "cmd.undo.result.skipped", map[string]any{"count": result.Skipped}))
	}
	if result.Unrecoverable > 0 {
		parts = append(parts, cliText(tr, "cmd.undo.result.unrecoverable", map[string]any{"count": result.Unrecoverable}))
	}
	if len(parts) == 0 {
		parts = append(parts, cliText(tr, "cmd.undo.result.nothing"))
	}
	for _, row := range rows {
		for _, shell := range row.Shell {
			parts = append(parts, cliText(cliTranslator(), "cmd.undo.shell.note", map[string]any{"command": cleanUndoValue(shell.Command), "task": undoTaskSuffix(shell.TaskID), "session": cleanUndoValue(row.TurnID)}))
		}
	}
	return strings.Join(parts, "；") + "。"
}

func hasUndoSnapshots(rows []personal.Checkpoint) bool {
	for _, row := range rows {
		if row.HasSnapshots() {
			return true
		}
	}
	return false
}

func undoResultExitCode(result personal.UndoResult, rows []personal.Checkpoint) int {
	if result.Turns == 0 || result.Unrecoverable > 0 || len(result.Failures) > 0 {
		return 3
	}
	if result.Restored > 0 || result.Deleted > 0 {
		return 0
	}
	for _, row := range rows {
		for _, item := range row.Items {
			if (item.Action == personal.CheckpointActionRestore || item.Action == personal.CheckpointActionDelete) &&
				item.RootStatus == personal.SnapshotRootTrusted && !item.Conflict {
				return 0 // already at the before-image: an idempotent no-op
			}
		}
	}
	return 3
}

func cleanUndoValue(value string) string {
	return strings.Join(strings.Fields(termtext.Clean(value)), " ")
}

func undoTaskSuffix(taskID string) string {
	if taskID == "" {
		return ""
	}
	return "（task " + cleanUndoValue(taskID) + "）"
}

func undoActionLabel(action personal.CheckpointAction) string {
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

func undoReasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return "  " + cleanUndoValue(reason)
}

func undoSizeSuffix(size int64) string {
	if size <= 0 {
		return ""
	}
	return "  " + formatUndoBytes(size)
}

func formatUndoBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}
