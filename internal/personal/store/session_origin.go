package store

// Session origins (meta.origin). A session records which entry point created
// it, and `suga data gc --retention` picks the retention period from that: an
// interactive chat is worth keeping much longer than a session a batch job or
// an editor integration started (design 0.3, 会话来源).
const (
	// MetaOrigin is the session meta key holding the entry point.
	MetaOrigin = "origin"
	// OriginChat is an interactive `suga chat`, OriginRun a one-shot
	// `suga run`, OriginACP a session an editor created over ACP,
	// OriginAgent a sub-agent session and OriginXhen a topic session a host
	// application (xhen) created in the shared home.
	OriginChat  = "chat"
	OriginRun   = "run"
	OriginACP   = "acp"
	OriginAgent = "agent"
	// OriginXhen names a session of a host application that embeds suga
	// (xhen). Its lifecycle belongs to the host: the default `suga session list`
	// listing leaves it out and `suga data gc --retention` never removes it.
	OriginXhen = "xhen"
	// MetaUsedInteractively marks a session created by a non-interactive entry
	// point that a chat continued afterwards: from then on it counts as
	// interactive.
	MetaUsedInteractively = "used_interactively"
)

// SessionOrigin returns the meta.origin of a session. It is empty for a session
// created before the key existed (by an older suga).
func SessionOrigin(sess Session) string {
	origin, _ := sess.Meta[MetaOrigin].(string)
	return origin
}

// SessionUsedInteractively reports whether a chat continued a session that a
// non-interactive entry point created (meta.used_interactively).
func SessionUsedInteractively(sess Session) bool {
	used, _ := sess.Meta[MetaUsedInteractively].(bool)
	return used
}

// InteractiveSession reports whether the retention rules count a session as
// interactive: created by a chat, continued by a chat, or created before
// meta.origin existed. A historical session is deliberately treated as
// interactive, that is as the class with the longer period.
func InteractiveSession(sess Session) bool {
	if SessionUsedInteractively(sess) {
		return true
	}
	switch SessionOrigin(sess) {
	case OriginRun, OriginACP, OriginAgent, OriginXhen:
		return false
	default:
		return true // chat, and any session from before the key existed
	}
}

// HostManagedSession reports whether a session belongs to a host application
// that embeds suga (origin xhen). Such a session is kept out of the default
// `suga session list` listing and out of the age-based retention pass of
// `suga data gc --retention`: the host decides how long it lives, while an
// explicit `--older-than` still removes it.
func HostManagedSession(sess Session) bool {
	return SessionOrigin(sess) == OriginXhen
}
