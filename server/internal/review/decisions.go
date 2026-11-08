package review

import (
	"context"
	"database/sql"

	"github.com/aduverger/prunelle/server/internal/store"
)

var decisionKinds = []string{"COMMENT", "APPROVE", "REQUEST_CHANGES"}

// CreateDecision records a reviewer's verdict on a revision of the review, by default the current one.
// Only humans decide. COMMENT needs a body; REQUEST_CHANGES selects the actionable findings and
// discussions, which no other verdict does.
func (s *Service) CreateDecision(ctx context.Context, reviewID string, req CreateDecisionRequest, actor Actor) (Decision, error) {
	if actor.Kind != "HUMAN" {
		return Decision{}, &Error{Code: CodePermissionDenied, Message: "decisions are made by humans; agent " + actor.ID + " cannot submit one"}
	}
	revision, err := s.reviewRevision(ctx, reviewID, req.RevisionID)
	if err != nil {
		return Decision{}, err
	}
	var p problems
	p.oneOf("decision", req.Decision, decisionKinds)
	selects := len(req.FindingIDs)+len(req.DiscussionIDs) > 0
	switch {
	case req.Decision == "COMMENT":
		p.required("body", req.Body)
	case req.Decision == "REQUEST_CHANGES" && !selects:
		p.add("finding_ids", "REQUEST_CHANGES must select at least one finding or discussion")
	}
	if selects && req.Decision != "REQUEST_CHANGES" {
		p.add("finding_ids", "only REQUEST_CHANGES selects findings or discussions")
	}
	id := store.NewID("dec")
	now := s.now()
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		records, err := loadReviewRecords(ctx, tx, reviewID)
		if err != nil {
			return err
		}
		records.check(&p, "finding_ids", "FINDING", req.FindingIDs)
		records.check(&p, "discussion_ids", "DISCUSSION", req.DiscussionIDs)
		if err := p.err("decision is invalid"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO decisions (id, review_id, revision_id, decision, body, author_kind, author_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, reviewID, revision.ID, req.Decision, req.Body, actor.Kind, actor.ID, store.FormatTime(now)); err != nil {
			return err
		}
		for _, findingID := range uniqueIDs(req.FindingIDs) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO decision_findings (decision_id, finding_id) VALUES (?, ?)`, id, findingID); err != nil {
				return err
			}
		}
		for _, discussionID := range uniqueIDs(req.DiscussionIDs) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO decision_discussions (decision_id, discussion_id) VALUES (?, ?)`, id, discussionID); err != nil {
				return err
			}
		}
		return recordEvent(ctx, tx, "decision.created", id, reviewID, actor, now)
	})
	if err != nil {
		return Decision{}, err
	}
	decisions, err := s.queryDecisions(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Decision{}, err
	}
	return decisions[0], nil
}

// ListDecisions returns the review's decisions, oldest first.
func (s *Service) ListDecisions(ctx context.Context, reviewID string) ([]Decision, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	return s.queryDecisions(ctx, `WHERE review_id = ? ORDER BY created_at, id`, reviewID)
}

func (s *Service) queryDecisions(ctx context.Context, where string, args ...any) ([]Decision, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id, review_id, revision_id, decision, body, author_kind, author_id, created_at
		FROM decisions `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	decisions := []Decision{}
	for rows.Next() {
		var d Decision
		var created string
		if err := rows.Scan(&d.ID, &d.ReviewID, &d.RevisionID, &d.Decision, &d.Body, &d.Author.Kind, &d.Author.ID, &created); err != nil {
			return nil, err
		}
		d.CreatedAt = store.ParseTime(created)
		decisions = append(decisions, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range decisions {
		d := &decisions[i]
		if d.FindingIDs, err = queryStrings(ctx, s.db.Reader(),
			`SELECT finding_id FROM decision_findings WHERE decision_id = ? ORDER BY finding_id`, d.ID); err != nil {
			return nil, err
		}
		if d.DiscussionIDs, err = queryStrings(ctx, s.db.Reader(),
			`SELECT discussion_id FROM decision_discussions WHERE decision_id = ? ORDER BY discussion_id`, d.ID); err != nil {
			return nil, err
		}
	}
	return decisions, nil
}
