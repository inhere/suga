package wiring

import (
	"context"
	"fmt"
	"strings"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	capskills "github.com/inhere/suga/pkg/agentcore/skills"
)

// Resident context soft thresholds (docs/design, harness2 exploration 3.6).
// They are warnings, never limits: nothing is dropped because a part crossed
// one, but the manifest marks the part and chat says it once at session start.
// The values follow what the first (uncached) request of every session pays:
//   - rules 32KB: an AGENTS.md chain beyond this is usually history rather than
//     instructions (the hard cap stays 128KB, see personal/rules.go);
//   - memory index 8KB: the index lists titles only, so growth means entries
//     should be merged into summaries;
//   - skills listing 8KB: past this the listing costs more per turn than
//     loading a skill on demand with the skill tool;
//   - total 64KB: keeps a provider prefix-cache miss cheap.
const (
	ResidentRulesSoftBytes       = 32 * 1024
	ResidentMemoryIndexSoftBytes = 8 * 1024
	ResidentSkillsSoftBytes      = 8 * 1024
	ResidentTotalSoftBytes       = 64 * 1024
)

// Suggestions shown next to an over-budget part; they name a concrete action
// the user can take instead of only the number.
const (
	rulesSuggestion  = "精简规则：常驻规则只写普遍适用的短条目，有触发场景的内容写成 skill"
	memorySuggestion = "运行 suga memory summaries 合并记忆条目，或减少常驻的 feedback 条目"
	skillsSuggestion = "减少常驻 skills 列表：移出不常用的 skill，或调小 skills.list_max"
	totalSuggestion  = "常驻上下文偏大：先精简规则与记忆索引"
)

// ResidentContext is the text sent with every request of a session besides its
// history: the system prompt plus the segment context parts. The manifest and
// the chat start notice measure exactly these strings, so the numbers describe
// what the provider receives.
type ResidentContext struct {
	SystemPrompt string
	Environment  string
	Rules        string
	RuleStats    personal.RuleStats
	MemoryIndex  string // rendered memory section (see personal.RenderMemorySection)
	Skills       string
	AppendPrompt string
	// HostContext is the embedding host's context text (Options.HostContext);
	// empty when the host provides none (the section is then not rendered).
	HostContext   string
	DeferredTools string
	// MemoryIndexNote explains a memory index that was not read (a command
	// backend is never run to size it).
	MemoryIndexNote string
	// DeferredToolsNote explains an empty deferred listing of a standalone
	// manifest (no MCP server is started, so nothing is deferred).
	DeferredToolsNote string
}

// ResidentReport is the resident context of a runtime plus the skills load
// diagnostics: chat's start notice needs both, the manifest tool builds its
// context part from Context (its skills part carries the diagnostics).
type ResidentReport struct {
	Context ManifestContext
	Skills  ManifestSkills
}

// NewResidentReport measures rc and pairs it with the skill diagnostics.
func NewResidentReport(rc ResidentContext, reg *capskills.Registry) ResidentReport {
	skills := ManifestSkills{Skipped: []ManifestSkillSkipped{}, Shadowed: []ManifestSkillShadow{}}
	if reg != nil {
		skills.Count = reg.Len()
		for _, s := range reg.Skipped() {
			skills.Skipped = append(skills.Skipped, ManifestSkillSkipped(s))
		}
		for _, s := range reg.Shadowed() {
			skills.Shadowed = append(skills.Shadowed, ManifestSkillShadow(s))
		}
	}
	return ResidentReport{Context: BuildManifestContext(rc), Skills: skills}
}

// ManifestContextPart is the size report of one resident context part.
type ManifestContextPart struct {
	Bytes  int `json:"bytes"`
	Tokens int `json:"tokens"`
	// LimitBytes is the soft threshold of this part (0 = no threshold).
	LimitBytes int                       `json:"limit_bytes,omitempty"`
	Over       bool                      `json:"over,omitempty"`
	Suggestion string                    `json:"suggestion,omitempty"`
	Files      []ManifestContextFile     `json:"files,omitempty"`
	Truncated  *ManifestContextTruncated `json:"truncated,omitempty"`
	Note       string                    `json:"note,omitempty"`
}

// ManifestContextFile is one rule source file and what it contributes.
type ManifestContextFile struct {
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	Tokens    int    `json:"tokens"`
	Truncated bool   `json:"truncated,omitempty"`
	Included  bool   `json:"included,omitempty"`
}

// ManifestContextTruncated is set when the rules exceeded the 128KB total (or a
// single file the 64KB cap) and were cut: the original size of the read files
// and the rule bytes that were kept.
type ManifestContextTruncated struct {
	OriginalBytes int `json:"original_bytes"`
	KeptBytes     int `json:"kept_bytes"`
}

// ManifestContext reports the size of the resident context and the parts that
// crossed a soft threshold (OverBudget, by part name, plus "total").
type ManifestContext struct {
	SystemPrompt ManifestContextPart `json:"system_prompt"`
	Environment  ManifestContextPart `json:"environment"`
	Rules        ManifestContextPart `json:"rules"`
	MemoryIndex  ManifestContextPart `json:"memory_index"`
	Skills       ManifestContextPart `json:"skills"`
	AppendPrompt ManifestContextPart `json:"append_prompt"`
	// HostContext measures the section an embedding host adds (Options.HostContext).
	HostContext   ManifestContextPart `json:"host_context"`
	DeferredTools ManifestContextPart `json:"deferred_tools"`
	TotalBytes    int                 `json:"total_bytes"`
	TotalTokens   int                 `json:"total_tokens"`
	OverBudget    []string            `json:"over_budget"`
}

// newContextPart measures one part; limit 0 means it has no soft threshold.
func newContextPart(text string, limit int, suggestion string) ManifestContextPart {
	part := ManifestContextPart{Bytes: len(text), Tokens: personal.EstimateTokens(text), LimitBytes: limit}
	if limit > 0 && part.Bytes > limit {
		part.Over, part.Suggestion = true, suggestion
	}
	return part
}

// BuildManifestContext measures the resident context parts, lists the rule
// source files and flags every part (and the total) over its soft threshold.
func BuildManifestContext(in ResidentContext) ManifestContext {
	c := ManifestContext{
		SystemPrompt:  newContextPart(in.SystemPrompt, 0, ""),
		Environment:   newContextPart(in.Environment, 0, ""),
		Rules:         newContextPart(in.Rules, ResidentRulesSoftBytes, rulesSuggestion),
		MemoryIndex:   newContextPart(in.MemoryIndex, ResidentMemoryIndexSoftBytes, memorySuggestion),
		Skills:        newContextPart(in.Skills, ResidentSkillsSoftBytes, skillsSuggestion),
		AppendPrompt:  newContextPart(in.AppendPrompt, 0, ""),
		HostContext:   newContextPart(in.HostContext, 0, ""),
		DeferredTools: newContextPart(in.DeferredTools, 0, ""),
		OverBudget:    []string{},
	}
	c.MemoryIndex.Note, c.DeferredTools.Note = in.MemoryIndexNote, in.DeferredToolsNote
	for _, f := range in.RuleStats.Files {
		// Tokens of the part of the file that reached the context (a truncated
		// file contributes only its kept bytes).
		text := f.Text
		if f.Kept >= 0 && f.Kept < len(text) {
			text = text[:f.Kept]
		}
		c.Rules.Files = append(c.Rules.Files, ManifestContextFile{
			Path: f.Path, Bytes: f.Kept, Tokens: personal.EstimateTokens(text), Truncated: f.Truncated, Included: f.Included,
		})
	}
	if in.RuleStats.Truncated {
		c.Rules.Truncated = &ManifestContextTruncated{
			OriginalBytes: in.RuleStats.OriginalBytes, KeptBytes: in.RuleStats.KeptBytes,
		}
	}
	parts := []struct {
		name string
		part ManifestContextPart
	}{
		{"system_prompt", c.SystemPrompt}, {"environment", c.Environment}, {"rules", c.Rules},
		{"memory_index", c.MemoryIndex}, {"skills", c.Skills}, {"append_prompt", c.AppendPrompt},
		{"host_context", c.HostContext}, {"deferred_tools", c.DeferredTools},
	}
	for _, p := range parts {
		c.TotalBytes += p.part.Bytes
		c.TotalTokens += p.part.Tokens
		if p.part.Over {
			c.OverBudget = append(c.OverBudget, p.name)
		}
	}
	if c.TotalBytes > ResidentTotalSoftBytes {
		c.OverBudget = append(c.OverBudget, "total")
	}
	return c
}

// hostContextText reads the host's context text for the resident report (""
// when the host provides none: an empty section is never rendered).
func hostContextText(src func() (content, hash string)) string {
	if src == nil {
		return ""
	}
	content, _ := src()
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return content
}

// TotalSuggestion is the action shown when the resident context total is over
// its soft threshold.
func TotalSuggestion() string { return totalSuggestion }

// ContextNotices returns the resident context problems chat prints once when it
// opens a session. Each line names what is wrong and the command that shows the
// details; run and ACP log the same facts instead (see Runtime.ResidentReport).
func ContextNotices(c ManifestContext, skills ManifestSkills) []string {
	var out []string
	if t := c.Rules.Truncated; t != nil {
		out = append(out, fmt.Sprintf("提示：项目规则已截断（原 %d 字节，保留 %d）；运行 suga inspect manifest 查看各来源文件。",
			t.OriginalBytes, t.KeptBytes))
	}
	if len(c.OverBudget) > 0 {
		var parts []string
		for _, name := range c.OverBudget {
			if name == "total" {
				parts = append(parts, fmt.Sprintf("合计 %d/%d 字节", c.TotalBytes, ResidentTotalSoftBytes))
				continue
			}
			part := c.Part(name)
			parts = append(parts, fmt.Sprintf("%s %d/%d 字节", name, part.Bytes, part.LimitBytes))
		}
		out = append(out, fmt.Sprintf("提示：常驻上下文超过软阈值（%s）；运行 suga inspect manifest 查看分布与建议。", strings.Join(parts, ", ")))
	}
	if n := len(skills.Skipped) + len(skills.Shadowed); n > 0 {
		out = append(out, fmt.Sprintf("提示：%d 个 SKILL.md 被跳过、%d 个 skill 被同名遮蔽；运行 suga inspect manifest 查看原因与来源。",
			len(skills.Skipped), len(skills.Shadowed)))
	}
	return out
}

// Part returns the named context part (the zero part for an unknown name).
func (c ManifestContext) Part(name string) ManifestContextPart {
	switch name {
	case "system_prompt":
		return c.SystemPrompt
	case "environment":
		return c.Environment
	case "rules":
		return c.Rules
	case "memory_index":
		return c.MemoryIndex
	case "skills":
		return c.Skills
	case "append_prompt":
		return c.AppendPrompt
	case "host_context":
		return c.HostContext
	case "deferred_tools":
		return c.DeferredTools
	}
	return ManifestContextPart{}
}

// residentMemoryIndex reads the memory index for the resident report. A scope
// whose backend runs a command (gofer or exec) is not read: `suga inspect
// manifest` never runs a backend command, so the part is left empty with a note.
func residentMemoryIndex(ctx context.Context, mem *memoryRuntime) (string, string) {
	if mem == nil || mem.setup == nil {
		return "", ""
	}
	for _, c := range []MemoryBackendChoice{mem.setup.User, mem.setup.Project} {
		if c.Backend != "files" {
			return "", fmt.Sprintf("memory backend %s is not read here (no backend command is run); a session reads it", c.Backend)
		}
	}
	index, err := mem.index(ctx)
	if err != nil {
		return "", "memory index could not be read: " + err.Error()
	}
	return index, ""
}

// loadResidentContext builds the resident report of the standalone manifest: it
// reads the same sources a session start does, but starts no MCP server (so the
// deferred tools listing is empty) and runs no memory backend command.
func loadResidentContext(workspace string, preferred []PreferredToolStatus, skillReg *capskills.Registry, mem *memoryRuntime) ResidentContext {
	ruleFile, err := appconfig.ReadRuleFileName()
	if err != nil {
		ruleFile = ""
	}
	sugaHome, _ := appconfig.ResolveSugaHome()
	rules, _, ruleStats, err := personal.LoadRulesDetailed(workspace, sugaHome, false, ruleFile)
	if err != nil {
		rules, ruleStats = "", personal.RuleStats{}
	}
	index, indexNote := residentMemoryIndex(context.Background(), mem)
	skills, _ := skillsPrompt(skillReg, readSkillPolicy())
	return ResidentContext{
		SystemPrompt:      basePrompt(),
		Environment:       environmentPrompt(Options{Workspace: workspace, AllowWrite: true, PreferredTools: preferred}),
		Rules:             rules,
		RuleStats:         ruleStats,
		MemoryIndex:       personal.RenderMemorySection(index),
		MemoryIndexNote:   indexNote,
		Skills:            skills,
		AppendPrompt:      appendSystemPrompt(""),
		DeferredToolsNote: "no MCP server is started by this command; a session lists deferred tools when it has some",
	}
}
