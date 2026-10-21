package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/gookit/goutil/cflag/capp"
	"github.com/inhere/suga/internal/wiring"
)

// newManifestCommand builds `suga inspect manifest`: what this instance can do,
// without contacting the provider or starting any MCP server.
func newManifestCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var asJSON bool
	var model, thinking string
	cmd := capp.NewCmd("manifest", "Show capabilities: models, tools, preferred tools, MCP, skills, memory backends, output limits", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga inspect manifest [--model m] [--thinking level] [--json]")
		}
		m, err := wiring.LoadManifest(wiring.ManifestOptions{Model: model, Thinking: thinking})
		if err != nil {
			return err
		}
		if asJSON {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(m)
		}
		return writeManifestText(stdout, m)
	})
	cmd.Aliases = []string{"info"}
	cmd.SetOutput(stderr)
	cmd.StringVar(&model, "model", "", "model to report, provider/model;;m")
	cmd.StringVar(&thinking, "thinking", "", "thinking level to report (like run --thinking)")
	cmd.BoolVar(&asJSON, "json", false, "write the manifest as JSON")
	return cmd
}

func writeManifestText(out io.Writer, m wiring.Manifest) error {
	var b strings.Builder
	fmt.Fprintf(&b, "suga %s\n\n", m.Version)

	b.WriteString("Models\n")
	main := m.Models.Main
	sends := ""
	if main.ModelID != "" {
		sends = ", sends " + main.ModelID
	}
	if main.Thinking != "" {
		sends += ", thinking " + main.Thinking
	}
	fmt.Fprintf(&b, "  main      %s/%s (context %d%s)\n", main.Provider, main.Model, main.ContextWindow, sends)
	if c := m.Models.Compact; c != nil {
		fmt.Fprintf(&b, "  compact   %s/%s\n", c.Provider, c.Model)
	}
	for _, f := range m.Models.Fallbacks {
		fmt.Fprintf(&b, "  fallback  %s/%s\n", f.Provider, f.Model)
	}

	b.WriteString("\nTools\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tCATEGORY\tFLAGS")
	for _, t := range m.Tools {
		var flags []string
		for _, f := range []struct {
			on   bool
			name string
		}{
			{t.ReadOnly, "read-only"}, {t.NeedsAllowWrite, "needs-allow-write"}, {t.External, "external"},
			{t.Rereadable, "rereadable"}, {t.AgentState, "agent-state"}, {t.MCP, "mcp:" + t.MCPServer},
		} {
			if f.on {
				flags = append(flags, f.name)
			}
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", t.Name, t.Category, strings.Join(flags, ","))
	}
	_ = tw.Flush()

	b.WriteString("\nMCP servers (not started; their tools are listed only in a running session)\n")
	if len(m.MCPServers) == 0 {
		b.WriteString("  none\n")
	}
	for _, s := range m.MCPServers {
		fmt.Fprintf(&b, "  %s: %s\n", s.ID, s.Command)
	}

	// tools.preferred: every declaration is listed with its reason, so a false
	// entry explains why the prompt does not offer it. MCP declarations are only
	// reported as configured: this command never starts a server.
	b.WriteString("\nPreferred tools (tools.preferred; MCP connections are confirmed when a session starts)\n")
	if len(m.Preferred) == 0 {
		b.WriteString("  none\n")
	}
	for _, p := range m.Preferred {
		fmt.Fprintf(&b, "  %s (%s) present=%t: %s", p.Name, p.Kind, p.Present, p.Reason)
		if p.When != "" {
			fmt.Fprintf(&b, " — %s", p.When)
		}
		b.WriteByte('\n')
	}

	fmt.Fprintf(&b, "\nSkills\n  found %d\n", m.Skills.Count)
	for _, r := range m.Skills.Roots {
		fmt.Fprintf(&b, "  root %s\n", r)
	}
	for _, s := range m.Skills.Skipped {
		fmt.Fprintf(&b, "  skipped %s: %s\n", s.Path, s.Err)
	}
	for _, s := range m.Skills.Shadowed {
		fmt.Fprintf(&b, "  shadowed %s: %s overrides %s\n", s.Name, s.Winner, s.Loser)
	}

	// Read-only roots: lower-case, so the section reads as a directory list.
	// The labels name the source (skills, Go modules, configured, ...).
	b.WriteString("\nRead-only roots outside the workspace\n")
	if len(m.ReadRoots) == 0 {
		b.WriteString("  none\n")
	}
	for _, r := range m.ReadRoots {
		fmt.Fprintf(&b, "  %s: %s\n", r.Label, r.Path)
	}

	writeManifestContext(&b, m.Context)

	b.WriteString("\nMemory\n")
	for _, mem := range m.Memory {
		fmt.Fprintf(&b, "  %s: %s (%s) at %s, replaceable=%t\n", mem.Scope, mem.Backend, mem.Reason, mem.Location, mem.Replaceable)
	}
	fmt.Fprintf(&b, "  write=%s write_tainted=%s\n", m.WritePolicy.MemoryWrite, m.WritePolicy.MemoryWriteTainted)

	fmt.Fprintf(&b, "\nApproval rules\n  enabled=%t tools.approval=%s\n", m.ApprovalRules.Enabled, m.ApprovalRules.Defaults.ToolsApproval)
	for _, source := range []struct {
		name  string
		value wiring.ManifestApprovalSource
	}{{"user", m.ApprovalRules.User}, {"project", m.ApprovalRules.Project}} {
		fmt.Fprintf(&b, "  %s path=%s effective=%d", source.name, source.value.Path, source.value.EffectiveRules)
		if source.value.Hash != "" {
			fmt.Fprintf(&b, " hash=%s", source.value.Hash)
		}
		if source.value.TrustReason != "" {
			fmt.Fprintf(&b, " trust=%s", source.value.TrustReason)
		}
		b.WriteByte('\n')
		for _, ignored := range source.value.Ignored {
			fmt.Fprintf(&b, "    ignored #%d: %s (%s)\n", ignored.Index, ignored.Summary, ignored.Reason)
		}
	}
	fmt.Fprintf(&b, "\nOutput\n  inline_bytes=%d spill_bytes=%d\n  sessions_dir=%s\n", m.Output.InlineBytes, m.Output.SpillBytes, m.Output.SessionsDir)
	fmt.Fprintf(&b, "\nTaint\n  taint_tools=%s\n  taint_on_spill=%s\n", strings.Join(m.Taint.TaintTools, ","), strings.Join(m.Taint.TaintOnSpill, ","))

	// Nudge rules: the effective ones of the layered load (builtin, SUGA_HOME,
	// workspace), so a rule file's changes show up here.
	b.WriteString("\nNudge rules\n")
	if len(m.NudgeRules) == 0 {
		b.WriteString("  none\n")
	}
	for _, r := range m.NudgeRules {
		fmt.Fprintf(&b, "  %s: %s (%s)\n", r.Name, r.Kind, r.Source)
	}

	_, err := io.WriteString(out, b.String())
	return err
}

// writeManifestContext writes the resident context section: the bytes and
// estimated tokens of every part sent with each request, the per-file split of
// the rules, the truncation and the parts over their soft threshold with the
// action they suggest.
func writeManifestContext(b *strings.Builder, c wiring.ManifestContext) {
	b.WriteString("\nResident context (bytes, estimated tokens; soft limits)\n")
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  PART\tBYTES\t~TOKENS\tLIMIT")
	for _, p := range []struct {
		name string
		part wiring.ManifestContextPart
	}{
		{"system_prompt", c.SystemPrompt}, {"environment", c.Environment}, {"rules", c.Rules},
		{"memory_index", c.MemoryIndex}, {"skills", c.Skills}, {"append_prompt", c.AppendPrompt},
		{"deferred_tools", c.DeferredTools},
	} {
		limit := ""
		if p.part.LimitBytes > 0 {
			limit = strconv.Itoa(p.part.LimitBytes)
			if p.part.Over {
				limit += " OVER"
			}
		}
		fmt.Fprintf(tw, "  %s\t%d\t%d\t%s\n", p.name, p.part.Bytes, p.part.Tokens, limit)
	}
	fmt.Fprintf(tw, "  total\t%d\t%d\t%d\n", c.TotalBytes, c.TotalTokens, wiring.ResidentTotalSoftBytes)
	_ = tw.Flush()

	for _, f := range c.Rules.Files {
		flags := ""
		if f.Included {
			flags += " included"
		}
		if f.Truncated {
			flags += " TRUNCATED"
		}
		fmt.Fprintf(b, "  rule file %s: %d bytes ~%d tokens%s\n", f.Path, f.Bytes, f.Tokens, flags)
	}
	if t := c.Rules.Truncated; t != nil {
		fmt.Fprintf(b, "  rules truncated: 已截断（原 %d 字节，保留 %d）\n", t.OriginalBytes, t.KeptBytes)
	}
	if c.MemoryIndex.Note != "" {
		fmt.Fprintf(b, "  memory_index note: %s\n", c.MemoryIndex.Note)
	}
	if c.DeferredTools.Note != "" {
		fmt.Fprintf(b, "  deferred_tools note: %s\n", c.DeferredTools.Note)
	}
	for _, name := range c.OverBudget {
		if name == "total" {
			fmt.Fprintf(b, "  over soft limit total: %d/%d bytes — %s\n", c.TotalBytes, wiring.ResidentTotalSoftBytes, wiring.TotalSuggestion())
			continue
		}
		part := c.Part(name)
		fmt.Fprintf(b, "  over soft limit %s: %d/%d bytes — %s\n", name, part.Bytes, part.LimitBytes, part.Suggestion)
	}
}
