-- Migrate:UP
-- Tool body files (design 2026-10-19, tool-output-files): a tool entry whose
-- body moved to sessions/<sid>/tools.jsonl keeps a short stub in entries.json
-- and the record location in this column ("<off>,<len>,<sha256>"). NULL means
-- the row's JSON is the body itself, which is every row written before this
-- version.
--
-- One ALTER only: adding a nullable column is O(1) in SQLite, no table is
-- rewritten and no existing row is read (a 1 GB ledger must open instantly,
-- see v0.31.0).
ALTER TABLE entries ADD COLUMN body_ref TEXT;

-- Migrate:DOWN
ALTER TABLE entries DROP COLUMN body_ref;
