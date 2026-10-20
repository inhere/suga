// Package wiring assembles the personal runner (provider, tools, store, rules)
// shared by the CLI and ACP front ends.
package wiring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	gkconfig "github.com/gookit/config/v2"
	"github.com/inhere/suga/internal/adddir"
	"github.com/inhere/suga/internal/clipboardimage"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/llm"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// Options carries everything needed to assemble one runtime.
type Options struct {
	// Workspace is the tool workspace root; RuleCWD is where project rules are read.
	// Both are filled by OpenCLI.
	Workspace string
	RuleCWD   string
	Model     string
	// Thinking overrides the main model's thinking level (--thinking).
	Thinking string
	// AllowWrite exposes workspace write and shell tools.
	AllowWrite bool
	// ReadOnly (OpenCLI only) narrows the MCP gate to tools.read_only_allow and
	// overrides AllowWrite.
	ReadOnly bool
	// Servers are extra MCP servers (from --mcp-config or the ACP session).
	Servers []mcp.ServerSettings
	// PreferredTools is the runtime discovery snapshot shown in segment context.
	PreferredTools []PreferredToolStatus
	// MCPConfigs are Claude-style config files read by OpenCLI into Servers.
	MCPConfigs []string
	Hooks      []agentcore.Hook
	// Approver is the permission policy of the approval layer (run: MCP
	// allowlist; chat: chat approver set by OpenCLI; ACP: RequestPermission);
	// nil denies every tool call (fail closed).
	Approver           tools.Approver
	AppendSystemPrompt string
	// ClearAppendSystemPrompt drops the session's saved append prompt. An empty
	// AppendSystemPrompt keeps it instead: argv templates (job runners)
	// often always pass the flag and render it empty when there is no prompt.
	ClearAppendSystemPrompt bool
	NoProjectRules          bool
	// CLIMode always exposes configured MCP tools; the dispatch gate enforces permissions.
	CLIMode bool
	// ChatMode (OpenCLI only) marks the interactive chat: tools.allow_write may
	// enable write tools there, and tools are approved by the chat approver.
	ChatMode bool
	// MemoryReadOnly (--read-only, ACP read_only) limits the memory tool to
	// get/search. The zero value allows memory writes: memory is suga's own
	// state, not the workspace, so a plain run may write it.
	MemoryReadOnly bool
	// Tasks runs shell_exec with background: true and backs the task tool
	// (write modes only); nil leaves background tasks off. The host owns it
	// and closes it (killing its tasks) when the session or process ends.
	Tasks *tasks.Manager
	// MaxSteps overrides the config's max_steps for the main agent's turns
	// (--max-steps, run only); 0 uses SUGA_MAX_STEPS, then max_steps.
	MaxSteps int
	// Budget carries raw run/chat flag overrides. Zero values defer to
	// SUGA_BUDGET_* and budget.* settings.
	Budget appconfig.BudgetFlags
	// MaxOutputTokens overrides the configured output token limit for this
	// invocation (--max-output-tokens); 0 uses SUGA_MAX_OUTPUT_TOKENS, then
	// providers.<id>.models.<m>.max_output_tokens /
	// providers.<id>.max_output_tokens (see appconfig.ResolveMaxOutputTokens).
	MaxOutputTokens int
	// MemoryConfirm confirms memory writes of tainted sessions (ACP: the turn
	// gate's permission request; chat: set by OpenCLI to ask at the terminal).
	// nil (run) refuses them.
	MemoryConfirm MemoryConfirmFunc
	// AddDirs are the additional directories this invocation asks for
	// (--add-dir, SUGA_ADD_DIRS, already parsed by the front end). A session
	// keeps them in its metadata; ApplyAddDirs publishes the merged set.
	AddDirs []adddir.Dir
	// sessionDirs is the live set the tools and the environment prompt read. It
	// is created by OpenCLI so the chat approver and the tools share one set; nil
	// (a runtime assembled directly) means no additional directory.
	sessionDirs *native.AddDirs
	// approvalPolicy is compiled before Open builds the registry. A child takes a
	// fresh copy through this callback so chat session rules are included without
	// sharing a mutable policy with an already-running child.
	approvalPolicy   *approvalPolicy
	approvalSnapshot func() approvalPolicy
}

// Runtime is one assembled personal runner and the resources it owns.
// ModelOption is one configured model that chat can select with :model.
type ModelOption struct {
	Provider        string
	Model           string
	Thinking        string
	ContextWindow   int
	MaxOutputTokens int
}

// Ref returns the canonical provider/model spelling used by :model.
func (m ModelOption) Ref() string { return m.Provider + "/" + m.Model }

type Runtime struct {
	Runner *personal.Runner
	// Registry is the base tool registry (lets ACP tell MCP tools apart).
	Registry *tools.Registry
	// PreferredTools is the startup discovery snapshot used by prompt and
	// manifest layers; it is frozen for this runtime.
	PreferredTools []PreferredToolStatus
	// ACPPolicy exposes the same compiled policy to the ACP permission hook
	// without coupling the internal wiring package to the ACP server package.
	ACPPolicy func(agentcore.ToolCall, agentcore.Tool) (action, rule, scope string)
	// ContextWindow is the window of the model actually used by the provider.
	ContextWindow int
	// Budget is the resolved budget input retained for later runner/provider
	// assembly. Its zero value means that budgeting is disabled.
	Budget appconfig.BudgetSettings
	// Thinking is the main model's effective thinking level ("" = the
	// provider's default).
	Thinking string
	// Asker (chat mode only, else nil) is where the chat approver asks its
	// questions; chat binds its terminal input to it. Unbound, every tool that
	// needs a confirmation is refused.
	Asker *tools.BindableAsker
	// WriteTools reports whether workspace write and shell tools are exposed.
	WriteTools bool
	// AddDirs is the session's additional-directory set (--add-dir): the tools
	// read it at every call, so the front end fills it once the session is known
	// (ApplyAddDirs).
	AddDirs *native.AddDirs
	// Resident reports the resident context of this runtime (system prompt,
	// environment, rules, memory index, skills, append prompt, deferred tools)
	// for the manifest and the chat start notice; nil leaves both without it.
	Resident func(context.Context) ResidentReport

	repo         *store.Store
	capabilities defaultCapabilities
	// sessionDirs is the handle on the session's added directories: ApplyAddDirs
	// publishes them through it, chat's :add-dir/:rm-dir commands and the
	// outside-directory question change them through it too, so the metadata and
	// the live set stay in step.
	sessionDirs *SessionAddDirs
	// local is the runner of chat's local commands ("!cmd"/"!!cmd"): the shell,
	// workspace and output limits are the ones the shell tools use, so a command
	// a person types there behaves like one the model runs.
	local *native.LocalCommand
	// sessionEnv holds the variables suga exports to subprocesses of a session
	// (SUGA_SESSION_ID/NAME/TURN_ID): the shell tools and chat's local commands
	// read it at every run, and chat hands it to the hook client.
	sessionEnv   *sessionEnvProvider
	modelOptions []ModelOption
	modelSpecs   map[string]appconfig.ModelSpec
	fallbacks    []appconfig.ModelSpec
	// These retain invocation-level overrides when :model selects another model.
	thinkingOverride  string
	maxOutputOverride int
	// tools is the registry stack the runner calls (visibility -> approval
	// -> base); kept for tests.
	tools    agentcore.ToolRegistry
	vision   *tools.VisionGate
	approval approvalPolicy
}

// ModelOptions returns the configured models in deterministic order.
func (rt *Runtime) ModelOptions() []ModelOption {
	if rt == nil {
		return nil
	}
	return append([]ModelOption(nil), rt.modelOptions...)
}

// SwitchModel builds the selected main provider, retaining the runtime's
// fallback pool, then asks the runner to persist and activate it for the next turn.
func (rt *Runtime) SwitchModel(ctx context.Context, sessionID, ref string) (ModelOption, error) {
	if rt == nil || rt.Runner == nil {
		return ModelOption{}, errors.New("personal runtime is nil")
	}
	ref = strings.TrimSpace(ref)
	spec, ok := rt.modelSpecs[ref]
	if !ok {
		refs := make([]string, 0, len(rt.modelOptions))
		for _, option := range rt.modelOptions {
			refs = append(refs, option.Ref())
		}
		return ModelOption{}, fmt.Errorf("model %q is not configured; choose one of: %s", ref, strings.Join(refs, ", "))
	}
	if rt.thinkingOverride != "" {
		spec.Thinking = rt.thinkingOverride
	}
	provider, err := buildMainProviderSpecsWithEstimate(spec, rt.fallbacks, estimateOptionsForBudget(rt.Budget.Tokens, rt.Budget.CostUSD, rt.Budget.Estimate, rt.Budget.EstimateFactor))
	if err != nil {
		return ModelOption{}, err
	}
	maxOutputTokens, err := appconfig.ResolveMaxOutputTokens(rt.maxOutputOverride, spec)
	if err != nil {
		return ModelOption{}, err
	}
	option := ModelOption{Provider: spec.Provider.ID, Model: spec.Model, Thinking: spec.ThinkingLevel(),
		ContextWindow: appconfig.ContextWindowFor(spec), MaxOutputTokens: maxOutputTokens}
	if err := rt.Runner.SwitchModel(ctx, sessionID, provider, option.Provider, option.Model, option.ContextWindow, maxOutputTokens); err != nil {
		return ModelOption{}, err
	}
	// The tool table follows the new model: view_image is listed only while the
	// current main model can see images.
	rt.vision.SetVision(appconfig.VisionFor(spec))
	rt.ContextWindow, rt.Thinking = option.ContextWindow, option.Thinking
	return option, nil
}

// LocalCommands is the runner of chat's local commands ("!cmd"/"!!cmd"): chat
// runs a line typed at the prompt through it. It is nil for a nil runtime.
func (rt *Runtime) LocalCommands() *native.LocalCommand {
	if rt == nil {
		return nil
	}
	return rt.local
}

// SessionAddDirs is the runtime's handle on the session's added directories
// (--add-dir): the chat front end lists and changes them through it after the
// session is open (ApplyAddDirs).
func (p *Runtime) SessionAddDirs() *SessionAddDirs {
	if p == nil {
		return nil
	}
	return p.sessionDirs
}

// Close releases the session store and tool providers.
func (p *Runtime) Close() error {
	if p == nil {
		return nil
	}
	return errors.Join(p.repo.Close(), p.capabilities.Close())
}

// ReadImage loads a picture for chat's :image / the Alt+V clipboard paste with
// the view_image rules (design 0.3): the same path resolution, input limits
// and scaling, so both sides hand the model identical pictures.
func (rt *Runtime) ReadImage(path string) (agentcore.ImagePart, string, error) {
	if rt == nil || rt.sessionDirs == nil {
		return agentcore.ImagePart{}, "", errors.New("读图在本进程不可用")
	}
	return ReadImageAt(rt.sessionDirs.Workspace(), path)
}

// ReadImageAt loads a picture with the rules of Runtime.ReadImage, resolving a
// relative path against workspace. `suga send --image` uses it with the sender's
// own working directory, so both ends check, scale and name a picture the same
// way (design send --image).
//
// The path is one the person typed (:image, run --image, send --image), not a
// model's tool call, so it is not confined to the workspace: its own directory
// is added as a read root. Every other view_image check (regular file only,
// input size, pixel and format limits) still applies.
func ReadImageAt(workspace, path string) (agentcore.ImagePart, string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workspace, abs)
	}
	data, name, err := native.ReadImageFile(workspace, []string{filepath.Dir(filepath.Clean(abs))}, nil, path)
	if err != nil {
		return agentcore.ImagePart{}, "", err
	}
	return native.PrepareImage(data, name, imageMaxSide())
}

// VisionGate exposes the live "the current model can see" gate: chat refuses
// :image / Alt+V while the current model cannot accept pictures.
func (rt *Runtime) VisionGate() *tools.VisionGate {
	return rt.vision
}

// PasteImageCallback wires chat's Alt+V (design 0.3): the platform clipboard
// reader plus the view_image pipeline (the same checks and scaling), so a
// paste and a :image path end up identical for the model.
func (rt *Runtime) PasteImageCallback() func() (agentcore.ImagePart, string, error) {
	if rt == nil || rt.sessionDirs == nil {
		return nil
	}
	read := clipboardimage.Reader()
	maxSide := imageMaxSide()
	return func() (agentcore.ImagePart, string, error) {
		data, name, err := read()
		if err != nil {
			return agentcore.ImagePart{}, "", err
		}
		return native.PrepareImage(data, name, maxSide)
	}
}

// SessionModelReference reads the last main model of a persisted session before
// runtime assembly. Chat uses it so --resume follows :model unless --model was
// explicitly supplied on the command line.
func SessionModelReference(ctx context.Context, sessionID string) (string, error) {
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return "", err
	}
	repo, err := store.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer repo.Close()
	session, err := repo.GetSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	provider, _ := session.Meta["provider"].(string)
	model, _ := session.Meta["model"].(string)
	if provider == "" || model == "" {
		return "", nil
	}
	return provider + "/" + model, nil
}

// OpenCLI resolves the workspace, cwd, MCP configs and the CLI MCP gate from
// opts, then assembles the runtime via Open.
func OpenCLI(ctx context.Context, opts Options) (*Runtime, error) {
	workspace, err := filepath.Abs(appconfig.ResolveWorkspacePath())
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	servers, err := appconfig.ReadClaudeMCPConfigs(opts.MCPConfigs)
	if err != nil {
		return nil, err
	}
	opts.CLIMode = true
	// The run approver is the compiled policy; it has no terminal, so ask
	// decisions fail closed as tool denials while the turn can continue.
	gate, err := newCLIMCPGate(opts.ReadOnly)
	if err != nil {
		return nil, err
	}
	opts.approvalPolicy = &gate.policy
	// One additional-directory set per runtime, shared by the tools, the
	// environment prompt and the chat approver; the tools read it per call, so
	// the session opened later can still fill it (ApplyAddDirs).
	dirs := native.NewAddDirs()
	opts.sessionDirs = dirs
	var approver tools.Approver = newPolicyApprover(gate.policy)
	var asker *tools.BindableAsker
	var chatGate *chatApprover
	if opts.ChatMode {
		// chat wraps the gate: MCP rules unchanged, other tools confirmed
		// through the asker (bound later by chat) per tools.approval.
		auto, err := toolsApprovalAuto()
		if err != nil {
			return nil, err
		}
		// Keep mode-scoped file rules aligned with the interactive entry point.
		gate.policy.Mode = approvalrules.ModeChat
		gate.policy.Defaults = defaultApprovalDefaults(approvalrules.ModeChat, auto)
		asker = tools.NewBindableAsker()
		// The outside-directory question needs the tool's static read roots (a
		// path under one needs no authorization) and the session's added-directory
		// set, both read once here.
		roots, _ := readRoots(workspace)
		chatGate = newChatApprover(chatApproverOptions{
			gate: gate, auto: auto, asker: asker, workspace: workspace, dirs: dirs,
			readRoots: native.RootPaths(roots), readOnly: opts.ReadOnly,
			askOutside: appconfig.ChatAskOutsideDirs(),
		})
		approver = chatGate
		// Memory writes of tainted sessions are confirmed at the same terminal.
		opts.MemoryConfirm = newChatMemoryConfirmer(asker).confirm
		opts.approvalSnapshot = func() approvalPolicy {
			chatGate.mu.Lock()
			defer chatGate.mu.Unlock()
			return cloneApprovalPolicy(chatGate.policy)
		}
	}
	opts.Workspace, opts.RuleCWD = workspace, cwd
	// tools.allow_write applies to chat only: run (often started by a job
	// runner sharing this config) still needs an explicit --allow-write.
	// --read-only always wins.
	configWrite := false
	if opts.ChatMode {
		if configWrite, err = toolsAllowWrite(); err != nil {
			return nil, err
		}
	}
	opts.AllowWrite = (opts.AllowWrite || configWrite) && !opts.ReadOnly
	opts.Servers, opts.Hooks, opts.Approver = servers, nil, approver
	rt, err := Open(ctx, opts)
	if err != nil {
		return nil, err
	}
	rt.Asker, rt.WriteTools = asker, opts.AllowWrite
	if chatGate != nil {
		// The outside-directory question authorizes a path through the same
		// handle ApplyAddDirs publishes the session's directories with.
		chatGate.SetAddDirs(rt.sessionDirs)
	}
	return rt, nil
}

// toolsAllowWrite reads tools.allow_write (default false); a non-boolean
// value is a startup error instead of being guessed.
func toolsAllowWrite() (bool, error) {
	switch value := gkconfig.Get("tools.allow_write").(type) {
	case nil:
		return false, nil
	case bool:
		return value, nil
	default:
		return false, fmt.Errorf("tools.allow_write must be a boolean (true or false), got %v", value)
	}
}

// Open assembles one runtime.
func Open(ctx context.Context, opts Options) (*Runtime, error) {
	workspace, ruleCWD, allowWrite, servers := opts.Workspace, opts.RuleCWD, opts.AllowWrite, opts.Servers
	approval := approvalPolicy{}
	if opts.approvalPolicy != nil {
		approval = *opts.approvalPolicy
	} else {
		var policyErr error
		approval, policyErr = compileACPApprovalPolicy(workspace, opts.ReadOnly || opts.MemoryReadOnly)
		if policyErr != nil {
			return nil, policyErr
		}
	}
	approvalSnapshot := opts.approvalSnapshot
	if approvalSnapshot == nil {
		approvalSnapshot = func() approvalPolicy { return cloneApprovalPolicy(approval) }
	}
	// The session's additional directories (--add-dir): the tools read the set at
	// every call, so the requested ones are published here already (a new
	// session's first segment lists them) and ApplyAddDirs merges the session's
	// own ones in once it is open. A read-only run exposes the read-only view.
	dirs := opts.sessionDirs
	if dirs == nil {
		dirs = native.NewAddDirs()
	}
	dirs.Set(nativeAddDirs(adddir.Effective(opts.AddDirs, opts.ReadOnly)))
	opts.sessionDirs = dirs
	rs, err := loadRuntimeSettings(workspace, opts.Model, opts.Thinking)
	if err != nil {
		return nil, err
	}
	modelOptions, modelSpecs, err := configuredModelOptions(opts.Model)
	if err != nil {
		return nil, err
	}
	// Project rules are one file per level, named by rules.file; a bad name is a
	// startup error rather than a silently skipped rule file.
	ruleFile, err := appconfig.ReadRuleFileName()
	if err != nil {
		return nil, err
	}
	// Step limit of one main-agent turn: --max-steps > SUGA_MAX_STEPS > max_steps.
	maxSteps, err := appconfig.ReadMaxSteps(opts.MaxSteps)
	if err != nil {
		return nil, err
	}
	// Output token limit of the main model's requests: --max-output-tokens >
	// SUGA_MAX_OUTPUT_TOKENS > models.<m>.max_output_tokens >
	// providers.<id>.max_output_tokens (0 = the provider's own default).
	maxOutputTokens, err := appconfig.ResolveMaxOutputTokens(opts.MaxOutputTokens, rs.models.Main)
	if err != nil {
		return nil, err
	}
	budget, err := appconfig.ReadBudgetSettings(opts.Budget)
	if err != nil {
		return nil, err
	}

	settings, taintTools, taintOnSpill := rs.models, rs.taintTools, rs.taintOnSpill
	memSetup, memPolicy, sessionsDir := rs.memSetup, rs.memPolicy, rs.sessionsDir
	// Picture limits of one request (images.*): a bad value is a startup error
	// rather than a silently missing cap.
	images, err := appconfig.ReadImageSettings()
	if err != nil {
		return nil, err
	}
	// One live vision gate per runtime: the main model builds the tool set with
	// it, and :model moves it (view_image is filtered per current model).
	visionGate := tools.NewVisionGate(appconfig.VisionFor(settings.Main))
	provider, err := buildMainProviderWithEstimate(settings, estimateOptionsForBudget(budget.Tokens, budget.CostUSD, budget.Estimate, budget.EstimateFactor))
	if err != nil {
		return nil, err
	}
	contextWindow := appconfig.ContextWindowFor(settings.Main)
	// A bad proxy is a startup error rather than a silently missing web_fetch.
	if err := validateNetworkProxy(); err != nil {
		return nil, err
	}
	snapshotter := configuredSnapshotter()
	// The variables every subprocess of the session gets (its session, short name
	// and turn): filled at build time, bound to the runner below.
	sessionEnv := newSessionEnvProvider()
	var capabilities defaultCapabilities
	if opts.CLIMode {
		capabilities, err = buildPersonalCLICapabilitiesWithSnapshot(ctx, workspace, dirs, allowWrite, servers, snapshotter)
	} else {
		capabilities, err = buildPersonalCapabilitiesWithServersAndSnapshot(ctx, workspace, dirs, allowWrite, servers, snapshotter)
	}
	if err != nil {
		return nil, err
	}
	// Bind view_image to the runtime's gate: the tool was built from the startup
	// model, the gate follows every later :model switch.
	if t, err := capabilities.Registry.Get("view_image"); err == nil {
		if img, ok := t.(*native.ViewImageTool); ok {
			img.SetVision(visionGate)
		}
	}
	// Every command the session runs sees its session and turn in the environment
	// (read at each call, so a sub-agent's session and an ACP session switch are
	// followed).
	if t, err := capabilities.Registry.Get("shell_exec"); err == nil {
		if shell, ok := t.(*native.ShellExecTool); ok {
			shell.SetExtraEnv(sessionEnv.Pairs)
		}
	}
	preferredTools, err := ResolvePreferredTools(capabilities.Registry, servers)
	if err != nil {
		_ = capabilities.Close()
		return nil, err
	}
	// The preferred snapshot is captured after MCP registration. It is passed
	// into the environment source rather than rebuilt by run/chat/ACP, so all
	// entry points describe the same runtime facts.
	opts.PreferredTools = append([]PreferredToolStatus(nil), preferredTools...)
	// preferredNames holds the names the environment section listed last;
	// preferredEnvironment is the only writer, so the rendered text and the
	// turn_context event cannot drift apart.
	preferredNames := &listedPreferredNames{}
	preferredEnvironment := newPreferredEnvironmentSource(opts, preferredNames)
	// Background tasks: shell commands in write modes (they need
	// shell_exec); sub-agents (below) in every mode, so the task tool and the
	// notices are there in read-only sessions too.
	if opts.Tasks != nil {
		if t, err := capabilities.Registry.Get("shell_exec"); err == nil && allowWrite {
			if shell, ok := t.(*native.ShellExecTool); ok {
				shell.SetTasks(opts.Tasks)
			}
		}
		capabilities.Registry.Register(native.NewTaskTool(opts.Tasks, nativeEnv(workspace, dirs).ShellTimeoutMax))
	}
	// agent hands a task to a read-only sub-agent (every mode; agents.enabled).
	agentSettings, err := appconfig.ReadAgentSettings()
	if err != nil {
		_ = capabilities.Close()
		return nil, err
	}
	if agentSettings.Enabled {
		agentTool := native.NewAgentTool(&subAgents{workspace: workspace, ruleCWD: ruleCWD, mainModel: opts.Model,
			noProjectRules: opts.NoProjectRules, ruleFile: ruleFile, settings: agentSettings, addDirs: dirs,
			budget: personal.BudgetSettings{Tokens: budget.Tokens, CostUSD: budget.CostUSD, Scope: budget.Scope,
				Estimate: budget.Estimate, EstimateFactor: budget.EstimateFactor, NoticeRatio: budget.NoticeRatio},
			deadlineEnv:    appconfig.ReadRunDeadlineEnv(),
			policySnapshot: approvalSnapshot})
		if opts.Tasks != nil {
			agentTool.SetTasks(opts.Tasks)
		}
		capabilities.Registry.Register(agentTool)
	}
	// update_plan only writes session state, so it is registered in every mode
	// (including read-only). The recorder is bound to the runner in personal.New.
	plans := personal.NewPlanRecorder()
	capabilities.Registry.Register(native.NewUpdatePlanTool(plans))
	// memory changes suga's own state too, so it is registered in every mode;
	// read-only modes keep get/search only (the tool refuses writes itself).
	// The runner and the provenance store are bound after personal.New.
	memHost := &memoryHost{}
	mem := newMemoryRuntime(memSetup, memPolicy, opts.MemoryReadOnly, opts.MemoryConfirm, memHost)
	capabilities.Registry.Register(mem.tool)
	// session_search / recall only read the session store and the saved
	// outputs, so they are registered in every mode as well; the ledger is
	// bound to the runner's store in personal.New.
	ledger := personal.NewLedger(sessionsDir)
	capabilities.Registry.Register(native.NewSessionSearchTool(ledger))
	capabilities.Registry.Register(native.NewRecallTool(ledger, inlineOutputBytes()))
	// Skills are scanned once per process (the built-in ones are released into
	// <SUGA_HOME>/skills first); the skill tool (read-only, every mode) and the
	// prompt listing exist only when some skill was found.
	skillReg := buildDefaultSkillRegistry(workspace)
	if skillReg.Len() > 0 {
		capabilities.Registry.Register(native.NewSkillTool(skillReg, nativeEnv(workspace, dirs).RawBytes))
	}
	// suga_manifest is read-only local data, so it is registered in every
	// mode. It reports the live registry (MCP tools included) when called.
	// resident is assigned below, once the visibility layer (deferred tools)
	// exists; the tool only reads it when it is called.
	var resident func(context.Context) ResidentReport
	capabilities.Registry.Register(NewManifestTool(func(mctx context.Context) (any, error) {
		mcpServers, err := appconfig.ReadMCPServerSettings()
		if err != nil {
			return nil, err
		}
		residentInput := ResidentReport{}
		if resident != nil {
			residentInput = resident(mctx)
		}
		return BuildManifest(ManifestInput{
			Version: buildVersion, Settings: settings, Registry: capabilities.Registry,
			MCPServers:    append(mcpServers, servers...),
			ToolConflicts: capabilities.Conflicts(),
			Preferred:     preferredTools,
			SkillRoots:    skillRoots(workspace), Skills: skillReg,
			Context: residentInput.Context,
			Memory:  memSetup, Policy: memPolicy, Approval: refreshManifestApprovalRules(approval.RuleSource),
			ToolsApproval: manifestToolsApprovalAction(),
			InlineBytes:   rs.inlineBytes, SpillBytes: rs.spillBytes, SessionsDir: sessionsDir,
			TaintTools: taintTools, TaintOnSpill: taintOnSpill,
		}), nil
	}))
	// Tool registry stack, outer to inner: observe (added in personal.New) ->
	// visibility -> approval -> base registry. Unloaded deferred tools are
	// answered by visibility and never reach the approver.
	// Dangerous deletes are stopped before any approver (guardDangerous).
	approvalLayer := tools.NewApproval(capabilities.Registry, guardDangerous(opts.Approver))
	approvalLayer.SetDisabled(disabledToolHints(capabilities.Registry, opts))
	vis, err := newToolVisibility(capabilities.Registry, approvalLayer, contextWindow,
		preferredMCPToolNames(capabilities.Registry, preferredTools))
	if err != nil {
		_ = capabilities.Close()
		return nil, err
	}
	var toolRegistry agentcore.ToolRegistry = vis
	// resident builds the resident context report from the live sources: the
	// same environment, rules, skills and deferred-tools texts the runner puts
	// into a segment, measured by the manifest and the chat start notice. It
	// runs no memory backend command when a scope uses gofer/exec.
	resident = func(rctx context.Context) ResidentReport {
		sugaHome, _ := appconfig.ResolveSugaHome()
		rules, _, ruleStats, rerr := personal.LoadRulesDetailed(ruleCWD, sugaHome, opts.NoProjectRules, ruleFile)
		if rerr != nil {
			rules, ruleStats = "", personal.RuleStats{}
		}
		index, indexNote := residentMemoryIndex(rctx, mem)
		skillsText, _ := skillsPrompt(skillReg, readSkillPolicy())
		return NewResidentReport(ResidentContext{
			SystemPrompt:    basePrompt(),
			Environment:     environmentPrompt(opts),
			Rules:           rules,
			RuleStats:       ruleStats,
			MemoryIndex:     personal.RenderMemorySection(index),
			MemoryIndexNote: indexNote,
			Skills:          skillsText,
			AppendPrompt:    appendSystemPrompt(opts.AppendSystemPrompt),
			DeferredTools:   vis.PromptSection(),
		}, skillReg)
	}
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		_ = capabilities.Close()
		return nil, fmt.Errorf("resolve personal session database: %w", err)
	}
	transcriptDir := ""
	if appconfig.TranscriptsEnabled() {
		home, err := appconfig.ResolveSugaHome()
		if err != nil {
			_ = capabilities.Close()
			return nil, fmt.Errorf("resolve transcript root: %w", err)
		}
		transcriptDir = filepath.Join(home, "transcripts")
	}
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		_ = capabilities.Close()
		return nil, fmt.Errorf("resolve session process root: %w", err)
	}
	lockRoot := filepath.Join(home, "run", "locks")
	processRoot := filepath.Join(home, "run")
	// store.body_inline_bytes: a tool result body above this many bytes moves
	// to sessions/<id>/bodies.jsonl (0 keeps every body in the ledger). chat, run
	// and ACP all open the store here.
	toolBody, err := appconfig.ReadToolBodySettings()
	if err != nil {
		_ = capabilities.Close()
		return nil, err
	}
	repo, err := store.Open(dbPath, store.WithToolBodyInlineBytes(toolBody.InlineBytes))
	if err != nil {
		_ = capabilities.Close()
		return nil, err
	}
	// compact_model: summaries use that model (and its provider); unset means
	// the turn's own provider.
	var compactProvider agentcore.Provider
	compactModel := ""
	if settings.Compact != nil {
		built, err := buildProviderWithEstimate(*settings.Compact, estimateOptionsForBudget(budget.Tokens, budget.CostUSD, budget.Estimate, budget.EstimateFactor))
		if err != nil {
			_ = repo.Close()
			_ = capabilities.Close()
			return nil, fmt.Errorf("build compact model provider: %w", err)
		}
		compactProvider, compactModel = built, settings.Compact.Model
	}
	// A nil manager must stay a nil interface (no tasks).
	var taskNotices personal.TaskNotices
	if opts.Tasks != nil {
		taskNotices = opts.Tasks
	}
	runner, err := personal.New(personal.Config{
		Tasks:         taskNotices,
		ContextWindow: contextWindow,
		// History folding of the request view (context.fold_every/fold_keep).
		FoldEvery:         rs.fold.Every,
		FoldKeep:          rs.fold.Keep,
		ImageHistoryTurns: images.HistoryTurns, ImageRequestCount: images.MaxRequestCount,
		ImageRequestBytes: images.MaxRequestBytes,
		CompactProvider:   compactProvider,
		CompactModel:      compactModel,
		Plans:             plans,
		Ledger:            ledger,
		MemoryIndex:       mem.index,
		Workspace:         workspace,
		Version:           buildVersion,
		Mode:              runMode(allowWrite),
		// chat shows what a file tool changed (chat.diff): its tool_call events then
		// carry the change on disk (internal/diffview). run, ACP and stream-json keep
		// file text out of their events; ACP builds its own diff for its client.
		DiffPreview:  opts.ChatMode && appconfig.ChatDiffEnabled(),
		TaintTools:   taintTools,
		TaintOnSpill: taintOnSpill,
		Visibility:   vis,
		Price:        priceLookup(settings),
		Budget:       personal.BudgetSettings{Tokens: budget.Tokens, CostUSD: budget.CostUSD, Scope: budget.Scope, Estimate: budget.Estimate, EstimateFactor: budget.EstimateFactor, NoticeRatio: budget.NoticeRatio},
		// DeadlineEnv satisfies run.deadline_env (gofer sets GOFER_JOB_DEADLINE); empty = the transient deadline hint is off.
		DeadlineEnv: appconfig.ReadRunDeadlineEnv(),
		// Tool outputs above tools.max_output_bytes go to
		// <SUGA_HOME>/sessions/<id>/outputs/ and the session keeps a ref.
		InlineOutputBytes: inlineOutputBytes(),
		SessionsDir:       sessionsDir,
		Spiller:           personal.NewSessionSpiller(sessionsDir).WithQuota(appconfig.SpillQuotaBytes()),
		MinimizeOutput:    minimizeOutput(workspace),
		PrefetchTools:     prefetchTools(capabilities.Registry),
		Repository:        repo,
		Provider:          provider,
		Model:             settings.Main.Model,
		ProviderName:      settings.Main.Provider.ID,
		Tools:             toolRegistry,
		Vision:            visionGate,
		PromptBuilder:     staticPrompt(basePrompt()),
		// Environment and skills go into the segment context (design D1).
		// preferredEnvironment records the names it lists, so turn_context and
		// the prompt agree on what the turn saw.
		EnvironmentSource:        func() string { return preferredEnvironment(opts.PreferredTools) },
		SegmentEnvironmentSource: func() string { return preferredEnvironment(refreshPreferredCommands(opts.PreferredTools)) },
		PreferredToolsSource:     preferredNames.get,
		// The turn_context event records the session's added directories.
		AddDirs: func() []string { return dirs.List() },
		SkillsSource: func() string {
			text, _ := skillsPrompt(skillReg, readSkillPolicy())
			return text
		},
		SkillsStats: func() personal.SkillsStats {
			_, stats := skillsPrompt(skillReg, readSkillPolicy())
			return stats
		},
		Hooks:    opts.Hooks,
		MaxSteps: maxSteps,
		// The output limit of the main model's requests: the CLI flag and the
		// environment override the configured value.
		MaxOutputTokens: maxOutputTokens,
		RuleSource: func() (string, string, error) {
			home, err := appconfig.ResolveSugaHome()
			if err != nil {
				return "", "", err
			}
			return personal.LoadRules(ruleCWD, home, opts.NoProjectRules, ruleFile)
		},
		AppendSystemPrompt:    appendSystemPrompt(opts.AppendSystemPrompt),
		AppendSystemPromptSet: opts.ClearAppendSystemPrompt,
		TranscriptDir:         transcriptDir,
		LockRoot:              lockRoot,
		ProcessRoot:           processRoot,
	})
	if err != nil {
		_ = repo.Close()
		_ = capabilities.Close()
		return nil, err
	}
	memSetup.Bind(repo)
	memHost.bind(runner, repo)
	sessionEnv.Bind(runner)
	// run and ACP have no chat terminal: their resident context warnings go to
	// the log once per process, so stdout and the ACP stream stay unchanged.
	if !opts.ChatMode {
		report := resident(ctx)
		for _, notice := range ContextNotices(report.Context, report.Skills) {
			slog.Warn("resident context", "notice", notice)
		}
	}
	rt := &Runtime{Runner: runner, Registry: capabilities.Registry, PreferredTools: preferredTools,
		Resident: resident,
		ACPPolicy: func(call agentcore.ToolCall, tool agentcore.Tool) (action, rule, scope string) {
			policyAction, hit := approval.Decide(approval.targetFor(call, tool), tools.MetaOf(tool))
			if hit != nil {
				rule = hit.Summary()
			}
			scope = approvalScopeKey(approval, call, tool)
			return string(policyAction), rule, scope
		},
		repo: repo, capabilities: capabilities, ContextWindow: contextWindow, Budget: budget,
		Thinking: settings.Main.ThinkingLevel(), tools: toolRegistry, approval: approval,
		vision: visionGate, sessionEnv: sessionEnv,
		AddDirs: dirs, modelOptions: modelOptions, modelSpecs: modelSpecs,
		fallbacks:        append([]appconfig.ModelSpec(nil), settings.Fallbacks...),
		thinkingOverride: opts.Thinking, maxOutputOverride: opts.MaxOutputTokens}
	// The runner of chat's local commands ("!cmd"/"!!cmd"): they run like
	// shell_exec ones (same shell, workspace, environment and output rules) but
	// outside the tools and every turn - chat never asks for them (用户 2026-10-14).
	env := nativeEnv(workspace, dirs)
	rt.local = native.NewLocalCommand(native.LocalCommandOptions{
		Workspace: workspace, Shell: env.Shell, Timeout: env.ShellTimeoutMax,
		RawBytes: env.RawBytes, InlineBytes: env.InlineBytes, Minimize: minimizeOutput(workspace),
		ExtraEnv: sessionEnv.Pairs,
	})
	// The added directories of the session being served (--add-dir): the front
	// end fills the set once the session is open (ApplyAddDirs) through this
	// handle, which chat's commands and the outside-directory question share.
	rt.sessionDirs = newSessionAddDirs(runner, dirs, opts.AddDirs, opts.ReadOnly, workspace, sessionsDir)
	// Session image refs (images/<sha256>.<ext>): view_image resolves them
	// against the session being served, which only opens later (ApplyAddDirs /
	// chat's SetupSession); the resolver hands the ref rule the live directory
	// at every call instead of the one the tool was built with.
	if t, err := capabilities.Registry.Get("view_image"); err == nil {
		if img, ok := t.(*native.ViewImageTool); ok {
			img.SetImagesDirResolver(rt.sessionDirs.ImagesDir)
		}
	}
	return rt, nil
}

// buildMainProvider builds the main provider without budget estimation. It is
// kept as the default helper for tests and callers that intentionally assemble
// an unbudgeted provider.
func buildMainProvider(settings appconfig.ModelSettings) (agentcore.Provider, error) {
	return buildMainProviderSpecs(settings.Main, settings.Fallbacks)
}

// buildMainProviderWithEstimate wraps every real provider before it enters the
// fallback pool. This preserves member capabilities and lets pool attempts carry
// their own model's estimated usage into the observer.
func buildMainProviderWithEstimate(settings appconfig.ModelSettings, options personal.EstimateOptions) (agentcore.Provider, error) {
	return buildMainProviderSpecsWithEstimate(settings.Main, settings.Fallbacks, options)
}

func buildMainProviderSpecs(mainSpec appconfig.ModelSpec, fallbacks []appconfig.ModelSpec) (agentcore.Provider, error) {
	return buildMainProviderSpecsWithEstimate(mainSpec, fallbacks, personal.EstimateOptions{Mode: personal.EstimateOff})
}

func buildMainProviderSpecsWithEstimate(mainSpec appconfig.ModelSpec, fallbacks []appconfig.ModelSpec, options personal.EstimateOptions) (agentcore.Provider, error) {
	main, err := buildProviderWithEstimate(mainSpec, options)
	if err != nil {
		return nil, err
	}
	if len(fallbacks) == 0 {
		return main, nil
	}
	providers := []llm.Provider{main}
	for i, spec := range fallbacks {
		fb, err := buildProviderWithEstimate(spec, options)
		if err != nil {
			return nil, fmt.Errorf("fallback_models[%d]: %w", i, err)
		}
		providers = append(providers, fb)
	}
	return llm.NewPool(providers), nil
}

func buildProviderWithEstimate(spec appconfig.ModelSpec, options personal.EstimateOptions) (agentcore.Provider, error) {
	// The global network.proxy selector is resolved once per provider; a bad
	// value fails startup here (see validateNetworkProxy).
	selector, err := networkProxySelector()
	if err != nil {
		return nil, err
	}
	provider, err := appconfig.BuildProviderWithProxy(spec, networkProxyFunc(selector))
	if err != nil {
		return nil, err
	}
	return personal.NewEstimatingProvider(provider, options), nil
}

// estimateOptionsForBudget disables the estimator when no budget is configured;
// otherwise it applies the resolved budget policy to every provider path.
func estimateOptionsForBudget(tokens int, costUSD float64, mode string, factor float64) personal.EstimateOptions {
	if tokens <= 0 && costUSD <= 0 {
		return personal.EstimateOptions{Mode: personal.EstimateOff}
	}
	if mode == "" {
		mode = personal.EstimateAuto
	}
	if factor <= 0 {
		factor = 1
	}
	return personal.EstimateOptions{Mode: mode, Factor: factor}
}

// configuredModelOptions uses the same canonical catalog projection as the
// offline models command. Runtime-only details such as provider specs and
// output limits are attached after the projection has fixed ref order. The
// model flag is included so an explicit current model is also selectable and
// receives the same current marker in chat.
func configuredModelOptions(model string) ([]ModelOption, map[string]appconfig.ModelSpec, error) {
	providers, err := appconfig.ReadProviders()
	if err != nil {
		return nil, nil, err
	}
	settings, err := appconfig.ReadModelSettings(model)
	if err != nil {
		return nil, nil, err
	}
	switchable, err := appconfig.SwitchableModelSpecs(providers, settings)
	if err != nil {
		return nil, nil, err
	}
	options := make([]ModelOption, 0, len(switchable))
	specs := make(map[string]appconfig.ModelSpec, len(switchable))
	for _, spec := range switchable {
		ref := spec.Ref()
		specs[ref] = spec
		options = append(options, ModelOption{Provider: spec.Provider.ID, Model: spec.Model,
			Thinking: spec.ThinkingLevel(), ContextWindow: appconfig.ContextWindowFor(spec),
			MaxOutputTokens: appconfig.MaxOutputTokensFor(spec)})
	}
	return options, specs, nil
}

func appendSystemPrompt(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("SUGA_APPEND_SYSTEM_PROMPT")
}

func buildPersonalCapabilities(workspace string, dirs *native.AddDirs, allowWrite bool) (defaultCapabilities, error) {
	return buildPersonalCapabilitiesWithServers(context.Background(), workspace, dirs, allowWrite, nil)
}

func buildPersonalCapabilitiesWithServers(ctx context.Context, workspace string, dirs *native.AddDirs, allowWrite bool, servers []mcp.ServerSettings) (defaultCapabilities, error) {
	return buildPersonalCapabilitiesWithServersAndSnapshot(ctx, workspace, dirs, allowWrite, servers, nil)
}

func buildPersonalCapabilitiesWithServersAndSnapshot(ctx context.Context, workspace string, dirs *native.AddDirs, allowWrite bool, servers []mcp.ServerSettings, snapshotter native.Snapshotter) (defaultCapabilities, error) {
	if allowWrite {
		return buildDefaultCapabilitiesWithServersAndSnapshot(ctx, workspace, dirs, servers, snapshotter)
	}
	// Read-only: only the catalog's read tools, and no MCP (ACP read_only
	// sessions must not start MCP servers).
	registry := tools.NewRegistry()
	for _, tool := range native.BuildTools(nativeEnvWithSnapshot(workspace, dirs, snapshotter), false) {
		registry.Register(tool)
	}
	return defaultCapabilities{Registry: registry}, nil
}

func buildPersonalCLICapabilitiesWithSnapshot(ctx context.Context, workspace string, dirs *native.AddDirs, allowWrite bool, servers []mcp.ServerSettings, snapshotter native.Snapshotter) (defaultCapabilities, error) {
	if allowWrite {
		return buildDefaultCapabilitiesWithServersAndSnapshot(ctx, workspace, dirs, servers, snapshotter)
	}
	built, err := buildPersonalCapabilitiesWithServers(ctx, workspace, dirs, false, nil)
	if err != nil {
		return defaultCapabilities{}, err
	}
	configured, err := appconfig.ReadMCPServerSettings()
	if err != nil {
		_ = built.Close()
		return defaultCapabilities{}, err
	}
	for _, settings := range append(configured, servers...) {
		provider, err := mcpProviderFactory(ctx, settings)
		if err != nil {
			_ = built.Close()
			return defaultCapabilities{}, fmt.Errorf("build mcp provider %q: %w", settings.ID, err)
		}
		if err := provider.RegisterTools(built.Registry); err != nil {
			_ = provider.Close()
			_ = built.Close()
			return defaultCapabilities{}, fmt.Errorf("register mcp tools for %q: %w", settings.ID, err)
		}
		built.recordConflicts(provider)
		built.closers = append(built.closers, provider)
	}
	return built, nil
}

// priceLookup adapts the configured model prices for personal, which never
// reads global configuration itself.
func priceLookup(settings appconfig.ModelSettings) func(provider, model string) (personal.Price, bool) {
	lookup := settings.PriceLookup()
	return func(provider, model string) (personal.Price, bool) {
		p, ok := lookup(provider, model)
		return personal.Price{Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite}, ok
	}
}

// runMode names the run mode for turn_context events.
func runMode(allowWrite bool) string {
	if allowWrite {
		return personal.ModeAllowWrite
	}
	return personal.ModeReadOnly
}

// toolNameList reads a list of tool names; unset means def, an explicit empty
// list means none. Anything but a list of non-empty strings is a startup
// error.
func toolNameList(key string, def []string) ([]string, error) {
	raw := gkconfig.Get(key)
	if raw == nil {
		return def, nil
	}
	var items []any
	switch value := raw.(type) {
	case []string:
		for _, v := range value {
			items = append(items, v)
		}
	case []any:
		items = value
	default:
		return nil, fmt.Errorf("%s must be a list of tool names", key)
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s must contain non-empty tool names, got %v", key, item)
		}
		names = append(names, strings.TrimSpace(name))
	}
	return names, nil
}
