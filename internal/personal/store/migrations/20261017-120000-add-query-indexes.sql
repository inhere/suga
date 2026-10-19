-- Migrate:UP
-- Indexes for the store's read paths (issue suga-q3jy). Chosen by EXPLAIN
-- QUERY PLAN and measured on a store-shaped table (60k entries, 400k events):
--
--   entries(type): HasSearchableEntries scans ftsSourceSelect's
--     "FROM entries WHERE type IN (...)" on every search of an empty scope; the
--     plan went from SCAN entries to SEARCH entries USING INDEX idx_entries_type.
--     LatestEntry (session_id + type) keeps using the (session_id, seq)
--     primary key and gains nothing, which is why no composite is added.
--
--   events(turn_id): 'suga trace --turn' filters events by turn; the plan
--     changed from idx_events_session + sort to idx_events_turn (3.6ms -> 0ms
--     on the measured table).
--
-- NOT added: events(kind). No query filters by kind alone, and on the measured
-- table the planner picked it for SumUsage (per session: 3.6ms -> 72ms) and for
-- the global report (221ms -> 432ms), because a kind value is not selective
-- and a non-covering index costs a random read per row. A (kind, session_id)
-- variant made the global report 1.95s, so it stays out too.
--
-- NOT indexed: entries(type) is created on an existing ledger, so the first
-- Open after the upgrade writes an index over every entry. It is a single
-- sequential scan plus a sort, not a per-row subquery: measured at under a
-- second for 60k entries (TestQueryIndexMigrationStaysFastOnALargeLedger).
CREATE INDEX IF NOT EXISTS idx_entries_type ON entries(type);
CREATE INDEX IF NOT EXISTS idx_events_turn ON events(turn_id);

-- Migrate:DOWN
DROP INDEX IF EXISTS idx_events_turn;
DROP INDEX IF EXISTS idx_entries_type;
