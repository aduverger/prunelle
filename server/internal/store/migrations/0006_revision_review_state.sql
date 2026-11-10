ALTER TABLE findings ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

CREATE TABLE revision_review_states (
    revision_id TEXT PRIMARY KEY REFERENCES revisions(id) ON DELETE CASCADE
);

CREATE TABLE revision_topics (
    view_revision_id TEXT NOT NULL REFERENCES revision_review_states(revision_id) ON DELETE CASCADE,
    id TEXT NOT NULL REFERENCES topics(id),
    review_id TEXT NOT NULL REFERENCES reviews(id),
    parent_id TEXT REFERENCES topics(id),
    title TEXT NOT NULL,
    summary TEXT NOT NULL,
    origin TEXT NOT NULL,
    state TEXT NOT NULL,
    position INTEGER NOT NULL,
    author_kind TEXT NOT NULL,
    author_id TEXT NOT NULL,
    import_id TEXT REFERENCES imports(id),
    version INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (view_revision_id, id)
);

CREATE TABLE revision_topic_intents (
    view_revision_id TEXT NOT NULL,
    topic_id TEXT NOT NULL,
    intent_id TEXT NOT NULL REFERENCES intents(id),
    PRIMARY KEY (view_revision_id, topic_id, intent_id),
    FOREIGN KEY (view_revision_id, topic_id) REFERENCES revision_topics(view_revision_id, id) ON DELETE CASCADE
);
CREATE TABLE revision_topic_changes (
    view_revision_id TEXT NOT NULL,
    topic_id TEXT NOT NULL,
    change_id TEXT NOT NULL REFERENCES observed_changes(id),
    PRIMARY KEY (view_revision_id, topic_id, change_id),
    FOREIGN KEY (view_revision_id, topic_id) REFERENCES revision_topics(view_revision_id, id) ON DELETE CASCADE
);
CREATE TABLE revision_topic_findings (
    view_revision_id TEXT NOT NULL,
    topic_id TEXT NOT NULL,
    finding_id TEXT NOT NULL REFERENCES findings(id),
    PRIMARY KEY (view_revision_id, topic_id, finding_id),
    FOREIGN KEY (view_revision_id, topic_id) REFERENCES revision_topics(view_revision_id, id) ON DELETE CASCADE
);
CREATE TABLE revision_topic_evidence (
    view_revision_id TEXT NOT NULL,
    topic_id TEXT NOT NULL,
    evidence_id TEXT NOT NULL REFERENCES evidence(id),
    PRIMARY KEY (view_revision_id, topic_id, evidence_id),
    FOREIGN KEY (view_revision_id, topic_id) REFERENCES revision_topics(view_revision_id, id) ON DELETE CASCADE
);

CREATE TABLE revision_findings (
    view_revision_id TEXT NOT NULL REFERENCES revision_review_states(revision_id) ON DELETE CASCADE,
    id TEXT NOT NULL REFERENCES findings(id),
    review_id TEXT NOT NULL REFERENCES reviews(id),
    first_seen_revision_id TEXT NOT NULL REFERENCES revisions(id),
    kind TEXT NOT NULL,
    severity TEXT NOT NULL,
    status TEXT NOT NULL,
    title TEXT NOT NULL,
    rationale TEXT NOT NULL,
    provenance TEXT NOT NULL,
    author_kind TEXT NOT NULL,
    author_id TEXT NOT NULL,
    import_id TEXT REFERENCES imports(id),
    version INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (view_revision_id, id)
);
CREATE TABLE revision_finding_evidence (
    view_revision_id TEXT NOT NULL,
    finding_id TEXT NOT NULL,
    evidence_id TEXT NOT NULL REFERENCES evidence(id),
    PRIMARY KEY (view_revision_id, finding_id, evidence_id),
    FOREIGN KEY (view_revision_id, finding_id) REFERENCES revision_findings(view_revision_id, id) ON DELETE CASCADE
);

CREATE TABLE revision_representations (
    view_revision_id TEXT NOT NULL REFERENCES revision_review_states(revision_id) ON DELETE CASCADE,
    id TEXT NOT NULL REFERENCES representations(id),
    topic_id TEXT NOT NULL REFERENCES topics(id),
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    config TEXT NOT NULL,
    origin TEXT NOT NULL,
    position INTEGER NOT NULL,
    author_kind TEXT NOT NULL,
    author_id TEXT NOT NULL,
    import_id TEXT REFERENCES imports(id),
    version INTEGER NOT NULL,
    PRIMARY KEY (view_revision_id, id)
);

CREATE TABLE revision_intents (
    view_revision_id TEXT NOT NULL REFERENCES revision_review_states(revision_id) ON DELETE CASCADE,
    id TEXT NOT NULL REFERENCES intents(id),
    PRIMARY KEY (view_revision_id, id)
);
CREATE TABLE revision_changes (
    view_revision_id TEXT NOT NULL REFERENCES revision_review_states(revision_id) ON DELETE CASCADE,
    id TEXT NOT NULL REFERENCES observed_changes(id),
    PRIMARY KEY (view_revision_id, id)
);
CREATE TABLE revision_evidence (
    view_revision_id TEXT NOT NULL REFERENCES revision_review_states(revision_id) ON DELETE CASCADE,
    id TEXT NOT NULL REFERENCES evidence(id),
    PRIMARY KEY (view_revision_id, id)
);
CREATE TABLE revision_attention (
    view_revision_id TEXT NOT NULL REFERENCES revision_review_states(revision_id) ON DELETE CASCADE,
    id TEXT NOT NULL REFERENCES attention_signals(id),
    PRIMARY KEY (view_revision_id, id)
);

CREATE TABLE source_topic_assignments (
    snapshot_id TEXT NOT NULL,
    path TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('DERIVED', 'EXPLICIT')),
    version INTEGER NOT NULL,
    PRIMARY KEY (snapshot_id, path),
    FOREIGN KEY (snapshot_id, path) REFERENCES snapshot_files(snapshot_id, path) ON DELETE CASCADE
);
CREATE TABLE source_topic_assignment_topics (
    snapshot_id TEXT NOT NULL,
    path TEXT NOT NULL,
    topic_id TEXT NOT NULL REFERENCES topics(id),
    PRIMARY KEY (snapshot_id, path, topic_id),
    FOREIGN KEY (snapshot_id, path) REFERENCES source_topic_assignments(snapshot_id, path) ON DELETE CASCADE
);
