package wiring

import (
	"os/exec"
	"sort"
	"strings"
	"sync"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
)

const (
	// preferredReasonFoundPath: the command is on PATH. A session lists such a
	// declaration only when it registered shell_exec (a read-only session cannot
	// run it), so the manifest names that limit instead of promising that the
	// prompt shows it.
	preferredReasonFoundPath        = "found on PATH; read-only sessions do not list it"
	preferredReasonMissingPath      = "not found on PATH"
	preferredReasonMCPNotConfigured = "server not configured"
	// preferredReasonMCPConfigured is the reason without a runtime to observe:
	// the standalone manifest never starts MCP (design 0.2), so it only reports
	// that the server is declared.
	preferredReasonMCPConfigured  = "configured; connection confirmed at session start"
	preferredReasonMCPConnected   = "connected at runtime"
	preferredReasonMCPUnavailable = "not connected at runtime"
)

// PreferredToolStatus is the safe runtime fact used by later prompt and
// manifest layers. It intentionally carries no command path, MCP arguments, or
// environment values.
type PreferredToolStatus struct {
	Name    string
	Kind    string
	When    string
	Present bool
	Reason  string
}

// ResolvePreferredTools validates the configured declarations and resolves
// their current availability without executing a command. MCP availability is
// inferred from the already-built registry, so this function never starts a
// second provider or changes provider lifecycle. A nil registry means the
// caller has no runtime (the standalone manifest): a configured MCP server is
// then reported as configured only.
func ResolvePreferredTools(registry *tools.Registry, extraServers []mcp.ServerSettings) ([]PreferredToolStatus, error) {
	preferred, err := appconfig.ReadPreferredTools()
	if err != nil {
		return nil, err
	}
	servers := append(appconfig.ReadMCPServerSettings(), extraServers...)
	configured := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		id := strings.TrimSpace(server.ID)
		if id == "" {
			id = strings.TrimSpace(server.Command)
		}
		if id != "" {
			configured[id] = struct{}{}
		}
	}

	statuses := make([]PreferredToolStatus, 0, len(preferred))
	for _, item := range preferred {
		status := PreferredToolStatus{Name: item.Name, Kind: item.Kind, When: item.When}
		switch item.Kind {
		case "command":
			if _, err := exec.LookPath(item.Name); err == nil {
				status.Present = true
				status.Reason = preferredReasonFoundPath
			} else {
				status.Reason = preferredReasonMissingPath
			}
		case "mcp":
			if _, ok := configured[item.Name]; !ok {
				status.Reason = preferredReasonMCPNotConfigured
				break
			}
			if registry == nil {
				status.Reason = preferredReasonMCPConfigured
				break
			}
			if registryHasMCPServer(registry, item.Name) {
				status.Present = true
				status.Reason = preferredReasonMCPConnected
			} else {
				status.Reason = preferredReasonMCPUnavailable
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// refreshPreferredCommands rechecks only command declarations at a segment
// boundary. MCP statuses stay frozen for the runtime, as required by the
// simplified design; no provider is restarted or probed here.
func refreshPreferredCommands(statuses []PreferredToolStatus) []PreferredToolStatus {
	refreshed := append([]PreferredToolStatus(nil), statuses...)
	for i := range refreshed {
		if refreshed[i].Kind != "command" {
			continue
		}
		if _, err := exec.LookPath(refreshed[i].Name); err == nil {
			refreshed[i].Present = true
			refreshed[i].Reason = preferredReasonFoundPath
		} else {
			refreshed[i].Present = false
			refreshed[i].Reason = preferredReasonMissingPath
		}
	}
	return refreshed
}

// preferredListed reports whether the environment section lists one declaration
// in this session: a declaration that was not discovered never reaches the
// prompt (design D6), a connected MCP server is listed in every mode, while a
// command is listed only when the session registered shell_exec — a read-only
// session cannot run it, so naming it would offer a tool the model lacks. The
// manifest still reports the discovery fact (present) for commands and its
// reason carries the same limit.
func preferredListed(status PreferredToolStatus, shellExec bool) bool {
	if !status.Present {
		return false
	}
	return status.Kind != "command" || shellExec
}

// listedPreferredToolNames returns the declarations the environment section
// actually lists, in configuration order. It applies the same filter as the
// rendered text, so the turn_context event cannot report a name the prompt did
// not show.
func listedPreferredToolNames(statuses []PreferredToolStatus, shellExec bool) []string {
	var names []string
	for _, status := range statuses {
		if preferredListed(status, shellExec) {
			names = append(names, status.Name)
		}
	}
	return names
}

// listedPreferredNames records the names the environment section lists for the
// current snapshot. The environment sources write it whenever they rebuild the
// text (session start, new segment, context update), so the turn_context event
// reports exactly the list the prompt showed without re-probing PATH per turn.
type listedPreferredNames struct {
	mu    sync.Mutex
	names []string
}

func (l *listedPreferredNames) set(names []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.names = append([]string(nil), names...)
}

func (l *listedPreferredNames) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.names...)
}

// newPreferredEnvironmentSource builds the environment source of a runtime: it
// renders the preferred declarations this session can use and records the names
// it listed in listed, so the turn_context event reports exactly the list the
// prompt shows. opts.AllowWrite is also what registers shell_exec (BuildTools
// skips every non-read-only tool without it), so it selects whether command
// declarations are listed.
func newPreferredEnvironmentSource(opts Options, listed *listedPreferredNames) func([]PreferredToolStatus) string {
	return func(statuses []PreferredToolStatus) string {
		listed.set(listedPreferredToolNames(statuses, opts.AllowWrite))
		envOpts := opts
		envOpts.PreferredTools = statuses
		return environmentPrompt(envOpts)
	}
}

// preferredMCPToolNames returns every registered tool belonging to a preferred
// MCP server. Pinning is by tool name at the Visibility boundary, while the
// resolver remains concerned only with safe server availability facts.
func preferredMCPToolNames(registry *tools.Registry, statuses []PreferredToolStatus) []string {
	if registry == nil {
		return nil
	}
	servers := make(map[string]bool)
	for _, status := range statuses {
		if status.Kind == "mcp" && status.Present {
			servers[status.Name] = true
		}
	}
	var names []string
	for _, name := range registry.List() {
		tool, err := registry.Get(name)
		if err == nil && tools.IsMCP(tool) && servers[tools.MCPServerOf(tool)] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func registryHasMCPServer(registry *tools.Registry, serverID string) bool {
	for _, name := range registry.List() {
		tool, err := registry.Get(name)
		if err == nil && tools.IsMCP(tool) && tools.MCPServerOf(tool) == serverID {
			return true
		}
	}
	return false
}
