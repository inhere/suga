package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/gookit/goutil/cflag/capp"
	"github.com/inhere/suga/internal/acpserver"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

func newACPCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) *capp.Cmd {
	acp := capp.NewCmd("acp", "Serve an ACP session over stdio", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) != 0 {
			return fmt.Errorf("suga acp does not accept arguments")
		}
		if err := RunPersonalACP(ctx, stdin, stdout); err != nil {
			return fmt.Errorf("acp: %w", err)
		}
		return nil
	})
	acp.SetOutput(stderr)
	return acp
}

// RunPersonalACP serves the same persistent personal runner used by CLI over stdio.
func RunPersonalACP(ctx context.Context, input io.Reader, output io.Writer) error {
	// Background tasks belong to the ACP process, not a prompt: each prompt
	// opens its own runtime, while a task keeps running across turns (its end
	// notice goes into the session's next prompt). session/cancel cancels the
	// turn only; tasks still running when the connection closes are killed.
	taskManager, _, err := wiring.NewTaskManager(nil)
	if err != nil {
		return err
	}
	defer taskManager.Close()
	// ContentBlock::Image is declared (design 0.3, ACP second phase) when any
	// model of this configuration can see pictures; initialize happens before
	// any session exists, so the offline catalog decides.
	promptImage, err := promptImageCapability()
	if err != nil {
		return err
	}
	return acpserver.Serve(ctx, input, output, func(ctx context.Context, cwd string, readOnly bool, servers []mcp.ServerSettings, gate acpserver.TurnGate) (acpserver.Runtime, error) {
		workspace, err := filepath.Abs(cwd)
		if err != nil {
			return acpserver.Runtime{}, fmt.Errorf("resolve ACP workspace: %w", err)
		}
		// The gate is both the notification hook and the approval policy. A nil
		// gate (session create/load, no turn) leaves the approver nil, which the
		// approval layer treats as deny-all.
		var hooks []agentcore.Hook
		var approver tools.Approver
		var memoryConfirm wiring.MemoryConfirmFunc
		if gate != nil {
			hooks, approver, memoryConfirm = []agentcore.Hook{gate}, gate, gate.ConfirmMemory
		}
		// read_only also limits the memory tool to get/search; memory writes of
		// a tainted session are confirmed through the gate's permission request.
		runtime, err := wiring.Open(ctx, wiring.Options{Workspace: workspace, RuleCWD: workspace, AllowWrite: !readOnly,
			MemoryReadOnly: readOnly, MemoryConfirm: memoryConfirm, Servers: servers, Hooks: hooks, Approver: approver,
			Tasks: taskManager})
		if err != nil {
			return acpserver.Runtime{}, err
		}
		registry := runtime.Registry
		lookup := func(name string) agentcore.Tool {
			tool, err := registry.Get(name)
			if err != nil {
				return nil
			}
			return tool
		}
		models := make([]acpserver.ModelOption, 0, len(runtime.ModelOptions()))
		for _, m := range runtime.ModelOptions() {
			models = append(models, acpserver.ModelOption{Ref: m.Ref(), Name: m.Model, Thinking: m.Thinking})
		}
		// The model select and its switch ride on the runtime: CurrentModel reads
		// the runner's live main model, SwitchModel goes through conversation's
		// :model path (wiring.Runtime.SwitchModel) so persistence and trace agree.
		rt := acpserver.Runtime{Runner: runtime.Runner, Close: runtime.Close, LookupTool: lookup,
			Policy: runtime.ACPPolicy, ContextWindow: runtime.ContextWindow, Vision: runtime.VisionGate(),
			Models: models,
			CurrentModel: func() string {
				provider, model, _, _ := runtime.Runner.ModelSettings()
				if provider == "" || model == "" {
					return ""
				}
				return provider + "/" + model
			},
			SwitchModel: func(ctx context.Context, sessionID, ref string) (string, error) {
				option, err := runtime.SwitchModel(ctx, sessionID, ref)
				if err != nil {
					return "", err
				}
				return option.Ref(), nil
			}}
		return rt, nil
	}, promptImage)
}

// promptImageCapability reads the configured providers and model roles and
// reports whether one of the model catalog's entries can see pictures.
func promptImageCapability() (bool, error) {
	providers, err := appconfig.ReadProviders()
	if err != nil {
		return false, err
	}
	settings, err := appconfig.ReadModelSettings("")
	if err != nil {
		return false, err
	}
	return appconfig.AnyVisionModel(providers, settings), nil
}
