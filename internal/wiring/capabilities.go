package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	gkconfig "github.com/gookit/config/v2"

	axconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
	"github.com/inhere/suga/pkg/agentcore/tools"
	toolsmcp "github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

type capabilityToolProvider interface {
	RegisterTools(registry *tools.Registry) error
	Close() error
}

var mcpProviderFactory = func(ctx context.Context, settings toolsmcp.ServerSettings) (capabilityToolProvider, error) {
	settings = withMCPTimeouts(settings)
	if settings.RawBytes == 0 {
		settings.RawBytes = rawOutputBytes()
	}
	return toolsmcp.NewProvider(ctx, settings)
}

// withMCPTimeouts applies mcp.call_timeout_sec and mcp.startup_timeout_sec to a
// server without its own timeouts. Every server (config, Claude MCP files, ACP
// session) starts through mcpProviderFactory, so this is the one place the
// settings are read.
func withMCPTimeouts(settings toolsmcp.ServerSettings) toolsmcp.ServerSettings {
	if settings.CallTimeout == 0 {
		settings.CallTimeout = axconfig.MCPCallTimeout()
	}
	if settings.StartupTimeout == 0 {
		settings.StartupTimeout = axconfig.MCPStartupTimeout()
	}
	return settings
}

type defaultCapabilities struct {
	Registry *tools.Registry
	// conflicts records the same-name tool collisions seen while registering
	// MCP providers (first-wins, design D8); the live manifest reports them.
	conflicts []toolsmcp.Conflict
	closers   []capabilityToolProvider
}

// recordConflicts collects the tool-name collisions of one provider. Providers
// that do not report conflicts (test doubles) are ignored.
func (c *defaultCapabilities) recordConflicts(provider capabilityToolProvider) {
	reporter, ok := provider.(interface{ Conflicts() []toolsmcp.Conflict })
	if !ok {
		return
	}
	c.conflicts = append(c.conflicts, reporter.Conflicts()...)
}

// Conflicts reports the tool-name collisions of the assembled capabilities, so
// the runtime manifest can explain a tool that another server already owned.
func (c defaultCapabilities) Conflicts() []toolsmcp.Conflict {
	return append([]toolsmcp.Conflict(nil), c.conflicts...)
}

// buildDefaultCapabilities assembles native and configured MCP tools for the
// personal Agent's writable mode.
func buildDefaultCapabilities(workspace string) (defaultCapabilities, error) {
	return buildDefaultCapabilitiesWithServers(context.Background(), workspace, nil, nil)
}

func buildDefaultCapabilitiesWithServers(ctx context.Context, workspace string, dirs *native.AddDirs, additional []toolsmcp.ServerSettings) (defaultCapabilities, error) {
	return buildDefaultCapabilitiesWithServersAndSnapshot(ctx, workspace, dirs, additional, nil)
}

func buildDefaultCapabilitiesWithServersAndSnapshot(ctx context.Context, workspace string, dirs *native.AddDirs, additional []toolsmcp.ServerSettings, snapshotter native.Snapshotter) (defaultCapabilities, error) {
	return buildDefaultCapabilitiesWithExtras(ctx, workspace, dirs, additional, snapshotter, nil)
}

// buildDefaultCapabilitiesWithExtras is buildDefaultCapabilitiesWithServersAndSnapshot
// plus extra, the host's in-process tools (Options.ExtraTools) of an embedding
// caller. They are ordinary tools of the main session: registered after the MCP
// ones (so a name collision is reported instead of shadowing), and tools.disabled
// applies to them like to every other tool.
func buildDefaultCapabilitiesWithExtras(ctx context.Context, workspace string, dirs *native.AddDirs, additional []toolsmcp.ServerSettings, snapshotter native.Snapshotter, extra []api.Tool) (defaultCapabilities, error) {
	// The language rules shape the symbol and reference tools and the test
	// command recognition, so they are loaded before the tools are built.
	loadLangRuleFiles(workspace)
	registry := tools.NewRegistry()
	for _, tool := range native.BuildTools(nativeEnvWithSnapshot(workspace, dirs, snapshotter), true) {
		registry.MustRegister(tool)
	}
	servers, err := axconfig.ReadMCPServerSettings()
	if err != nil {
		return defaultCapabilities{}, err
	}
	built := defaultCapabilities{Registry: registry}
	servers = append(servers, additional...)
	for _, settings := range servers {
		provider, err := mcpProviderFactory(ctx, settings)
		if err != nil {
			_ = built.Close()
			return defaultCapabilities{}, fmt.Errorf("build mcp provider %q: %w", settings.ID, err)
		}
		if err := provider.RegisterTools(registry); err != nil {
			_ = provider.Close()
			_ = built.Close()
			return defaultCapabilities{}, fmt.Errorf("register mcp tools for %q: %w", settings.ID, err)
		}
		built.recordConflicts(provider)
		built.closers = append(built.closers, provider)
	}
	// The host's own tools are registered before tools.disabled is applied, so a
	// disabled host tool (or MCP/server name) is validated and removed here too.
	if err := registerHostTools(registry, extra); err != nil {
		_ = built.Close()
		return defaultCapabilities{}, err
	}
	// tools.disabled: a listed tool is not registered, so it is absent from the
	// schemas (and from tool_search and the manifest) instead of failing on call.
	if err := applyDisabledTools(registry); err != nil {
		_ = built.Close()
		return defaultCapabilities{}, err
	}
	return built, nil
}

func (c defaultCapabilities) Close() error {
	var firstErr error
	for _, closer := range c.closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// lateToolNames are the tools Open registers itself after the capability
// build. A host tool with one of these names would be replaced by its own
// (tool_search) or panic at registration, so Options.ExtraTools rejects them
// up front instead of failing halfway through a runtime.
var lateToolNames = []string{
	"agent", "memory", "recall", "session_search", "skill", "suga_manifest", "task", "tool_search", "update_plan",
}

// validateExtraToolNames rejects a host tool whose name the runtime registers
// later or another host tool already uses. A nil tool or an empty name is
// reported by registerHostTools, where the registry is at hand.
func validateExtraToolNames(extra []api.Tool) error {
	seen := make(map[string]bool, len(extra))
	for i, t := range extra {
		if t == nil {
			continue
		}
		name := strings.TrimSpace(t.Name())
		for _, reserved := range lateToolNames {
			if name == reserved {
				return fmt.Errorf("extra tool %q (Options.ExtraTools): the name is reserved by the runtime", name)
			}
		}
		if seen[name] {
			return fmt.Errorf("extra tool #%d (Options.ExtraTools): duplicate name %q", i, name)
		}
		seen[name] = true
	}
	return nil
}

// registerHostTools adds the host's in-process tools to reg. A name a built-in
// or MCP tool already owns, or an empty name, is a startup error: the host must
// not silently shadow a suga tool (and a duplicate registration would panic).
func registerHostTools(reg *tools.Registry, extra []api.Tool) error {
	for i, t := range extra {
		if t == nil {
			return fmt.Errorf("extra tool #%d (Options.ExtraTools): nil tool", i)
		}
		name := strings.TrimSpace(t.Name())
		if name == "" {
			return fmt.Errorf("extra tool #%d (Options.ExtraTools): empty name", i)
		}
		if _, err := reg.Get(name); err == nil {
			return fmt.Errorf("extra tool %q: a built-in or MCP tool already has that name", name)
		}
		reg.Register(t)
	}
	return nil
}

// inlineOutputBytes is tools.max_output_bytes: the largest tool result kept
// whole in the session (file_read pages to it; the output layer externalises
// anything larger).
func inlineOutputBytes() int {
	return gkconfig.Int("tools.max_output_bytes", native.DefaultMaxOutputBytes)
}

// shellLimits reads tools.shell_limits: the memory, process count and CPU time
// cap of one shell_exec run and its process tree. memory defaults to 4GiB and
// processes to 256; an explicit 0 (or "off") turns that one limit off and
// tools.shell_limits.off turns all of them off. An unreadable value keeps the
// default (Open validates the section at startup).
func shellLimits() native.ShellLimits {
	if gkconfig.Bool("tools.shell_limits.off") {
		return native.ShellLimits{}
	}
	limits := native.ShellLimits{
		MemoryBytes: native.DefaultShellLimitMemory,
		Processes:   native.DefaultShellLimitProcesses,
	}
	if raw := strings.TrimSpace(gkconfig.String("tools.shell_limits.memory")); raw != "" {
		memory, err := native.ParseMemorySize(raw)
		if err != nil {
			slog.Warn("skip tools.shell_limits.memory", "err", err)
		} else {
			limits.MemoryBytes = memory
		}
	}
	if n := gkconfig.Int("tools.shell_limits.processes", -1); n >= 0 {
		limits.Processes = n
	}
	limits.CPUTime = shellLimitCPUTime()
	return limits
}

// shellLimitCPUTime is tools.shell_limits.cpu_time: a duration ("10m") or a
// bare number of seconds; empty or 0 means no CPU time limit.
func shellLimitCPUTime() time.Duration {
	raw := strings.TrimSpace(gkconfig.String("tools.shell_limits.cpu_time"))
	if raw == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil && secs > 0 {
		if d, ok := api.DurationFromSeconds(secs, 0); ok {
			return d
		}
		return 0
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return 0
}

// rawOutputBytes is tools.max_spill_bytes: the raw output cap of one tool call
// (native and MCP), i.e. the most the output layer saves to a session file.
func rawOutputBytes() int {
	return gkconfig.Int("tools.max_spill_bytes", native.DefaultRawOutputBytes)
}

// shellTaskTimeoutMax is tools.task_timeout_max, a bare number of seconds.
// It is never below the foreground shell cap so a handoff cannot shorten a call.
func shellTaskTimeoutMax(shellMax time.Duration) time.Duration {
	n := gkconfig.Int("tools.task_timeout_max", int(native.DefaultTaskTimeoutMax/time.Second))
	d := time.Duration(n) * time.Second
	if d < shellMax {
		return shellMax
	}
	return d
}

// transfers a command to tasks. A bare number is seconds, matching other
// duration settings; zero deliberately disables the handoff.
func shellBackgroundAfter() time.Duration {
	raw := strings.TrimSpace(gkconfig.String("tools.shell_background_after"))
	if raw == "" {
		return native.DefaultShellBackgroundAfter
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		if d, ok := api.DurationFromSeconds(float64(seconds), 0); ok {
			return d
		}
		return native.DefaultShellBackgroundAfter
	}
	if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
		return d
	}
	return native.DefaultShellBackgroundAfter
}

// warnReadRootsOnce reports rejected tools.read_roots entries once per process
// (nativeEnv runs several times).
var warnReadRootsOnce sync.Once

// readRoots returns the directories outside the workspace the read-only file
// tools may read: the detected dependency sources (tools.dependency_roots,
// default true), the idev-std sources (tools.standards_roots, default true),
// the skill directories (tools.skill_roots, default true), and tools.read_roots.
// The optional workspace is used for workspace-bound roots.
func readRoots(workspaces ...string) ([]native.ReadRoot, []error) {
	workspace := ""
	if len(workspaces) > 0 {
		workspace = workspaces[0]
	} else {
		workspace = axconfig.ResolveWorkspacePath()
	}
	var roots []native.ReadRoot
	if gkconfig.Bool("tools.dependency_roots", true) {
		roots = append(roots, native.DependencyRoots()...)
	}
	if gkconfig.Bool("tools.standards_roots", true) {
		roots = append(roots, native.StandardsRoots(workspace)...)
	}
	if gkconfig.Bool("tools.skill_roots", true) {
		// The skill roots outside the workspace (SUGA_HOME/skills and
		// ~/.agents/skills; the workspace ones are already reachable), so the
		// file tools read a skill's references without an approval question.
		roots = append(roots, skillReadRoots(workspace)...)
	}
	extra, errs := native.ConfiguredReadRoots(gkconfig.Strings("tools.read_roots"))
	return native.DeduplicateReadRoots(append(roots, extra...)), errs
}

// skillReadRoots turns the skill directories outside the workspace into read
// roots: only existing directories are kept, like the other root detectors.
// Workspace skill roots are skipped: they already lie inside the workspace the
// file tools reach.
func skillReadRoots(workspace string) []native.ReadRoot {
	homeDir, _ := os.UserHomeDir()
	sugaHome, err := axconfig.ResolveSugaHome()
	if err != nil {
		sugaHome = ""
	}
	var roots []native.ReadRoot
	for _, sk := range defaultSkillRoots(workspace, homeDir, sugaHome) {
		if sk.Workspace {
			continue
		}
		if root, ok := native.ExistingReadRoot("skills", sk.Dir); ok {
			roots = append(roots, root)
		}
	}
	return roots
}

// minimizeOutput is the tool result tidier (tools.minimize_output, default
// true): test summaries, folded repeats, no colour codes; nil when off. Its
// output rules are the built-in ones, overridden and extended by
// $SUGA_HOME/output-rules.yaml and then <workspace>/.suga/output-rules.yaml.
func minimizeOutput(workspace string) func(agentcore.ToolCall, string) (string, bool) {
	if !gkconfig.Bool("tools.minimize_output", true) {
		return nil
	}
	rules, errs := native.LoadOutputRules(layeredRulePaths(workspace, outputRulesFile)...)
	for _, err := range errs {
		slog.Warn("skip output rules file", "err", err)
	}
	return native.NewOutputMinimizer(rules).Minimize
}

// prefetchTools lets a reply's local read calls (file_read, file_list, file_grep, file_find) run concurrently (tools.parallel_reads, default true);
// nil when off.
func prefetchTools(reg *tools.Registry) func(context.Context, []agentcore.ToolCall) {
	if reg == nil || !gkconfig.Bool("tools.parallel_reads", true) {
		return nil
	}
	return reg.Prefetch
}

// outputRulesFile names the user and project output rule files.
const outputRulesFile = "output-rules.yaml"

// nudgeRulesFile names the user and project nudge rule files.
const nudgeRulesFile = "nudge-rules.yaml"

// layeredRulePaths returns the layer paths of one tunable rule file kind, in
// load order (later files win): $SUGA_HOME/<name>, then
// <workspace>/.suga/<name>.
func layeredRulePaths(workspace, name string) []string {
	var files []string
	if home, err := axconfig.ResolveSugaHome(); err == nil && filepath.IsAbs(home) {
		files = append(files, filepath.Join(home, name))
	}
	if workspace != "" {
		files = append(files, filepath.Join(workspace, ".suga", name))
	}
	return files
}

// langRulesFile names the user and project language rule files: the
// declaration languages of file_symbols/file_refs and their test commands
// (design docs/design/2026-10-22-lang-rules-design.md).
const langRulesFile = "lang-rules.yaml"

// loadLangRuleFiles returns the effective language registry: the built-in rules
// overridden and extended by $SUGA_HOME/lang-rules.yaml and then
// <workspace>/.suga/lang-rules.yaml, installed as the process's registry. A
// file that does not parse or holds a broken rule is reported and skipped as a
// whole, like an output-rules file, and the manifest lists it.
func loadLangRuleFiles(workspace string) codeintel.LangRegistry {
	files := layeredRulePaths(workspace, langRulesFile)
	reg, errs := codeintel.LoadLangRules(files...)
	codeintel.InstallLangRules(reg)
	for _, err := range errs {
		slog.Warn("skip lang rules file", "err", err)
	}
	for _, warning := range reg.Warnings {
		slog.Warn("lang rules conflict", "warning", warning)
	}
	return reg
}

// loadNudgeRuleFiles returns the effective nudge rules (personal.LoadNudgeRules):
// the built-in ones, overridden and extended by $SUGA_HOME/nudge-rules.yaml and
// then <workspace>/.suga/nudge-rules.yaml. A file that does not parse or holds
// a broken rule is reported and skipped as a whole, like an output-rules file.
func loadNudgeRuleFiles(workspace string) []personal.NudgeRule {
	rules, errs := personal.LoadNudgeRules(layeredRulePaths(workspace, nudgeRulesFile)...)
	for _, err := range errs {
		slog.Warn("skip nudge rules file", "err", err)
	}
	return rules
}

// nudgeRuleSource is the runner's rules source: the layered files, read once
// per process.
func nudgeRuleSource(workspace string) func() []personal.NudgeRule {
	var once sync.Once
	var rules []personal.NudgeRule
	return func() []personal.NudgeRule {
		once.Do(func() { rules = loadNudgeRuleFiles(workspace) })
		return rules
	}
}

// trashDir is where file_remove moves removed paths: <SUGA_HOME>/trash, or
// "" (file_remove unavailable) when SUGA_HOME cannot be resolved.
func trashDir() string {
	home, err := axconfig.ResolveSugaHome()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, "trash")
}

// nativeEnv reads the tools.* settings native tools are built with. dirs is
// the session's additional-directory set (--add-dir), which the tools read at
// every call, so it may still be empty here (nil = none).
func nativeEnv(workspace string, dirs *native.AddDirs) native.Env {
	return nativeEnvWithSnapshot(workspace, dirs, nil)
}

func nativeEnvWithSnapshot(workspace string, dirs *native.AddDirs, snapshotter native.Snapshotter) native.Env {
	roots, errs := readRoots(workspace)
	warnReadRootsOnce.Do(func() {
		for _, err := range errs {
			slog.Warn("skip read root", "err", err)
		}
	})
	return native.Env{
		Workspace:         workspace,
		AddDirs:           dirs,
		TrashDir:          trashDir(),
		ReadRoots:         native.RootPaths(roots),
		Snapshots:         snapshotter,
		InlineBytes:       inlineOutputBytes(),
		ReadPageBytes:     gkconfig.Int("tools.read_page_bytes", native.DefaultReadPageBytes),
		RawBytes:          rawOutputBytes(),
		SerpAPIKey:        gkconfig.String("serpapi_key"),
		Shell:             gkconfig.String("tools.shell"),
		Interpreters:      detectInterpreters(),
		FetchAllowPrivate: gkconfig.Bool("tools.web_fetch.allow_private"),
		FetchProxy:        gkconfig.String("tools.web_fetch.proxy"),
		FetchUserAgent:    gkconfig.String("tools.web_fetch.user_agent"),
		NetworkProxy:      configuredNetworkProxy(),
		ShellTimeoutMax: time.Duration(gkconfig.Int("tools.shell_timeout_max",
			int(native.DefaultShellTimeoutMax/time.Second))) * time.Second,
		TaskTimeoutMax: shellTaskTimeoutMax(time.Duration(gkconfig.Int("tools.shell_timeout_max",
			int(native.DefaultShellTimeoutMax/time.Second))) * time.Second),
		ShellBackgroundAfter: shellBackgroundAfter(),
		ShellLimits:          shellLimits(),
		ImageMaxSide:         imageMaxSide(),
		// session_message: the tool that messages the other chat sessions of this
		// SUGA_HOME; nil when it is turned off.
		SessionMessenger: newSessionMessenger(),
		TurnID:           currentTurnID,
	}
}

// imageMaxSide is view_image's default long side (images.max_side). Open
// validates the section at startup, so an invalid value keeps the built-in
// default here instead of failing tool construction.
func imageMaxSide() int {
	settings, err := axconfig.ReadImageSettings()
	if err != nil {
		return axconfig.DefaultImageMaxSide
	}
	return settings.MaxSide
}

// configuredNetworkProxy resolves network.proxy for the native tools. Open()
// validates the value at startup, so an error here means a caller built tools
// without that step (a test or the standalone manifest): warn and keep the
// previous per-tool behavior instead of applying half a configuration.
func configuredNetworkProxy() *netproxy.Selector {
	selector, err := networkProxySelector()
	if err != nil {
		slog.Warn("skip invalid network.proxy", "err", err)
		return nil
	}
	return selector
}

func swapMCPProviderFactoryForTest(factory func(ctx context.Context, settings toolsmcp.ServerSettings) (capabilityToolProvider, error)) func() {
	prev := mcpProviderFactory
	mcpProviderFactory = factory
	return func() {
		mcpProviderFactory = prev
	}
}

// newToolVisibility wraps the (approval-gated) registry in the deferred-tool
// layer per tools.deferred, tools.deferred_native and tools.deferred_apply. When
// tools end up deferred, tool_search is added to base so the model can load
// them; with segment apply, tool_call is added for the calls inside a segment.
// Otherwise neither is registered at all.
func newToolVisibility(base *tools.Registry, inner agentcore.ToolRegistry, contextWindow int, pinnedMCPTools []string) (*tools.Visibility, error) {
	mode, err := tools.ParseDeferredMode(gkconfig.String("tools.deferred"))
	if err != nil {
		return nil, err
	}
	apply, err := tools.ParseDeferredApplyMode(gkconfig.String("tools.deferred_apply"))
	if err != nil {
		return nil, err
	}
	nativeTools, err := axconfig.ReadDeferredNativeTools()
	if err != nil {
		return nil, err
	}
	if mode == tools.DeferredNever {
		// tools.deferred: never turns the whole deferred mechanism off, native
		// names included: the tool table must stay exactly as before.
		nativeTools = nil
	}
	lookup, ok := inner.(interface {
		agentcore.ToolRegistry
		tools.Lookup
	})
	if !ok {
		return nil, fmt.Errorf("tool registry %T does not support lookup", inner)
	}
	vis := tools.NewVisibility(lookup, tools.VisibilityOptions{
		Mode: mode, ContextWindow: contextWindow, PinnedMCPTools: pinnedMCPTools,
		NativeTools: nativeTools, Apply: apply,
	})
	if vis.Active() {
		base.MustRegister(native.NewToolSearchTool(vis))
	}
	if vis.HasSegmentNative() {
		base.MustRegister(tools.NewToolCallProxy(vis))
	}
	return vis, nil
}
