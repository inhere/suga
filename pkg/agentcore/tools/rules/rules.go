// Package rules contains the approval-rule model and matcher.
//
// Rule decisions are deterministic; path matching only examines the target's
// filesystem metadata and never reads configuration.
package rules

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Action is the result of an approval decision.
type Action string

const (
	Allow Action = "allow"
	Ask   Action = "ask"
	Deny  Action = "deny"

	// The prefixed names make call sites that already use an Action namespace
	// explicit while the short names keep rule construction readable.
	ActionAllow = Allow
	ActionAsk   = Ask
	ActionDeny  = Deny
)

// Source identifies where a rule came from. The values are kept as strings so
// they can be written directly to reports and trace attributes.
type Source string

const (
	SourceUser    Source = "user"
	SourceProject Source = "project"
	SourceSession Source = "session"
	SourceLegacy  Source = "legacy"
)

// Mode limits a rule to one entry point. An empty Modes list means all modes.
type Mode string

const (
	ModeChat Mode = "chat"
	ModeRun  Mode = "run"
	ModeACP  Mode = "acp"
)

// Target is the identity and optional call data used by Policy.Decide.
//
// MCP targets must set MCP and use the server/tool form in Tool. MCPServer is
// accepted as a convenience for callers that keep the server and tool name
// separately; Tool is still the canonical identity used by glob matching.
// Command and Path are intentionally part of the value now so command and
// filesystem matchers can be added without changing the Policy API.
type Target struct {
	Tool      string
	MCP       bool
	MCPServer string
	Mode      Mode
	// Shell is the resolved shell name used by shell_exec (for example sh,
	// bash, pwsh, powershell, or cmd). An unknown name is fail-closed for
	// command allows. Windows controls case and .exe normalization explicitly
	// because the rules package is also used by non-Windows hosts.
	Shell   string
	Windows bool
	Command string
	Path    string
	Root    string
	// Protected roots are supplied by wiring; keeping them on the target lets
	// Policy.Decide apply the same allow clamp for every entry point.
	Workspace string
	SugaHome  string
	RuleFiles []string
}

// Rule is one declarative approval rule. Command and Path are slices because
// the configuration format permits either one pattern or a list; the loader
// normalizes both forms before constructing a Rule.
type Rule struct {
	Source  Source
	Index   int
	Tool    string
	Command []string
	Path    []string
	Root    string
	Modes   []Mode
	Action  Action
}

// Policy is an ordered set of rules and the action used when none matches.
// Rules are evaluated together: deny is stronger than ask, which is stronger
// than allow. Order is used only to choose a representative among equal
// strength matches, so adding a later deny cannot be hidden by an earlier
// allow.
type Policy struct {
	Rules   []Rule
	Default Action
}

// Validate checks the schema-level invariants that are independent of a host's
// configuration or filesystem. Detailed command and path semantics belong to
// the command/path matcher tasks, but malformed rule shapes fail here so the
// wiring layer can return an actionable startup error.
func (r Rule) Validate() error {
	if !validAction(r.Action) {
		return fmt.Errorf("rule action must be allow, ask, or deny, got %q", r.Action)
	}
	if len(r.Command) > 0 && len(r.Path) > 0 {
		return fmt.Errorf("rule cannot declare both command and path")
	}
	if len(r.Command) > 0 && r.Tool != "" && r.Tool != "shell_exec" {
		return fmt.Errorf("command rules require tool shell_exec, got %q", r.Tool)
	}
	if len(r.Path) > 0 && !isFileTool(r.Tool) {
		return fmt.Errorf("path rules require an exact file_write, file_edit, or file_remove tool, got %q", r.Tool)
	}
	if len(r.Path) > 0 && r.Root == "" {
		return fmt.Errorf("path rules require root")
	}
	if r.Tool != "" {
		if _, err := path.Match(r.Tool, ""); err != nil {
			return fmt.Errorf("invalid tool glob %q: %w", r.Tool, err)
		}
	}
	for _, mode := range r.Modes {
		if !validMode(mode) {
			return fmt.Errorf("rule mode must be chat, run, or acp, got %q", mode)
		}
	}
	return nil
}

// Validate checks every rule and the policy default action.
func (p Policy) Validate() error {
	if p.Default != "" && !validAction(p.Default) {
		return fmt.Errorf("policy default must be allow, ask, or deny, got %q", p.Default)
	}
	for i, rule := range p.Rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("rule %d: %w", i, err)
		}
	}
	return nil
}

// Decide returns the strongest matching action and its representative rule.
// When no rule matches, the policy default is returned and the rule is nil.
// An unset default is deliberately fail-closed as deny.
//
// protected is true when a matched allow rule was downgraded because the target
// is a protected control-plane path (see ClampAllow); the default action is not
// clamped here, callers that clamp it report their own flag.
func (p Policy) Decide(target Target) (Action, *Rule, bool) {
	var winner *Rule
	for i := range p.Rules {
		rule := &p.Rules[i]
		if !rule.matches(target) || !validAction(rule.Action) {
			continue
		}
		if winner == nil || strength(rule.Action) > strength(winner.Action) {
			winner = rule
		}
	}
	if winner != nil {
		action, protected := ClampAllow(winner.Action, target, target.Workspace, target.SugaHome, target.RuleFiles...)
		return action, winner, protected
	}
	if validAction(p.Default) {
		return p.Default, nil, false
	}
	return Deny, nil, false
}

// MatchesTool reports whether the rule's tool glob applies to target. It is
// exported separately because later specialized matchers need to retain this
// identity check before inspecting commands or paths.
func (r Rule) MatchesTool(target Target) bool {
	identity, isMCP := target.toolIdentity()
	pattern := r.Tool
	if pattern == "" {
		// A bare implicit * is a native-tool rule. MCP rules must name their
		// server explicitly, preventing a native rule from impersonating one.
		return !isMCP
	}
	if strings.Contains(pattern, "/") != isMCP {
		return false
	}
	matched, err := path.Match(pattern, identity)
	return err == nil && matched
}

func (r Rule) matches(target Target) bool {
	if !r.MatchesTool(target) || !modeMatches(r.Modes, target.Mode) {
		return false
	}
	if len(r.Command) > 0 {
		return r.MatchesCommand(target)
	}
	if len(r.Path) > 0 {
		return r.MatchesPath(target)
	}
	return true
}

func (t Target) toolIdentity() (string, bool) {
	if !t.MCP {
		return t.Tool, false
	}
	if strings.Contains(t.Tool, "/") {
		return t.Tool, true
	}
	if t.MCPServer == "" {
		return "", true
	}
	return t.MCPServer + "/" + t.Tool, true
}

func modeMatches(modes []Mode, target Mode) bool {
	if len(modes) == 0 {
		return true
	}
	for _, mode := range modes {
		if mode == target {
			return true
		}
	}
	return false
}

func isFileTool(tool string) bool {
	switch tool {
	case "file_write", "file_edit", "file_remove":
		return true
	default:
		return false
	}
}

func validAction(action Action) bool {
	return action == Allow || action == Ask || action == Deny
}

func validMode(mode Mode) bool {
	return mode == ModeChat || mode == ModeRun || mode == ModeACP
}

func strength(action Action) int {
	switch action {
	case Allow:
		return 1
	case Ask:
		return 2
	case Deny:
		return 3
	default:
		return 0
	}
}

// ClampAllow prevents an approval rule from granting access to suga's own
// control-plane files. The entry point still decides whether a protected path
// is askable: chat and ACP can ask, while non-interactive run/sub-agents deny.
// protected reports that an Allow was downgraded because of the protected path,
// so later layers can tell this Ask from an ordinary one.
func ClampAllow(action Action, target Target, workspace, sugaHome string, ruleFiles ...string) (clamped Action, protected bool) {
	if action != Allow || !IsProtectedPath(target, workspace, sugaHome, ruleFiles...) {
		return action, false
	}
	if target.Mode == ModeChat || target.Mode == ModeACP {
		return Ask, true
	}
	return Deny, true
}

// IsProtectedPath reports whether a file target is inside an internal control
// path. This is intentionally owned by the rules package so every entry point
// applies the same protection list instead of maintaining local copies.
func IsProtectedPath(target Target, workspace, sugaHome string, ruleFiles ...string) bool {
	if target.MCP || !isFileTool(target.Tool) || target.Path == "" {
		return false
	}
	candidate := target.Path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(target.Root, candidate)
	}
	candidate, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return false
	}
	protected := []string{sugaHome}
	if workspace != "" {
		protected = append(protected,
			filepath.Join(workspace, ".git"),
			filepath.Join(workspace, ".suga"),
		)
	}
	protected = append(protected, ruleFiles...)
	for _, root := range protected {
		if root == "" {
			continue
		}
		root, err = filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		if samePath(candidate, root) || pathWithin(root, candidate) {
			return true
		}
	}
	return false
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Summary returns the stable human-readable form used by approval reports.
func (r Rule) Summary() string {
	var b strings.Builder
	source := string(r.Source)
	if source == "" {
		source = "unknown"
	}
	fmt.Fprintf(&b, "%s#%d %s", source, r.Index, r.Tool)
	if len(r.Command) > 0 {
		b.WriteString(" command=")
		writeQuotedList(&b, r.Command)
	}
	if len(r.Path) > 0 {
		b.WriteString(" path=")
		writeQuotedList(&b, r.Path)
	}
	fmt.Fprintf(&b, " action=%s", r.Action)
	return b.String()
}

func writeQuotedList(b *strings.Builder, values []string) {
	if len(values) == 1 {
		b.WriteString(strconv.Quote(values[0]))
		return
	}
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(value))
	}
	b.WriteByte(']')
}
