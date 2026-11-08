CREATE TABLE workspaces (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE repositories (
    id                TEXT PRIMARY KEY,
    workspace_id      TEXT NOT NULL REFERENCES workspaces(id),
    label             TEXT NOT NULL,
    root_path         TEXT NOT NULL,
    base_ref          TEXT NOT NULL,
    include_untracked INTEGER NOT NULL,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    UNIQUE (workspace_id, label),
    UNIQUE (workspace_id, root_path)
);

CREATE TABLE reviews (
    id                  TEXT PRIMARY KEY,
    workspace_id        TEXT NOT NULL REFERENCES workspaces(id),
    title               TEXT NOT NULL,
    description         TEXT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('OPEN', 'CLOSED')),
    current_revision_id TEXT REFERENCES revisions(id),
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

CREATE TABLE revisions (
    id          TEXT PRIMARY KEY,
    review_id   TEXT NOT NULL REFERENCES reviews(id),
    ordinal     INTEGER NOT NULL,
    captured_at TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('CONSISTENT', 'INCONSISTENT')),
    UNIQUE (review_id, ordinal)
);

CREATE TABLE blobs (
    hash    TEXT PRIMARY KEY,
    size    INTEGER NOT NULL,
    content BLOB NOT NULL
);

CREATE TABLE repo_snapshots (
    id              TEXT PRIMARY KEY,
    revision_id     TEXT NOT NULL REFERENCES revisions(id),
    repository_id   TEXT NOT NULL REFERENCES repositories(id),
    base_ref        TEXT NOT NULL,
    base_sha        TEXT NOT NULL,
    head_sha        TEXT NOT NULL,
    branch          TEXT NOT NULL,
    staged_count    INTEGER NOT NULL,
    unstaged_count  INTEGER NOT NULL,
    untracked_count INTEGER NOT NULL,
    fingerprint     TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('CONSISTENT', 'INCONSISTENT')),
    warnings        TEXT NOT NULL,
    UNIQUE (revision_id, repository_id)
);

CREATE TABLE snapshot_files (
    snapshot_id     TEXT NOT NULL REFERENCES repo_snapshots(id),
    path            TEXT NOT NULL,
    old_path        TEXT,
    status          TEXT NOT NULL CHECK (status IN ('ADDED', 'MODIFIED', 'DELETED', 'RENAMED', 'TYPE_CHANGED', 'UNTRACKED')),
    base_mode       TEXT,
    head_mode       TEXT,
    base_oid        TEXT,
    head_oid        TEXT,
    base_blob       TEXT REFERENCES blobs(hash),
    head_blob       TEXT REFERENCES blobs(hash),
    binary          INTEGER NOT NULL,
    content_omitted TEXT,
    PRIMARY KEY (snapshot_id, path)
);

CREATE TABLE imports (
    id          TEXT PRIMARY KEY,
    review_id   TEXT NOT NULL REFERENCES reviews(id),
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('HUMAN', 'AGENT')),
    author_id   TEXT NOT NULL,
    imported_at TEXT NOT NULL
);

CREATE TABLE topics (
    id         TEXT PRIMARY KEY,
    review_id  TEXT NOT NULL REFERENCES reviews(id),
    parent_id  TEXT REFERENCES topics(id),
    title      TEXT NOT NULL,
    summary    TEXT NOT NULL,
    origin     TEXT NOT NULL CHECK (origin IN ('MANUAL', 'IMPORTED', 'GENERATED')),
    state      TEXT NOT NULL CHECK (state IN ('ACTIVE', 'OBSOLETE')),
    position   INTEGER NOT NULL,
    import_id  TEXT REFERENCES imports(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE intents (
    id                  TEXT PRIMARY KEY,
    review_id           TEXT NOT NULL REFERENCES reviews(id),
    kind                TEXT NOT NULL CHECK (kind IN ('BEHAVIOR', 'CONSTRAINT')),
    statement           TEXT NOT NULL,
    source              TEXT NOT NULL,
    acceptance_criteria TEXT,
    provenance          TEXT NOT NULL CHECK (provenance IN ('MEASURED', 'OBSERVED', 'INFERRED', 'UNKNOWN')),
    import_id           TEXT REFERENCES imports(id),
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

CREATE TABLE observed_changes (
    id                  TEXT PRIMARY KEY,
    revision_id         TEXT NOT NULL REFERENCES revisions(id),
    kind                TEXT NOT NULL CHECK (kind IN ('BEHAVIOR', 'INTERFACE', 'DATA', 'DEPENDENCY', 'CONFIGURATION', 'UI', 'OTHER')),
    summary             TEXT NOT NULL,
    before_claim        TEXT NOT NULL,
    after_claim         TEXT NOT NULL,
    components          TEXT NOT NULL,
    verification_status TEXT NOT NULL CHECK (verification_status IN ('UNVERIFIED', 'SUPPORTED', 'CONTRADICTED')),
    provenance          TEXT NOT NULL CHECK (provenance IN ('MEASURED', 'OBSERVED', 'INFERRED', 'UNKNOWN')),
    import_id           TEXT REFERENCES imports(id),
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

CREATE TABLE findings (
    id                     TEXT PRIMARY KEY,
    review_id              TEXT NOT NULL REFERENCES reviews(id),
    first_seen_revision_id TEXT NOT NULL REFERENCES revisions(id),
    kind                   TEXT NOT NULL CHECK (kind IN ('CORRECTNESS', 'DESIGN', 'SECURITY', 'PERFORMANCE', 'TEST_GAP', 'OTHER')),
    severity               TEXT NOT NULL CHECK (severity IN ('HIGH', 'MEDIUM', 'LOW', 'INFO')),
    status                 TEXT NOT NULL CHECK (status IN ('OPEN', 'DISMISSED', 'RESOLVED')),
    title                  TEXT NOT NULL,
    rationale              TEXT NOT NULL,
    provenance             TEXT NOT NULL CHECK (provenance IN ('MEASURED', 'OBSERVED', 'INFERRED', 'UNKNOWN')),
    import_id              TEXT REFERENCES imports(id),
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL
);

CREATE TABLE evidence (
    id                TEXT PRIMARY KEY,
    revision_id       TEXT NOT NULL REFERENCES revisions(id),
    kind              TEXT NOT NULL CHECK (kind IN ('CODE_RANGE', 'TEST_RESULT', 'SPECIFICATION', 'ANALYSIS_OUTPUT', 'ARTIFACT')),
    title             TEXT NOT NULL,
    content           TEXT,
    source            TEXT NOT NULL,
    collection_method TEXT NOT NULL CHECK (collection_method IN ('CAPTURED', 'REPORTED')),
    provenance        TEXT NOT NULL CHECK (provenance IN ('MEASURED', 'OBSERVED', 'INFERRED', 'UNKNOWN')),
    import_id         TEXT REFERENCES imports(id),
    created_at        TEXT NOT NULL
);

CREATE TABLE evidence_code_ranges (
    evidence_id   TEXT PRIMARY KEY REFERENCES evidence(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    path          TEXT NOT NULL,
    side          TEXT NOT NULL CHECK (side IN ('BASE', 'HEAD')),
    start_line    INTEGER NOT NULL CHECK (start_line >= 1),
    end_line      INTEGER NOT NULL CHECK (end_line >= start_line),
    blob_hash     TEXT NOT NULL REFERENCES blobs(hash),
    selected_text TEXT NOT NULL
);

CREATE TABLE attention_signals (
    id          TEXT PRIMARY KEY,
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    topic_id    TEXT REFERENCES topics(id),
    finding_id  TEXT REFERENCES findings(id),
    change_id   TEXT REFERENCES observed_changes(id),
    priority    TEXT NOT NULL CHECK (priority IN ('HIGH', 'MEDIUM', 'LOW', 'UNKNOWN')),
    rationale   TEXT NOT NULL,
    uncertainty TEXT NOT NULL,
    source      TEXT NOT NULL,
    provenance  TEXT NOT NULL CHECK (provenance IN ('MEASURED', 'OBSERVED', 'INFERRED', 'UNKNOWN')),
    import_id   TEXT REFERENCES imports(id),
    created_at  TEXT NOT NULL,
    CHECK ((topic_id IS NOT NULL) + (finding_id IS NOT NULL) + (change_id IS NOT NULL) = 1)
);

CREATE TABLE representations (
    id          TEXT PRIMARY KEY,
    topic_id    TEXT NOT NULL REFERENCES topics(id),
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    type        TEXT NOT NULL CHECK (type IN ('TEXT', 'DIAGRAM', 'CODE_DIFF', 'EVIDENCE_LIST')),
    title       TEXT NOT NULL,
    config      TEXT NOT NULL,
    origin      TEXT NOT NULL CHECK (origin IN ('MANUAL', 'IMPORTED', 'GENERATED')),
    position    INTEGER NOT NULL,
    import_id   TEXT REFERENCES imports(id),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE topic_intents (
    topic_id  TEXT NOT NULL REFERENCES topics(id),
    intent_id TEXT NOT NULL REFERENCES intents(id),
    PRIMARY KEY (topic_id, intent_id)
);

CREATE TABLE topic_changes (
    topic_id  TEXT NOT NULL REFERENCES topics(id),
    change_id TEXT NOT NULL REFERENCES observed_changes(id),
    PRIMARY KEY (topic_id, change_id)
);

CREATE TABLE topic_findings (
    topic_id   TEXT NOT NULL REFERENCES topics(id),
    finding_id TEXT NOT NULL REFERENCES findings(id),
    PRIMARY KEY (topic_id, finding_id)
);

CREATE TABLE topic_evidence (
    topic_id    TEXT NOT NULL REFERENCES topics(id),
    evidence_id TEXT NOT NULL REFERENCES evidence(id),
    PRIMARY KEY (topic_id, evidence_id)
);

CREATE TABLE intent_evidence (
    intent_id   TEXT NOT NULL REFERENCES intents(id),
    evidence_id TEXT NOT NULL REFERENCES evidence(id),
    PRIMARY KEY (intent_id, evidence_id)
);

CREATE TABLE change_evidence (
    change_id   TEXT NOT NULL REFERENCES observed_changes(id),
    evidence_id TEXT NOT NULL REFERENCES evidence(id),
    PRIMARY KEY (change_id, evidence_id)
);

CREATE TABLE finding_evidence (
    finding_id  TEXT NOT NULL REFERENCES findings(id),
    evidence_id TEXT NOT NULL REFERENCES evidence(id),
    PRIMARY KEY (finding_id, evidence_id)
);

CREATE TABLE attention_evidence (
    attention_id TEXT NOT NULL REFERENCES attention_signals(id),
    evidence_id  TEXT NOT NULL REFERENCES evidence(id),
    PRIMARY KEY (attention_id, evidence_id)
);

CREATE INDEX repositories_workspace ON repositories(workspace_id);
CREATE INDEX reviews_workspace ON reviews(workspace_id);
CREATE INDEX revisions_review ON revisions(review_id);
CREATE INDEX topics_review ON topics(review_id, parent_id, position);
CREATE INDEX intents_review ON intents(review_id);
CREATE INDEX changes_revision ON observed_changes(revision_id);
CREATE INDEX findings_review ON findings(review_id);
CREATE INDEX evidence_revision ON evidence(revision_id);
CREATE INDEX attention_revision ON attention_signals(revision_id);
CREATE INDEX representations_topic ON representations(topic_id, position);
CREATE INDEX code_ranges_file ON evidence_code_ranges(repository_id, path);
