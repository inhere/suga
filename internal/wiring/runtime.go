// Package wiring assembles the personal runner (provider, tools, store, rules)
// shared by the CLI and ACP front ends.
package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	gkconfig "github.com/gookit/config/v2"
	"github.com/inhere/suga/internal/adddir"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
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
	// SessionOrigin is the meta.origin an embedded host records on the sessions
	// it creates (e.g. personal.OriginXhen for xhen): its sessions then stay out
	// of the default `suga session list` list and of gc --retention. Empty keeps the
	// entry point's own origin (chat, run, ACP).
	SessionOrigin string
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
	// ExtraTools are the host's in-process tools (a program embedding suga, e.g.
	// xhen): Open registers them next to the built-in ones of the main session,
	// so they are in the tool table, the manifest and tool_search, and
	// tools.disabled removes them like any other tool. A name a built-in or MCP
	// tool already has (or one the runtime registers itself, see
	// runtimeToolNames) is a startup error. Sub-agents never get them. Execute
	// reads tools.InvocationID(ctx) - the id of the call, stable across the
	// hooks and unique within the session - so a host can key its own
	// side-effect ledger on it.
	ExtraTools []api.Tool
	// HostContext returns the host's context text (persona, topic index, verbatim
	// constraints) and a hash of it; nil = no host context. The segment start
	// freezes it in the segment context and every turn compares the hash: a
	// change inside a segment appends a context update. An empty text means none.
	HostContext func() (content, hash string)
	Hooks       []agentcore.Hook
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
	// Translator is an already-resolved host UI catalog; nil makes Open resolve
	// the language from config and SUGA_LANGUAGE itself.
	Translator i18n.Translator
	// language carries the resolved manifest value when OpenCLI resolves the
	// translator before constructing the chat approver.
	language ManifestLanguage
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
	// Translator is the immutable host UI catalog. It is never passed to agentcore.
	Translator i18n.Translator
	// Language is the resolved startup UI language.
	Language ManifestLanguage
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
	// approvalLayer is the approval stage of the tool registry stack: chat hands
	// it the PreToolUse hook through SetCallRewriter.
	approvalLayer *tools.Approval
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
	// Skills lists the runtime's loaded skills for chat's :skills; nil leaves
	// the command without a listing.
	Skills func() []SkillInfo
	// ContextReport reports the resident context parts and the deferred native
	// tool counts for chat's :context (same measurement as Resident); nil
	// leaves the command without it.
	ContextReport func(context.Context) ContextReport

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

// OpenCLI resolves the workspace, cwd, MCP configs and the CLI MCP gate from
// opts, then assembles the runtime via Open.
func OpenCLI(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.Translator == nil {
		translator, language, err := assembleTranslator()
		if err != nil {
			return nil, err
		}
		opts.Translator, opts.language = translator, language
	}
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
			translator: opts.Translator, readRoots: native.RootPaths(roots), readOnly: opts.ReadOnly,
			askOutside: appconfig.ChatAskOutsideDirs(),
		})
		approver = chatGate
		// Memory writes of tainted sessions are confirmed at the same terminal.
		opts.MemoryConfirm = newChatMemoryConfirmer(asker, opts.Translator).confirm
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

// AssembleTranslator resolves the startup UI catalog from configuration and
// SUGA_LANGUAGE, the way Open and the manifest do. The ACP server resolves it
// once at startup, before any runtime is opened, for the notices it sends the
// client itself.
func AssembleTranslator() (i18n.Translator, ManifestLanguage, error) { return assembleTranslator() }

func assembleTranslator() (i18n.Translator, ManifestLanguage, error) {
	configured := ""
	if value := gkconfig.Get("language"); value != nil {
		text, ok := value.(string)
		if !ok {
			return nil, ManifestLanguage{}, fmt.Errorf("language must be a string, got %T", value)
		}
		configured = text
	}
	if _, err := appconfig.ReadLanguage(); err != nil {
		return nil, ManifestLanguage{}, err
	}
	env := os.Getenv("SUGA_LANGUAGE")
	lang, setting, source, err := i18n.Resolve(env, configured, nil)
	if err != nil {
		return nil, ManifestLanguage{}, err
	}
	catalog, err := i18n.NewWithLogger(lang, func(message string) { slog.Warn("i18n", "message", message) })
	if err != nil {
		return nil, ManifestLanguage{}, fmt.Errorf("initialize UI language catalog: %w", err)
	}
	info := ManifestLanguage{Setting: string(setting), Resolved: string(lang), Source: source}
	if setting == i18n.SettingAuto {
		info.Locale = i18n.SystemLocale()
	}
	return catalog, info, nil
}

// Open assembles one runtime.
func Open(ctx context.Context, opts Options) (*Runtime, error) {
	translator := opts.Translator
	language := opts.language
	if translator == nil {
		var err error
		translator, language, err = assembleTranslator()
		if err != nil {
			return nil, err
		}
	}
	if err := validateExtraToolNames(opts.ExtraTools); err != nil {
		return nil, err
	}
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
		capabilities, err = buildPersonalCLICapabilitiesWithExtras(ctx, workspace, dirs, allowWrite, servers, snapshotter, opts.ExtraTools)
	} else {
		capabilities, err = buildPersonalCapabilitiesWithExtras(ctx, workspace, dirs, allowWrite, servers, snapshotter, opts.ExtraTools)
	}
	if err != nil {
		return nil, err
	}
	// Bind view_image to the runtime's gate: the tool was built from the startup
	// model, the gate follows every later :model switch. file_read reads pictures
	// through the same instance, so it follows the gate too.
	if t, err := capabilities.Registry.Get("view_image"); err == nil {
		if img, ok := t.(*native.ViewImageTool); ok {
			img.SetVision(visionGate)
			bindFileReadImage(capabilities.Registry, img)
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
			policySnapshot: approvalSnapshot, translator: translator})
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
	mem := newMemoryRuntime(memSetup, memPolicy, opts.MemoryReadOnly, opts.MemoryConfirm, memHost, resolveMemoryTexts(translator))
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
			Version: buildVersion, Settings: settings, Language: language, Registry: capabilities.Registry,
			MCPServers:    append(mcpServers, servers...),
			ToolConflicts: capabilities.Conflicts(),
			Preferred:     preferredTools,
			SkillRoots:    skillRoots(workspace), Skills: skillReg,
			Context: residentInput.Context,
			Memory:  memSetup, Policy: memPolicy, Approval: refreshManifestApprovalRules(approval.RuleSource),
			ToolsApproval: manifestToolsApprovalAction(),
			InlineBytes:   rs.inlineBytes, SpillBytes: rs.spillBytes, SessionsDir: sessionsDir,
			TaintTools: taintTools, TaintOnSpill: taintOnSpill,
			NudgeRules: loadNudgeRuleFiles(workspace),
			LangRules:  loadLangRuleFiles(workspace),
		}), nil
	}))
	// Tool registry stack, outer to inner: observe (added in personal.New) ->
	// visibility -> approval -> base registry. Unloaded deferred tools are
	// answered by visibility and never reach the approver.
	// Dangerous deletes are stopped before any approver (guardDangerous).
	// The second pass of tools.disabled: the tools registered above (task, agent,
	// update_plan, memory, session_search, recall, skill, suga_manifest) must be
	// removable too.
	removeDisabledTools(capabilities.Registry)
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
			SystemPrompt:      basePrompt(),
			Environment:       environmentPrompt(opts),
			Rules:             rules,
			RuleStats:         ruleStats,
			MemoryIndex:       personal.RenderMemorySection(index),
			MemoryIndexNote:   indexNote,
			Skills:            skillsText,
			AppendPrompt:      appendSystemPrompt(opts.AppendSystemPrompt),
			HostContext:       hostContextText(opts.HostContext),
			DeferredTools:     vis.PromptSection(),
			DeferredToolsNote: deferredToolsNote(rctx, vis),
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
	// models.idle / models.handoff: per-scenario providers of the idle
	// consolidation and the --from handoff package; unset scenarios leave nil
	// and fall back to the compact model at runtime.
	scenarioEstimate := estimateOptionsForBudget(budget.Tokens, budget.CostUSD, budget.Estimate, budget.EstimateFactor)
	idleProvider, idleModel, err := buildScenarioProvider(settings.Idle, scenarioEstimate)
	if err != nil {
		_ = repo.Close()
		_ = capabilities.Close()
		return nil, fmt.Errorf("build idle model provider: %w", err)
	}
	handoffProvider, handoffModel, err := buildScenarioProvider(settings.Handoff, scenarioEstimate)
	if err != nil {
		_ = repo.Close()
		_ = capabilities.Close()
		return nil, fmt.Errorf("build handoff model provider: %w", err)
	}
	// A nil manager must stay a nil interface (no tasks).
	var taskNotices personal.TaskNotices
	if opts.Tasks != nil {
		taskNotices = opts.Tasks
	}
	runner, err := personal.New(personal.Config{
		SessionOrigin: opts.SessionOrigin,
		Tasks:         taskNotices,
		ContextWindow: contextWindow,
		// History folding of the request view (context.fold_every/fold_keep/fold_at).
		FoldEvery:         rs.fold.Every,
		FoldKeep:          rs.fold.Keep,
		FoldAt:            rs.fold.At,
		ImageHistoryTurns: images.HistoryTurns, ImageRequestCount: images.MaxRequestCount,
		ImageRequestBytes: images.MaxRequestBytes,
		CompactProvider:   compactProvider,
		CompactModel:      compactModel,
		// The handoff package of a segment break continues the segment's own
		// prefix only when context.handoff_reuse_prefix turns it on: off by
		// default since the evaluation of design 2026-10-27 (0.5) found little
		// saving on the handoff call.
		HandoffReusePrefix: gkconfig.Bool("context.handoff_reuse_prefix", false),
		IdleProvider:       idleProvider,
		IdleModel:          idleModel,
		HandoffProvider:    handoffProvider,
		HandoffModel:       handoffModel,
		Plans:              plans,
		Ledger:             ledger,
		MemoryIndex:        mem.index,
		Workspace:          workspace,
		Version:            buildVersion,
		Mode:               runMode(allowWrite),
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
		// The model-facing wording of the tool stack and of this package's
		// hooks (i18n phase 1b): zh keeps the historical text, en is the
		// English catalog entry.
		OutputTexts:   resolveToolTexts(translator),
		Translator:    translator,
		NudgeRules:    nudgeRuleSource(workspace),
		PrefetchTools: prefetchTools(capabilities.Registry),
		// The loop guard is on unless tools.loop_guard says otherwise.
		LoopGuard:     gkconfig.Bool("tools.loop_guard", true),
		Repository:    repo,
		Provider:      provider,
		Model:         settings.Main.Model,
		ProviderName:  settings.Main.Provider.ID,
		Tools:         toolRegistry,
		Vision:        visionGate,
		PromptBuilder: staticPrompt(basePrompt()),
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
		HostContextSource:     opts.HostContext,
		TranscriptDir:         transcriptDir,
		LockRoot:              lockRoot,
		ProcessRoot:           processRoot,
	})
	if err != nil {
		_ = repo.Close()
		_ = capabilities.Close()
		return nil, err
	}
	// The admit ledger is written by the approval layer, after a call passed the
	// decision and before the tool runs (issue suga-r72b): a call the person
	// refused, or one whose approval prompt was cancelled, never runs and gets no
	// row, so the next turn does not replay it as "state unknown". A sub-agent's
	// table holds only read-only tools, so its own approval layer records nothing.
	approvalLayer.SetOnApproved(personal.AdmitApprovedCall)
	memSetup.Bind(repo)
	memHost.bind(runner, repo)
	sessionEnv.Bind(runner)
	// run and ACP have no chat terminal: their resident context warnings go to
	// the log once per process, so stdout and the ACP stream stay unchanged.
	if !opts.ChatMode {
		report := resident(ctx)
		for _, notice := range ContextNotices(translator, report.Context, report.Skills) {
			slog.Warn("resident context", "notice", notice)
		}
	}
	rt := &Runtime{Runner: runner, Registry: capabilities.Registry, PreferredTools: preferredTools,
		Translator: translator, Language: language,
		Resident: resident,
		// :skills lists what the registry holds right now; :context reports the
		// same resident parts the manifest and the start notice measure, plus what
		// the visibility layer deferred (the tool table leaves those out).
		Skills: func() []SkillInfo { return SkillInfos(translator, skillReg) },
		ContextReport: func(rctx context.Context) ContextReport {
			deferred, loaded := 0, 0
			if vis != nil {
				deferred, loaded = vis.DeferredStats(rctx)
			}
			return BuildContextReport(resident(rctx), deferred, loaded)
		},
		approvalLayer: approvalLayer,
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
	// at every call instead of the one the tool was built with. file_read shares
	// the instance, so a ref reads the same in both tools.
	if t, err := capabilities.Registry.Get("view_image"); err == nil {
		if img, ok := t.(*native.ViewImageTool); ok {
			img.SetImagesDirResolver(rt.sessionDirs.ImagesDir)
			bindFileReadImage(capabilities.Registry, img)
		}
	}
	return rt, nil
}

// bindFileReadImage hands file_read the picture reader of view_image (design
// 2026-10-23-file-read-images): one instance, so a :model switch and a session
// switch reach the tool that actually reads pictures.
func bindFileReadImage(reg *tools.Registry, img *native.ViewImageTool) {
	if t, err := reg.Get("file_read"); err == nil {
		if fr, ok := t.(*native.FileReadTool); ok {
			fr.SetImagePipeline(img)
		}
	}
}
