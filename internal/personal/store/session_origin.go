package store

// Session origins (meta.origin). A session records which entry point created
// it, and `suga gc --retention` picks the retention period from that: an
// interactive chat is worth keeping much longer than a session a batch job or
// an editor integration started (design 0.3, 会话来源).
const (
	// MetaOrigin is the session meta key holding the entry point.
	MetaOrigin = "origin"
	// OriginChat is an interactive `suga chat`, OriginRun a one-shot
	// `suga run`, OriginACP a session an editor created over ACP and
	// OriginAgent a sub-agent session.
	OriginChat  = "chat"
	OriginRun   = "run"
	OriginACP   = "acp"
	OriginAgent = "agent"
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
	case OriginRun, OriginACP, OriginAgent:
		return false
	default:
		return true // chat, and any session from before the key existed
	}
}
