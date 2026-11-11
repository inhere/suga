package wiring

import (
	"context"
	"fmt"
	"path"
	"strings"

	gkconfig "github.com/gookit/config/v2"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// compileLegacyMCPRules converts the two historical MCP lists into the common
// rule model. A bare auto_allow pattern becomes */toolglob, preserving its
// old any-server meaning while satisfying the rules package's MCP identity
// requirement. The modes keep these rules out of ACP even if a caller copies
// the resulting slice into another policy accidentally.
func compileLegacyMCPRules(readOnly bool) ([]approvalrules.Rule, error) {
	autoAllow, err := mcpAllowlist("tools.auto_allow", false)
	if err != nil {
		return nil, err
	}
	readOnlyAllow, err := mcpAllowlist("tools.read_only_allow", true)
	if err != nil {
		return nil, err
	}
	patterns := autoAllow
	if readOnly {
		patterns = readOnlyAllow
	}
	rules := make([]approvalrules.Rule, 0, len(patterns))
	for i, pattern := range patterns {
		if !strings.Contains(pattern, "/") {
			pattern = "*/" + pattern
		}
		rules = append(rules, approvalrules.Rule{
			Source: approvalrules.SourceLegacy,
			Index:  i + 1,
			Tool:   pattern,
			Modes:  []approvalrules.Mode{approvalrules.ModeChat, approvalrules.ModeRun},
			Action: approvalrules.Allow,
		})
	}
	return rules, nil
}

// cliMCPGate is the CLI tools.Approver: it checks the tool kind at dispatch, so
// --allow-write never grants MCP writes. Only MCP tools are gated; which native
// tools exist at all (--read-only, --allow-write) is decided by the tool set.
type cliMCPGate struct {
	policy approvalPolicy
}

// newCLIMCPGate builds the CLI dispatch approver. It loads the user/project
// rules here because OpenCLI calls this gate before assembling the registry;
// ACP uses Open directly and therefore never enters this legacy path.
func newCLIMCPGate(readOnly bool) (*cliMCPGate, error) {
	policy, err := compileCLIApprovalPolicy(appconfig.ResolveWorkspacePath(), approvalrules.ModeRun, false, readOnly)
	if err != nil {
		return nil, err
	}
	return newCLIMCPGateWithPolicy(policy), nil
}

func newCLIMCPGateWithPolicy(policy approvalPolicy) *cliMCPGate {
	return &cliMCPGate{policy: policy}
}

// Decide allows only a matching MCP rule. This gate cannot ask, so an explicit
// ask is fail-closed here; chat's later approval assembly can provide a real
// asker without changing the legacy/default policy.
func (g *cliMCPGate) Decide(_ context.Context, call agentcore.ToolCall, tool agentcore.Tool) error {
	if !tools.IsMCP(tool) {
		return nil
	}
	target := approvalrules.Target{
		Tool: call.Name, MCP: true, MCPServer: tools.MCPServerOf(tool), Mode: g.policy.Mode,
	}
	action, _, _ := g.policy.Decide(target, tools.MetaOf(tool))
	if action == approvalrules.Allow {
		return nil
	}
	return tools.Denied(fmt.Sprintf("需要许可：%s，本次运行未授权", call.Name))
}

// mcpAllowlist reads and validates one allowlist config key. qualifiedOnly
// rejects entries without a "server/" part.
func mcpAllowlist(key string, qualifiedOnly bool) ([]string, error) {
	raw := gkconfig.Get(key)
	if raw == nil {
		return nil, nil
	}
	var patterns []string
	switch value := raw.(type) {
	case []string:
		patterns = append(patterns, value...)
	case []any:
		for _, item := range value {
			pattern, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must contain glob strings", key)
			}
			patterns = append(patterns, pattern)
		}
	default:
		return nil, fmt.Errorf("%s must be a list of glob strings", key)
	}
	for _, pattern := range patterns {
		if strings.TrimSpace(pattern) == "" {
			return nil, fmt.Errorf("%s contains an empty glob", key)
		}
		serverPat, toolPat, qualified := strings.Cut(pattern, "/")
		if !qualified {
			if qualifiedOnly {
				return nil, fmt.Errorf("%s entry %q must be server-qualified (server/toolglob)", key, pattern)
			}
			toolPat = pattern
		} else if _, err := path.Match(serverPat, "sample"); err != nil {
			return nil, fmt.Errorf("%s glob %q: %w", key, pattern, err)
		}
		if _, err := path.Match(toolPat, "sample"); err != nil {
			return nil, fmt.Errorf("%s glob %q: %w", key, pattern, err)
		}
	}
	return patterns, nil
}
