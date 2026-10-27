-- Migrate:UP
-- Every memory entry gets a stable identity independent of its derived id.
-- The Go migration runner fills existing rows with random UUIDs after this
-- column is added, because SQLite has no portable UUID generator.
ALTER TABLE memory_meta ADD COLUMN uuid TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_meta_uuid ON memory_meta(uuid) WHERE uuid IS NOT NULL;

-- Migrate:DOWN
DROP INDEX IF EXISTS idx_memory_meta_uuid;
-- SQLite cannot drop a column on all supported versions; this migration is
-- forward-only like the other memory metadata changes.
