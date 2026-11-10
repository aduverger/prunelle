package review

import (
	"context"
	"database/sql"

	"github.com/aduverger/prunelle/server/internal/store"
)

func (s *Service) UpdateFinding(ctx context.Context, id string, version int, req UpdateFindingRequest, actor Actor) (Finding, error) {
	var result Finding
	var p problems
	if req.Status != nil {
		p.oneOf("status", *req.Status, []string{"OPEN", "RESOLVED", "DISMISSED"})
	}
	if req.Status == nil && req.EvidenceIDs == nil {
		p.add("status", "set a status or evidence_ids")
	}
	if err := p.err("finding update is invalid"); err != nil {
		return result, err
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		finding, err := scanFinding(tx.QueryRowContext(ctx, `SELECT `+findingColumns+` FROM findings WHERE id = ?`, id).Scan)
		if err == sql.ErrNoRows {
			return notFound("finding", id)
		}
		if err != nil {
			return err
		}
		if err := requireCurrentRevision(ctx, tx, finding.ReviewID, &req.RevisionID); err != nil {
			return err
		}
		if err := checkVersion("finding", id, finding.Version, version); err != nil {
			return err
		}
		if req.EvidenceIDs != nil {
			records, err := loadReviewRecords(ctx, tx, finding.ReviewID)
			if err != nil {
				return err
			}
			records.check(&p, "evidence_ids", "EVIDENCE", *req.EvidenceIDs)
			if err := p.err("finding update is invalid"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM finding_evidence WHERE finding_id=?`, id); err != nil {
				return err
			}
			for _, evidenceID := range uniqueIDs(*req.EvidenceIDs) {
				if _, err := tx.ExecContext(ctx, `INSERT INTO finding_evidence VALUES(?,?)`, id, evidenceID); err != nil {
					return err
				}
			}
		}
		if req.Status != nil {
			finding.Status = *req.Status
		}
		finding.Version++
		if _, err := tx.ExecContext(ctx, `UPDATE findings SET status=?, version=?, updated_at=? WHERE id=?`, finding.Status, finding.Version, store.FormatTime(s.now()), id); err != nil {
			return err
		}
		finding.EvidenceIDs, err = queryStrings(ctx, tx, `SELECT evidence_id FROM finding_evidence WHERE finding_id=? ORDER BY evidence_id`, id)
		if err != nil {
			return err
		}
		result = finding
		return recordEvent(ctx, tx, "finding.updated", id, finding.ReviewID, actor, s.now())
	})
	return result, err
}
