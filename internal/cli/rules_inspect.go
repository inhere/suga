package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gookit/goutil/cflag/capp"

	config "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// inspectWorkspace is the workspace the rule files apply to: the configured
// workspace, or the directory suga runs in (the same resolution the session
// startup uses).
func inspectWorkspace() string {
	return config.ResolveWorkspacePath()
}

// newInspectRulesCommand builds `suga inspect rules <kind>`: the effective
// rules of one rule file kind (and the files that did not apply), or with
// --builtin the shipped rules verbatim. A model or user reads it to write or
// validate a rule file change without starting a session. Read-only: same
// startup as `suga inspect manifest`, nothing in SUGA_HOME is written.
func newInspectRulesCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var asJSON, builtin bool
	cmd := capp.NewCmd("rules", "Show rule files: approval / output / nudge / lang", func(c *capp.Cmd) error {
		args := c.RemainArgs()
		if len(args) != 1 {
			return fmt.Errorf("usage: suga inspect rules <approval|output|nudge|lang> [--builtin] [--json]")
		}
		switch kind := args[0]; kind {
		case "approval":
			return inspectRulesApproval(stdout, builtin, asJSON)
		case "output":
			return inspectRulesOutput(stdout, builtin, asJSON)
		case "nudge":
			return inspectRulesNudge(stdout, builtin, asJSON)
		case "lang":
			return inspectRulesLang(stdout, builtin, asJSON)
		default:
			return fmt.Errorf("unknown kind %q: use approval, output, nudge or lang", kind)
		}
	})
	cmd.BoolVar(&asJSON, "json", false, "write the result as JSON")
	cmd.BoolVar(&builtin, "builtin", false, "print the built-in rules of that kind instead of the effective ones")
	return cmd
}

// builtinFile is one shipped rule file (the raw YAML text of an embedded file,
// or the marshalled built-in rules of a kind without rule files).
type builtinFile struct {
	File    string
	Content string
}

// builtinRuleFiles converts the packages' embedded file lists to the CLI's
// shape (each package keeps its own type, so this only copies the two fields).
func builtinRuleFiles[T any](files []T, name func(T) string, yaml func(T) []byte) []builtinFile {
	out := make([]builtinFile, 0, len(files))
	for _, file := range files {
		out = append(out, builtinFile{File: name(file), Content: string(yaml(file))})
	}
	return out
}

// rulesInspectJSON is the --json form of one kind: the effective rules (a
// kind-specific list) or the built-in ones, the files that did not apply and a
// note when the kind needs one.
type rulesInspectJSON struct {
	Kind    string                `json:"kind"`
	Note    string                `json:"note,omitempty"`
	Rules   any                   `json:"rules,omitempty"`
	Builtin []builtinFile         `json:"builtin,omitempty"`
	Skipped []wiring.RuleFileSkip `json:"skipped,omitempty"`
}

// writeToJSON encodes the report like the manifest does (indented).
func writeToJSON(out io.Writer, report rulesInspectJSON) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// writeBuiltinText prints the shipped rule files with a header line, exactly
// the YAML a rule file can be written from.
func writeBuiltinText(out io.Writer, kind string, files []builtinFile) error {
	var b strings.Builder
	fmt.Fprintf(&b, "suga inspect rules %s --builtin (each file ships with the binary)\n", kind)
	for _, file := range files {
		fmt.Fprintf(&b, "\n# %s\n", file.File)
		b.WriteString(file.Content)
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// emptySkips keeps the skipped list empty (not null) so consumers can rely on
// a list.
func emptySkips(skipped []wiring.RuleFileSkip) []wiring.RuleFileSkip {
	if skipped == nil {
		return []wiring.RuleFileSkip{}
	}
	return skipped
}

// writeSkipped appends the skipped files section and the JSON list.
func writeSkippedText(b *strings.Builder, skipped []wiring.RuleFileSkip) {
	if len(skipped) == 0 {
		return
	}
	b.WriteString("  skipped files\n")
	for _, skip := range skipped {
		fmt.Fprintf(b, "    %s: %s\n", skip.File, skip.Err)
	}
}

// approvalRuleJSON is one effective approval rule for `--json`.
type approvalRuleJSON struct {
	Source  string   `json:"source"`
	Index   int      `json:"index"`
	Tool    string   `json:"tool,omitempty"`
	Command []string `json:"command,omitempty"`
	Path    []string `json:"path,omitempty"`
	Modes   []string `json:"modes,omitempty"`
	Action  string   `json:"action"`
}

// inspectRulesApproval prints the approval rules: the effective rules in
// decision order, the project allows the trust gate filtered out and the
// settings. A malformed rule file is an error: approval is fail-closed, never
// silently skipped.
func inspectRulesApproval(out io.Writer, builtin, asJSON bool) error {
	if builtin {
		note := "approval has no built-in rules: without rule files every tool call falls back to tools.approval (ask by default) and demo rules that match no real call"
		if asJSON {
			return writeToJSON(out, rulesInspectJSON{Kind: "approval", Note: note})
		}
		_, err := fmt.Fprintf(out, "suga inspect rules approval\n  %s\n", note)
		return err
	}
	loaded, err := wiring.InspectApprovalRules(inspectWorkspace())
	if err != nil {
		return err
	}
	effective := loaded.EffectiveRules()
	rulesJSON := make([]approvalRuleJSON, 0, len(effective))
	var b strings.Builder
	fmt.Fprintf(&b, "Approval rules (enabled=%t)\n", loaded.Settings.Enabled)
	if !loaded.Settings.Enabled {
		b.WriteString("  disabled: tools.approval_rules.enabled=false, so no rule file is read\n")
	} else if len(effective) == 0 {
		b.WriteString("  none effective; the default (tools.approval) applies\n")
	} else {
		fmt.Fprintf(&b, "  user path: %s\n", loaded.User.Path)
		fmt.Fprintf(&b, "  project path: %s (trust: %s)\n", loaded.Project.Path, loaded.ProjectTrustReason())
		b.WriteString("  effective, in file order (approve asks win over the default on a later match)\n")
		for _, rule := range effective {
			fmt.Fprintf(&b, "  %s\n", rule.Summary())
			modes := make([]string, 0, len(rule.Modes))
			for _, mode := range rule.Modes {
				modes = append(modes, string(mode))
			}
			rulesJSON = append(rulesJSON, approvalRuleJSON{
				Source: string(rule.Source), Index: rule.Index, Tool: rule.Tool,
				Command: rule.Command, Path: rule.Path, Modes: modes, Action: string(rule.Action),
			})
		}
	}
	ignored := loaded.IgnoredApprovalRules()
	if len(ignored) > 0 {
		for _, rule := range ignored {
			fmt.Fprintf(&b, "  ignored #%d %s: %s (%s)\n", rule.Index, rule.Action, rule.Summary, rule.Reason)
		}
	}
	if asJSON {
		report := rulesInspectJSON{Kind: "approval", Rules: rulesJSON,
			Note: loaded.ProjectTrustReason(), Skipped: []wiring.RuleFileSkip{}}
		if !loaded.Settings.Enabled {
			report.Note = "tools.approval_rules.enabled=false: no rule file is read"
		}
		return writeToJSON(out, report)
	}
	_, err = io.WriteString(out, b.String())
	return err
}

// outputRuleJSON is one effective output rule for `--json` (name and layer,
// plus the match that decides when the rule applies at all).
type outputRuleJSON struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Match  string `json:"match"`
}

// inspectRulesOutput prints the effective output rules with their layers and
// every rule file that did not apply.
func inspectRulesOutput(out io.Writer, builtin, asJSON bool) error {
	if builtin {
		files := builtinRuleFiles(wiring.BuiltinOutputRuleFiles(),
			func(f wiring.BuiltinOutputRuleFile) string { return f.Name },
			func(f wiring.BuiltinOutputRuleFile) []byte { return f.YAML })
		if asJSON {
			return writeToJSON(out, rulesInspectJSON{Kind: "output", Builtin: files})
		}
		return writeBuiltinText(out, "output", files)
	}
	loaded, skipped := wiring.InspectOutputRules(inspectWorkspace())
	rulesJSON := make([]outputRuleJSON, 0, len(loaded))
	var b strings.Builder
	b.WriteString("Output rules (effective, in load order)\n")
	if len(loaded) == 0 {
		b.WriteString("  none\n")
	}
	for _, rule := range loaded {
		fmt.Fprintf(&b, "  %s (match %q, source %s)\n", rule.Name, rule.Match, rule.Source)
		rulesJSON = append(rulesJSON, outputRuleJSON{Name: rule.Name, Source: rule.Source, Match: rule.Match})
	}
	writeSkippedText(&b, skipped)
	if asJSON {
		return writeToJSON(out, rulesInspectJSON{Kind: "output", Rules: rulesJSON, Skipped: emptySkips(skipped)})
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// inspectRulesNudge prints the effective nudge rules (the check's rules, not
// its findings) with their layers.
func inspectRulesNudge(out io.Writer, builtin, asJSON bool) error {
	if builtin {
		file := builtinFile{File: "builtin", Content: string(wiring.BuiltinNudgeRulesYAML())}
		if asJSON {
			return writeToJSON(out, rulesInspectJSON{Kind: "nudge", Builtin: []builtinFile{file}})
		}
		var b strings.Builder
		b.WriteString("suga inspect rules nudge --builtin (the shipped rules, in the nudge-rules.yaml shape)\n")
		b.WriteString(file.Content)
		_, err := io.WriteString(out, b.String())
		return err
	}
	loaded, skipped := wiring.InspectNudgeRules(inspectWorkspace())
	var b strings.Builder
	b.WriteString("Nudge rules (effective)\n")
	if len(loaded) == 0 {
		b.WriteString("  none\n")
	}
	for _, rule := range loaded {
		fmt.Fprintf(&b, "  %s: %s (%s)\n", rule.Name, rule.Kind, rule.Source)
		if len(rule.Patterns) > 0 {
			fmt.Fprintf(&b, "    patterns=%s\n", strings.Join(rule.Patterns, " | "))
		}
		if len(rule.WriteTools) > 0 {
			fmt.Fprintf(&b, "    write_tools=%s check=%s command=%s\n",
				strings.Join(rule.WriteTools, ","), strings.Join(rule.CheckTools, ","), rule.CheckCommand)
		}
	}
	writeSkippedText(&b, skipped)
	if asJSON {
		return writeToJSON(out, rulesInspectJSON{Kind: "nudge", Rules: loaded, Skipped: emptySkips(skipped)})
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// inspectRulesLang prints the effective language rules (the code languages
// with their own recogniser and every rule language) with their layers.
func inspectRulesLang(out io.Writer, builtin, asJSON bool) error {
	if builtin {
		files := builtinRuleFiles(wiring.BuiltinLangRuleFiles(),
			func(f wiring.BuiltinLangRuleFile) string { return f.Name },
			func(f wiring.BuiltinLangRuleFile) []byte { return f.YAML })
		if asJSON {
			return writeToJSON(out, rulesInspectJSON{Kind: "lang", Builtin: files})
		}
		return writeBuiltinText(out, "lang", files)
	}
	reg, skipped := wiring.InspectLangRules(inspectWorkspace())
	languages := reg.ManifestLanguages()
	var b strings.Builder
	b.WriteString("Language rules (effective)\n")
	if len(languages) == 0 {
		b.WriteString("  none\n")
	}
	for _, lang := range languages {
		fmt.Fprintf(&b, "  %s: %s (%s, source=%s", lang.Name, strings.Join(lang.Exts, ", "), lang.Blocks, lang.Source)
		if lang.Ctags {
			b.WriteString(", ctags")
		}
		b.WriteString(")\n")
	}
	if len(reg.Disabled) > 0 {
		fmt.Fprintf(&b, "  disabled: %s\n", strings.Join(reg.Disabled, ", "))
	}
	writeSkippedText(&b, skipped)
	if asJSON {
		type langJSON struct {
			Languages []codeintel.LangManifest `json:"languages"`
			Disabled  []string                 `json:"disabled,omitempty"`
		}
		report := rulesInspectJSON{Kind: "lang",
			Rules: langJSON{Languages: languages, Disabled: reg.Disabled}, Skipped: emptySkips(skipped)}
		return writeToJSON(out, report)
	}
	_, err := io.WriteString(out, b.String())
	return err
}
