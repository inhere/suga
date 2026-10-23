package axconfig

import (
	gkconfig "github.com/gookit/config/v2"

	"github.com/inhere/suga/pkg/agentcore/tools"
)

// ReadDeferredNativeTools reads tools.deferred_native: unset/never defers no
// native tool, auto selects the built-in list, and a YAML list names the tools
// explicitly. A name that is not a native tool is an error, so a typo or an MCP
// tool name fails at startup instead of silently deferring nothing.
func ReadDeferredNativeTools() ([]string, error) {
	return tools.ParseNativeDeferred(gkconfig.Get("tools.deferred_native"))
}
