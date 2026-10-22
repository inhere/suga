package wiring

import (
	"errors"
	"io/fs"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
	"github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// RuleFileSkip is one rule file that did not apply, with the reason. The
// manifest and `suga inspect rules` show it, because a rule file with a typo
// would otherwise silently do nothing.
type RuleFileSkip struct {
	// File is the rule file's absolute path; the rule kind's name when the
	// reason belongs to no single file.
	File string `json:"file"`
	Err  string `json:"err"`
}

// ruleFileSkips turns a layered loader's errors into per-file skips: parsing
// errors are prefixed with their path, a read error carries it in *fs.PathError
// (an error of neither kind keeps the kind's file name as its file).
func ruleFileSkips(kind string, paths []string, errs []error) []RuleFileSkip {
	skips := make([]RuleFileSkip, 0, len(errs))
	for _, err := range errs {
		skip := RuleFileSkip{File: kind, Err: err.Error()}
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			skip.File = pathErr.Path
		} else {
			for _, path := range paths {
				// A parse error repeats the file path before the details.
				if path != "" && len(err.Error()) > len(path) && err.Error()[:len(path)] == path {
					skip.File = path
					break
				}
			}
		}
		skips = append(skips, skip)
	}
	return skips
}

// InspectOutputRules returns the effective output rules (the built-in ones
// overridden and extended by the layered files, disabled rules removed) and the
// files that did not apply. Read-only: nothing in SUGA_HOME is written.
func InspectOutputRules(workspace string) ([]native.OutputRule, []RuleFileSkip) {
	paths := layeredRulePaths(workspace, outputRulesFile)
	loaded, errs := native.LoadOutputRules(paths...)
	return loaded, ruleFileSkips(outputRulesFile, paths, errs)
}

// NudgeRuleReport is one effective nudge rule for the inspect command: the
// fields a rule file writes plus the layer it came from.
type NudgeRuleReport struct {
	Name         string   `json:"name"`
	Source       string   `json:"source"`
	Kind         string   `json:"kind"`
	Enabled      bool     `json:"enabled"`
	Patterns     []string `json:"patterns,omitempty"`
	WriteTools   []string `json:"write_tools,omitempty"`
	CheckTools   []string `json:"check_tools,omitempty"`
	CheckCommand string   `json:"check_command,omitempty"`
}

// InspectNudgeRules returns the effective nudge rules and the files that did
// not apply. Read-only: no runner or process state is touched.
func InspectNudgeRules(workspace string) ([]NudgeRuleReport, []RuleFileSkip) {
	paths := layeredRulePaths(workspace, nudgeRulesFile)
	loaded, errs := personal.LoadNudgeRules(paths...)
	out := make([]NudgeRuleReport, 0, len(loaded))
	for _, rule := range loaded {
		out = append(out, NudgeRuleReport{
			Name: rule.Name, Source: rule.Source, Kind: rule.Kind,
			Enabled:  rule.Enabled == nil || *rule.Enabled,
			Patterns: append([]string(nil), rule.Patterns...), WriteTools: append([]string(nil), rule.WriteTools...),
			CheckTools: append([]string(nil), rule.CheckTools...), CheckCommand: rule.CheckCommand,
		})
	}
	return out, ruleFileSkips(nudgeRulesFile, paths, errs)
}

// InspectLangRules returns the effective language registry and the files that
// did not apply, without installing the registry into the process.
func InspectLangRules(workspace string) (codeintel.LangRegistry, []RuleFileSkip) {
	paths := layeredRulePaths(workspace, langRulesFile)
	reg, errs := codeintel.LoadLangRules(paths...)
	skips := make([]RuleFileSkip, 0, len(reg.Skipped)+len(errs))
	for _, skip := range reg.Skipped {
		skips = append(skips, RuleFileSkip{File: skip.File, Err: skip.Err})
	}
	// An error outside reg.Skipped (a read failure LoadLangRules reports but
	// does not skip) still shows, attributed to the kind when its path is
	// unknown.
	skips = append(skips, ruleFileSkips(langRulesFile, paths, langOuterErrors(reg, errs))...)
	return reg, skips
}

// langOuterErrors keeps the load errors whose message no skip entry already
// carries (LoadLangRules reports every skipped file there too).
func langOuterErrors(reg codeintel.LangRegistry, errs []error) []error {
	var out []error
	for _, err := range errs {
		known := false
		for _, skip := range reg.Skipped {
			if skip.Err == err.Error() {
				known = true
				break
			}
		}
		if !known {
			out = append(out, err)
		}
	}
	return out
}

// InspectApprovalRules loads the approval rule snapshot for the inspect
// command. It is the manifest's load (fail-closed): a malformed file is an
// error, never a skip.
func InspectApprovalRules(workspace string) (ApprovalRules, error) {
	return LoadApprovalRules(workspace)
}

// BuiltinOutputRuleFile and BuiltinLangRuleFile are the embedded rule files'
// shared shape, aliased from their packages so the CLI does not need both.
type (
	BuiltinOutputRuleFile = native.BuiltinRuleFile
	BuiltinLangRuleFile   = codeintel.BuiltinRuleFile
)

// BuiltinOutputRuleFiles returns the embedded output rule files in load order.
func BuiltinOutputRuleFiles() []native.BuiltinRuleFile { return native.BuiltinOutputRuleFiles() }

// BuiltinLangRuleFiles returns the embedded language rule files in load order.
func BuiltinLangRuleFiles() []codeintel.BuiltinRuleFile { return codeintel.BuiltinLangRuleFiles() }

// BuiltinNudgeRulesYAML returns the built-in nudge rules as a rule file (the
// built-in rules live in Go code, so the dump is generated).
func BuiltinNudgeRulesYAML() []byte { return personal.BuiltinNudgeRulesYAML() }

// ProjectTrustReason returns why project allow rules are in or out of the
// effective set (shared by the manifest and `suga inspect rules approval`).
func (a ApprovalRules) ProjectTrustReason() string { return projectTrustReason(a) }

// IgnoredApprovalRules returns the parsed project rules that the effective set
// leaves out: project allows are ignored unless the workspace hash was trusted
// (deny and ask rules always apply).
func (a ApprovalRules) IgnoredApprovalRules() []ManifestApprovalIgnoredRule {
	if !a.Settings.Enabled {
		return nil
	}
	trusted := a.ProjectAllowTrusted()
	var ignored []ManifestApprovalIgnoredRule
	for _, rule := range a.Project.Rules {
		if rule.Action == rules.Allow && !trusted {
			ignored = append(ignored, ManifestApprovalIgnoredRule{
				Index: rule.Index, Action: string(rule.Action), Summary: rule.Summary(),
				Reason: projectTrustReason(a),
			})
		}
	}
	return ignored
}
