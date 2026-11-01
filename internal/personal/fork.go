package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/inhere/suga/internal/adddir"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal/store"
)

// Session fork (session rewind design 0.2 section 2 and R6, plan T6): a new
// session that holds the visible history of the source up to the cut of a user
// turn, so the person can continue from there while the source stays as it is.
//
// The new session is built from the store's own records, not by insertSession:
// no new start segment is written, because the source's visible segment records
// (compaction entries with a context) are copied and a fresh snapshot would
// re-read rules and memory the old conversation never saw. For the same reason
// Fork never calls writeStartSegment.
//
// Session meta is copied DEFAULT-DENY (plan F22): only the keys below come
// across, so a key added to the session later is not inherited by accident.
//
//	copied       cwd (the sessions column, not meta), add_dirs (:ro and :rw
//	             directories: the fork is the same person in the same workspace,
//	             so the reach is the same), model and provider (what the next
//	             turn would show), loaded_tools (as in handoff.go: the deferred
//	             tools the history may call)
//	rewritten    title (+ the "fork" suffix of personal.ini), origin (chat: a
//	             person continues it by hand, whatever started the source)
//	added        forked_from (source session id), forked_at_turn (source turn id)
//	inherited    tainted_at / tainted_by, only when the source was tainted no later
//	             than the cut entry (taintInherited)
//	not copied   fold_boundary / fold_active (the fork starts folding afresh, a
//	             missing key reads as 0 / false), cache_prefix_print (prefix cache
//	             of the source's requests), personal_rules_content / _hash /
//	             personal_append_prompt, memory_index_content / _hash (legacy
//	             segment cache, authoritative copy is the segment record that is
//	             copied with the entries), suga_version / run_mode (rewritten by
//	             the next turn), pinned (a pin belongs to one session),
//	             kind / parent_session_id (a fork is never a sub-agent session),
//	             used_interactively (origin is chat), suga.undo.pending (undo
//	             notices queued for the source's model), short_name (names the
//	             source), and every key not listed here.

// metaForkedFrom and metaForkedAtTurn record where a forked session came from.
const (
	metaForkedFrom   = "forked_from"
	metaForkedAtTurn = "forked_at_turn"
)

// forkTitleKey is the personal.ini key of the forked session's title; {title} is
// the source's title.
const forkTitleKey = "personal.session.fork_title"

// forkHook is a test seam: called with "files" after the attachments are copied
// and before the transaction, and with "created" after it committed. An error
// takes the same failure path as a real one.
var forkHook func(stage string) error

// ForkResult is what Fork created: the new session and what it holds.
type ForkResult struct {
	SessionID string
	Turns     int
	Entries   int
}

// Fork copies the visible history of sid up to the cut of the target user turn
// into a new session. It runs inside withSessionUnlessBusy (a running turn is
// refused with ErrRewindBusy) and writes nothing to the source session.
// sessionsRoot is where the attachments live ($SUGA_HOME/sessions); empty means
// the runner's own SessionsDir.
func (r *Runner) Fork(ctx context.Context, snapshots *SessionSnapshots, sessionsRoot, sid, target string) (ForkResult, error) {
	if r == nil || r.cfg.Repository == nil {
		return ForkResult{}, errors.New("personal runner is not configured")
	}
	if err := validateSessionID(sid); err != nil {
		return ForkResult{}, err
	}
	if sessionsRoot == "" {
		sessionsRoot = r.cfg.SessionsDir
	}
	var res ForkResult
	err := r.UndoCore(snapshots, sessionsRoot).withSessionUnlessBusy(ctx, sid, func() error {
		var err error
		res, err = r.forkLocked(ctx, sessionsRoot, sid, target)
		return err
	})
	return res, err
}

// forkLocked is Fork inside the session gate and lock.
func (r *Runner) forkLocked(ctx context.Context, sessionsRoot, sid, target string) (ForkResult, error) {
	repo := r.cfg.Repository
	if err := r.rewindPreconditions(ctx, sid); err != nil {
		return ForkResult{}, err
	}
	src, err := repo.GetSession(ctx, sid)
	if err != nil {
		return ForkResult{}, err
	}
	tgt, err := r.resolveTarget(ctx, sid, target)
	if err != nil {
		return ForkResult{}, err
	}
	kept, err := r.forkVisibleEntries(ctx, sid, cutFor(tgt))
	if err != nil {
		return ForkResult{}, err
	}
	newID, err := r.freshForkID(ctx, sessionsRoot)
	if err != nil {
		return ForkResult{}, err
	}
	// Everything is mapped before the transaction opens (plan F2): turn ids,
	// seqs 1..n, the seq references inside compaction records.
	turns, turnMap, err := r.forkTurns(ctx, newID, sid, kept)
	if err != nil {
		return ForkResult{}, err
	}
	entries, err := forkEntries(kept, turnMap)
	if err != nil {
		return ForkResult{}, err
	}
	sess := store.Session{
		ID:        newID,
		Title:     r.forkTitle(src),
		Cwd:       src.Cwd,
		Status:    store.SessionOpen,
		CreatedAt: time.Now().UTC(),
		Meta:      forkMeta(src, tgt.Turn.ID, kept[len(kept)-1].CreatedAt),
	}
	sess.UpdatedAt = sess.CreatedAt
	if len(turns) > 0 {
		sess.LastTurnID = turns[len(turns)-1].ID
	}

	// From the first attachment on, a failure leaves a session directory (and
	// after the commit a ledger row) that has to go again, as handoff does.
	fail := func(cause error) (ForkResult, error) {
		return ForkResult{}, errors.Join(cause, r.discardFork(ctx, sessionsRoot, newID))
	}
	if err := copyForkFiles(sessionsRoot, sid, newID, entries); err != nil {
		return fail(err)
	}
	if forkHook != nil {
		if err := forkHook("files"); err != nil {
			return fail(err)
		}
	}
	if err := repo.CreateForkedSession(ctx, store.ForkInput{Session: sess, Turns: turns, Entries: entries}); err != nil {
		if store.IsDuplicateKey(err) {
			// Someone else owns the id: its files are not ours to remove.
			return ForkResult{}, fmt.Errorf("create forked session: %w", err)
		}
		return fail(fmt.Errorf("create forked session: %w", err))
	}
	if forkHook != nil {
		if err := forkHook("created"); err != nil {
			return fail(err)
		}
	}
	if _, err := repo.AppendEvent(ctx, store.Event{
		SessionID: newID, Kind: store.EventSession, Name: "fork", Status: eventOK, StartedAt: time.Now(),
		Attrs: map[string]any{"from": sid, "turn": tgt.Turn.ID, "ordinal": tgt.Ordinal, "turns": len(turns), "entries": len(entries)},
	}); err != nil {
		return fail(fmt.Errorf("record fork: %w", err))
	}
	return ForkResult{SessionID: newID, Turns: len(turns), Entries: len(entries)}, nil
}

// forkVisibleEntries returns the entries of the session up to cut, bodies
// restored, without what a fork must not carry: rewind records (the new session
// has nothing to mask, and a copied cut_seq would point at the wrong entries)
// and the entries inside a masked interval. The result is never empty: the
// segment start record always precedes a user turn.
func (r *Runner) forkVisibleEntries(ctx context.Context, sid string, cut int64) ([]store.Entry, error) {
	masked, err := r.maskedRanges(ctx, sid)
	if err != nil {
		return nil, err
	}
	all, err := r.cfg.Repository.ListEntries(ctx, sid, store.EntryFilter{NoHydrate: true})
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	var kept []store.Entry
	for _, e := range all {
		if e.Seq > cut {
			break
		}
		if e.Type == store.EntryRewind || masked.Hidden(e.Seq) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("session %s has no entries before the target", sid)
	}
	if err := r.cfg.Repository.HydrateEntries(ctx, sid, kept); err != nil {
		return nil, fmt.Errorf("read the entries to copy: %w", err)
	}
	for _, e := range kept {
		if e.Stub() {
			return nil, fmt.Errorf("entry %d is still a stub: refusing to copy a body-less entry", e.Seq)
		}
	}
	return kept, nil
}

// freshForkID picks a session id no session and no directory uses yet.
func (r *Runner) freshForkID(ctx context.Context, sessionsRoot string) (string, error) {
	for range maxIDAttempts {
		id := newSessionID()
		if err := validateSessionID(id); err != nil {
			return "", err
		}
		if _, err := r.cfg.Repository.GetSession(ctx, id); !errors.Is(err, store.ErrSessionNotFound) {
			if err != nil {
				return "", err
			}
			continue
		}
		if sessionsRoot != "" {
			if _, err := os.Lstat(filepath.Join(sessionsRoot, id)); err == nil {
				continue
			}
		}
		return id, nil
	}
	return "", errors.New("could not pick a free session id for the fork")
}

// forkTurns builds the turn rows of the new session: one per source turn that
// still has an entry in kept, with a new id, the source's status, stop reason
// and times, and no state_json. The map is old id -> new id.
func (r *Runner) forkTurns(ctx context.Context, newID, sid string, kept []store.Entry) ([]store.Turn, map[string]string, error) {
	used := map[string]bool{}
	for _, e := range kept {
		if e.TurnID != "" {
			used[e.TurnID] = true
		}
	}
	srcTurns, err := r.cfg.Repository.ListTurns(ctx, sid)
	if err != nil {
		return nil, nil, fmt.Errorf("list turns: %w", err)
	}
	var out []store.Turn
	idMap := make(map[string]string, len(used))
	for _, t := range srcTurns {
		if !used[t.ID] {
			continue
		}
		nid := newTurnID()
		idMap[t.ID] = nid
		out = append(out, store.Turn{ID: nid, SessionID: newID, Status: t.Status, StopReason: t.StopReason,
			StartedAt: t.StartedAt, EndedAt: t.EndedAt})
	}
	for id := range used {
		if _, ok := idMap[id]; !ok {
			return nil, nil, fmt.Errorf("entry refers to turn %q that the session does not have", id)
		}
	}
	return out, idMap, nil
}

// forkEntries renumbers kept as 1..n (keeping created_at), swaps the turn ids
// and re-points the seq references of compaction records at the new numbers.
func forkEntries(kept []store.Entry, turnMap map[string]string) ([]store.NewEntry, error) {
	// newSeq maps an old seq to the new seq of the first copied entry at or after
	// it: a compaction's first_kept_seq may name an entry that was masked.
	oldSeqs := make([]int64, len(kept))
	for i, e := range kept {
		oldSeqs[i] = e.Seq
	}
	newSeqAtOrAfter := func(old int64) (int64, bool) {
		i := sort.Search(len(oldSeqs), func(i int) bool { return oldSeqs[i] >= old })
		return int64(i + 1), i < len(oldSeqs)
	}
	out := make([]store.NewEntry, len(kept))
	for i, e := range kept {
		ne := store.NewEntry{TurnID: turnMap[e.TurnID], Type: e.Type, JSON: e.JSON, Seq: int64(i + 1), CreatedAt: e.CreatedAt}
		if e.Type == store.EntryCompaction {
			raw, err := remapCompaction(e.JSON, ne.Seq, newSeqAtOrAfter)
			if err != nil {
				return nil, fmt.Errorf("compaction entry %d: %w", e.Seq, err)
			}
			ne.JSON = raw
		}
		out[i] = ne
	}
	return out, nil
}

// remapCompaction rewrites first_kept_seq of a compaction (or segment) record
// and leaves every other field, Refs included (R6), as stored. 0 stays 0 (a
// start record keeps every message). A clear record's K is its own seq. For the
// others it is the new seq of the first copied entry not older than the old
// value; with none (cannot happen for a record written after what it keeps) it
// falls back to the record's own seq.
func remapCompaction(raw json.RawMessage, own int64, at func(int64) (int64, bool)) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var rec compactionRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	if rec.FirstKeptSeq == 0 {
		return raw, nil
	}
	mapped := own
	if rec.Reason != segmentReasonClear {
		if n, ok := at(rec.FirstKeptSeq); ok {
			mapped = n
		}
	}
	enc, err := json.Marshal(mapped)
	if err != nil {
		return nil, err
	}
	fields["first_kept_seq"] = enc
	return json.Marshal(fields)
}

// forkMeta builds the new session's meta from the whitelist in the file comment.
func forkMeta(src store.Session, targetTurn string, cutAt time.Time) map[string]any {
	meta := map[string]any{
		store.MetaOrigin: store.OriginChat,
		metaForkedFrom:   src.ID,
		metaForkedAtTurn: targetTurn,
	}
	for _, key := range []string{adddir.MetaKey, "model", "provider", metaLoadedTools} {
		if v, ok := src.Meta[key]; ok {
			meta[key] = v
		}
	}
	if taintInherited(src.Meta[metaTaintedAt], cutAt) {
		meta[metaTaintedAt] = src.Meta[metaTaintedAt]
		if v, ok := src.Meta[metaTaintedBy]; ok {
			meta[metaTaintedBy] = v
		}
	}
	return meta
}

// taintInherited reports whether the fork inherits the source's taint: the
// source was tainted no later than the last copied entry was written. A taint
// stamp that cannot be read counts as inherited (the conservative side), and
// the stamp has second precision, so one set in the same second as the cut
// entry is inherited as well.
func taintInherited(stamp any, cutAt time.Time) bool {
	if stamp == nil {
		return false
	}
	s, ok := stamp.(string)
	if !ok {
		return true
	}
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return true
	}
	return !at.After(cutAt)
}

// forkTitle is the source's title plus the localized fork suffix.
func (r *Runner) forkTitle(src store.Session) string {
	title := src.Title
	if title == "" {
		title = defaultTitle(src.ID)
	}
	tr := r.cfg.Translator
	if tr == nil {
		tr = i18n.Fallback()
	}
	return tr.T(forkTitleKey, map[string]any{"title": title})
}

// discardFork removes what a failed fork left: the ledger row (turns, entries
// and events go with it) and the session directory with the copied attachments.
// The errors are returned so the caller reports a leftover instead of hiding it.
func (r *Runner) discardFork(ctx context.Context, sessionsRoot, newID string) error {
	wctx := context.WithoutCancel(ctx)
	var errs []error
	if err := r.cfg.Repository.DeleteSession(wctx, newID); err != nil && !errors.Is(err, store.ErrSessionNotFound) {
		errs = append(errs, fmt.Errorf("remove forked session %s: %w", newID, err))
	}
	if err := r.cfg.Repository.DeleteSessionFiles(newID); err != nil {
		errs = append(errs, err)
	}
	if sessionsRoot != "" {
		// The attachments went to sessionsRoot, which need not be the store's root.
		if err := os.RemoveAll(filepath.Join(sessionsRoot, newID)); err != nil {
			errs = append(errs, fmt.Errorf("remove files of forked session %s: %w", newID, err))
		}
	}
	return errors.Join(errs...)
}
