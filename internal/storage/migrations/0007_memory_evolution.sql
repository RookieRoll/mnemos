-- Topic keys group assertions; they do not select a single current version.
ALTER TABLE observations ADD COLUMN topic_key TEXT CHECK(topic_key IS NULL OR length(CAST(topic_key AS BLOB)) <= 256);
CREATE INDEX idx_obs_topic ON observations(agent_id, project, topic_key);

-- Preserve the original normalized replacement request after mutable lifecycle
-- fields change, without a separate operation ledger or changing legacy hashes.
ALTER TABLE observation_links ADD COLUMN replacement_hash TEXT;

CREATE TABLE observation_evidence (
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    evidence_key TEXT NOT NULL CHECK(length(evidence_key) = 64 AND evidence_key NOT GLOB '*[^0-9a-f]*'),
    locator TEXT NOT NULL CHECK(length(CAST(locator AS BLOB)) BETWEEN 1 AND 2048),
    relation TEXT NOT NULL CHECK(relation IN ('supports','refutes')),
    revision TEXT NOT NULL DEFAULT '' CHECK(length(CAST(revision AS BLOB)) <= 256),
    selector TEXT NOT NULL DEFAULT '' CHECK(length(CAST(selector AS BLOB)) <= 512),
    fingerprint TEXT NOT NULL DEFAULT '' CHECK(fingerprint = '' OR (length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*')),
    status TEXT NOT NULL DEFAULT 'unverified' CHECK(status IN ('unverified','matched','changed','unavailable')),
    checked_at TEXT,
    PRIMARY KEY(observation_id,evidence_key)
);

CREATE TRIGGER observation_evidence_bound BEFORE INSERT ON observation_evidence
WHEN NOT EXISTS(SELECT 1 FROM observation_evidence WHERE observation_id=NEW.observation_id AND evidence_key=NEW.evidence_key)
 AND (SELECT count(*) FROM observation_evidence WHERE observation_id=NEW.observation_id) >= 16
BEGIN SELECT RAISE(ABORT,'evidence reference limit exceeded'); END;
