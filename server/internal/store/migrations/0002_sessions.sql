CREATE TABLE session_backends (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL CHECK (kind IN ('REX', 'HERDR', 'MANAGED')),
    config     TEXT NOT NULL,
    enabled    INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE runtime_sessions (
    id               TEXT PRIMARY KEY,
    backend_id       TEXT NOT NULL REFERENCES session_backends(id),
    external_id      TEXT NOT NULL,
    state            TEXT NOT NULL CHECK (state IN ('ONLINE', 'OFFLINE', 'STOPPED', 'UNKNOWN')),
    label            TEXT NOT NULL,
    cwd              TEXT,
    process          TEXT,
    local            INTEGER NOT NULL,
    harness_hint     TEXT,
    metadata         TEXT NOT NULL,
    capabilities     TEXT NOT NULL,
    first_seen_at    TEXT NOT NULL,
    last_seen_at     TEXT NOT NULL,
    state_changed_at TEXT NOT NULL,
    UNIQUE (backend_id, external_id)
);

CREATE TABLE workspace_runtime_sessions (
    workspace_id       TEXT NOT NULL REFERENCES workspaces(id),
    runtime_session_id TEXT NOT NULL REFERENCES runtime_sessions(id),
    created_at         TEXT NOT NULL,
    PRIMARY KEY (workspace_id, runtime_session_id)
);

CREATE INDEX runtime_sessions_backend ON runtime_sessions(backend_id);
CREATE INDEX workspace_runtime_sessions_runtime ON workspace_runtime_sessions(runtime_session_id);
