package store

import (
	"context"
	"fmt"
	"time"
)

// Session retention by origin (design 0.3, 会话来源). `suga gc --retention`
// removes sessions by class instead of by one shared age: an interactive
// session is kept for the interactive period, a session a non-interactive
// entry point (run, ACP, sub-agent) created for the shorter one. The removal
// itself reuses the batched deletion of gc.go, so it is interruptible and
// never VACUUMs.
//
// The events of the removed sessions go with them; orphan events (their session
// row is already gone) are dropped once they are older than
// GCDefaultOrphanEventRetention. A per-session events TTL is deliberately not
// implemented (the design leaves it off by default).

// GCDefaultOrphanEventRetention is how long the events of a session that no
// longer exists are kept before `gc --retention --apply` drops them.
const GCDefaultOrphanEventRetention = 30 * 24 * time.Hour

// GCRetentionPolicy is the per-class session retention of `suga gc --retention`.
// A period of zero or less turns its class off: those sessions are kept.
type GCRetentionPolicy struct {
	// Interactive applies to chat sessions and to sessions created before
	// meta.origin existed; NonInteractive to run, ACP and sub-agent sessions.
	Interactive    time.Duration
	NonInteractive time.Duration
}

// GCRetentionClassReport is what one retention class of a retention pass
// removes, or would remove on a dry run.
type GCRetentionClassReport struct {
	// Interactive names the class and Retention is the period applied to it.
	Interactive bool
	Retention   time.Duration
	// Sessions are the sessions of the class this pass removes (or would).
	Sessions []Session
	// Entries counts all stored entries of those sessions.
	Entries int64
	// EstimatedBytes approximates the stored bytes they hold: the entry JSON
	// bodies plus the session meta. It is an estimate - snapshots, the events
	// and the free pages of the file are not counted, and only a VACUUM
	// returns space to the disk.
	EstimatedBytes int64
}

// GCRetentionReport is the result of a retention pass, one entry per class.
type GCRetentionReport struct {
	Interactive    GCRetentionClassReport
	NonInteractive GCRetentionClassReport
	// Applied is false for a dry run.
	Applied bool
}

// Classes lists both classes, interactive first.
func (r GCRetentionReport) Classes() []GCRetentionClassReport {
	return []GCRetentionClassReport{r.Interactive, r.NonInteractive}
}

// Sessions lists every session the pass removes, interactive first.
func (r GCRetentionReport) Sessions() []Session {
	return append(append([]Session{}, r.Interactive.Sessions...), r.NonInteractive.Sessions...)
}

// CollectGarbageByRetention removes the sessions whose class retention period
// has passed. keep lists the sessions never removed (a live process or a held
// lock). Without apply nothing is written.
func (s *Store) CollectGarbageByRetention(ctx context.Context, now time.Time, policy GCRetentionPolicy, keep map[string]bool, apply bool) (GCRetentionReport, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return GCRetentionReport{}, err
	}
	byID := make(map[string]Session, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = session
	}
	report := GCRetentionReport{
		Interactive:    GCRetentionClassReport{Interactive: true, Retention: policy.Interactive},
		NonInteractive: GCRetentionClassReport{Retention: policy.NonInteractive},
	}
	expired := func(session Session) bool {
		ttl, _ := retentionTTL(session, byID, policy)
		if ttl <= 0 {
			return false // this class is off
		}
		return session.UpdatedAt.Before(now.Add(-ttl)) && !keep[session.ID]
	}
	chosen := selectGCSessions(sessions, keep, expired)
	for _, session := range chosen {
		if _, interactive := retentionTTL(session, byID, policy); interactive {
			report.Interactive.Sessions = append(report.Interactive.Sessions, session)
		} else {
			report.NonInteractive.Sessions = append(report.NonInteractive.Sessions, session)
		}
	}
	for _, class := range []*GCRetentionClassReport{&report.Interactive, &report.NonInteractive} {
		if err := s.fillRetentionStats(ctx, class); err != nil {
			return report, err
		}
	}
	if !apply {
		return report, nil
	}
	if err := s.deleteSessionsBatched(ctx, sessionIDs(chosen)); err != nil {
		return report, err
	}
	if err := s.deleteOrphanEvents(ctx, now.Add(-GCDefaultOrphanEventRetention)); err != nil {
		return report, err
	}
	report.Applied = true
	return report, nil
}

// retentionTTL returns the retention period that applies to a session and
// whether that is the interactive class. A sub-agent session follows its
// parent: its own origin is "agent", but it belongs to the session that started
// it, so a sub-agent of a chat is kept as long as that chat is.
func retentionTTL(sess Session, byID map[string]Session, policy GCRetentionPolicy) (time.Duration, bool) {
	if IsAgentSession(sess) {
		if parent, ok := byID[ParentSession(sess)]; ok && !IsAgentSession(parent) {
			return policy.Interactive, true
		}
	}
	if InteractiveSession(sess) {
		return policy.Interactive, true
	}
	return policy.NonInteractive, false
}

// fillRetentionStats counts the entries and the stored bytes of a class's
// sessions, gcIDChunk sessions per statement.
func (s *Store) fillRetentionStats(ctx context.Context, class *GCRetentionClassReport) error {
	ids := sessionIDs(class.Sessions)
	if len(ids) == 0 {
		return nil
	}
	for start := 0; start < len(ids); start += gcIDChunk {
		end := min(start+gcIDChunk, len(ids))
		args := toAny(ids[start:end])
		var entries, entryBytes int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*), COALESCE(SUM(length(COALESCE(json, ''))), 0) FROM entries WHERE session_id IN (`+placeholders(end-start)+`)`,
			args...).Scan(&entries, &entryBytes); err != nil {
			return fmt.Errorf("measure entries of %d sessions: %w", end-start, err)
		}
		var metaBytes int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(length(COALESCE(meta_json, ''))), 0) FROM sessions WHERE id IN (`+placeholders(end-start)+`)`,
			args...).Scan(&metaBytes); err != nil {
			return fmt.Errorf("measure sessions of %d sessions: %w", end-start, err)
		}
		class.Entries += entries
		class.EstimatedBytes += entryBytes + metaBytes
	}
	return nil
}
