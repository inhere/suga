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
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
	"github.com/inhere/suga/pkg/agentcore/tools"
	toolsmcp "github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
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

// inlineOutputBytes is tools.max_output_bytes: the largest tool result kept
// whole in the session (file_read pages to it; the output layer externalises
// anything larger).
func inlineOutputBytes() int {
	return gkconfig.Int("tools.max_output_bytes", native.DefaultMaxOutputBytes)
}

// rawOutputBytes is tools.max_spill_bytes: the raw output cap of one tool call
// (native and MCP), i.e. the most the output layer saves to a session file.
func rawOutputBytes() int {
	return gkconfig.Int("tools.max_spill_bytes", native.DefaultRawOutputBytes)
}

// shellBackgroundAfter is the foreground grace period before shell_exec
// transfers a command to tasks. A bare number is seconds, matching other
// duration settings; zero deliberately disables the handoff.
func shellBackgroundAfter() time.Duration {
	raw := strings.TrimSpace(gkconfig.String("tools.shell_background_after"))
	if raw == "" {
		return native.DefaultShellBackgroundAfter
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
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
	var files []string
	if home, err := axconfig.ResolveSugaHome(); err == nil && filepath.IsAbs(home) {
		files = append(files, filepath.Join(home, outputRulesFile))
	}
	if workspace != "" {
		files = append(files, filepath.Join(workspace, ".suga", outputRulesFile))
	}
	rules, errs := native.LoadOutputRules(files...)
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
		NetworkProxy:      configuredNetworkProxy(),
		ShellTimeoutMax: time.Duration(gkconfig.Int("tools.shell_timeout_max",
			int(native.DefaultShellTimeoutMax/time.Second))) * time.Second,
		ShellBackgroundAfter: shellBackgroundAfter(),
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
// layer per tools.deferred. When tools end up deferred, tool_search is added to
// base so the model can load them; otherwise it is not registered at all.
func newToolVisibility(base *tools.Registry, inner agentcore.ToolRegistry, contextWindow int, pinnedMCPTools []string) (*tools.Visibility, error) {
	mode, err := tools.ParseDeferredMode(gkconfig.String("tools.deferred"))
	if err != nil {
		return nil, err
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
	})
	if vis.Active() {
		base.MustRegister(native.NewToolSearchTool(vis))
	}
	return vis, nil
}
