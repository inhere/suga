package wiring

import (
	"fmt"
	"path/filepath"
	"slices"

	gkconfig "github.com/gookit/config/v2"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	capskills "github.com/inhere/suga/pkg/agentcore/skills"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// buildVersion is the suga version shown by the manifest; the CLI sets it
// from the binary's build info.
var buildVersion = "dev"

// SetBuildVersion records the suga version for the manifest.
func SetBuildVersion(v string) {
	if v != "" {
		buildVersion = v
	}
}

// Manifest describes what this suga instance can do (design D15). It holds
// no secrets: no API keys, base URLs, MCP arguments or environment values.
type Manifest struct {
	Version       string                `json:"version"`
	Models        ManifestModels        `json:"models"`
	Tools         []ManifestTool        `json:"tools"`
	MCPServers    []ManifestMCPServer   `json:"mcp_servers"`
	Preferred     []ManifestPreferred   `json:"preferred_tools"`
	Skills        ManifestSkills        `json:"skills"`
	Context       ManifestContext       `json:"context"`
	Memory        []ManifestMemory      `json:"memory"`
	Output        ManifestOutput        `json:"output"`
	Taint         ManifestTaint         `json:"taint"`
	WritePolicy   ManifestWritePolicy   `json:"write_policy"`
	ApprovalRules ManifestApprovalRules `json:"approval_rules"`
	// ToolConflicts lists same-name tool collisions of the live registry; it is
	// always present and stays empty when nothing collided.
	ToolConflicts []ManifestToolConflict `json:"tool_conflicts"`
}

// ManifestModel is one model and the provider serving it.
type ManifestModel struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ModelID       string `json:"model_id,omitempty"` // sent to the provider when it differs from model
	ContextWindow int    `json:"context_window"`
	Thinking      string `json:"thinking,omitempty"` // effective thinking level ("" = provider default)
	// ThinkingStyle is the declared thinking request shape ("" = model-name heuristic).
	ThinkingStyle string `json:"thinking_style,omitempty"`
	// StreamRequired marks a model whose endpoint answers only streamed requests.
	StreamRequired bool `json:"stream_required,omitempty"`
	// Vision reports whether the model accepts images (see appconfig.VisionFor).
	Vision bool `json:"vision"`
}

// ManifestModels is the effective model selection.
type ManifestModels struct {
	Main      ManifestModel   `json:"main"`
	Compact   *ManifestModel  `json:"compact,omitempty"`
	Fallbacks []ManifestModel `json:"fallbacks,omitempty"`
}

// ManifestTool is one registered tool with its metadata.
type ManifestTool struct {
	Name       string `json:"name"`
	Category   string `json:"category"`
	ReadOnly   bool   `json:"read_only"`
	External   bool   `json:"external"`
	Rereadable bool   `json:"rereadable"`
	AgentState bool   `json:"agent_state"`
	MCP        bool   `json:"mcp"`
	MCPServer  string `json:"mcp_server,omitempty"`
	// NeedsAllowWrite: the tool changes the workspace or runs commands, so it
	// is only exposed with --allow-write (or tools.allow_write in chat).
	NeedsAllowWrite bool `json:"needs_allow_write"`
}

// ManifestMCPServer is a configured MCP server. Its tools are only listed
// when the runtime is running (the manifest command never starts servers).
type ManifestMCPServer struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

// ManifestPreferred is one declared preferred tool (tools.preferred) with its
// discovery reason. Declarations that are not available are kept so a manifest
// can explain why they are missing from the prompt. It holds no command path,
// MCP argument or environment value.
type ManifestPreferred struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	When    string `json:"when"`
	Present bool   `json:"present"`
	Reason  string `json:"reason"`
}

// ManifestSkills reports the skill roots and how many skills were found, plus
// the load diagnostics: SKILL.md files that were skipped (parse or validation
// error) and same-name skills shadowed by an earlier root.
type ManifestSkills struct {
	Roots    []string               `json:"roots"`
	Count    int                    `json:"count"`
	Skipped  []ManifestSkillSkipped `json:"skipped"`
	Shadowed []ManifestSkillShadow  `json:"shadowed"`
}

// ManifestSkillSkipped is one SKILL.md that was found but not registered.
type ManifestSkillSkipped struct {
	Path string `json:"path"`
	Err  string `json:"err"`
}

// ManifestSkillShadow is one same-name skill that lost to an earlier root.
type ManifestSkillShadow struct {
	Name   string `json:"name"`
	Winner string `json:"winner"`
	Loser  string `json:"loser"`
}

// ManifestMemory is the backend serving one memory scope.
type ManifestMemory struct {
	Scope    string `json:"scope"`
	Backend  string `json:"backend"`
	Reason   string `json:"reason"`
	Location string `json:"location"`
	// Replaceable is true for every scope: any backend can be swapped for an
	// exec backend through memory.<scope>.backend.
	Replaceable bool `json:"replaceable"`
}

// ManifestOutput reports the tool output governance limits.
type ManifestOutput struct {
	InlineBytes int    `json:"inline_bytes"`
	SpillBytes  int    `json:"spill_bytes"`
	SessionsDir string `json:"sessions_dir"`
}

// ManifestTaint reports which tool results mark a session as tainted.
type ManifestTaint struct {
	TaintTools   []string `json:"taint_tools"`
	TaintOnSpill []string `json:"taint_on_spill"`
}

// ManifestWritePolicy is memory.write / memory.write_tainted.
type ManifestWritePolicy struct {
	MemoryWrite        string `json:"memory_write"`
	MemoryWriteTainted string `json:"memory_write_tainted"`
}

// ManifestApprovalRules describes both rule files and the default used by the
// legacy tools.approval setting. It deliberately reports diagnostics rather
// than the full rule pool, so a manifest explains trust without duplicating the
// policy used by the approval layer.
type ManifestApprovalRules struct {
	Enabled  bool                     `json:"enabled"`
	User     ManifestApprovalSource   `json:"user"`
	Project  ManifestApprovalSource   `json:"project"`
	Defaults ManifestApprovalDefaults `json:"defaults"`
}

// ManifestApprovalSource is the diagnostic summary for one rule file.
type ManifestApprovalSource struct {
	Path           string                        `json:"path"`
	Hash           string                        `json:"hash,omitempty"`
	EffectiveRules int                           `json:"effective_rules"`
	Ignored        []ManifestApprovalIgnoredRule `json:"ignored"`
	Trusted        bool                          `json:"trusted,omitempty"`
	TrustReason    string                        `json:"trust_reason,omitempty"`
}

// ManifestApprovalIgnoredRule explains why a parsed rule was not effective.
type ManifestApprovalIgnoredRule struct {
	Index   int    `json:"index"`
	Action  string `json:"action"`
	Summary string `json:"summary"`
	Reason  string `json:"reason"`
}

// ManifestApprovalDefaults records the old chat configuration in action terms.
type ManifestApprovalDefaults struct {
	ToolsApproval string `json:"tools_approval"`
}

// ManifestToolConflict is one same-name tool collision seen while registering
// the runtime tools (design D8): Tool keeps its first owner (KeptBy) and the
// dropped Server is listed here. It carries no endpoint, argument, environment
// variable or header value.
type ManifestToolConflict struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	KeptBy string `json:"kept_by"`
}

// ManifestInput is everything BuildManifest summarizes.
type ManifestInput struct {
	Version  string
	Settings appconfig.ModelSettings
	// Registry is the base tool registry (MCP tools included when running).
	Registry   *tools.Registry
	MCPServers []mcp.ServerSettings
	// ToolConflicts are the same-name collisions registered during startup; it
	// stays empty for the standalone manifest, which never starts a server.
	ToolConflicts []mcp.Conflict
	// Preferred is the discovery snapshot shown by the manifest; nil = no
	// declared tool. The runtime manifest reuses its startup snapshot.
	Preferred  []PreferredToolStatus
	SkillRoots []capskills.Root
	Skills     *capskills.Registry
	// Context is the resident context size report (built by the caller from the
	// same sources the session uses).
	Context ManifestContext
	Memory  *MemorySetup
	Policy  memoryWritePolicy

	// Approval is the exact file snapshot used to assemble the runtime policy.
	Approval ApprovalRules
	// ToolsApproval is the effective legacy chat default rendered as an action.
	ToolsApproval string

	InlineBytes, SpillBytes  int
	SessionsDir              string
	TaintTools, TaintOnSpill []string
}

// BuildManifest summarizes in. It only reads the given structures: no
// network, no process, no gofer command.
func BuildManifest(in ManifestInput) Manifest {
	m := Manifest{
		Version: in.Version,
		Models:  ManifestModels{Main: manifestModel(in.Settings.Main)},
		Output:  ManifestOutput{InlineBytes: in.InlineBytes, SpillBytes: in.SpillBytes, SessionsDir: in.SessionsDir},
		Taint: ManifestTaint{
			TaintTools:   nonNil(in.TaintTools),
			TaintOnSpill: nonNil(in.TaintOnSpill),
		},
		WritePolicy:   ManifestWritePolicy{MemoryWrite: in.Policy.Write, MemoryWriteTainted: in.Policy.Tainted},
		ApprovalRules: manifestApprovalRules(in.Approval, in.ToolsApproval),
		Tools:         []ManifestTool{},
		MCPServers:    []ManifestMCPServer{},
		Preferred:     []ManifestPreferred{},
		Memory:        []ManifestMemory{},
		Skills:        ManifestSkills{Roots: []string{}, Skipped: []ManifestSkillSkipped{}, Shadowed: []ManifestSkillShadow{}},
		Context:       in.Context,
		// A standalone manifest starts no server, so this keeps the empty list
		// that means "no runtime conflict discovered".
		ToolConflicts: []ManifestToolConflict{},
	}
	if in.Settings.Compact != nil {
		c := manifestModel(*in.Settings.Compact)
		m.Models.Compact = &c
	}
	for _, f := range in.Settings.Fallbacks {
		m.Models.Fallbacks = append(m.Models.Fallbacks, manifestModel(f))
	}
	if in.Registry != nil {
		names := in.Registry.List()
		slices.Sort(names)
		for _, name := range names {
			t, err := in.Registry.Get(name)
			if err != nil {
				continue
			}
			meta := tools.MetaOf(t)
			m.Tools = append(m.Tools, ManifestTool{
				Name: name, Category: string(meta.Category), ReadOnly: meta.ReadOnly, External: meta.External,
				Rereadable: meta.Rereadable, AgentState: meta.AgentState,
				MCP: tools.IsMCP(t), MCPServer: tools.MCPServerOf(t),
				NeedsAllowWrite: !meta.ReadOnly && !meta.AgentState && !tools.IsMCP(t),
			})
		}
	}
	for _, s := range in.MCPServers {
		m.MCPServers = append(m.MCPServers, ManifestMCPServer{ID: s.ID, Command: s.Command})
	}
	for _, conflict := range in.ToolConflicts {
		m.ToolConflicts = append(m.ToolConflicts, ManifestToolConflict(conflict))
	}
	// Declaration order: the resolver keeps the configured order, so a manifest
	// renders the same list the prompt shows. The conversion copies exactly the
	// safe fields (name, kind, scenario, present, reason).
	for _, p := range in.Preferred {
		m.Preferred = append(m.Preferred, ManifestPreferred(p))
	}
	for _, r := range in.SkillRoots {
		m.Skills.Roots = append(m.Skills.Roots, r.Dir)
	}
	if in.Skills != nil {
		m.Skills.Count = in.Skills.Len()
		for _, s := range in.Skills.Skipped() {
			m.Skills.Skipped = append(m.Skills.Skipped, ManifestSkillSkipped(s))
		}
		for _, s := range in.Skills.Shadowed() {
			m.Skills.Shadowed = append(m.Skills.Shadowed, ManifestSkillShadow(s))
		}
	}
	if in.Memory != nil {
		for _, c := range []MemoryBackendChoice{in.Memory.User, in.Memory.Project} {
			m.Memory = append(m.Memory, ManifestMemory{
				Scope: c.Scope.String(), Backend: c.Backend, Reason: c.Reason, Location: c.Location, Replaceable: true,
			})
		}
	}
	return m
}

// refreshManifestApprovalRules rereads a valid rule snapshot for the live
// manifest. The approval layer keeps its creation-time Policy snapshot, while
// diagnostics should still show a valid file hash changed during that runtime.
// A malformed or unreadable edit is left at the last valid snapshot, matching
// the fail-closed reload contract.
func refreshManifestApprovalRules(previous ApprovalRules) ApprovalRules {
	current, err := LoadApprovalRules(previous.Workspace)
	if err != nil {
		return previous
	}
	return current
}

// manifestToolsApprovalAction reports the legacy chat setting in action terms.
// It is shared by the static command and live runtime manifest; run/ACP keep
// their own policy defaults but still expose the configured chat default.
func manifestToolsApprovalAction() string {
	if gkconfig.String("tools.approval") == "auto" {
		return string(approvalrules.Allow)
	}
	return string(approvalrules.Ask)
}

// manifestApprovalRules is shared by static and runtime manifests. Project
// deny/ask rules are effective without trust; only project allows are filtered.
func manifestApprovalRules(loaded ApprovalRules, toolsApproval string) ManifestApprovalRules {
	result := ManifestApprovalRules{
		Enabled:  loaded.Settings.Enabled,
		User:     ManifestApprovalSource{Path: loaded.User.Path, Ignored: []ManifestApprovalIgnoredRule{}},
		Project:  ManifestApprovalSource{Path: loaded.Project.Path, Ignored: []ManifestApprovalIgnoredRule{}},
		Defaults: ManifestApprovalDefaults{ToolsApproval: toolsApproval},
	}
	result.User.Hash = loaded.User.Fingerprint.Hash
	if loaded.Settings.Enabled {
		result.User.EffectiveRules = len(loaded.User.Rules)
	}
	result.Project.Hash = loaded.Project.Fingerprint.Hash
	result.Project.Trusted = loaded.ProjectAllowTrusted()
	result.Project.TrustReason = projectTrustReason(loaded)
	if loaded.Settings.Enabled {
		for _, rule := range loaded.Project.Rules {
			if rule.Action == approvalrules.Allow && !result.Project.Trusted {
				result.Project.Ignored = append(result.Project.Ignored, ManifestApprovalIgnoredRule{
					Index: rule.Index, Action: string(rule.Action), Summary: rule.Summary(),
					Reason: result.Project.TrustReason,
				})
				continue
			}
			result.Project.EffectiveRules++
		}
	}
	return result
}

func projectTrustReason(loaded ApprovalRules) string {
	if !loaded.Project.Fingerprint.Exists {
		return "project rule file is not present"
	}
	if loaded.ProjectAllowTrusted() {
		return "trusted workspace and hash"
	}
	workspace := filepath.Clean(loaded.ProjectRoot())
	expected, ok := loaded.Settings.ProjectTrust[workspace]
	if !ok || expected == "" {
		return "workspace has no trusted hash"
	}
	return fmt.Sprintf("hash changed since trust (expected %s)", expected)
}

func manifestModel(s appconfig.ModelSpec) ManifestModel {
	m := ManifestModel{Provider: s.Provider.ID, Model: s.Model, ContextWindow: appconfig.ContextWindowFor(s),
		Thinking: s.ThinkingLevel(), ThinkingStyle: appconfig.ThinkingStyleFor(s), StreamRequired: appconfig.StreamRequiredFor(s),
		Vision: appconfig.VisionFor(s)}
	if id := s.APIModel(); id != s.Model {
		m.ModelID = id
	}
	return m
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ManifestOptions selects the model and workspace of LoadManifest
type ManifestOptions struct {
	// Model overrides the configured model (like --model).
	Model string
	// Thinking overrides the main model's thinking level (like --thinking).
	Thinking string
	// Workspace is the directory whose skills and rules apply; "" resolves
	// the configured workspace (or the current directory).
	Workspace string
}

// LoadManifest builds the manifest from the current configuration without
// opening the runtime: the provider is not built or contacted, MCP servers
// are not started and the session database is not opened. Tools are the
// built-in set with every mode enabled (see ManifestTool.NeedsAllowWrite).
func LoadManifest(opts ManifestOptions) (Manifest, error) {
	m, _, err := loadManifest(opts)
	return m, err
}

// manifestParts are what loadManifest built besides the manifest, for
// LoadLocalEnv.
type manifestParts struct {
	workspace string
	registry  *tools.Registry
	skills    *capskills.Registry
}

func loadManifest(opts ManifestOptions) (Manifest, manifestParts, error) {
	workspace := opts.Workspace
	if workspace == "" {
		workspace = appconfig.ResolveWorkspacePath()
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return Manifest{}, manifestParts{}, fmt.Errorf("resolve workspace: %w", err)
	}
	st, err := loadRuntimeSettings(workspace, opts.Model, opts.Thinking)
	if err != nil {
		return Manifest{}, manifestParts{}, err
	}
	approval, err := LoadApprovalRules(workspace)
	if err != nil {
		return Manifest{}, manifestParts{}, err
	}
	if _, err := toolsApprovalAuto(); err != nil {
		return Manifest{}, manifestParts{}, err
	}
	reg := tools.NewRegistry()
	for _, t := range native.BuildTools(nativeEnv(workspace, nil), true) {
		reg.Register(t)
	}
	reg.Register(native.NewUpdatePlanTool(nil))
	// chat and run add the task tool next to shell_exec (background tasks).
	reg.Register(native.NewTaskTool(nil, 0))
	if agents, err := appconfig.ReadAgentSettings(); err == nil && agents.Enabled {
		reg.Register(native.NewAgentTool(nil))
	}
	mem := newMemoryRuntime(st.memSetup, st.memPolicy, false, nil, &memoryHost{})
	reg.Register(mem.tool)
	ledger := personal.NewLedger(st.sessionsDir)
	reg.Register(native.NewSessionSearchTool(ledger))
	reg.Register(native.NewRecallTool(ledger, st.inlineBytes))
	// Read-only diagnose: the built-in skills are enumerated from the embedded
	// resources instead of being released into <SUGA_HOME>/skills.
	skillReg := buildReadOnlySkillRegistry(workspace)
	if skillReg.Len() > 0 {
		reg.Register(native.NewSkillTool(skillReg, st.spillBytes))
	}
	reg.Register(NewManifestTool(nil))

	// tools.preferred: command presence comes from PATH; a configured MCP
	// server is reported as configured, because this command never starts one
	// (design 0.2). A malformed declaration fails the manifest instead of
	// silently dropping the list.
	preferred, err := ResolvePreferredTools(nil, nil)
	if err != nil {
		return Manifest{}, manifestParts{}, err
	}

	mcpServers, err := appconfig.ReadMCPServerSettings()
	if err != nil {
		return Manifest{}, manifestParts{}, err
	}
	m := BuildManifest(ManifestInput{
		Version: buildVersion, Settings: st.models, Registry: reg,
		MCPServers: mcpServers, Preferred: preferred,
		SkillRoots: skillRoots(workspace), Skills: skillReg,
		Context: BuildManifestContext(loadResidentContext(workspace, preferred, skillReg, mem)),
		Memory:  st.memSetup, Policy: st.memPolicy, Approval: approval,
		ToolsApproval: manifestToolsApprovalAction(),
		InlineBytes:   st.inlineBytes, SpillBytes: st.spillBytes, SessionsDir: st.sessionsDir,
		TaintTools: st.taintTools, TaintOnSpill: st.taintOnSpill,
	})
	return m, manifestParts{workspace: workspace, registry: reg, skills: skillReg}, nil
}

// runtimeSettings are the configuration-derived inputs shared by Open and
// LoadManifest, so both read the same keys with the same validation.
type runtimeSettings struct {
	models                   appconfig.ModelSettings
	taintTools, taintOnSpill []string
	memSetup                 *MemorySetup
	memPolicy                memoryWritePolicy
	sessionsDir              string
	inlineBytes, spillBytes  int
	fold                     appconfig.FoldSettings
}

// loadRuntimeSettings reads and validates the settings that need no I/O
// beyond configuration and path resolution. It runs no backend command.
func loadRuntimeSettings(workspace, model, thinking string) (runtimeSettings, error) {
	var s runtimeSettings
	var err error
	if s.models, err = appconfig.ReadModelSettings(model); err != nil {
		return s, err
	}
	// --thinking applies to the main model only; compact and fallback models
	// keep their configured levels.
	if s.models.Main.Thinking, err = appconfig.ParseThinking(thinking); err != nil {
		return s, fmt.Errorf("--thinking: %w", err)
	}
	if s.fold, err = appconfig.ReadFoldSettings(); err != nil {
		return s, err
	}
	// Folding rewrites earlier messages, which breaks Claude's signed
	// thinking (the signature binds the request prefix) and Claude prompt
	// caching anyway: no folding when the main model speaks Anthropic, neither
	// by step cadence nor by size (context.fold_at).
	if s.models.Main.Provider.Type == appconfig.ProviderTypeAnthropic {
		s.fold.Every, s.fold.At = 0, 0
	}
	// memory.taint_tools taint on every result (default none);
	// memory.taint_on_spill only when the output was externalised (default
	// shell_exec; an empty list turns it off).
	if s.taintTools, err = toolNameList("memory.taint_tools", nil); err != nil {
		return s, err
	}
	if s.taintOnSpill, err = toolNameList("memory.taint_on_spill", []string{"shell_exec"}); err != nil {
		return s, err
	}
	memConfig, err := loadMemoryConfig()
	if err != nil {
		return s, err
	}
	if s.memPolicy, err = loadMemoryWritePolicy(); err != nil {
		return s, err
	}
	// Backend selection only (no backend command runs here).
	if s.memSetup, err = buildMemory(workspace, memConfig); err != nil {
		return s, err
	}
	if s.sessionsDir, err = appconfig.ResolveSessionsDir(); err != nil {
		return s, fmt.Errorf("resolve sessions directory: %w", err)
	}
	s.inlineBytes, s.spillBytes = inlineOutputBytes(), rawOutputBytes()
	return s, nil
}
