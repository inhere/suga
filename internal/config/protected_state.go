package axconfig

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/inhere/suga/internal/i18n"
)

// Catalog keys of the startup notice (internal/i18n/catalog/*/status.ini, the
// [status] section).
const (
	protectedChangedKey     = "status.protected.changed"
	protectedChangedFileKey = "status.protected.changed_file"
	protectedUnreadableKey  = "status.protected.unreadable"
	protectedSeparatorKey   = "status.protected.separator"
)

// protectedStates caches the startup comparison of one config file per process.
// ACP opens a runtime for every request, so the file is compared once and the
// sessions that already saw the notice are deduplicated by ProtectedState.
var (
	protectedStatesMu sync.Mutex
	protectedStates   = map[string]*ProtectedState{}
)

// ProtectedState is the startup comparison of the protected settings of one
// config file: what changed since the user last confirmed them, the digest the
// comparison ran on and the sessions that already got the notice.
//
// NoticeFor and Advance run under mu: ACP serves every request on its own
// goroutine, so one process has several sessions asking about the same state.
type ProtectedState struct {
	mu sync.Mutex
	// Change is what differs from the snapshot the user last confirmed (empty:
	// nothing changed).
	Change ProtectedChange
	// Digest is the digest Change was compared from. Advance writes exactly this
	// one, so a file edited between the comparison and the banner is reported
	// again next start instead of being absorbed unseen.
	Digest ProtectedDigest
	// Path is the config file as LoadedConfigBytes reported it (the absolute path
	// shown to the user and written to the snapshot); the cache key is its
	// normalized form instead.
	Path string
	// ok reports whether the comparison ran. Without it the state neither shows a
	// notice nor advances the snapshot.
	ok bool
	// recorded holds the sessions that already got the notice.
	recorded map[string]bool
}

// CheckProtectedOnce returns the process-wide comparison of the protected
// settings of the config file Load read. The first caller compares and caches;
// every later caller (a second ACP prompt, a resumed chat) gets the same state,
// so the file is compared once per process.
//
// A configuration this process never loaded, an unreadable digest or a failed
// comparison is logged and cached as "no change, cannot advance": the notice is
// an observation of the startup state, never a reason to stop the process.
func CheckProtectedOnce() *ProtectedState {
	path, data, loaded := LoadedConfigBytes()
	if !loaded {
		// Not cached: a later Load in this process (a host embedding the SDK) has
		// to be compared then.
		slog.Warn("protected settings: the configuration was not loaded; the startup comparison is skipped")
		return &ProtectedState{}
	}
	state := &ProtectedState{Path: path}
	digest, err := DigestProtected(data)
	if err != nil {
		slog.Warn("protected settings: digest the config file", "path", path, "err", err)
		return storeProtectedState(normalizeConfigPath(path), state)
	}
	change, err := CompareProtected(path, digest)
	if err != nil {
		slog.Warn("protected settings: compare with the confirmed snapshot", "path", path, "err", err)
		return storeProtectedState(normalizeConfigPath(path), state)
	}
	state.Digest, state.Change = digest, change
	state.ok, state.recorded = true, make(map[string]bool)
	return storeProtectedState(normalizeConfigPath(path), state)
}

// storeProtectedState publishes state for one config file. The first comparison
// of the process wins, so every session of this process sees the same one.
func storeProtectedState(key string, state *ProtectedState) *ProtectedState {
	protectedStatesMu.Lock()
	defer protectedStatesMu.Unlock()
	if existing, ok := protectedStates[key]; ok {
		return existing
	}
	protectedStates[key] = state
	return state
}

// Advance writes the compared digest as the snapshot the user has confirmed by
// seeing it: chat calls it once the banner showed the notice. Nothing is
// written without a usable comparison or without a change, and run/ACP never
// call it (they only report).
func (s *ProtectedState) Advance() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ok || s.Change.Empty() {
		return nil
	}
	return AdvanceProtected(s.Path, s.Digest)
}

// NoticeFor returns the change this session has to be told about, once per
// session: ACP opens a runtime per request, so the same session asks again on
// every prompt and must see the notice only the first time. The check and the
// mark happen under one lock, so two requests of one session cannot both get it.
func (s *ProtectedState) NoticeFor(sessionID string) (ProtectedChange, bool) {
	if s == nil {
		return ProtectedChange{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ok || s.Change.Empty() || s.recorded[sessionID] {
		return ProtectedChange{}, false
	}
	s.recorded[sessionID] = true
	return s.Change, true
}

// ResetProtectedStateForTest forgets the cached comparisons, so a test can
// compare another config file in the same process. Tests call it from their
// cleanup; production code never does.
func ResetProtectedStateForTest() {
	protectedStatesMu.Lock()
	defer protectedStatesMu.Unlock()
	protectedStates = map[string]*ProtectedState{}
}

// ProtectedChangeText renders the startup notice of a change of the protected
// settings, for chat's banner and run's stderr line alike: the keys are named
// (never their values - protected keys include secrets) and joined with the
// catalog's separator ("、" in zh, "," in en).
func ProtectedChangeText(tr i18n.Translator, c ProtectedChange, path string) string {
	if tr == nil {
		tr = i18n.Fallback()
	}
	switch {
	case c.WholeFile:
		return tr.T(protectedChangedFileKey, map[string]any{"path": path})
	case c.SnapshotUnreadable:
		return tr.T(protectedUnreadableKey, map[string]any{"keys": joinProtectedKeys(tr, c.Keys), "path": path})
	default:
		return tr.T(protectedChangedKey, map[string]any{"keys": joinProtectedKeys(tr, c.Keys), "path": path})
	}
}

// joinProtectedKeys joins the changed key names with the language's separator.
func joinProtectedKeys(tr i18n.Translator, keys []string) string {
	return strings.Join(keys, tr.T(protectedSeparatorKey))
}
