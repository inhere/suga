package wiring

import (
	appconfig "github.com/inhere/suga/internal/config"
)

// protectedCheck is the startup comparison of the protected settings of the
// config file this process loaded: Open runs it once per process, unless the
// caller turned it off (Options.SkipProtectedCheck: `suga session compact`
// starts a runtime without a session and must not create a snapshot).
func protectedCheck(opts Options) *appconfig.ProtectedState {
	if opts.SkipProtectedCheck {
		return nil
	}
	return appconfig.CheckProtectedOnce()
}

// protectedNoticeCallback adapts the state to personal.Config.ProtectedNotice:
// the personal runner records the trace event of the sessions that have not
// seen the notice yet, this package only converts the change.
func protectedNoticeCallback(state *appconfig.ProtectedState) func(sessionID string) (keys []string, wholeFile, unreadable bool, ok bool) {
	if state == nil {
		return nil
	}
	return func(sessionID string) ([]string, bool, bool, bool) {
		change, ok := state.NoticeFor(sessionID)
		if !ok {
			return nil, false, false, false
		}
		return change.Keys, change.WholeFile, change.SnapshotUnreadable, true
	}
}

// ProtectedState returns the startup comparison of the protected settings this
// runtime was opened with; nil when the check was skipped or the configuration
// was not loaded in this process. chat shows its notice in the banner and
// advances the snapshot, run writes it to stderr.
func (rt *Runtime) ProtectedState() *appconfig.ProtectedState {
	if rt == nil {
		return nil
	}
	return rt.protectedState
}
