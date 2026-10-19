-- Continuity identity is deliberately separate from the legacy project label.
CREATE TABLE session_continuity_bindings (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    workspace_key TEXT NOT NULL,
    workspace_root TEXT NOT NULL,
    harness_kind TEXT NOT NULL,
    host_session_id TEXT NOT NULL DEFAULT '',
    start_key TEXT NOT NULL,
    start_request_hash TEXT NOT NULL,
    UNIQUE(agent_id, workspace_key, harness_kind, start_key)
);
CREATE INDEX idx_continuity_workspace ON session_continuity_bindings(workspace_key, agent_id);

-- A NULL payload is a retained clear marker, not an absent revision.
CREATE TABLE session_checkpoints (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK(revision > 0),
    payload TEXT CHECK(payload IS NULL OR length(CAST(payload AS BLOB)) <= 16384),
    visibility TEXT NOT NULL CHECK(visibility IN ('owner','workspace')),
    updated_at DATETIME NOT NULL,
    last_request_id TEXT NOT NULL,
    last_request_hash TEXT NOT NULL
);
