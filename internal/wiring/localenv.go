package wiring

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	gkconfig "github.com/gookit/config/v2"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/redact"
)

// LocalEnv is what a session in a workspace would get on this machine now:
// the manifest plus the details behind it (tool schemas, each skill and how
// the listing presents it, the rule files, hooks and MCP servers). The trace
// page shows it next to what the session recorded, so the two can be
// compared. It is read from the configuration only: no provider, no MCP
// server, no hook command is started.
type LocalEnv struct {
	Workspace   string               `json:"workspace"`
	Manifest    Manifest             `json:"manifest"`
	Tools       []EnvTool            `json:"tools"`
	Skills      []EnvSkill           `json:"skills"`
	SkillsStats personal.SkillsStats `json:"skills_stats"`
	SkillsText  string               `json:"skills_text"`
	Rules       EnvRules             `json:"rules"`
	Hooks       EnvHooks             `json:"hooks"`
	MCPServers  []EnvMCPServer       `json:"mcp_servers"`
}

// EnvTool is a tool's description and input schema (its flags are in
// Manifest.Tools).
type EnvTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// EnvSkill is one installed skill and how the skills listing presents it:
// "preloaded", "listed" (with its description), "named" (name only) or
// "counted" (past the names budget).
type EnvSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Workspace   bool   `json:"workspace"`
	Shown       string `json:"shown"`
}

// EnvRules are the rule files read for the workspace and their merged text.
type EnvRules struct {
	File  string        `json:"file"` // rules.file
	Files []EnvRuleFile `json:"files"`
	Text  string        `json:"text"`
	Hash  string        `json:"hash"` // compares with turn_context rules_hash
	Error string        `json:"error,omitempty"`
}

// EnvRuleFile is a rule file candidate.
type EnvRuleFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes"`
}

// EnvHooks is the chat hook configuration (hooks run in chat only).
type EnvHooks struct {
	Enabled bool   `json:"enabled"`
	Off     bool   `json:"off"` // SUGA_HOOKS=off
	Config  string `json:"config,omitempty"`
	Error   string `json:"error,omitempty"`
}

// EnvMCPServer is a configured MCP server; env values are not shown.
type EnvMCPServer struct {
	ID      string   `json:"id"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	EnvKeys []string `json:"env_keys,omitempty"`
}

// LoadLocalEnv reads the local environment for workspace ("" = the
// configured workspace).
func LoadLocalEnv(workspace string) (LocalEnv, error) {
	m, parts, err := loadManifest(ManifestOptions{Workspace: workspace})
	if err != nil {
		return LocalEnv{}, err
	}
	env := LocalEnv{Workspace: parts.workspace, Manifest: m, Tools: []EnvTool{}, Skills: []EnvSkill{}, MCPServers: []EnvMCPServer{}}
	for _, name := range parts.registry.List() {
		t, err := parts.registry.Get(name)
		if err != nil {
			continue
		}
		schema := t.Schema()
		env.Tools = append(env.Tools, EnvTool{Name: name, Description: schema.Description, InputSchema: schema.InputSchema})
	}
	slices.SortFunc(env.Tools, func(a, b EnvTool) int { return strings.Compare(a.Name, b.Name) })

	pol := readSkillPolicy()
	env.SkillsText, env.SkillsStats = skillsPrompt(parts.skills, pol)
	plan := planSkills(parts.skills, pol)
	shown := map[string]string{}
	for _, s := range plan.preload {
		shown[s.Name] = "preloaded"
	}
	for _, s := range plan.listed {
		shown[s.Name] = "listed"
	}
	for _, n := range plan.named {
		shown[n] = "named"
	}
	for _, s := range parts.skills.List() {
		how := shown[s.Name]
		if how == "" {
			how = "counted"
		}
		env.Skills = append(env.Skills, EnvSkill{Name: s.Name, Description: s.Description, Path: s.Path, Workspace: s.Workspace, Shown: how})
	}

	env.Rules = loadEnvRules(parts.workspace)
	env.Hooks = loadEnvHooks()
	configured, err := appconfig.ReadMCPServerSettings()
	if err != nil {
		return env, err
	}
	for _, s := range configured {
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			args[i] = redact.Secrets(a)
		}
		env.MCPServers = append(env.MCPServers, EnvMCPServer{ID: s.ID, Command: s.Command, Args: args, EnvKeys: keys})
	}
	return env, nil
}

// loadEnvRules reads the rule files the way a session in workspace would.
func loadEnvRules(workspace string) EnvRules {
	var out EnvRules
	name, err := appconfig.ReadRuleFileName()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.File = name
	home, err := appconfig.ResolveSugaHome()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	paths, err := personal.RuleFiles(workspace, home, false, name)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	for _, p := range paths {
		f := EnvRuleFile{Path: p}
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			f.Exists, f.Bytes = true, info.Size()
		}
		out.Files = append(out.Files, f)
	}
	if out.Text, out.Hash, err = personal.LoadRules(workspace, home, false, name); err != nil {
		out.Error = err.Error()
	}
	return out
}

// loadEnvHooks reports whether chat hooks are on and their configuration
// (redacted).
func loadEnvHooks() EnvHooks {
	out := EnvHooks{Off: strings.EqualFold(strings.TrimSpace(os.Getenv("SUGA_HOOKS")), "off")}
	if raw := gkconfig.Get("hooks"); raw != nil {
		if data, err := json.MarshalIndent(raw, "", "  "); err == nil {
			out.Config = redact.Secrets(string(data))
		}
	}
	client, err := hooks.New()
	if err != nil {
		out.Error = err.Error()
	}
	out.Enabled = client != nil
	return out
}
