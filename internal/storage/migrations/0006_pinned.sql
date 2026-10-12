-- 0006_pinned.sql
-- Pinned core memory (lifecycle-and-graph-retrieval): an observation can
-- be pinned to opt out of time decay and earn a Tier-0 prewarm slot.
--
-- SQLite's ADD COLUMN carries the CHECK for new rows; existing rows default
-- to 0 (unpinned), so ranking behaviour for existing stores is unchanged.

ALTER TABLE observations ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1));

CREATE INDEX IF NOT EXISTS idx_obs_pinned ON observations(pinned);
