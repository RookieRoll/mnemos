-- 0012_observation_near_dups.sql
-- Near-duplicate save folding (dedup-and-session-guard D1-D3).
--
-- The normalized hash is a derived, additive key: content_hash keeps its
-- original definition and value, stored content is never rewritten, and
-- existing rows keep content_hash_normalized = NULL. NULL therefore means
-- "written before this behaviour existed" and never matches a near-duplicate
-- rule, which is the no-backfill guarantee stated in the spec delta.
ALTER TABLE observations ADD COLUMN content_hash_normalized TEXT;

-- Rule D2.2: normalised-identical live row in the same (agent, project).
CREATE INDEX IF NOT EXISTS idx_obs_agent_project_normalized
    ON observations(agent_id, project, content_hash_normalized);

-- Rule D2.3: same session and title inside the window.
CREATE INDEX IF NOT EXISTS idx_obs_session_title
    ON observations(session_id, title, created_at);

-- Provenance for a folded save. incoming_content holds the merged-away body
-- verbatim (not truncated, not re-normalized) so a merge is auditable and can
-- be reversed by saving that text again. reason names the rule that folded it.
-- This table is written once per merge and read only by audit tooling: it never
-- feeds search, ranking, graph expansion, or prewarm, so a dedup trace cannot
-- move a score.
CREATE TABLE observation_near_dups (
    survivor_id      TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    incoming_content TEXT NOT NULL,
    incoming_hash    TEXT NOT NULL,
    normalized_hash  TEXT NOT NULL,
    reason           TEXT NOT NULL CHECK (reason IN ('content_hash', 'session_title')),
    agent_id         TEXT NOT NULL,
    project          TEXT,
    session_id       TEXT,
    title            TEXT NOT NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_near_dups_survivor ON observation_near_dups(survivor_id);
