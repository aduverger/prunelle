DROP VIEW resource_workspaces;

CREATE TABLE repositories_new (
    id TEXT PRIMARY KEY,
    root_path TEXT NOT NULL,
    created_at TEXT NOT NULL,
    legacy_base_ref TEXT
);
INSERT INTO repositories_new SELECT id, root_path, created_at, base_ref FROM repositories;

CREATE TABLE reviews_new (
    id TEXT PRIMARY KEY,
    context_path TEXT,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('OPEN', 'CLOSED')),
    current_revision_id TEXT REFERENCES revisions(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO reviews_new
SELECT r.id, w.root_path, r.title, r.description, r.status, r.current_revision_id, r.created_at, r.updated_at
FROM reviews r JOIN workspaces w ON w.id = r.workspace_id;

CREATE TABLE review_repositories (
    review_id TEXT NOT NULL REFERENCES reviews(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    label TEXT NOT NULL,
    include_untracked INTEGER NOT NULL,
    PRIMARY KEY (review_id, repository_id),
    UNIQUE (review_id, label)
);
INSERT INTO review_repositories
SELECT r.id, p.id, p.label, p.include_untracked
FROM reviews r JOIN repositories p ON p.workspace_id = r.workspace_id;

CREATE TABLE review_runtime_sessions (
    review_id TEXT NOT NULL REFERENCES reviews(id),
    runtime_session_id TEXT NOT NULL REFERENCES runtime_sessions(id),
    created_at TEXT NOT NULL,
    PRIMARY KEY (review_id, runtime_session_id)
);
INSERT INTO review_runtime_sessions
SELECT r.id, a.runtime_session_id, a.created_at
FROM reviews r JOIN workspace_runtime_sessions a ON a.workspace_id = r.workspace_id;

DROP TABLE workspace_runtime_sessions;
DROP TABLE reviews;
DROP TABLE repositories;
ALTER TABLE reviews_new RENAME TO reviews;
ALTER TABLE repositories_new RENAME TO repositories;
DROP TABLE workspaces;

CREATE INDEX reviews_context ON reviews(context_path);
CREATE INDEX review_repositories_repository ON review_repositories(repository_id);
CREATE INDEX review_runtime_sessions_runtime ON review_runtime_sessions(runtime_session_id);

CREATE VIEW resource_reviews AS
SELECT id, id AS review_id FROM reviews
UNION ALL SELECT id, review_id FROM revisions
UNION ALL SELECT id, review_id FROM imports
UNION ALL SELECT id, review_id FROM topics
UNION ALL SELECT id, review_id FROM intents
UNION ALL SELECT c.id, v.review_id FROM observed_changes c JOIN revisions v ON v.id = c.revision_id
UNION ALL SELECT id, review_id FROM findings
UNION ALL SELECT e.id, v.review_id FROM evidence e JOIN revisions v ON v.id = e.revision_id
UNION ALL SELECT a.id, v.review_id FROM attention_signals a JOIN revisions v ON v.id = a.revision_id
UNION ALL SELECT p.id, t.review_id FROM representations p JOIN topics t ON t.id = p.topic_id
UNION ALL SELECT id, review_id FROM discussions
UNION ALL SELECT m.id, d.review_id FROM discussion_messages m JOIN discussions d ON d.id = m.discussion_id
UNION ALL SELECT id, review_id FROM decisions;

CREATE TABLE idempotency_keys_new (
    caller TEXT NOT NULL,
    key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status INTEGER,
    headers TEXT,
    body BLOB,
    created_at TEXT NOT NULL,
    review_id TEXT REFERENCES reviews(id) ON DELETE CASCADE,
    PRIMARY KEY (caller, key)
);
INSERT INTO idempotency_keys_new
SELECT k.caller, k.key, k.request_hash, k.status, k.headers, k.body, k.created_at,
    (SELECT review_id FROM resource_reviews WHERE id = CASE WHEN json_valid(CAST(k.body AS TEXT))
        THEN COALESCE(json_extract(CAST(k.body AS TEXT), '$.id'), json_extract(CAST(k.body AS TEXT), '$.import_id')) END)
FROM idempotency_keys k
WHERE k.status IS NULL OR k.workspace_id IS NULL OR EXISTS (
    SELECT 1 FROM resource_reviews WHERE id = CASE WHEN json_valid(CAST(k.body AS TEXT))
        THEN COALESCE(json_extract(CAST(k.body AS TEXT), '$.id'), json_extract(CAST(k.body AS TEXT), '$.import_id')) END
);

DROP TABLE idempotency_keys;
ALTER TABLE idempotency_keys_new RENAME TO idempotency_keys;
CREATE INDEX idempotency_keys_created ON idempotency_keys(created_at);
CREATE INDEX idempotency_keys_review ON idempotency_keys(review_id);

DELETE FROM repositories WHERE
    NOT EXISTS (SELECT 1 FROM review_repositories WHERE repository_id = repositories.id)
    AND NOT EXISTS (SELECT 1 FROM repo_snapshots WHERE repository_id = repositories.id)
    AND NOT EXISTS (SELECT 1 FROM evidence_code_ranges WHERE repository_id = repositories.id)
    AND NOT EXISTS (SELECT 1 FROM discussion_code_ranges WHERE repository_id = repositories.id);
