package httpapi

import (
	"context"
	"database/sql"
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
		_, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, created_at, updated_at) VALUES ('ws_fixture', 'Private title', 'now', 'now')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cache := idempotency{db: db}
	if _, err := cache.claim(ctx, "fixture", "create", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := review.NewService(db, nil).DeleteWorkspace(ctx, "ws_fixture", review.Actor{Author: review.Author{Kind: "HUMAN", ID: "fixture"}, Login: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.complete("fixture", "create", storedResponse{status: 201, headers: map[string]string{}, body: []byte(`{"id":"ws_fixture","name":"Private title"}`)}); err != nil {
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
	if _, err := cache.claim(ctx, "fixture", "create", "hash"); err == nil {
		t.Fatal("unknown-outcome claim was released, allowing an implicit duplicate")
	}
}
