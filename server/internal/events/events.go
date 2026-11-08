// Package events records review changes in an ordered, durable log that clients follow to stay current.
// A change and its event commit in the same transaction, so the log never misses or invents a change.
package events

import (
	"context"
	"database/sql"
	"time"

	"github.com/aduverger/prunelle/server/internal/store"
)

// Event announces that an entity changed; clients refetch it. This is the API wire format.
type Event struct {
	Sequence   int64     `json:"sequence"`
	Type       string    `json:"type"`
	EntityID   string    `json:"entity_id"`
	ReviewID   *string   `json:"review_id"`
	Actor      Actor     `json:"actor"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Actor is who made the change, in the review domain's author format.
type Actor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Append records an event inside the transaction that makes the change.
func Append(ctx context.Context, tx *sql.Tx, typ, entityID string, reviewID *string, actor Actor, at time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO events (type, entity_id, review_id, actor_kind, actor_id, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?)`, typ, entityID, reviewID, actor.Kind, actor.ID, store.FormatTime(at))
	return err
}

// Head returns the sequence of the latest event, 0 when there is none.
func Head(ctx context.Context, db *store.DB) (int64, error) {
	var head int64
	err := db.Reader().QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM events`).Scan(&head)
	return head, err
}

// After returns up to limit events following sequence after, in order.
func After(ctx context.Context, db *store.DB, after int64, limit int) ([]Event, error) {
	rows, err := db.Reader().QueryContext(ctx, `SELECT sequence, type, entity_id, review_id, actor_kind, actor_id, occurred_at
		FROM events WHERE sequence > ? ORDER BY sequence LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		var reviewID sql.NullString
		var at string
		if err := rows.Scan(&e.Sequence, &e.Type, &e.EntityID, &reviewID, &e.Actor.Kind, &e.Actor.ID, &at); err != nil {
			return nil, err
		}
		if reviewID.Valid {
			e.ReviewID = &reviewID.String
		}
		e.OccurredAt = store.ParseTime(at)
		events = append(events, e)
	}
	return events, rows.Err()
}
