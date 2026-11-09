package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

func TestLateReceiptCannotRestoreDeletedContent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO reviews (id, title, description, status, created_at, updated_at) VALUES ('rvw_fixture', 'Private title', '', 'OPEN', 'now', 'now')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cache := idempotency{db: db}
	if _, err := cache.claim(ctx, "fixture", "create", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := review.NewService(db, "main").DeleteReview(ctx, "rvw_fixture", review.Actor{Author: review.Author{Kind: "HUMAN", ID: "fixture"}, Login: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.complete("fixture", "create", storedResponse{status: 201, headers: map[string]string{}, body: []byte(`{"id":"rvw_fixture","title":"Private title"}`)}); err != nil {
		t.Fatal(err)
	}
	var status sql.NullInt64
	var body []byte
	if err := db.Reader().QueryRowContext(ctx, `SELECT status, body FROM idempotency_keys WHERE key = 'create'`).Scan(&status, &body); err != nil {
		t.Fatal(err)
	}
	if status.Valid || len(body) != 0 {
		t.Fatalf("late receipt restored deleted content: %s", body)
	}
	_, err = cache.claim(ctx, "fixture", "create", "hash")
	var conflict *review.Error
	if !errors.As(err, &conflict) || conflict.Code != review.CodeConflict {
		t.Fatalf("unknown-outcome retry must conflict without replay or execution: %v", err)
	}
}
