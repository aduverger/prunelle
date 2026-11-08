package review

import (
	"context"
	"database/sql"
	"fmt"
)

// recordRef is the entity type of a review record and, for evidence and representations, its kind.
type recordRef struct {
	entityType, kind string
}

// reviewRecords indexes every record of one review by ID, to validate the references a mutation
// makes. IDs are globally unique, so one map serves every entity type.
type reviewRecords map[string]recordRef

// loadReviewRecords reads inside the mutation's transaction, so references are checked against the
// state the mutation commits on.
func loadReviewRecords(ctx context.Context, tx *sql.Tx, reviewID string) (reviewRecords, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT 'TOPIC', id, '' FROM topics WHERE review_id = ?1
		UNION ALL SELECT 'INTENT', id, '' FROM intents WHERE review_id = ?1
		UNION ALL SELECT 'FINDING', id, '' FROM findings WHERE review_id = ?1
		UNION ALL SELECT 'CHANGE', c.id, '' FROM observed_changes c JOIN revisions r ON r.id = c.revision_id WHERE r.review_id = ?1
		UNION ALL SELECT 'EVIDENCE', e.id, e.kind FROM evidence e JOIN revisions r ON r.id = e.revision_id WHERE r.review_id = ?1
		UNION ALL SELECT 'REPRESENTATION', p.id, p.type FROM representations p JOIN topics t ON t.id = p.topic_id WHERE t.review_id = ?1
		UNION ALL SELECT 'DISCUSSION', id, '' FROM discussions WHERE review_id = ?1`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := reviewRecords{}
	for rows.Next() {
		var id string
		var ref recordRef
		if err := rows.Scan(&ref.entityType, &id, &ref.kind); err != nil {
			return nil, err
		}
		records[id] = ref
	}
	return records, rows.Err()
}

// check reports every ID that is not a record of entityType in the review.
func (r reviewRecords) check(p *problems, path, entityType string, ids []string) {
	for i, id := range ids {
		r.checkOne(p, fmt.Sprintf("%s[%d]", path, i), entityType, id)
	}
}

func (r reviewRecords) checkOne(p *problems, path, entityType, id string) bool {
	if r[id].entityType != entityType {
		p.add(path, "%s is not a %s of this review", id, entityTypeNames[entityType])
		return false
	}
	return true
}

var entityTypeNames = map[string]string{
	"TOPIC": "topic", "INTENT": "intent", "FINDING": "finding", "CHANGE": "change", "EVIDENCE": "evidence",
	"REPRESENTATION": "representation", "DISCUSSION": "discussion",
}

// uniqueIDs drops repeated IDs, keeping the first occurrence.
func uniqueIDs(ids []string) []string {
	seen := map[string]bool{}
	unique := []string{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique
}
