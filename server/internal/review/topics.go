package review

import (
	"context"
	"database/sql"
	"strings"

	"github.com/aduverger/prunelle/server/internal/store"
)

var topicStates = []string{"ACTIVE", "OBSOLETE"}

// topicLinkKinds lists the records a topic links to: request field, entity type, link table and column.
var topicLinkKinds = []struct{ field, entityType, table, column string }{
	{"intent_ids", "INTENT", "topic_intents", "intent_id"},
	{"change_ids", "CHANGE", "topic_changes", "change_id"},
	{"finding_ids", "FINDING", "topic_findings", "finding_id"},
	{"evidence_ids", "EVIDENCE", "topic_evidence", "evidence_id"},
}

// CreateTopic adds a manual topic as the last child of its parent (or last root topic).
func (s *Service) CreateTopic(ctx context.Context, reviewID string, req CreateTopicRequest, actor Author) (Topic, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return Topic{}, err
	}
	var p problems
	p.required("title", req.Title)
	links := map[string][]string{"intent_ids": req.IntentIDs, "change_ids": req.ChangeIDs, "finding_ids": req.FindingIDs, "evidence_ids": req.EvidenceIDs}
	id := store.NewID("top")
	now := s.now()
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		records, err := loadReviewRecords(ctx, tx, reviewID)
		if err != nil {
			return err
		}
		if req.ParentID != nil {
			records.checkOne(&p, "parent_id", "TOPIC", *req.ParentID)
		}
		checkTopicLinks(&p, records, links)
		if err := p.err("topic is invalid"); err != nil {
			return err
		}
		var position int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM topics WHERE review_id = ? AND parent_id IS ?`,
			reviewID, nullString(req.ParentID)).Scan(&position); err != nil {
			return err
		}
		at := store.FormatTime(now)
		if _, err := tx.ExecContext(ctx, `INSERT INTO topics (id, review_id, parent_id, title, summary, origin, state, position, author_kind, author_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'MANUAL', 'ACTIVE', ?, ?, ?, ?, ?)`, id, reviewID, nullString(req.ParentID), strings.TrimSpace(req.Title), req.Summary,
			position, actor.Kind, actor.ID, at, at); err != nil {
			return err
		}
		if err := writeTopicLinks(ctx, tx, id, links); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "topic.created", id, reviewID, actor, now)
	})
	if err != nil {
		return Topic{}, err
	}
	return s.GetTopic(ctx, id)
}

// UpdateTopic edits a topic last read at version. Imported topics are editable; their origin and author stay.
func (s *Service) UpdateTopic(ctx context.Context, id string, version int, req UpdateTopicRequest, actor Author) (Topic, error) {
	var p problems
	if req.Title != nil {
		p.required("title", *req.Title)
	}
	if req.State != nil {
		p.oneOf("state", *req.State, topicStates)
	}
	links := map[string][]string{}
	for field, ids := range map[string]*[]string{"intent_ids": req.IntentIDs, "change_ids": req.ChangeIDs, "finding_ids": req.FindingIDs, "evidence_ids": req.EvidenceIDs} {
		if ids != nil {
			links[field] = *ids
		}
	}
	now := s.now()
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		t, err := lockTopic(ctx, tx, id, version)
		if err != nil {
			return err
		}
		records, err := loadReviewRecords(ctx, tx, t.reviewID)
		if err != nil {
			return err
		}
		checkTopicLinks(&p, records, links)
		if err := p.err("topic update is invalid"); err != nil {
			return err
		}
		if req.Title != nil {
			t.title = strings.TrimSpace(*req.Title)
		}
		if req.Summary != nil {
			t.summary = *req.Summary
		}
		if req.State != nil {
			t.state = *req.State
		}
		if _, err := tx.ExecContext(ctx, `UPDATE topics SET title = ?, summary = ?, state = ?, version = version + 1, updated_at = ? WHERE id = ?`,
			t.title, t.summary, t.state, store.FormatTime(now), id); err != nil {
			return err
		}
		if err := writeTopicLinks(ctx, tx, id, links); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "topic.updated", id, t.reviewID, actor, now)
	})
	if err != nil {
		return Topic{}, err
	}
	return s.GetTopic(ctx, id)
}

// MoveTopic places a topic last read at version under a new parent (nil for the root) at a position
// among its new siblings, clamped to the end. Sibling positions stay dense; every topic whose
// position changes gets a new version.
func (s *Service) MoveTopic(ctx context.Context, id string, version int, req MoveTopicRequest, actor Author) (Topic, error) {
	if req.Position < 0 {
		return Topic{}, invalid("position must be 0 or more")
	}
	now := s.now()
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		t, err := lockTopic(ctx, tx, id, version)
		if err != nil {
			return err
		}
		if req.ParentID != nil {
			if err := checkNewParent(ctx, tx, id, t, *req.ParentID); err != nil {
				return err
			}
		}
		at := store.FormatTime(now)
		if _, err := tx.ExecContext(ctx, `UPDATE topics SET position = position - 1, version = version + 1, updated_at = ?
			WHERE review_id = ? AND parent_id IS ? AND position > ?`, at, t.reviewID, t.parentID, t.position); err != nil {
			return err
		}
		var siblings int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM topics WHERE review_id = ? AND parent_id IS ? AND id != ?`,
			t.reviewID, nullString(req.ParentID), id).Scan(&siblings); err != nil {
			return err
		}
		position := min(req.Position, siblings)
		if _, err := tx.ExecContext(ctx, `UPDATE topics SET position = position + 1, version = version + 1, updated_at = ?
			WHERE review_id = ? AND parent_id IS ? AND position >= ? AND id != ?`, at, t.reviewID, nullString(req.ParentID), position, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE topics SET parent_id = ?, position = ?, version = version + 1, updated_at = ? WHERE id = ?`,
			nullString(req.ParentID), position, at, id); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "topic.updated", id, t.reviewID, actor, now)
	})
	if err != nil {
		return Topic{}, err
	}
	return s.GetTopic(ctx, id)
}

// topicRow is the stored state of a topic being modified.
type topicRow struct {
	reviewID              string
	parentID              sql.NullString
	position              int
	title, summary, state string
}

// lockTopic reads a topic inside the write transaction and checks that the caller saw its current version.
func lockTopic(ctx context.Context, tx *sql.Tx, id string, version int) (topicRow, error) {
	var t topicRow
	var stored int
	err := scanOne(tx.QueryRowContext(ctx, `SELECT review_id, parent_id, position, title, summary, state, version FROM topics WHERE id = ?`, id),
		"topic", id, &t.reviewID, &t.parentID, &t.position, &t.title, &t.summary, &t.state, &stored)
	if err != nil {
		return topicRow{}, err
	}
	return t, checkVersion("topic", id, stored, version)
}

// checkNewParent rejects a parent from another review, the topic itself, and its descendants.
func checkNewParent(ctx context.Context, tx *sql.Tx, topicID string, t topicRow, parentID string) error {
	var parentReview string
	if err := scanOne(tx.QueryRowContext(ctx, `SELECT review_id FROM topics WHERE id = ?`, parentID), "topic", parentID, &parentReview); err != nil {
		return err
	}
	if parentReview != t.reviewID {
		return invalid("parent topic %s belongs to another review", parentID)
	}
	var cycle bool
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE lineage(id) AS (
			SELECT ?1 UNION SELECT t.parent_id FROM topics t JOIN lineage l ON t.id = l.id WHERE t.parent_id IS NOT NULL)
		SELECT EXISTS (SELECT 1 FROM lineage WHERE id = ?2)`, parentID, topicID).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return invalid("topic %s cannot move under itself or one of its descendants", topicID)
	}
	return nil
}

func checkTopicLinks(p *problems, records reviewRecords, links map[string][]string) {
	for _, kind := range topicLinkKinds {
		if ids, ok := links[kind.field]; ok {
			records.check(p, kind.field, kind.entityType, ids)
		}
	}
}

// writeTopicLinks replaces the topic's links of every kind present in links.
func writeTopicLinks(ctx context.Context, tx *sql.Tx, topicID string, links map[string][]string) error {
	for _, kind := range topicLinkKinds {
		ids, ok := links[kind.field]
		if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+kind.table+` WHERE topic_id = ?`, topicID); err != nil {
			return err
		}
		for _, id := range uniqueIDs(ids) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO `+kind.table+` (topic_id, `+kind.column+`) VALUES (?, ?)`, topicID, id); err != nil {
				return err
			}
		}
	}
	return nil
}
