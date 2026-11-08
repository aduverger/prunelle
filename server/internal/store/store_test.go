package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
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
		_, err := tx.ExecContext(ctx, `INSERT INTO repositories (id, workspace_id, label, root_path, base_ref, include_untracked, created_at, updated_at)
			VALUES ('repo_1', 'ws_missing', 'api', '/tmp/api', 'main', 1, 'now', 'now')`)
		return err
	})
	if err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestReaderCannotWrite(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Reader().ExecContext(ctx, `INSERT INTO workspaces VALUES ('ws_1', 'x', 'now', 'now')`); err == nil {
		t.Fatal("reader connection accepted a write")
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
