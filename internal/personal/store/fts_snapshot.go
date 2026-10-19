package store

import (
	"context"
	"fmt"
	"time"
)

const (
	searchSnapshotTTL    = 10 * time.Minute
	maxSearchSnapshots   = 64
	searchSnapshotNotice = "search results exceed 500 hits; showing the first 500; narrow the query, time window, kind or scope"
)

type searchSnapshot struct {
	id          string
	fingerprint searchFingerprint
	hits        []EntryHit
	createdAt   time.Time
	lastUsedAt  time.Time
}

// SearchEntriesPage returns one bounded page from a stable process-local
// snapshot. The SQL runs only when Cursor is empty or its snapshot is stale;
// valid continuation cursors slice the stored hits and preserve their original
// snippets and ordering.
func (s *Store) SearchEntriesPage(ctx context.Context, q EntrySearch) (SearchResult, error) {
	pageLimit := q.Limit
	if pageLimit <= 0 {
		pageLimit = defaultSearchLimit
	}
	pageLimit = min(pageLimit, maxSearchLimit)

	fingerprint, err := fingerprintEntrySearch(q)
	if err != nil {
		return SearchResult{}, err
	}
	cursor, notice := parseSearchCursor(q.Cursor)
	if q.Cursor != "" && notice == "" {
		if snapshot, ok := s.loadSearchSnapshot(cursor.SnapshotID, fingerprint, cursor.Offset); ok {
			return pageSearchSnapshot(snapshot, cursor.Offset, pageLimit)
		}
		notice = searchCursorRestartNote
	}

	// A snapshot is built from one query with a single extra row. This keeps the
	// 100-row page limit independent from the 500-row bounded snapshot and lets
	// us explain truncation without claiming that the result set is complete.
	hits, err := s.searchEntries(ctx, q, maxSearchSnapshotHits+1)
	if err != nil {
		return SearchResult{}, err
	}
	truncated := len(hits) > maxSearchSnapshotHits
	if truncated {
		hits = hits[:maxSearchSnapshotHits]
	}
	now := time.Now()
	snapshot := &searchSnapshot{
		fingerprint: fingerprint,
		hits:        append([]EntryHit(nil), hits...),
		createdAt:   now,
		lastUsedAt:  now,
	}
	s.storeSearchSnapshot(snapshot)
	first, err := pageSearchSnapshot(snapshot, 0, pageLimit)
	if err != nil {
		return SearchResult{}, err
	}
	first.Notice = notice
	if truncated {
		first.Notice = joinSearchNotice(first.Notice, searchSnapshotNotice)
	}
	// While the maintenance command has not shrunk the historical tool rows,
	// some hits are full-text matches that the shrunk index would not have. The
	// note is attached once per snapshot (not to a continuation page) and is
	// memoized in the store.
	if note := s.noteShrinkPending(ctx); note != "" {
		first.Notice = joinSearchNotice(first.Notice, note)
	}
	return first, nil
}

func (s *Store) loadSearchSnapshot(id string, fingerprint searchFingerprint, offset int) (*searchSnapshot, bool) {
	now := time.Now()
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if s.searchSnapshots == nil {
		return nil, false
	}
	snapshot, ok := s.searchSnapshots[id]
	if !ok || now.Sub(snapshot.createdAt) >= searchSnapshotTTL || snapshot.fingerprint != fingerprint || offset > len(snapshot.hits) {
		if ok && now.Sub(snapshot.createdAt) >= searchSnapshotTTL {
			delete(s.searchSnapshots, id)
		}
		return nil, false
	}
	snapshot.lastUsedAt = now
	return snapshot, true
}

func (s *Store) storeSearchSnapshot(snapshot *searchSnapshot) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if s.searchSnapshots == nil {
		s.searchSnapshots = make(map[string]*searchSnapshot)
	}
	s.searchSnapshotN++
	snapshot.id = fmt.Sprintf("s%x", s.searchSnapshotN)
	for len(s.searchSnapshots) >= maxSearchSnapshots {
		s.evictOldestSearchSnapshot()
	}
	s.searchSnapshots[snapshot.id] = snapshot
}

func (s *Store) evictOldestSearchSnapshot() {
	var oldestID string
	var oldest time.Time
	for id, snapshot := range s.searchSnapshots {
		if oldestID == "" || snapshot.lastUsedAt.Before(oldest) ||
			(snapshot.lastUsedAt.Equal(oldest) && id < oldestID) {
			oldestID, oldest = id, snapshot.lastUsedAt
		}
	}
	if oldestID != "" {
		delete(s.searchSnapshots, oldestID)
	}
}

func pageSearchSnapshot(snapshot *searchSnapshot, offset, limit int) (SearchResult, error) {
	if offset < 0 || offset > len(snapshot.hits) {
		return SearchResult{}, fmt.Errorf("search snapshot offset %d is out of range", offset)
	}
	end := min(offset+limit, len(snapshot.hits))
	result := SearchResult{Hits: append([]EntryHit(nil), snapshot.hits[offset:end]...)}
	if end < len(snapshot.hits) {
		cursor, err := encodeSearchCursor(searchCursor{SnapshotID: snapshot.id, Offset: end})
		if err != nil {
			return SearchResult{}, err
		}
		result.NextCursor = cursor
	}
	return result, nil
}

func joinSearchNotice(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + "; " + second
}
