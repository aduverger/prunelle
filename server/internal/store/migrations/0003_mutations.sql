ALTER TABLE topics ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE representations ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

CREATE TABLE discussions (
    id                TEXT PRIMARY KEY,
    review_id         TEXT NOT NULL REFERENCES reviews(id),
    revision_id       TEXT NOT NULL REFERENCES revisions(id),
    anchor_type       TEXT NOT NULL CHECK (anchor_type IN ('REVIEW', 'TOPIC', 'INTENT', 'CHANGE', 'FINDING', 'EVIDENCE', 'CODE_RANGE', 'DIAGRAM_ELEMENT')),
    topic_id          TEXT REFERENCES topics(id),
    intent_id         TEXT REFERENCES intents(id),
    change_id         TEXT REFERENCES observed_changes(id),
    finding_id        TEXT REFERENCES findings(id),
    evidence_id       TEXT REFERENCES evidence(id),
    representation_id TEXT REFERENCES representations(id),
    element_id        TEXT,
    status            TEXT NOT NULL CHECK (status IN ('OPEN', 'RESOLVED')),
    author_kind       TEXT NOT NULL CHECK (author_kind IN ('HUMAN', 'AGENT')),
    author_id         TEXT NOT NULL,
    version           INTEGER NOT NULL,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    CHECK ((anchor_type = 'TOPIC') = (topic_id IS NOT NULL)),
    CHECK ((anchor_type = 'INTENT') = (intent_id IS NOT NULL)),
    CHECK ((anchor_type = 'CHANGE') = (change_id IS NOT NULL)),
    CHECK ((anchor_type = 'FINDING') = (finding_id IS NOT NULL)),
    CHECK ((anchor_type = 'EVIDENCE') = (evidence_id IS NOT NULL)),
    CHECK ((anchor_type = 'DIAGRAM_ELEMENT') = (representation_id IS NOT NULL)),
    CHECK ((anchor_type = 'DIAGRAM_ELEMENT') = (element_id IS NOT NULL))
);

CREATE TABLE discussion_code_ranges (
    discussion_id TEXT PRIMARY KEY REFERENCES discussions(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    path          TEXT NOT NULL,
    side          TEXT NOT NULL CHECK (side IN ('BASE', 'HEAD')),
    start_line    INTEGER NOT NULL CHECK (start_line >= 1),
    end_line      INTEGER NOT NULL CHECK (end_line >= start_line),
    blob_hash     TEXT NOT NULL REFERENCES blobs(hash),
    selected_text TEXT NOT NULL
);

CREATE TABLE discussion_messages (
    id            TEXT PRIMARY KEY,
    discussion_id TEXT NOT NULL REFERENCES discussions(id),
    author_kind   TEXT NOT NULL CHECK (author_kind IN ('HUMAN', 'AGENT')),
    author_id     TEXT NOT NULL,
    body          TEXT NOT NULL,
    created_at    TEXT NOT NULL
);

CREATE TABLE decisions (
    id          TEXT PRIMARY KEY,
    review_id   TEXT NOT NULL REFERENCES reviews(id),
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    decision    TEXT NOT NULL CHECK (decision IN ('COMMENT', 'APPROVE', 'REQUEST_CHANGES')),
    body        TEXT NOT NULL,
    author_kind TEXT NOT NULL CHECK (author_kind IN ('HUMAN', 'AGENT')),
    author_id   TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE TABLE decision_findings (
    decision_id TEXT NOT NULL REFERENCES decisions(id),
    finding_id  TEXT NOT NULL REFERENCES findings(id),
    PRIMARY KEY (decision_id, finding_id)
);

CREATE TABLE decision_discussions (
    decision_id   TEXT NOT NULL REFERENCES decisions(id),
    discussion_id TEXT NOT NULL REFERENCES discussions(id),
    PRIMARY KEY (decision_id, discussion_id)
);

CREATE TABLE events (
    sequence    INTEGER PRIMARY KEY AUTOINCREMENT,
    type        TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    review_id   TEXT,
    actor_kind  TEXT NOT NULL,
    actor_id    TEXT NOT NULL,
    occurred_at TEXT NOT NULL
);

CREATE TABLE idempotency_keys (
    caller       TEXT NOT NULL,
    key          TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status       INTEGER,
    headers      TEXT,
    body         BLOB,
    created_at   TEXT NOT NULL,
    PRIMARY KEY (caller, key)
);

CREATE INDEX discussions_review ON discussions(review_id, created_at);
CREATE INDEX discussion_messages_discussion ON discussion_messages(discussion_id, created_at);
CREATE INDEX decisions_review ON decisions(review_id, created_at);
CREATE INDEX idempotency_keys_created ON idempotency_keys(created_at);
