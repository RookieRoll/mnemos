-- One durable lease and cycle marker is sufficient for safe maintenance
-- takeover. Per-run history and diagnostic prose are intentionally absent.
CREATE TABLE maintenance_coordinator (
    singleton      INTEGER PRIMARY KEY CHECK (singleton = 1),
    last_success   REAL,
    owner_token    TEXT,
    lease_until    REAL,
    cycle_id       TEXT,
    decay_applied  INTEGER NOT NULL DEFAULT 0 CHECK (decay_applied IN (0, 1)),
    CHECK ((owner_token IS NULL) = (lease_until IS NULL)),
    CHECK (cycle_id IS NOT NULL OR decay_applied = 0)
);

INSERT INTO maintenance_coordinator(singleton, decay_applied) VALUES (1, 0);
