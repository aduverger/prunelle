ALTER TABLE workspaces ADD COLUMN root_path TEXT;
CREATE UNIQUE INDEX workspaces_root_path ON workspaces(root_path) WHERE root_path IS NOT NULL;

CREATE VIEW resource_workspaces AS
SELECT id, id AS workspace_id FROM workspaces
UNION ALL SELECT id, workspace_id FROM repositories
UNION ALL SELECT id, workspace_id FROM reviews
UNION ALL SELECT v.id, r.workspace_id FROM revisions v JOIN reviews r ON r.id = v.review_id
UNION ALL SELECT i.id, r.workspace_id FROM imports i JOIN reviews r ON r.id = i.review_id
UNION ALL SELECT t.id, r.workspace_id FROM topics t JOIN reviews r ON r.id = t.review_id
UNION ALL SELECT i.id, r.workspace_id FROM intents i JOIN reviews r ON r.id = i.review_id
UNION ALL SELECT c.id, r.workspace_id FROM observed_changes c JOIN revisions v ON v.id = c.revision_id JOIN reviews r ON r.id = v.review_id
UNION ALL SELECT f.id, r.workspace_id FROM findings f JOIN reviews r ON r.id = f.review_id
UNION ALL SELECT e.id, r.workspace_id FROM evidence e JOIN revisions v ON v.id = e.revision_id JOIN reviews r ON r.id = v.review_id
UNION ALL SELECT a.id, r.workspace_id FROM attention_signals a JOIN revisions v ON v.id = a.revision_id JOIN reviews r ON r.id = v.review_id
UNION ALL SELECT p.id, r.workspace_id FROM representations p JOIN topics t ON t.id = p.topic_id JOIN reviews r ON r.id = t.review_id
UNION ALL SELECT d.id, r.workspace_id FROM discussions d JOIN reviews r ON r.id = d.review_id
UNION ALL SELECT m.id, r.workspace_id FROM discussion_messages m JOIN discussions d ON d.id = m.discussion_id JOIN reviews r ON r.id = d.review_id
UNION ALL SELECT d.id, r.workspace_id FROM decisions d JOIN reviews r ON r.id = d.review_id;

ALTER TABLE idempotency_keys ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
CREATE INDEX idempotency_keys_workspace ON idempotency_keys(workspace_id);
UPDATE idempotency_keys SET workspace_id = (
    SELECT workspace_id FROM resource_workspaces
    WHERE id = COALESCE(json_extract(CAST(body AS TEXT), '$.id'), json_extract(CAST(body AS TEXT), '$.import_id'))
) WHERE json_valid(CAST(body AS TEXT));
