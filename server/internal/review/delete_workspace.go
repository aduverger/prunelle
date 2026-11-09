package review

import (
	"context"
	"database/sql"
	"errors"
)

const workspaceDeletionScope = `WITH
	owned_reviews AS (SELECT id FROM reviews WHERE workspace_id = ?1),
	owned_revisions AS (SELECT id FROM revisions WHERE review_id IN (SELECT id FROM owned_reviews)),
	owned_topics AS (SELECT id FROM topics WHERE review_id IN (SELECT id FROM owned_reviews)),
	owned_discussions AS (SELECT id FROM discussions WHERE review_id IN (SELECT id FROM owned_reviews)),
	owned_decisions AS (SELECT id FROM decisions WHERE review_id IN (SELECT id FROM owned_reviews)) `

func (s *Service) DeleteWorkspace(ctx context.Context, id string, actor Actor) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM workspaces WHERE id = ?`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, query := range []string{
			`DELETE FROM events WHERE review_id IN (SELECT id FROM owned_reviews) OR entity_id = ?1`,
			`DELETE FROM decision_findings WHERE decision_id IN (SELECT id FROM owned_decisions)`,
			`DELETE FROM decision_discussions WHERE decision_id IN (SELECT id FROM owned_decisions)`,
			`DELETE FROM decisions WHERE review_id IN (SELECT id FROM owned_reviews)`,
			`DELETE FROM discussion_code_ranges WHERE discussion_id IN (SELECT id FROM owned_discussions)`,
			`DELETE FROM discussion_messages WHERE discussion_id IN (SELECT id FROM owned_discussions)`,
			`DELETE FROM discussions WHERE review_id IN (SELECT id FROM owned_reviews)`,
			`DELETE FROM topic_intents WHERE topic_id IN (SELECT id FROM owned_topics)`,
			`DELETE FROM topic_changes WHERE topic_id IN (SELECT id FROM owned_topics)`,
			`DELETE FROM topic_findings WHERE topic_id IN (SELECT id FROM owned_topics)`,
			`DELETE FROM topic_evidence WHERE topic_id IN (SELECT id FROM owned_topics)`,
			`DELETE FROM intent_evidence WHERE intent_id IN (SELECT id FROM intents WHERE review_id IN (SELECT id FROM owned_reviews))`,
			`DELETE FROM change_evidence WHERE change_id IN (SELECT id FROM observed_changes WHERE revision_id IN (SELECT id FROM owned_revisions))`,
			`DELETE FROM finding_evidence WHERE finding_id IN (SELECT id FROM findings WHERE review_id IN (SELECT id FROM owned_reviews))`,
			`DELETE FROM attention_evidence WHERE attention_id IN (SELECT id FROM attention_signals WHERE revision_id IN (SELECT id FROM owned_revisions))`,
			`DELETE FROM representations WHERE topic_id IN (SELECT id FROM owned_topics)`,
			`DELETE FROM attention_signals WHERE revision_id IN (SELECT id FROM owned_revisions)`,
			`DELETE FROM evidence_code_ranges WHERE evidence_id IN (SELECT id FROM evidence WHERE revision_id IN (SELECT id FROM owned_revisions))`,
			`DELETE FROM evidence WHERE revision_id IN (SELECT id FROM owned_revisions)`,
			`DELETE FROM findings WHERE review_id IN (SELECT id FROM owned_reviews)`,
			`DELETE FROM observed_changes WHERE revision_id IN (SELECT id FROM owned_revisions)`,
			`DELETE FROM intents WHERE review_id IN (SELECT id FROM owned_reviews)`,
			`DELETE FROM topics WHERE review_id IN (SELECT id FROM owned_reviews)`,
			`DELETE FROM imports WHERE review_id IN (SELECT id FROM owned_reviews)`,
			`UPDATE reviews SET current_revision_id = NULL WHERE workspace_id = ?1`,
			`DELETE FROM snapshot_files WHERE snapshot_id IN (SELECT id FROM repo_snapshots WHERE revision_id IN (SELECT id FROM owned_revisions))`,
			`DELETE FROM repo_snapshots WHERE revision_id IN (SELECT id FROM owned_revisions)`,
			`DELETE FROM revisions WHERE review_id IN (SELECT id FROM owned_reviews)`,
			`DELETE FROM reviews WHERE workspace_id = ?1`,
			`DELETE FROM repositories WHERE workspace_id = ?1`,
			`DELETE FROM workspace_runtime_sessions WHERE workspace_id = ?1`,
			`DELETE FROM workspaces WHERE id = ?1`,
		} {
			if _, err := tx.ExecContext(ctx, workspaceDeletionScope+query, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM blobs WHERE
			NOT EXISTS (SELECT 1 FROM snapshot_files WHERE base_blob = blobs.hash OR head_blob = blobs.hash)
			AND NOT EXISTS (SELECT 1 FROM evidence_code_ranges WHERE blob_hash = blobs.hash)
			AND NOT EXISTS (SELECT 1 FROM discussion_code_ranges WHERE blob_hash = blobs.hash)`); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "workspace.deleted", id, "", actor, s.now())
	})
}
