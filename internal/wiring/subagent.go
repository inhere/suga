package wiring

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// subAgents runs the agent tool's sub-agents (docs/design/2026-10-13-
// subagents-design.md). Each call builds a Runner of its own: its own store
// handle (a background sub-agent may outlive an ACP prompt's runtime) and
// provider (a shared llm.Pool would get its observer replaced), read-only
// tools by type, a sub-agent prompt and environment, and the agents.* limits.
type subAgents struct {
	workspace, ruleCWD string
	// projectMemoryKey is the parent session's host-pinned project memory scope
	// (Options.ProjectMemoryKey). Sub-agents get no memory tools, but their
	// runtime settings still assemble a memory setup; passing the key keeps that
	// assembly from probing the repository the workspace sits in.
	projectMemoryKey string
	// mainModel is the parent runtime's --model override ("" = the config's
	// model); sub-agents use it unless agents.model names their own.
	mainModel      string
	noProjectRules bool
	ruleFile       string
	settings       appconfig.AgentSettings
	// budget is the parent's resolved budget. Child runners inspect it only
	// for foreground parent-budget enforcement; agents.max_tokens remains
	// the child's own limit.
	budget personal.BudgetSettings
	// addDirs is the parent session's additional-directory set (--add-dir): a
	// sub-agent works on the same task, so it reads the same directories.
	addDirs *native.AddDirs
	// deadlineEnv is the parent's run.deadline_env value: a sub-agent works on
	// the same job deadline (its own agents.timeout still wraps it up first
	// when it hits that sooner).
	deadlineEnv string
	// policySnapshot returns the parent's policy at child creation time. The
	// child copies it before building its approval layer, so later session-rule
	// changes cannot alter an already-created child.
	policySnapshot func() approvalPolicy
	// translator is the parent's host catalog: a sub-agent request carries the
	// model-facing wording in the same language as the parent's (i18n phase
	// 1b); nil is the Chinese compatibility fallback.
	translator i18n.Translator
}

var _ native.SubAgents = (*subAgents)(nil)

// subAgentTools are the catalog tools of each sub-agent type. recall is left
// out: it marks the session as having read external content, and a fresh
// sub-agent session has nothing of its own to recall.
var subAgentTools = map[string][]string{
	native.AgentExplore:  {"file_read", "file_list", "file_grep", "file_find", "file_symbols", "file_refs"},
	native.AgentResearch: {"file_read", "file_list", "file_grep", "file_find", "file_symbols", "file_refs", "web_fetch", "web_search"},
}

func (s *subAgents) Scope(ctx context.Context) (string, string) { return personal.CurrentTurn(ctx) }

func (s *subAgents) RunAgent(ctx context.Context, req native.AgentRequest) (native.AgentReport, error) {
	child, closeChild, err := s.build(req.Type)
	if err != nil {
		return native.AgentReport{Stop: "failed"}, err
	}
	defer closeChild()
	var progress personal.EmitFunc
	if !req.Background {
		progress = personal.AgentProgress(ctx)
	}
	res, runErr := personal.RunChild(ctx, child, personal.ChildRequest{
		ParentSession: req.ParentSession, ParentTurn: req.ParentTurn, ParentCall: req.CallID,
		Type: req.Type, Description: req.Description, Prompt: req.Prompt, Background: req.Background,
		Timeout: s.settings.Timeout, Emit: progress,
	})
	rep := native.AgentReport{SessionID: res.SessionID, Report: res.Report, Stop: res.Stop, Steps: res.Steps,
		ToolCalls: res.ToolCalls, Usage: res.Usage, Duration: res.Duration, Tainted: res.Tainted}
	// A background report reaches the parent through a notice or the task
	// tool, which carry no taint of their own: mark the parent here.
	if req.Background && res.Tainted && req.ParentSession != "" {
		if err := markTainted(context.WithoutCancel(ctx), req.ParentSession, res.SessionID); err != nil {
			return rep, err
		}
	}
	return rep, runErr
}

// markTainted marks session as having read external content (through the
// sub-agent session by), unless it already is.
func markTainted(ctx context.Context, session, by string) error {
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return err
	}
	repo, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = repo.Close() }()
	_, err = repo.PatchSessionMetaIfAbsent(ctx, session, "tainted_at", map[string]any{
		"tainted_at": time.Now().UTC().Format(time.RFC3339), "tainted_by": "agent:" + by,
	})
	return err
}

// childPolicy takes a detached policy snapshot. A zero-value subAgents is kept
// useful for direct callers and older tests by falling back to the run defaults.
func (s *subAgents) childPolicy() approvalPolicy {
	policy := approvalPolicy{
		Mode:      approvalrules.ModeRun,
		Workspace: s.workspace,
		Defaults:  defaultApprovalDefaults(approvalrules.ModeRun, false),
	}
	if s.policySnapshot != nil {
		policy = s.policySnapshot()
	}
	if policy.Mode == "" {
		policy.Mode = approvalrules.ModeRun
	}
	if policy.Workspace == "" {
		policy.Workspace = s.workspace
	}
	if policy.Defaults.Native == "" {
		policy.Defaults = defaultApprovalDefaults(policy.Mode, false)
	}
	return cloneApprovalPolicy(policy)
}

// build assembles the Runner of one sub-agent run; the func closes it.
func (s *subAgents) build(kind string) (*personal.Runner, func(), error) {
	model := s.settings.Model
	if model == "" {
		model = s.mainModel // the parent's model, --model included
	}
	rs, err := loadRuntimeSettings(s.workspace, model, "", s.projectMemoryKey)
	if err != nil {
		return nil, nil, fmt.Errorf("agents.model: %w", err)
	}
	provider, err := buildMainProviderWithEstimate(rs.models, estimateOptionsForBudget(s.budget.Tokens, s.budget.CostUSD, s.budget.Estimate, s.budget.EstimateFactor))
	if err != nil {
		return nil, nil, err
	}
	images, err := appconfig.ReadImageSettings()
	if err != nil {
		return nil, nil, err
	}
	registry, err := s.toolRegistry(kind)
	if err != nil {
		return nil, nil, err
	}
	// Every tool here only reads; anything else is not there to call. The
	// parent policy still applies explicit deny/ask rules, while an ask has no
	// child terminal and therefore fails closed.
	policy := s.childPolicy()
	approval := tools.NewApproval(registry, newPolicyApprover(policy))
	approval.SetDisabled(subAgentDisabledHints(registry))
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return nil, nil, err
	}
	repo, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: provider, Tools: approval,
		Model: rs.models.Main.Model, ProviderName: rs.models.Main.Provider.ID,
		ContextWindow: appconfig.ContextWindowFor(rs.models.Main),
		Price:         priceLookup(rs.models),
		Workspace:     s.workspace, Version: buildVersion, Mode: runMode(false),
		TaintTools: rs.taintTools, TaintOnSpill: rs.taintOnSpill,
		ImageHistoryTurns: images.HistoryTurns, ImageRequestCount: images.MaxRequestCount,
		ImageRequestBytes: images.MaxRequestBytes,
		InlineOutputBytes: inlineOutputBytes(), Spiller: personal.NewSessionSpiller(rs.sessionsDir).WithQuota(appconfig.SpillQuotaBytes()),
		SessionsDir:    rs.sessionsDir,
		MinimizeOutput: minimizeOutput(s.workspace), PrefetchTools: prefetchTools(registry),
		PromptBuilder:     staticPrompt(subAgentPrompt()),
		EnvironmentSource: func() string { return subAgentEnvironment(s.workspace, kind) },
		RuleSource: func() (string, string, error) {
			home, err := appconfig.ResolveSugaHome()
			if err != nil {
				return "", "", err
			}
			return personal.LoadRules(s.ruleCWD, home, s.noProjectRules, s.ruleFile)
		},
		MaxSteps: s.settings.MaxSteps, MaxRunTokens: s.settings.MaxTokens, RunTimeout: s.settings.Timeout,
		Budget: s.budget,
		// The job deadline of the parent (run.deadline_env): the child tells
		// the model the remaining time per request, like the parent does.
		DeadlineEnv: s.deadlineEnv,
		// A child request carries the model-facing wording of the parent's
		// language (i18n phase 1b).
		OutputTexts: resolveToolTexts(s.translator),
		Translator:  s.translator,
		// The sub-agent model's own output limit (agents.model); 0 leaves it to
		// the provider's default.
		MaxOutputTokens: appconfig.MaxOutputTokensFor(rs.models.Main),
	})
	if err != nil {
		_ = repo.Close()
		return nil, nil, err
	}
	return runner, func() { _ = repo.Close() }, nil
}

// toolRegistry builds a sub-agent's registry: the read-only tools of its kind's
// whitelist, with tools.disabled applied so a disabled tool is missing from the
// child as well.
func (s *subAgents) toolRegistry(kind string) (*tools.Registry, error) {
	wanted := make(map[string]bool, len(subAgentTools[kind]))
	for _, name := range subAgentTools[kind] {
		wanted[name] = true
	}
	registry := tools.NewRegistry()
	for _, tool := range native.BuildTools(nativeEnv(s.workspace, s.addDirs), false) {
		if wanted[tool.Name()] {
			registry.Register(tool)
		}
	}
	if err := applyDisabledTools(registry); err != nil {
		return nil, err
	}
	return registry, nil
}

// cloneApprovalPolicy makes the rule and protected-path slices independent of
// the parent. This is important when chat appends a session rule after a child
// has started.
func cloneApprovalPolicy(policy approvalPolicy) approvalPolicy {
	policy.Rules = append([]approvalrules.Rule(nil), policy.Rules...)
	policy.RuleFiles = append([]string(nil), policy.RuleFiles...)
	return policy
}

// subAgentDisabledHints: a catalog tool a sub-agent lacks is refused with a
// hint to report what it would need instead of asking the user.
func subAgentDisabledHints(registry *tools.Registry) map[string]string {
	hints := map[string]string{}
	for _, spec := range native.Catalog() {
		if _, err := registry.Get(spec.Name); err != nil {
			hints[spec.Name] = "sub-agents only read: say in your report what should be run or changed, and why"
		}
	}
	return hints
}

// subAgentPrompt is the system prompt of every sub-agent (one per process,
// so it is cache-stable like basePrompt).
func subAgentPrompt() string {
	var b strings.Builder
	b.WriteString("You are a suga sub-agent: another agent handed you one task. Work it out with your read-only tools and answer with a report for that agent; nobody else reads it, and you cannot ask questions.\n\n")
	b.WriteString("# Working conventions\n")
	b.WriteString("- Start with the most precise search that can answer the task (one file_grep with a specific pattern often does); read files only for what the matches leave open, never one by one through a directory. Put independent searches and reads in the same response.\n")
	b.WriteString("- Stay on the task; stop as soon as you can answer it. Few steps are the goal: the agent waiting for you pays for each one.\n")
	b.WriteString("- Your final answer is the report: the conclusion first, then the evidence (file:line, short quotes), then what you could not settle. Keep it under about 4000 characters; no greetings, no restating the task.\n")
	b.WriteString("- You cannot write files or run commands; when the task needs that, say in the report what should be done and why.\n")
	b.WriteString("- Answer in the language of the task.\n")
	return strings.TrimRight(b.String(), "\n")
}

// subAgentEnvironment is a sub-agent's environment section: always read-only,
// never telling it to have the user enable writes.
func subAgentEnvironment(workspace, kind string) string {
	var b strings.Builder
	b.WriteString("# Environment\n")
	fmt.Fprintf(&b, "- OS: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "- Workspace (file tools are confined to it): %s\n", workspace)
	if roots, _ := readRoots(workspace); len(roots) > 0 {
		b.WriteString("- Read-only roots outside the workspace (the file tools accept absolute paths under them):\n")
		for _, r := range roots {
			fmt.Fprintf(&b, "  - %s: %s\n", r.Label, r.Path)
		}
	}
	fmt.Fprintf(&b, "- Sub-agent type: %s; read-only.", kind)
	return b.String()
}
