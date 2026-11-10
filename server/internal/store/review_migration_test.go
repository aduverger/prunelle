package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestReviewOwnershipMigrationRollsBackInvalidGraphAndRestoresEnforcement(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", dsn(filepath.Join(dir, fileName)))
	if err != nil {
		t.Fatal(err)
	}
	legacy.SetMaxOpenConns(1)
	defer legacy.Close()
	for _, migration := range mustMigrations(t)[:4] {
		if _, err := legacy.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO workspaces VALUES ('ws_old','Old','now','now',NULL);
		INSERT INTO repositories VALUES ('repo_old','ws_old','api','/source/api','develop',1,'now','now');
		INSERT INTO reviews VALUES ('rvw_old','ws_old','Review','','OPEN',NULL,'now','now');
		PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	db := &DB{writer: legacy}
	broken := mustMigrations(t)[4] + `INSERT INTO review_repositories VALUES ('missing-review','repo_old','invalid',1);`
	if err := db.migrateReviewOwnership(ctx, broken); err == nil {
		t.Fatal("invalid migration graph committed")
	}
	var owner string
	if err := legacy.QueryRowContext(ctx, `SELECT workspace_id FROM reviews WHERE id='rvw_old'`).Scan(&owner); err != nil || owner != "ws_old" {
		t.Fatalf("migration did not roll back: %q %v", owner, err)
	}
	var enabled int
	if err := legacy.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("enforcement was not restored: %d %v", enabled, err)
	}
	legacy.Close()
	upgraded, err := Open(ctx, dir)
	if err != nil {
		t.Fatal("migration retry failed:", err)
	}
	upgraded.Close()
}
