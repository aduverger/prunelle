ALTER TABLE agent_sessions ADD COLUMN queue_paused INTEGER NOT NULL DEFAULT 0 CHECK (queue_paused IN (0,1));
ALTER TABLE agent_sessions ADD COLUMN queue_version INTEGER NOT NULL DEFAULT 1;
UPDATE agent_sessions SET queue_paused=1 WHERE EXISTS (SELECT 1 FROM agent_invocations WHERE agent_session_id=agent_sessions.id AND status='UNKNOWN');

CREATE TABLE queued_agent_invocations (
    id TEXT PRIMARY KEY,
    review_id TEXT NOT NULL REFERENCES reviews(id),
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    agent_session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    connection_id TEXT NOT NULL,
    intent TEXT NOT NULL CHECK (intent IN ('DISCUSS','PREPARE_REVIEW','IMPLEMENT')),
    status TEXT NOT NULL CHECK (status IN ('QUEUED','PENDING','RUNNING','SUCCEEDED','FAILED','UNKNOWN','CANCELLED')),
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
    updated_at TEXT NOT NULL,
    CHECK (status != 'QUEUED' OR (intent = 'DISCUSS' AND connection_id = ''))
);
INSERT INTO queued_agent_invocations SELECT * FROM agent_invocations ORDER BY rowid;
CREATE TABLE queued_agent_invocation_replies (
    invocation_id TEXT PRIMARY KEY REFERENCES queued_agent_invocations(id) ON DELETE CASCADE,
    message_id TEXT NOT NULL UNIQUE REFERENCES discussion_messages(id)
);
INSERT INTO queued_agent_invocation_replies SELECT * FROM agent_invocation_replies;
DROP VIEW resource_reviews;
DROP TABLE agent_invocation_replies;
DROP TABLE agent_invocations;
ALTER TABLE queued_agent_invocations RENAME TO agent_invocations;
ALTER TABLE queued_agent_invocation_replies RENAME TO agent_invocation_replies;
CREATE INDEX agent_invocations_review ON agent_invocations(review_id, created_at);
CREATE INDEX agent_invocations_queue ON agent_invocations(agent_session_id, status);

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
