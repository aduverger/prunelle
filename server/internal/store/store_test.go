package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAppliesMigrationsOnceAndEnforcesForeignKeys(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data dir with spaces?")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()

	var version int
	if err := db.Reader().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if want := len(mustMigrations(t)); version != want {
		t.Fatalf("user_version = %d, want %d", version, want)
	}

	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO reviews (id, title, description, status, current_revision_id, created_at, updated_at)
			VALUES ('rvw_fixture', 'Review', '', 'OPEN', 'rev_missing', 'now', 'now')`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("a valid write referencing a missing revision must fail on its foreign key: %v", err)
	}
}

func TestReaderCannotWrite(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Reader().ExecContext(ctx, `INSERT INTO reviews (id, title, description, status, created_at, updated_at) VALUES ('rvw_1', 'x', '', 'OPEN', 'now', 'now')`); err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("reader connection must reject a valid write as readonly: %v", err)
	}
}

func TestReviewMigrationScopesExistingReceipts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", dsn(filepath.Join(dir, fileName)))
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range mustMigrations(t)[:3] {
		if _, err := legacy.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO workspaces VALUES ('ws_legacy', 'Legacy', 'now', 'now')`,
		`INSERT INTO reviews VALUES ('rvw_legacy', 'ws_legacy', 'Review', '', 'OPEN', NULL, 'now', 'now')`,
		`INSERT INTO revisions VALUES ('rev_legacy', 'rvw_legacy', 1, 'now', 'hash', 'CONSISTENT')`,
		`INSERT INTO imports VALUES ('imp_legacy', 'rvw_legacy', 'rev_legacy', 'AGENT', 'fixture', 'now')`,
		`INSERT INTO idempotency_keys VALUES ('caller', 'review', 'hash', 201, '{}', CAST('{"id":"rvw_legacy","title":"stored text"}' AS BLOB), 'now')`,
		`INSERT INTO idempotency_keys VALUES ('caller', 'import', 'hash', 201, '{}', CAST('{"import_id":"imp_legacy","ids":{}}' AS BLOB), 'now')`,
		`PRAGMA user_version = 3`,
	} {
		if _, err := legacy.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, key := range []string{"review", "import"} {
		var owner string
		if err := db.Reader().QueryRowContext(ctx, `SELECT review_id FROM idempotency_keys WHERE key = ?`, key).Scan(&owner); err != nil || owner != "rvw_legacy" {
			t.Fatalf("legacy %s receipt owner = %q: %v", key, owner, err)
		}
	}
}

func mustMigrations(t *testing.T) []string {
	t.Helper()
	m, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return m
}
