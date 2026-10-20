package cli

import (
	"context"
	"io"
	"os"

	"github.com/inhere/suga/internal/chat"
	appconfig "github.com/inhere/suga/internal/config"
	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// runChat starts the interactive REPL on an assembled runner.
// The runtime's asker (chat approver questions) is bound by chat when stdin is a terminal.
func runChat(ctx context.Context, runtime *wiring.Runtime, opts personalOptions, stdin io.Reader, stdout, stderr io.Writer,
	taskManager *tasks.Manager, taskWake <-chan struct{}) error {
	// The hook command runs with the session's variables too (SUGA_SESSION_ID,
	// SUGA_SESSION_NAME and, inside a turn, SUGA_TURN_ID).
	hooks, err := sugahooks.New(sugahooks.WithExtraEnv(runtime.SessionEnv))
	if err != nil {
		return err
	}
	idle, err := appconfig.ReadIdleSettings()
	if err != nil {
		return err
	}
	diffLines, err := appconfig.ChatDiffLines()
	if err != nil {
		return err
	}
	codeMaxLines, err := appconfig.ChatCodeMaxLines()
	if err != nil {
		return err
	}
	notify, err := appconfig.ChatNotifyMode()
	if err != nil {
		return err
	}
	notifyAfter, err := appconfig.ChatNotifyAfter()
	if err != nil {
		return err
	}
	agentProgress, err := appconfig.ChatAgentProgress()
	if err != nil {
		return err
	}
	modelOptions := make([]chat.ModelOption, 0, len(runtime.ModelOptions()))
	for _, option := range runtime.ModelOptions() {
		modelOptions = append(modelOptions, chat.ModelOption{Provider: option.Provider, Model: option.Model,
			Thinking: option.Thinking, ContextWindow: option.ContextWindow, MaxOutputTokens: option.MaxOutputTokens})
	}
	modelSwitch := func(ctx context.Context, sessionID, ref string) (chat.ModelOption, error) {
		option, err := runtime.SwitchModel(ctx, sessionID, ref)
		if err != nil {
			return chat.ModelOption{}, err
		}
		return chat.ModelOption{Provider: option.Provider, Model: option.Model, Thinking: option.Thinking,
			ContextWindow: option.ContextWindow, MaxOutputTokens: option.MaxOutputTokens}, nil
	}
	return chat.Run(ctx, runtime.Runner, chat.Options{
		SessionID: opts.SessionID, ResumeID: opts.ResumeID, FromID: opts.FromID,
		Asker: runtime.Asker, WriteTools: runtime.WriteTools,
		Thinking: runtime.Thinking, ContextWindow: runtime.ContextWindow,
		Markdown:       appconfig.ChatMarkdownEnabled(),
		Spinner:        appconfig.ChatSpinnerEnabled(),
		AgentProgress:  agentProgress,
		HighlightInput: appconfig.ChatHighlightInputEnabled(),
		Steering:       appconfig.ChatSteeringEnabled(),
		TerminalTitle:  appconfig.ChatTerminalTitleEnabled(),
		// What a file tool changed (chat.diff / chat.diff_lines): the diff under the
		// tool result, before an approval question and with :diff. The change itself
		// comes from the runner's tool_call events, which wiring turns on for chat.
		Diff:      appconfig.ChatDiffEnabled(),
		DiffLines: diffLines,
		// Code blocks (:code, chat.code_*): highlighting by the fence's language,
		// its theme and the fold limit that names the :code command.
		CodeHighlight: appconfig.ChatCodeHighlightEnabled(),
		CodeTheme:     appconfig.ChatCodeTheme(),
		CodeMaxLines:  codeMaxLines,
		IdleAfter:     idle.After, IdleMinRatio: idle.MinRatio,
		Tasks: taskManager, TaskWake: taskWake, StopBackground: sugahooks.StopBackground(), Notify: notify, NotifyAfter: notifyAfter,
		ModelOptions: modelOptions, ModelSwitch: modelSwitch,
		SetupSession: opts.setupSession,
		// The session's added directories (:add-dir/:dirs/:rm-dir): the handle
		// shares the metadata and the set the tools read with ApplyAddDirs.
		Dirs: runtime.SessionAddDirs(),
		// :image / Alt+V (design 0.3): the view_image pipeline and the live vision
		// gate of the runtime, so a refusal follows a :model switch.
		ReadImage: runtime.ReadImage, Vision: runtime.VisionGate(),
		// Alt+V (design 0.3): the same pipeline behind :image, over the clipboard.
		PasteImage: runtime.PasteImageCallback(),
		// The local commands of the prompt (!cmd / !!cmd): the same shell and
		// workspace the shell tools use, and no turn of their own.
		Local: runtime.LocalCommands(),
	}, stdin, stdout, stderr, hooks)
}

// RunPersonalChat gives the terminal REPL the same persistent session engine as run mode.
func RunPersonalChat(ctx context.Context, args []string) error {
	return RunPersonalCLI(ctx, "chat", args, os.Stdin, os.Stdout, os.Stderr)
}
