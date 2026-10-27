package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gookit/goutil/cflag/capp"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/wiring"
)

// compactReport is the result of `suga session compact` (also its --json form).
type compactReport struct {
	SessionID    string `json:"session_id"`
	TokensBefore int    `json:"tokens_before"`
	TokensAfter  int    `json:"tokens_after"`
	Segment      int    `json:"segment"`
}

// newCompactCommand builds `suga session compact`: the command-line form of chat's
// :compact. It starts a new segment of an existing session between two turns,
// so a script (the eval runner's compact_after) or a job can force a context
// compaction at a chosen point of a long conversation without a terminal.
func newCompactCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var model, thinking string
	var asJSON bool
	cmd := capp.NewCmd("compact", "Start a new segment of a session now (the CLI form of chat :compact)", func(c *capp.Cmd) error {
		sessionID := strings.TrimSpace(c.Arg("session").String())
		if sessionID == "" {
			return errors.New("compact: a session id is required")
		}
		// Without --model the summary is written by the model the session already
		// uses (as a resumed chat does), not by the config default.
		if model == "" {
			if ref, err := wiring.SessionModelReference(ctx, sessionID); err == nil {
				model = ref
			}
		}
		// The runtime is needed for the model that writes the handoff summary;
		// no turn runs, so no tool of the session is called. The tool table is
		// part of the request the handoff package continues, so this process
		// declares the same tools a turn does: the mode the session last ran in
		// (write tools) and the background-task tool every turn process registers:
		// a table that differs from the last turn's falls back to
		// handoff_reuse=fallback:prefix_changed:tools (H4).
		wopts := wiring.Options{Model: model, Thinking: thinking}
		if mode, err := wiring.SessionRunMode(ctx, sessionID); err == nil && mode == personal.ModeAllowWrite {
			wopts.AllowWrite = true
		}
		taskManager, _, err := wiring.NewTaskManager(nil)
		if err != nil {
			return err
		}
		defer taskManager.Close()
		wopts.Tasks = taskManager
		runtime, err := wiring.OpenCLI(ctx, wopts)
		if err != nil {
			return err
		}
		defer func() { _ = runtime.Close() }()
		out, err := runtime.Runner.Compact(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("compact %s: %w", sessionID, err)
		}
		rep := compactReport{SessionID: sessionID, TokensBefore: out.TokensBefore, TokensAfter: out.TokensAfter, Segment: out.Segment}
		if asJSON {
			raw, err := json.Marshal(rep)
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, string(raw))
			return nil
		}
		// Same wording as chat's :compact report.
		fmt.Fprintf(stdout, "Compacted context: ~%d -> ~%d tokens (estimate)\n", rep.TokensBefore, rep.TokensAfter)
		return nil
	})
	cmd.SetOutput(stderr)
	cmd.StringVar(&model, "model", "", "model that writes the handoff summary (default: the session's model)")
	cmd.StringVar(&thinking, "thinking", "", "thinking level of that model: off, minimal, low, medium, high, xhigh, max, auto")
	cmd.BoolVar(&asJSON, "json", false, "write one JSON object instead of the text report")
	cmd.AddArg("session", "the session ID to compact", true)
	return cmd
}
