CREATE TABLE agent_sessions (
    id TEXT PRIMARY KEY,
    external_id TEXT NOT NULL UNIQUE,
    cwd TEXT NOT NULL,
    version TEXT NOT NULL,
    local INTEGER NOT NULL CHECK (local IN (0,1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE review_agent_bindings (
    review_id TEXT PRIMARY KEY REFERENCES reviews(id) ON DELETE CASCADE,
    agent_session_id TEXT REFERENCES agent_sessions(id),
    mode TEXT NOT NULL CHECK (mode IN ('AUTO','MANUAL','DISCONNECTED')),
    version INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((mode = 'MANUAL' AND agent_session_id IS NOT NULL) OR mode = 'AUTO' OR (mode = 'DISCONNECTED' AND agent_session_id IS NULL))
);

CREATE TABLE agent_invocations (
    id TEXT PRIMARY KEY,
    review_id TEXT NOT NULL REFERENCES reviews(id),
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    agent_session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    connection_id TEXT NOT NULL,
    intent TEXT NOT NULL CHECK (intent IN ('DISCUSS','PREPARE_REVIEW','IMPLEMENT')),
    status TEXT NOT NULL CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),
    discussion_id TEXT REFERENCES discussions(id),
    message_id TEXT REFERENCES discussion_messages(id),
    decision_id TEXT REFERENCES decisions(id),
    prompt TEXT NOT NULL,
    author_kind TEXT NOT NULL,
    author_id TEXT NOT NULL,
    login TEXT NOT NULL,
    model TEXT NOT NULL,
    context_id TEXT NOT NULL,
    input_fingerprint TEXT NOT NULL,
    result TEXT,
    result_hash TEXT,
    error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX agent_invocations_review ON agent_invocations(review_id, created_at);

CREATE TABLE agent_invocation_replies (
    invocation_id TEXT PRIMARY KEY REFERENCES agent_invocations(id) ON DELETE CASCADE,
    message_id TEXT NOT NULL UNIQUE REFERENCES discussion_messages(id)
);

DROP VIEW resource_reviews;
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
UNION ALL SELECT id, review_id FROM decisions
UNION ALL SELECT id, review_id FROM agent_invocations
UNION ALL SELECT id, NULL FROM agent_sessions;

DELETE FROM idempotency_keys WHERE status IS NOT NULL AND
    (CASE WHEN json_valid(CAST(body AS TEXT)) THEN json_extract(CAST(body AS TEXT), '$.id') END)
    IN (SELECT id FROM runtime_sessions UNION SELECT id FROM session_backends);
DROP TABLE review_runtime_sessions;
DROP TABLE runtime_sessions;
DROP TABLE session_backends;
