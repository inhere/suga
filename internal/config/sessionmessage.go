package axconfig

import (
	"os"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// The session_message tool (design 2026-10-21 session_message D2/D3): whether a
// model may list and message the other chat sessions of the same SUGA_HOME, and
// how deep the automatic replies between two sessions may go.
const (
	// DefaultSessionMessageMaxHops is the automatic-reply depth at which a
	// received session message no longer starts a turn of its own.
	DefaultSessionMessageMaxHops = 3
	// envSessionMessageOff turns the tool off for one process tree
	// (SUGA_SESSION_MESSAGE=off), which is easier to set for a gofer job than a
	// config file.
	envSessionMessageOff = "SUGA_SESSION_MESSAGE"
)

// SessionMessageEnabled reports tools.session_message.enabled (default true),
// with SUGA_SESSION_MESSAGE=off as a process-level switch.
func SessionMessageEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envSessionMessageOff))) {
	case "off", "0", "false", "no":
		return false
	}
	return gkconfig.Bool("tools.session_message.enabled", true)
}

// SessionMessageMaxHops is tools.session_message.max_hops (default 3): a session
// message with this many hops is queued for the next input instead of starting a
// turn. A value below 1 is read as the default, so the loop guard cannot be
// turned off by accident.
func SessionMessageMaxHops() int {
	hops := gkconfig.Int("tools.session_message.max_hops", DefaultSessionMessageMaxHops)
	if hops < 1 {
		return DefaultSessionMessageMaxHops
	}
	return hops
}
