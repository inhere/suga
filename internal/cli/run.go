package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gookit/goutil/cflag"
	"github.com/gookit/goutil/cflag/capp"
	"github.com/inhere/suga/internal/adddir"
	"github.com/inhere/suga/internal/chat"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/output"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/outschema"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

var ErrInputRequired = errors.New("turn requires more input")

type personalOptions struct {
	Prompt    string
	Model     string
	Thinking  string
	SessionID string
	ResumeID  string
	// FromID starts a NEW session that inherits summary, plan and loaded tools
	// from this session (read-only on the source).
	FromID             string
	OutputFormat       string
	ReadOnly           bool
	AllowWrite         bool
	AppendSystemPrompt string
	// ClearAppendSystemPrompt drops the session's saved append prompt. An empty
	// --append-system-prompt keeps it instead: argv templates (job runners)
	// often always pass the flag and render it empty when there is no prompt.
	ClearAppendSystemPrompt bool
	NoProjectRules          bool
	// Injected (run only) marks this turn's prompt as not typed by a person;
	// the transcript records it as injected.
	Injected bool
	// MaxSteps (run only) overrides the config's max_steps for this turn
	// (--max-steps); 0 uses SUGA_MAX_STEPS, then the config.
	MaxSteps int
	// Verify (run only) is the --verify command: after a normally finished turn
	// suga runs it in the session workspace and hands a failure back to the
	// model as an injected turn (--verify-timeout, --verify-rounds).
	Verify string
	// VerifyRounds is how many fix rounds follow a failing check (1-5); the
	// first check does not count.
	VerifyRounds int
	// VerifyTimeout bounds one run of the verify command; a timeout is a
	// failure.
	VerifyTimeout time.Duration
	// OutputSchema is the --output-schema value (run only): "-" reads stdin, a
	// value starting with { or [ is inline JSON, anything else is a file path
	// (design 2026-10-22 2.1).
	OutputSchema string
	// contract and schemaText hold the compiled --output-schema (nil without it):
	// they travel in memory only and are never written to the session.
	contract   *outschema.Contract
	schemaText []byte
	// MaxOutputTokens overrides the config's max_output_tokens for this
	// invocation (--max-output-tokens); 0 uses SUGA_MAX_OUTPUT_TOKENS, then
	// models.<m>.max_output_tokens, then providers.<id>.max_output_tokens.
	MaxOutputTokens int
	// Budget contains the run/chat budget flag overrides. Zero values defer to
	// SUGA_BUDGET_* and budget.* settings.
	Budget     appconfig.BudgetFlags
	MCPConfigs []string
	CLIMode    bool
	// AddDirs are this invocation's --add-dir values ("<absolute
	// directory>[:ro|:rw]", default :ro); SUGA_ADD_DIRS is read alongside them.
	AddDirs []string
	// AllowCwdChange continues a resumed session in the current directory even
	// when it was created in another one (--allow-cwd-change). Without it, a
	// resume elsewhere is refused (chat asks first when it has a terminal).
	AllowCwdChange bool
	// ImagePaths are the run's --image values (repeatable, order kept). They are
	// prepared before the first turn and ride with Prompt
	// (personal.RunRequest.Images, the chat :image pipeline).
	ImagePaths []string
	// Images are the prepared image parts of ImagePaths. runPersonalWithOptions
	// sets them once the runtime is assembled; tests may set them directly.
	Images []agentcore.ImagePart
	// setupSession runs once the session is open and before the first turn: the
	// CLI publishes the session's added directories there.
	setupSession func(ctx context.Context, sessionID string) error
	// tasks is the run's background task manager (set by RunPersonalCLI, not
	// a flag); nil in tests that build a runner themselves.
	tasks *tasks.Manager
}

// RunPersonalCLI is the shared headless and interactive entry, independent of the old Web host.
func RunPersonalCLI(ctx context.Context, mode string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if mode != "run" && mode != "chat" {
		return fmt.Errorf("unsupported personal mode %q", mode)
	}
	return NewPersonalCommand(ctx, mode, stdin, stdout, stderr).Parse(args)
}

func runPersonalWithOptions(ctx context.Context, mode string, opts personalOptions, stdin io.Reader, stdout, stderr io.Writer) (retErr error) {
	// The session's additional directories (--add-dir, SUGA_ADD_DIRS) are parsed
	// first: a bad --add-dir value is a startup error, a bad SUGA_ADD_DIRS entry
	// is a warning (an outer tool set it, this process cannot fix it).
	dirs, warnings, err := adddir.Resolve(opts.AddDirs, os.Getenv(adddir.EnvVar), opts.ReadOnly)
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		fmt.Fprintln(stderr, "warning: "+warning)
	}
	// A resumed chat follows the session's selected model unless --model was
	// explicitly supplied. Read it before assembling the provider runtime.
	if mode == "chat" && opts.Model == "" && opts.FromID == "" {
		resumeID := opts.ResumeID
		if resumeID == "" {
			resumeID = opts.SessionID
		}
		if resumeID != "" {
			ref, readErr := wiring.SessionModelReference(ctx, resumeID)
			if readErr == nil {
				opts.Model = ref
			} else if !errors.Is(readErr, personal.ErrSessionNotFound) {
				return readErr
			}
		}
	}
	// A session continued in another directory is asked about (chat at a
	// terminal) or refused (run, and chat reading a pipe) before the runtime is
	// assembled: the workspace is fixed from here on, and no tool may run against
	// the wrong directory. The check may move this process into the session's
	// directory (chat's first choice), which everything below then follows.
	current, err := effectiveWorkspace()
	if err != nil {
		return err
	}
	cwdAction, err := guardResumeCwd(ctx, opts, current, mode == "chat" && isTerminalInput(stdin), stdin, stderr)
	if err != nil {
		return err
	}
	if cwdAction == cwdCancelled {
		fmt.Fprintln(stderr, cliText(cliTranslator(), "cli.cwd.cancelled"))
		return nil
	}
	// Background tasks (write modes): chat wakes on a task end while it
	// waits for input; the manager is closed (its tasks killed) before the
	// runtime, so their end events still reach the store.
	wake := make(chan struct{}, 1)
	taskManager, taskSettings, err := wiring.NewTaskManager(func(tasks.Info) {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return err
	}
	wopts := opts.wiringOptions(mode)
	wopts.AddDirs = dirs
	wopts.Tasks = taskManager
	runtime, err := wiring.OpenCLI(ctx, wopts)
	if err != nil {
		taskManager.Close()
		return err
	}
	defer func() { retErr = errors.Join(retErr, runtime.Close()) }()
	defer taskManager.Close()
	// The session is opened after the runtime is assembled, so its own
	// additional directories (--add-dir) are published once it is known,
	// before the first turn.
	opts.setupSession = func(ctx context.Context, sessionID string) error {
		if err := runtime.ApplyAddDirs(ctx, sessionID); err != nil {
			return err
		}
		if cwdAction != cwdUseCurrent {
			return nil
		}
		// Continued here on purpose (chat's second choice, --allow-cwd-change):
		// move the session's recorded directory with the process, so the listing
		// and a later resume agree (and the trace shows the move).
		_, err := runtime.Runner.AdoptSessionCwd(ctx, sessionID, current)
		return err
	}
	if mode == "chat" {
		var taskWake <-chan struct{}
		if taskSettings.WakeOnExit {
			taskWake = wake
		}
		return runChat(ctx, runtime, opts, stdin, stdout, stderr, taskManager, taskWake)
	}
	opts.tasks = taskManager
	// The --image pictures are read before the turn starts: an unusable one
	// (not readable, model of the run cannot see) fails the run without
	// creating a session or asking the model anything.
	images, err := prepareRunImages(runtime, opts.ImagePaths)
	if err != nil {
		return err
	}
	opts.Images = images
	// The output contract is compiled before the session exists: a bad schema
	// creates nothing and asks the model nothing (exit code 2).
	if value := strings.TrimSpace(opts.OutputSchema); value != "" {
		contract, schema, err := compileOutputSchema(value, stdin)
		if err != nil {
			return err
		}
		opts.contract, opts.schemaText = contract, schema
	}
	return executePersonalRun(ctx, runtime.Runner, opts, stdout, stderr)
}

// NewPersonalCommand owns the flags and validation shared by direct CLI calls
// and the root command registry.
func NewPersonalCommand(ctx context.Context, mode string, stdin io.Reader, stdout, stderr io.Writer) *capp.Cmd {
	var opts personalOptions
	desc := "Run one personal-agent turn"
	isChatMode := mode == "chat"
	if isChatMode {
		desc = "Chat with the personal agent"
	}
	cmd := capp.NewCmd(mode, desc, func(c *capp.Cmd) error {
		if len(c.RemainArgs()) != 0 {
			return fmt.Errorf("unexpected positional arguments: %s", strings.Join(c.RemainArgs(), " "))
		}
		if err := validatePersonalOptions(mode, opts); err != nil {
			return err
		}
		return runPersonalWithOptions(ctx, mode, opts, stdin, stdout, stderr)
	})
	cmd.SetOutput(stderr)
	cmd.StringVar(&opts.Model, "model", "", "model, <provider>/<model> or a bare model id (overrides SUGA_MODEL){[;m]}")
	cmd.StringVar(&opts.Thinking, "thinking", "", "thinking level of the main model: off, minimal, low, medium, high, xhigh, max, auto (overrides the config)")
	cmd.IntVar(&opts.MaxOutputTokens, "max-output-tokens", 0, "max output tokens per model request, >= 1 (overrides SUGA_MAX_OUTPUT_TOKENS, models.<m>.max_output_tokens and providers.<id>.max_output_tokens)")
	cmd.IntVar(&opts.Budget.Tokens, "budget-tokens", 0, "token budget for this turn or session, >= 0 (overrides SUGA_BUDGET_TOKENS and budget.tokens)")
	cmd.Float64Var(&opts.Budget.CostUSD, "budget-cost-usd", 0, "cost budget in USD, >= 0 (overrides SUGA_BUDGET_COST_USD and budget.cost_usd)")
	cmd.StringVar(&opts.Budget.Scope, "budget-scope", "", "budget scope: turn or session (overrides SUGA_BUDGET_SCOPE and budget.scope)")
	cmd.StringVar(&opts.SessionID, "session-id", "", "create or use this session ID;;sid")
	cmd.StringVar(&opts.ResumeID, "resume", "", "resume an existing session ID;;r")
	cmd.StringVar(&opts.FromID, "from", "", "start a new session that inherits the summary, plan and loaded tools of this session")
	cmd.StringVar(&opts.OutputFormat, "output-format", "text", "text or stream-json")
	cmd.BoolVar(&opts.ReadOnly, "read-only", false, "disable every mutating tool; MCP tools are limited to tools.read_only_allow{[;ro]}")
	cmd.BoolVar(&opts.AllowWrite, "allow-write", false, "allow workspace write and shell tools for this run")
	cmd.StringVar(&opts.AppendSystemPrompt, "append-system-prompt", "", "append system instructions after rules, empty keeps the saved prompt")
	cmd.BoolVar(&opts.ClearAppendSystemPrompt, "clear-append-system-prompt", false, "clear the session's saved append prompt (a non-empty --append-system-prompt still replaces it)")
	cmd.BoolVar(&opts.NoProjectRules, "no-project-rules", false, "skip project rule files for debugging")
	cmd.Var((*cflag.Strings)(&opts.MCPConfigs), "mcp-config", "Claude-style JSON MCP server config{[;mcp]}")
	cmd.Var((*cflag.Strings)(&opts.AddDirs), "add-dir", "extra absolute directory for this session as \"<directory>[:ro|:rw]\" (default :ro), repeatable; SUGA_ADD_DIRS adds more")
	cmd.BoolVar(&opts.AllowCwdChange, "allow-cwd-change", false, "continue a resumed session in the current directory even if it was created in another one")
	cmd.Var((*cflag.Strings)(&opts.ImagePaths), "image", "image file attached to this prompt (same checks, scaling and storage as chat :image)")
	// Registered for both modes like --image, and refused by chat in
	// validatePersonalOptions, so the error says why instead of "flag not defined".
	cmd.StringVar(&opts.OutputSchema, "output-schema", "", "JSON Schema the final answer must satisfy: a file path, inline JSON, or - for stdin; an answer that does not satisfy it is corrected once, then the run ends with exit code 6")
	if !isChatMode {
		cmd.StringVar(&opts.Prompt, "prompt", "", "task prompt for run mode;;p")
		cmd.BoolVar(&opts.Injected, "injected", false, "mark this turn's prompt as not typed by a person")
		cmd.IntVar(&opts.MaxSteps, "max-steps", 0, "max tool steps in this turn, >= 1 (overrides SUGA_MAX_STEPS and max_steps); sub-agent agents.max_steps is unaffected")
		cmd.StringVar(&opts.Verify, "verify", "", "command to run after a finished turn; a failure is handed back to the model to fix (does not go through approval)")
		cmd.IntVar(&opts.VerifyRounds, "verify-rounds", DefaultVerifyRounds, "how many fix rounds follow a failing --verify command, 1-5")
		cmd.DurationVar(&opts.VerifyTimeout, "verify-timeout", DefaultVerifyTimeout, "timeout of one --verify command run, e.g. 10m; a timeout is a failure")
	}
	return cmd
}

// wiringOptions maps the CLI flags to the runtime assembly options. mode "chat"
// sets ChatMode (tools.allow_write and the chat approver apply only there).
func (o personalOptions) wiringOptions(mode string) wiring.Options {
	return wiring.Options{
		ChatMode:   mode == "chat",
		Model:      o.Model,
		Thinking:   o.Thinking,
		AllowWrite: o.AllowWrite, ReadOnly: o.ReadOnly,
		// --read-only also limits the memory tool to get/search.
		MemoryReadOnly:     o.ReadOnly,
		MCPConfigs:         o.MCPConfigs,
		AppendSystemPrompt: o.AppendSystemPrompt, ClearAppendSystemPrompt: o.ClearAppendSystemPrompt,
		NoProjectRules:  o.NoProjectRules,
		MaxSteps:        o.MaxSteps,
		MaxOutputTokens: o.MaxOutputTokens,
		Budget:          o.Budget,
	}
}

func validatePersonalOptions(mode string, opts personalOptions) error {
	if opts.SessionID != "" && opts.ResumeID != "" {
		return fmt.Errorf("--session-id and --resume cannot be combined")
	}
	if opts.FromID != "" && opts.ResumeID != "" {
		return fmt.Errorf("--from and --resume cannot be combined")
	}
	if opts.OutputFormat != "text" && opts.OutputFormat != "stream-json" {
		return fmt.Errorf("unsupported output format %q", opts.OutputFormat)
	}
	if mode == "run" && strings.TrimSpace(opts.Prompt) == "" {
		return fmt.Errorf("--prompt is required for run mode")
	}
	if mode == "chat" && opts.Prompt != "" {
		return fmt.Errorf("--prompt is only supported in run mode")
	}
	if mode == "chat" && len(opts.ImagePaths) > 0 {
		return fmt.Errorf("--image is only supported in run mode")
	}
	if mode == "chat" && strings.TrimSpace(opts.OutputSchema) != "" {
		return fmt.Errorf("--output-schema is only supported in run mode")
	}
	if mode == "chat" && opts.OutputFormat != "text" {
		return fmt.Errorf("chat mode requires text output")
	}
	if opts.MaxSteps < 0 {
		return fmt.Errorf("--max-steps must be a whole number >= 1, got %d", opts.MaxSteps)
	}
	if opts.MaxOutputTokens < 0 {
		return fmt.Errorf("--max-output-tokens must be a whole number >= 1, got %d", opts.MaxOutputTokens)
	}
	if mode != "run" {
		// The --verify* flags exist for run only; chat never checks a command.
		if opts.Verify != "" || opts.VerifyRounds != 0 || opts.VerifyTimeout != 0 {
			return fmt.Errorf("--verify is only supported in run mode")
		}
	} else if opts.Verify != "" {
		// The rounds and the timeout only matter once a command is given (an
		// options struct built without the flags keeps the defaults).
		if opts.VerifyRounds < 1 || opts.VerifyRounds > MaxVerifyRounds {
			return fmt.Errorf("--verify-rounds must be a whole number between 1 and %d, got %d", MaxVerifyRounds, opts.VerifyRounds)
		}
		if opts.VerifyTimeout < 0 {
			return fmt.Errorf("--verify-timeout must be a positive duration, got %s", opts.VerifyTimeout)
		}
	}
	if opts.Budget.Tokens < 0 {
		return fmt.Errorf("--budget-tokens must be a whole number >= 0, got %d", opts.Budget.Tokens)
	}
	if opts.Budget.CostUSD < 0 {
		return fmt.Errorf("--budget-cost-usd must be a number >= 0, got %v", opts.Budget.CostUSD)
	}
	return nil
}

func executePersonalRun(ctx context.Context, runner *personal.Runner, opts personalOptions, stdout, stderr io.Writer) error {
	sessionID, err := openRequestedSession(ctx, runner, opts)
	if err != nil {
		return err
	}
	// Publish the session's additional directories (--add-dir) now that the
	// session is known and before the first turn.
	if opts.setupSession != nil {
		if err := opts.setupSession(ctx, sessionID); err != nil {
			return err
		}
	}
	var encoder *json.Encoder
	if opts.OutputFormat == "stream-json" {
		encoder = json.NewEncoder(stdout)
		// The first line records how many pictures this prompt carries; a run
		// without --image keeps the line byte-for-byte as it was.
		session := output.SessionLine(sessionID)
		if len(opts.Images) > 0 {
			session["images"] = len(opts.Images)
		}
		if err := encoder.Encode(output.WithModel(session, runModelRef(runner))); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(stderr, "session id:", sessionID)
		fmt.Fprintln(stderr, "model:", termtext.Clean(chat.ModelLabel(runner.ProviderName(), runner.Model())))
	}
	live := output.NewLiveText(stdout)
	var budget *personal.BudgetSnapshot
	emit := func(_ context.Context, event personal.Event) error {
		if event.Budget != nil {
			budget = event.Budget
		} else if event.Usage != nil && event.Usage.Budget != nil {
			budget = event.Usage.Budget
		}
		if event.Kind == personal.EventSessionRecovered {
			// The session was left running by a process that died mid-turn and has
			// been taken back to open before this turn (personal/recovery.go);
			// stdout keeps the answer alone, like the retry notice below.
			fmt.Fprintln(stderr, personal.SessionRecoveredNotice)
			return nil
		}
		if encoder == nil {
			live.Handle(event) // text mode prints the answer as it streams
			if event.Kind == string(agentcore.StreamEventRetry) {
				fmt.Fprintln(stderr, output.RetryNotice(event)) // stdout stays the answer only
			}
			return nil
		}
		line, ok := output.EventLine(event)
		if !ok {
			return nil
		}
		return encoder.Encode(line)
	}
	runStart := time.Now()
	result, err := runner.Run(ctx, personal.RunRequest{SessionID: sessionID, Prompt: opts.Prompt, Images: opts.Images, Injected: opts.Injected, Emit: emit})
	if err == nil && result.State == personal.RunCompleted && opts.tasks != nil {
		result, err = finishBackgroundTasks(ctx, runner, opts.tasks, sessionID, result, emit, live, stderr)
	}
	// A turn that looks finished too early is pushed back with an injected turn
	// (personal.CheckNudge), before --verify: the check then covers what the pushed
	// turns changed. A --verify command checks the delivery itself, so N1 is skipped
	// there.
	if err == nil && result.State == personal.RunCompleted {
		result, err = nudgeAfterTurn(ctx, runner, sessionID, result, strings.TrimSpace(opts.Verify) != "", emit, live, encoder, stderr)
	}
	// The caller's --verify check runs after a turn finished normally (background
	// tasks settled first): a failing command comes back as an injected turn, so
	// the model gets a chance to fix it before the run ends. A turn that failed,
	// yielded or was cancelled is not checked.
	var verifyRep *verifyReport
	if opts.needsVerify(result, err) {
		workspace, wsErr := effectiveWorkspace()
		if wsErr != nil {
			return wsErr
		}
		result, verifyRep, err = verifyAfterTurn(ctx, runner, verifySettingsFrom(opts, workspace), sessionID, result, emit, live, encoder, stderr)
	}
	if encoder == nil && verifyRep != nil && !verifyRep.Passed && err == nil && result.State == personal.RunCompleted {
		// Text mode: the check that never passed is summarised on stderr, with the
		// tail of its last output; stdout stays the answer only.
		fmt.Fprintln(stderr, verifySummary(opts.Verify, verifyRep))
	}
	// The output contract is the pipeline's last step (design 2026-10-22 4.1): it
	// judges the answer the caller finally gets, after nudge and --verify had their
	// pushes. A failing answer gets one injected correction round, then a final
	// check; nothing earlier in the run consumed the contract's budget.
	var contractRep *personal.ContractReport
	if opts.contract != nil && err == nil {
		var rep personal.ContractReport
		result, rep, err = runner.RunWithContract(ctx, personal.RunRequest{SessionID: sessionID},
			opts.contract, opts.schemaText, result, personal.ContractHooks{
				// End the streamed answer line before the correction round streams its own.
				BeforeCorrection: func() { live.Finish() },
				Emit:             emit,
			})
		contractRep = &rep
	}
	streamed := live.Finish() // end the streamed line before any error or status output
	_, _, window, _ := runner.ModelSettings()
	if encoder == nil {
		// Text mode: a wrap-up names its reason on stderr, so a short answer
		// is not read as a finished one; stdout stays the answer only.
		if notice := output.WrapUpNotice(result.WrapUp, runner.MaxSteps()); notice != "" {
			fmt.Fprintln(stderr, notice)
		}
		// A wrap-up can itself be truncated by the per-request output limit;
		// keep that distinct from an ordinary truncated answer.
		if result.Truncated && result.WrapUp != "" {
			fmt.Fprintln(stderr, output.WrapUpTruncatedNotice(result.OutputLimit))
		} else if result.Truncated {
			fmt.Fprintln(stderr, output.OutputLimitNotice(result.OutputLimit))
		}
		// Text mode: a usage summary on stderr (stdout stays the answer only).
		fmt.Fprintln(stderr, runSummary(runner, result, time.Since(runStart), budget))
		if contractRep != nil && contractRep.Checked && !contractRep.Passed {
			// The answer that did not satisfy the caller's schema is named on stderr;
			// stdout still carries it, so a script can read what the model said.
			fmt.Fprintln(stderr, contractSummary(*contractRep))
		}
	}
	if err != nil {
		if encoder != nil {
			_ = encoder.Encode(output.WithModel(output.ErrorLine(sessionID, err, result, budget), runModelRef(runner)))
		}
		return err
	}
	if result.Status == agentcore.RunYielded {
		if encoder != nil {
			_ = encoder.Encode(output.WithModel(output.YieldedLine(sessionID, result, budget), runModelRef(runner)))
		}
		return ErrInputRequired
	}
	// A turn with any refused tool call ends with input_required, even when the
	// model worked around the refusal: the caller still learns authorization
	// was missing. denied_tools is the only field added to the line.
	if len(result.DeniedTools) > 0 && encoder != nil {
		if err := encoder.Encode(output.WithModel(output.DeniedLine(sessionID, result, budget), runModelRef(runner))); err != nil {
			return err
		}
	}
	// A check that never passed still reports the answer and the verify outcome on
	// the result line; the run then ends with exit code 5 (design §5).
	failedVerify := verifyRep != nil && !verifyRep.Passed
	if encoder != nil {
		line := output.ResultLineWithContext(sessionID, result, window, budget)
		if verifyRep != nil {
			line = output.WithVerify(line, verifyRep.Passed, verifyRep.Rounds)
		}
		if contractRep != nil {
			line = output.WithOutputSchema(line, *contractRep)
		}
		if err := encoder.Encode(output.WithModel(line, runModelRef(runner))); err != nil {
			return err
		}
	} else if !streamed {
		if _, err := fmt.Fprintln(stdout, result.Output); err != nil {
			return err
		}
	}
	if failedVerify {
		return processExitError{code: 5, err: fmt.Errorf("%w: %s → exit %d（已回灌 %d 轮）", ErrVerifyFailed,
			oneLineText(termtext.Clean(opts.Verify)), verifyRep.Last.ExitCode, max(verifyRep.Rounds-1, 0))}
	}
	if contractRep != nil && contractRep.Checked && !contractRep.Passed {
		// Exit code 6: the turn finished, but its answer does not have the shape the
		// caller asked for, even after one correction (design 4.4).
		return processExitError{code: 6, err: fmt.Errorf("%w: %s", ErrOutputContract, contractSummary(*contractRep))}
	}
	return nil
}

func openRequestedSession(ctx context.Context, runner *personal.Runner, opts personalOptions) (string, error) {
	if opts.FromID != "" {
		// --session-id names the new session; an existing one is rejected by Handoff.
		model, err := runner.Handoff(ctx, personal.HandoffRequest{SourceID: opts.FromID, NewID: opts.SessionID, Title: opts.Prompt, Origin: runner.SessionOrigin(personal.OriginRun)})
		if err != nil {
			return "", personal.SessionNotFound(err, opts.FromID)
		}
		return model.ID, nil
	}
	if opts.ResumeID != "" {
		model, err := runner.LoadSession(ctx, opts.ResumeID)
		if err != nil {
			return "", personal.SessionNotFound(err, opts.ResumeID)
		}
		return model.ID, nil
	}
	if opts.SessionID != "" {
		model, err := runner.LoadSession(ctx, opts.SessionID)
		if err == nil {
			return model.ID, nil
		}
		if !errors.Is(err, personal.ErrSessionNotFound) {
			return "", err
		}
		model, err = runner.NewSessionFor(ctx, opts.SessionID, opts.Prompt, runner.SessionOrigin(personal.OriginRun))
		if err != nil {
			return "", err
		}
		return model.ID, nil
	}
	model, err := runner.NewSessionFor(ctx, "", opts.Prompt, runner.SessionOrigin(personal.OriginRun))
	if err != nil {
		return "", err
	}
	return model.ID, nil
}

// runSummary is the text-mode closing line: model, this turn's tokens, the
// session total and context share when known, and the elapsed time.
func runSummary(runner *personal.Runner, result personal.RunResult, elapsed time.Duration, budget *personal.BudgetSnapshot) string {
	line := fmt.Sprintf("%s · in %d / out %d tokens", termtext.Clean(chat.ModelLabel(runner.ProviderName(), runner.Model())),
		result.Usage.InputTokens, result.Usage.OutputTokens)
	if s := result.SessionUsage; s.InputTokens > 0 || s.OutputTokens > 0 {
		line += fmt.Sprintf("（会话累计 in %d / out %d）", s.InputTokens, s.OutputTokens)
	}
	_, _, window, _ := runner.ModelSettings()
	if share := output.ContextShare(result.LastInputTokens, window); share != "" {
		line += " · " + share
	}
	if budget != nil && budget.Estimated {
		line += "（其中本轮用量含估算）"
	}
	return line + " · " + chat.FormatElapsed(elapsed)
}

// finishBackgroundTasks runs once more when the turn ended with background
// tasks still running: run exits after the turn and takes them along, so the
// model is reminded (once) to wait for or kill them first. Whatever still
// runs afterwards is killed now and named on stderr.
func finishBackgroundTasks(ctx context.Context, runner *personal.Runner, m *tasks.Manager, sessionID string,
	result personal.RunResult, emit personal.EmitFunc, live *output.LiveText, stderr io.Writer) (personal.RunResult, error) {
	if running := m.Running(sessionID); len(running) > 0 {
		live.Finish()
		var b strings.Builder
		fmt.Fprintf(&b, "[background tasks still running] This run ends after your answer and kills them:")
		for _, info := range running {
			b.WriteString("\n- " + tasks.Describe(info))
		}
		b.WriteString("\nWait for them (task wait) or kill them (task kill) as the task needs, then give your final answer again.")
		next, err := runner.Run(ctx, personal.RunRequest{SessionID: sessionID, Prompt: b.String(), Injected: true, Emit: emit})
		if err != nil {
			return next, err
		}
		result = next
	}
	for _, info := range m.KillSession(sessionID) {
		fmt.Fprintln(stderr, "killed background task at exit: "+tasks.Describe(info))
	}
	return result, nil
}

// runModelRef is the "provider/model" a run's stream-json lines carry: the
// first (session) line says which model the run starts with, and the closing
// line which model answered (a /model switch or a fallback can change it).
func runModelRef(runner *personal.Runner) string {
	return chat.ModelLabel(runner.ProviderName(), runner.Model())
}
