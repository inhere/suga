package wiring

import (
	"context"
	"fmt"

	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/tools"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// ResolveApprovalMode turns the --approve / --yolo start-up flags into one tier.
// --yolo is --approve trust; giving both is an error even when they agree, so a
// launch line cannot hide a contradiction. trust only ever comes from here (or
// the SDK option): never from configuration.
func ResolveApprovalMode(approve string, yolo bool) (tools.ApprovalMode, error) {
	if yolo && approve != "" {
		return tools.ApprovalModeDefault, fmt.Errorf("--approve and --yolo cannot be combined")
	}
	if yolo {
		return tools.ApprovalModeTrust, nil
	}
	return tools.ParseApprovalMode(approve)
}

// chatAutoFor resolves chat's "native tools run without asking" switch: an
// explicit tier wins over tools.approval (ask forces asking, auto and trust
// auto-allow); no tier keeps the configured value.
func chatAutoFor(approval tools.ApprovalMode, configAuto bool) bool {
	switch approval {
	case tools.ApprovalModeAsk:
		return false
	case tools.ApprovalModeAuto, tools.ApprovalModeTrust:
		return true
	}
	return configAuto
}

// applyApprovalTier adjusts the compiled default actions for the tier. It runs
// on the defaults only: ClampAllow (protected paths) and the --read-only MCP
// refusal are applied by approvalPolicy.Decide after the default is chosen, so
// they keep winning over what trust opens here.
func applyApprovalTier(defaults approvalDefaults, approval tools.ApprovalMode) approvalDefaults {
	switch approval {
	case tools.ApprovalModeAuto:
		// ACP's native default becomes Allow; its MCP default stays Ask.
		defaults.Native = approvalrules.Allow
	case tools.ApprovalModeTrust:
		defaults.Native = approvalrules.Allow
		defaults.MCP = approvalrules.Allow
	}
	return defaults
}

// trustReleasesAsk reports whether the tier turns this policy result into an
// allow: only an Ask that was not downgraded from a protected path. An explicit
// deny, a protected-path ask and every other action are untouched.
func (p approvalPolicy) trustReleasesAsk(action approvalrules.Action, protected bool) bool {
	return p.Approval == tools.ApprovalModeTrust && action == approvalrules.Ask && !protected
}

// trustMemoryConfirm wraps an entry point's memory-write confirmation: in trust
// mode a tainted-session write is allowed without asking (nil confirm, as in
// run, included). The SDK does not use it: its host's MemoryConfirm decides.
func trustMemoryConfirm(approval tools.ApprovalMode, confirm MemoryConfirmFunc) MemoryConfirmFunc {
	if approval != tools.ApprovalModeTrust {
		return confirm
	}
	return func(context.Context, memory.Scope, memory.Op) error { return nil }
}
