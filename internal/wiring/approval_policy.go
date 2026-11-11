package wiring

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	gkconfig "github.com/gookit/config/v2"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// approvalDefaults holds the fallback action for each kind of tool. Rules are
// evaluated before these defaults; keeping the categories here prevents a native
// read-only or agent-state tool from being confused with an MCP tool.
type approvalDefaults struct {
	Native     approvalrules.Action
	MCP        approvalrules.Action
	ReadOnly   approvalrules.Action
	AgentState approvalrules.Action
}

// approvalPolicy is the entry-point snapshot. Legacy MCP rules are included
// only by the CLI compiler; ACP receives a separate snapshot without them.
type approvalPolicy struct {
	Mode     approvalrules.Mode
	ReadOnly bool
	Rules    []approvalrules.Rule
	Defaults approvalDefaults
	// Baseline is what Defaults would be without the start-up tier (chat: the
	// configured tools.approval). Only trustReleasedDefault reads it, to tell a
	// default allow that trust opened from one that was always an allow.
	Baseline approvalDefaults
	// Approval is the start-up approval tier (--approve/--yolo, SDK option); ""
	// keeps the entry point's own behaviour. Defaults already reflect it; the
	// approvers read it to let an unprotected ask through in trust mode.
	Approval tools.ApprovalMode

	// RuleSource retains the loaded files for diagnostics exposed by the
	// manifest. It is the same snapshot used to compile Rules, so static and
	// runtime manifests cannot drift in their trust/hash explanation.
	RuleSource ApprovalRules

	// These paths are copied into every target so allow rules cannot grant access
	// to the agent's control-plane files through one entry point only.
	Workspace string
	SugaHome  string
	RuleFiles []string

	// Chat-only session rule settings are copied from tools.approval_rules so the
	// approver can build a scoped rule without reading configuration per call.
	AlwaysPrefixTokens int
	PersistAsk         bool
	// compiled marks a policy loaded by wiring, distinguishing it from the
	// small hand-built policies used by package tests.
	compiled     bool
	rulesEnabled bool
}

// defaultApprovalDefaults returns the defaults fixed by the approval design.
// A read-only or agent-state tool is still allow-by-default, but an explicit
// rule can change that result before this fallback is consulted.
func defaultApprovalDefaults(mode approvalrules.Mode, chatAuto bool, approval tools.ApprovalMode) approvalDefaults {
	defaults := approvalDefaults{
		Native:     approvalrules.Allow,
		MCP:        approvalrules.Deny,
		ReadOnly:   approvalrules.Allow,
		AgentState: approvalrules.Allow,
	}
	switch mode {
	case approvalrules.ModeChat:
		defaults.Native = approvalrules.Ask
		if chatAuto {
			defaults.Native = approvalrules.Allow
		}
	case approvalrules.ModeACP:
		defaults.Native = approvalrules.Ask
		defaults.MCP = approvalrules.Ask
	case approvalrules.ModeRun:
		// run keeps its existing non-interactive default: native tools run and
		// MCP tools remain denied unless a legacy allowlist entry matches.
	}
	return applyApprovalTier(defaults, approval)
}

// compileApprovalPolicy merges file rules with the supplied entry-point rules.
// The caller supplies legacy rules only for chat/run; this deliberate seam is
// what keeps tools.auto_allow and tools.read_only_allow out of ACP.
func compileApprovalPolicy(mode approvalrules.Mode, chatAuto, readOnly bool, loaded ApprovalRules, legacy []approvalrules.Rule, approval tools.ApprovalMode) approvalPolicy {
	compiled := approvalPolicy{
		Mode: mode, ReadOnly: readOnly, Defaults: defaultApprovalDefaults(mode, chatAuto, approval), Approval: approval,
		Baseline:   defaultApprovalDefaults(mode, chatAuto, tools.ApprovalModeDefault),
		RuleSource: loaded, Workspace: loaded.Workspace, AlwaysPrefixTokens: loaded.Settings.AlwaysPrefixTokens,
		PersistAsk: loaded.Settings.PersistAsk, compiled: true,
		rulesEnabled: loaded.Settings.Enabled,
	}
	if loaded.User.Path != "" {
		compiled.SugaHome = filepath.Dir(loaded.User.Path)
		compiled.RuleFiles = append(compiled.RuleFiles, loaded.User.Path)
	}
	if loaded.Project.Path != "" {
		compiled.RuleFiles = append(compiled.RuleFiles, loaded.Project.Path)
	}
	if loaded.Settings.Enabled {
		compiled.Rules = append(compiled.Rules, loaded.EffectiveRules()...)
	}
	compiled.Rules = append(compiled.Rules, legacy...)
	return compiled
}

// compileCLIApprovalPolicy loads user/project rules and compiles the legacy MCP
// allowlist for exactly the chat/run entry points.
func compileCLIApprovalPolicy(workspace string, mode approvalrules.Mode, chatAuto, readOnly bool, approval tools.ApprovalMode) (approvalPolicy, error) {
	loaded, err := LoadApprovalRules(workspace)
	if err != nil {
		return approvalPolicy{}, err
	}
	legacy, err := compileLegacyMCPRules(readOnly)
	if err != nil {
		return approvalPolicy{}, err
	}
	return compileApprovalPolicy(mode, chatAuto, readOnly, loaded, legacy, approval), nil
}

// compileACPApprovalPolicy intentionally has no legacy parameter. ACP's MCP
// default is ask (or deny in read-only mode at its own entry gate), independent
// of the CLI's auto_allow/read_only_allow configuration.
func compileACPApprovalPolicy(workspace string, readOnly bool, approval tools.ApprovalMode) (approvalPolicy, error) {
	loaded, err := LoadApprovalRules(workspace)
	if err != nil {
		return approvalPolicy{}, err
	}
	return compileApprovalPolicy(approvalrules.ModeACP, false, readOnly, loaded, nil, approval), nil
}

// targetFor turns the tool call into the protocol-neutral identity consumed by
// the shared matcher. Shell and path data are filled here, rather than in each
// entry point, so an allow rule cannot accidentally match a different target.
func (p approvalPolicy) targetFor(call agentcore.ToolCall, tool agentcore.Tool) approvalrules.Target {
	target := approvalrules.Target{
		Tool: call.Name, MCP: tools.IsMCP(tool), MCPServer: tools.MCPServerOf(tool),
		Mode: p.Mode, Root: p.Workspace, Workspace: p.Workspace, SugaHome: p.SugaHome,
		RuleFiles: append([]string(nil), p.RuleFiles...),
	}
	if call.Name == "shell_exec" {
		target.Shell = native.ShellName(gkconfig.String("tools.shell"))
		target.Windows = runtime.GOOS == "windows"
		target.Command, _ = call.Arguments["command"].(string)
	}
	if call.Name == "file_write" || call.Name == "file_edit" || call.Name == "file_remove" {
		target.Path, _ = call.Arguments["path"].(string)
	}
	return target
}

// policyApprover is the non-interactive translation of Policy.Decide. An ask
// has no terminal in run, so it is deliberately converted to ErrDenied rather
// than treated as an allow or abort; the runner can then continue the turn.
type policyApprover struct{ policy approvalPolicy }

func newPolicyApprover(policy approvalPolicy) tools.Approver { return &policyApprover{policy: policy} }

func (a *policyApprover) Decide(ctx context.Context, call agentcore.ToolCall, tool agentcore.Tool) error {
	target := a.policy.targetFor(call, tool)
	action, hit, protected := a.policy.Decide(target, tools.MetaOf(tool))
	if hit != nil {
		tools.ReportRule(ctx, hit.Summary(), "")
	}
	if action == approvalrules.Allow {
		if a.policy.trustReleasedDefault(action, hit, target.MCP, tools.MetaOf(tool)) {
			tools.ReportApproval(ctx, tools.ApprovedByTrust, 0)
		}
		return nil
	}
	if a.policy.trustReleasesAsk(action, protected) {
		tools.ReportApproval(ctx, tools.ApprovedByTrust, 0)
		return nil
	}
	if hit != nil {
		return tools.Denied(fmt.Sprintf("规则 %s 拒绝执行 %s", hit.Summary(), call.Name))
	}
	return tools.Denied(fmt.Sprintf("需要许可：%s，本次运行未授权", call.Name))
}

// Decide applies explicit rules first and then the category-specific default.
// An ACP/run/chat caller can translate Ask into its own interaction mechanism;
// this layer only computes the stable policy result. protected is true when an
// allow (a rule's or the default) was downgraded because the target is a
// protected control-plane path; the Ask or Deny then differs from an ordinary one.
func (p approvalPolicy) Decide(target approvalrules.Target, meta tools.Meta) (approvalrules.Action, *approvalrules.Rule, bool) {
	if target.Mode == "" {
		target.Mode = p.Mode
	}
	defaultAction := p.Defaults.Native
	switch {
	case target.MCP:
		defaultAction = p.Defaults.MCP
	case meta.AgentState:
		defaultAction = p.Defaults.AgentState
	case meta.ReadOnly:
		defaultAction = p.Defaults.ReadOnly
	}
	action, hit, protected := (approvalrules.Policy{Rules: p.Rules, Default: defaultAction}).Decide(target)
	if p.ReadOnly && target.MCP && (hit == nil || hit.Source != approvalrules.SourceLegacy) {
		// --read-only keeps its old MCP boundary: only the explicitly configured
		// read_only_allow legacy entries can grant an MCP call.
		return approvalrules.Deny, hit, false
	}
	if hit != nil {
		return action, hit, protected
	}
	// The default run policy also has to protect the control plane. ClampAllow
	// used to run only for explicit rules, which let a no-rules run write into
	// .git, .suga, SUGA_HOME, or an approval-rules file.
	action, protected = approvalrules.ClampAllow(action, target, p.Workspace, p.SugaHome, p.RuleFiles...)
	return action, nil, protected
}

// approvalScopeKey gives ACP and chat the same coarse session-rule identity.
// It is only an in-memory key; ACP never writes this value to a rule file.
func approvalScopeKey(policy approvalPolicy, call agentcore.ToolCall, tool agentcore.Tool) string {
	target := policy.targetFor(call, tool)
	if target.MCP {
		if target.MCPServer != "" {
			return target.MCPServer + "/" + call.Name
		}
		return call.Name
	}
	if call.Name == "shell_exec" {
		command, _ := call.Arguments["command"].(string)
		tokens, ok := scopeCommandTokens(command)
		if !ok || len(tokens) == 0 {
			// An unsafe shell string has no reusable allow_always scope.
			return ""
		}
		count := policy.AlwaysPrefixTokens
		if count < 1 {
			count = 2
		}
		if count > len(tokens) {
			count = len(tokens)
		}
		return call.Name + ":" + scopeTokensText(tokens[:count])
	}
	if call.Name == "file_write" || call.Name == "file_edit" || call.Name == "file_remove" {
		pathValue, _ := call.Arguments["path"].(string)
		if relative, ok := approvalRelativeScopePath(policy.Workspace, pathValue); ok {
			if call.Name == "file_remove" {
				return call.Name + ":" + relative
			}
			// Like chat, a write or edit is scoped to the directory of the
			// file (docs/a.txt -> docs/**), not to the file as a directory.
			dir := path.Dir(relative)
			if dir == "." {
				return call.Name + ":**"
			}
			return call.Name + ":" + dir + "/**"
		}
		return ""
	}
	return call.Name
}

func approvalRelativeScopePath(workspace, value string) (string, bool) {
	if workspace == "" || value == "" {
		return "", false
	}
	absolute := value
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(workspace, absolute)
	}
	relative, err := filepath.Rel(workspace, filepath.Clean(absolute))
	if err != nil || filepath.IsAbs(relative) || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}
