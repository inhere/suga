package personal

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"

	"github.com/goccy/go-yaml"
)

// The fixed rule kinds of the "stopped too early" check (design
// docs/design/2026-10-22-nudge-design.md): configuration can tune the built-in
// rules, add phrasings and switch a rule off, but it cannot teach them new
// logic - the kinds and what each one reads are fixed here.
const (
	// NudgeKindPromise marks a promise rule: the answer ends with "and now I
	// will ..." but no tool call follows it (the old N2).
	NudgeKindPromise = "promise"
	// NudgeKindUnverifiedWrite marks a write rule: a successful write tool call
	// ran and no check (a call of a check tool whose command matches the rule's
	// check_command) followed it.
	NudgeKindUnverifiedWrite = "unverified_write"
	// NudgeKindPlanOpen marks the plan rule: the session plan still has open
	// steps (it reads no field of its own).
	NudgeKindPlanOpen = "plan_open"
)

// The built-in rule names; a rule file replaces them by name or switches them
// off with enabled: false.
const (
	NudgeRulePromiseZh       = "promise-zh"
	NudgeRulePromiseEn       = "promise-en"
	NudgeRuleUnverifiedWrite = "unverified-write"
	NudgeRulePlanOpen        = "plan-open"
)

// RuleSourceBuiltin marks a rule that ships with the binary; a user or
// project rule file carries its path instead.
const RuleSourceBuiltin = "builtin"

// defaultCheckTool is the tool that validates a change when a write rule does
// not name check_tools of its own; it also fills the evidence line.
const defaultCheckTool = "shell_exec"

// defaultWriteTools are the tools whose successful call changed the workspace
// when a write rule does not name write_tools of its own.
var defaultWriteTools = map[string]bool{"file_edit": true, "file_write": true, "file_remove": true}

// defaultCheckTools are the tools that can validate a change when a write rule
// does not name check_tools of its own.
var defaultCheckTools = map[string]bool{defaultCheckTool: true}

// builtinCheckCommand is the check_command of the built-in unverified-write
// rule: the common test, vet and build commands count as having verified a
// change, so a bare `ls` (or cat/git status) after an edit no longer does.
const builtinCheckCommand = "(go +(test|vet|build)|make +|npm +(test|run)|pnpm +(test|run)|yarn +(test|run)|pytest|python +-m +pytest|cargo +(test|check|build)|mvn +|gradle +|dotnet +(test|build)|ctest +)"

// NudgeRule is one rule of the check. Configuration writes it as a `rules`
// list in nudge-rules.yaml; a rule file entry replaces a same-name rule whole
// (an empty field falls back to the kind's default), so an entry that only
// sets check_command on unverified-write rebuilds the rule from its defaults.
type NudgeRule struct {
	Name string `yaml:"name"`
	// Kind is one of the three fixed kinds above; a new rule (one no built-in
	// rule carries) must name it, so the evaluation knows what to read.
	Kind string `yaml:"kind"`
	// Enabled switches the rule off (nil = on): a same-name entry with
	// enabled: false removes the earlier rule.
	Enabled *bool `yaml:"enabled"`
	// Patterns lists the promise kinds' continuation phrasings (Go regexp,
	// matched against the answer's last paragraph).
	Patterns []string `yaml:"patterns"`
	// WriteTools lists the tools whose successful call changed the workspace;
	// empty = the default (file_edit/file_write/file_remove).
	WriteTools []string `yaml:"write_tools"`
	// CheckTools lists the tools that can validate a change; empty = the
	// default (shell_exec).
	CheckTools []string `yaml:"check_tools"`
	// CheckCommand, when set, is the regexp a check call's command must match
	// to count as a verification; empty = any call of a check tool counts.
	CheckCommand string `yaml:"check_command"`

	// Source is where the rule came from (builtin, or the loaded file's path);
	// LoadNudgeRules fills it.
	Source string `yaml:"-"`

	// Compiled forms of the fields above.
	patterns   []*regexp.Regexp
	checkCmd   *regexp.Regexp
	writeTools map[string]bool
	checkTools map[string]bool
}

type nudgeRuleFile struct {
	Rules []NudgeRule `yaml:"rules"`
}

// builtinNudgeRules are the rules the check starts from: the answer-end
// phrasings split by language (the old N2), the write-without-a-check rule for
// the common test and build commands and the open-plan rule (all three of the
// old N1, now separately switchable).
func builtinNudgeRules() []NudgeRule {
	return []NudgeRule{
		{
			Name: NudgeRulePromiseZh, Kind: NudgeKindPromise, Source: RuleSourceBuiltin,
			// Each phrasing needs a first-person future marker (我(会|将|要|来|去|先|再)
			// 接下来 / 下一步 / 马上 / 这就): a plain description of what was done
			// ("先读接口，再跑只读接口") must not read as a promise to continue.
			Patterns: []string{
				`我(会|将|要|来|去|先|再)(去)?(运行|执行|跑|检查|验证|测试|修改|更新|修复|继续|补上)`,
				`接下来(我)?(会|将|要|来|去|先|再|就)`,
				`下一步(我)?(会|将|要|来|去|就)`,
				`马上(我)?(就|去|来|再)?(运行|执行|跑|检查|验证|测试|修改|更新|修复|继续)`,
				`这就(去)?(运行|执行|跑|检查|验证|测试|修改|更新|修复|继续|开始)`,
				`让我(先|再)?(运行|执行|跑|检查|验证|测试|修改|更新|修复)`,
			},
		},
		{
			Name: NudgeRulePromiseEn, Kind: NudgeKindPromise, Source: RuleSourceBuiltin,
			Patterns: []string{
				`(?i)\bI('ll| will| am going to|'m going to)\s+(now\s+)?(run|check|fix|update|add|write|test|verify|continue)`,
				`(?i)\bNext,?\s+I('ll|’ll| will)`,
				`(?i)\bLet me\s+(now\s+)?(run|check|fix|update|add|write|test|verify|try)`,
			},
		},
		{
			Name: NudgeRuleUnverifiedWrite, Kind: NudgeKindUnverifiedWrite, Source: RuleSourceBuiltin,
			CheckCommand: builtinCheckCommand,
		},
		{Name: NudgeRulePlanOpen, Kind: NudgeKindPlanOpen, Source: RuleSourceBuiltin},
	}
}

// BuiltinNudgeRulesYAML returns the built-in rules in the nudge-rules.yaml file
// shape, so `suga inspect rules nudge --builtin` prints them and a rule file
// can be written from the shipped rules (parsed again by LoadNudgeRules).
func BuiltinNudgeRulesYAML() []byte {
	data, err := yaml.Marshal(&nudgeRuleFile{Rules: builtinNudgeRules()})
	if err != nil {
		panic("personal: built-in nudge rules: " + err.Error())
	}
	return data
}

// builtinNudgeRuleFunc is the runner's rules source when wiring passes none
// (tests build runners directly): the built-in rules, compiled once.
func builtinNudgeRuleFunc() func() []NudgeRule {
	once := sync.OnceValue(func() []NudgeRule { return compileBuiltinRules() })
	return func() []NudgeRule { return once() }
}

// compileBuiltinRules compiles the built-in rules; a pattern that does not
// compile is a build-time defect, so it fails the process like an embedded
// rule file would.
func compileBuiltinRules() []NudgeRule {
	rules := builtinNudgeRules()
	for i := range rules {
		if err := (&rules[i]).compile(); err != nil {
			panic("personal: built-in nudge rule " + rules[i].Name + ": " + err.Error())
		}
	}
	return rules
}

// compile fills the compiled forms an evaluation reads: the write/check tool
// maps and the patterns / check command regexp. Empty tool lists fall back to
// the kind's defaults, so a rule file entry may omit them.
func (r *NudgeRule) compile() error {
	// Empty tool lists fall back to the kind's defaults, so a rule file entry
	// may omit them.
	r.writeTools, r.checkTools = defaultWriteTools, defaultCheckTools
	if len(r.WriteTools) > 0 {
		r.writeTools = map[string]bool{}
		for _, name := range r.WriteTools {
			r.writeTools[name] = true
		}
	}
	if len(r.CheckTools) > 0 {
		r.checkTools = map[string]bool{}
		for _, name := range r.CheckTools {
			r.checkTools[name] = true
		}
	}
	if r.CheckCommand != "" {
		re, err := regexp.Compile(r.CheckCommand)
		if err != nil {
			return fmt.Errorf("check_command %q: %w", r.CheckCommand, err)
		}
		r.checkCmd = re
	}
	r.patterns = r.patterns[:0]
	for _, pattern := range r.Patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("pattern %q: %w", pattern, err)
		}
		r.patterns = append(r.patterns, re)
	}
	return nil
}

// LoadNudgeRules returns the built-in rules merged with the rule files in
// order (later files win): a rule with the name of an earlier one replaces it
// whole, a new name is appended. Missing files are skipped; a file that does
// not parse or holds a broken rule (bad regexp, unknown or missing kind) is
// reported with its path and skipped as a whole. The result is the enabled
// rules in load order, each with its Source set.
func LoadNudgeRules(paths ...string) ([]NudgeRule, []error) {
	rules := compileBuiltinRules()
	var errs []error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		file, err := parseNudgeRules(data, path, builtinKinds(rules))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rules = mergeNudgeRules(rules, file)
	}
	out := make([]NudgeRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		out = append(out, rule)
	}
	return out, errs
}

// parseNudgeRules decodes and compiles one rule file: every rule needs a name
// (and, when it does not replace a built-in rule, one of the fixed kinds),
// every pattern and check_command must compile. A rule a load layer adds must
// say its kind, so the evaluation knows what to read.
func parseNudgeRules(data []byte, path string, builtin map[string]string) ([]NudgeRule, error) {
	var file nudgeRuleFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	rules := make([]NudgeRule, 0, len(file.Rules))
	for i := range file.Rules {
		r := &file.Rules[i]
		if r.Name == "" {
			return nil, fmt.Errorf("%s: rules[%d]: name is required", path, i)
		}
		r.Source = path
		if r.Enabled != nil && !*r.Enabled {
			rules = append(rules, *r) // only switches a rule of that name off
			continue
		}
		if r.Kind == "" {
			// A replacement of a built-in rule keeps that rule's fixed kind.
			r.Kind = builtin[r.Name]
		}
		if !knownNudgeKind(r.Kind) {
			return nil, fmt.Errorf("%s: rule %s: kind must be one of %s, %s or %s",
				path, r.Name, NudgeKindPromise, NudgeKindUnverifiedWrite, NudgeKindPlanOpen)
		}
		if err := r.compile(); err != nil {
			return nil, fmt.Errorf("%s: rule %s: %w", path, r.Name, err)
		}
		rules = append(rules, *r)
	}
	return rules, nil
}

// knownNudgeKind reports one of the three fixed kinds.
func knownNudgeKind(kind string) bool {
	switch kind {
	case NudgeKindPromise, NudgeKindUnverifiedWrite, NudgeKindPlanOpen:
		return true
	}
	return false
}

// builtinKinds maps the rule names of the loaded rules to their kinds, so a
// file entry that only tunes a built-in rule may leave its kind out.
func builtinKinds(rules []NudgeRule) map[string]string {
	kinds := make(map[string]string, len(rules))
	for _, rule := range rules {
		kinds[rule.Name] = rule.Kind
	}
	return kinds
}

// mergeNudgeRules merges a file's rules into the loaded ones: a same-name rule
// is replaced whole (unwritten fields are not inherited from the old one),
// a new name is appended.
func mergeNudgeRules(base, extra []NudgeRule) []NudgeRule {
	for _, r := range extra {
		replaced := false
		for i := range base {
			if base[i].Name == r.Name {
				base[i], replaced = r, true
				break
			}
		}
		if !replaced {
			base = append(base, r)
		}
	}
	return base
}
