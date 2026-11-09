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

func TestReviewOwnershipMigrationPreservesSharedReferences(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", dsn(filepath.Join(dir, fileName)))
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range mustMigrations(t)[:4] {
		if _, err := legacy.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO workspaces VALUES ('ws_shared', 'Old grouping', 'now', 'now', '/source/group')`,
		`INSERT INTO repositories VALUES ('repo_original', 'ws_shared', 'api', '/source/group/api', 'origin/develop', 0, 'now', 'now')`,
		`INSERT INTO reviews VALUES ('rvw_a', 'ws_shared', 'First', '', 'OPEN', NULL, 'now', 'now')`,
		`INSERT INTO reviews VALUES ('rvw_b', 'ws_shared', 'Second', '', 'OPEN', NULL, 'now', 'now')`,
		`INSERT INTO revisions VALUES ('rev_original', 'rvw_a', 1, 'now', 'original-fingerprint', 'CONSISTENT')`,
		`UPDATE reviews SET current_revision_id = 'rev_original' WHERE id = 'rvw_a'`,
		`INSERT INTO repo_snapshots VALUES ('snap_original', 'rev_original', 'repo_original', 'origin/develop', 'base', 'head', 'feature', 0, 1, 0, 'repo-fingerprint', 'CONSISTENT', '[]')`,
		`INSERT INTO blobs VALUES ('blob_original', 4, CAST('code' AS BLOB))`,
		`INSERT INTO snapshot_files VALUES ('snap_original', 'file.ts', NULL, 'MODIFIED', '100644', '100644', 'base-oid', 'head-oid', 'blob_original', 'blob_original', 0, NULL)`,
		`INSERT INTO discussions VALUES ('d_original', 'rvw_a', 'rev_original', 'CODE_RANGE', NULL, NULL, NULL, NULL, NULL, NULL, NULL, 'OPEN', 'HUMAN', 'fixture', 1, 'now', 'now')`,
		`INSERT INTO discussion_code_ranges VALUES ('d_original', 'repo_original', 'file.ts', 'HEAD', 1, 1, 'blob_original', 'code')`,
		`INSERT INTO discussion_messages VALUES ('msg_original', 'd_original', 'HUMAN', 'fixture', 'Keep this message', 'now')`,
		`INSERT INTO session_backends VALUES ('sb_fixture', 'Fake', 'REX', '{}', 1, 'now', 'now')`,
		`INSERT INTO runtime_sessions VALUES ('rts_original', 'sb_fixture', 'external', 'ONLINE', 'Fixture', NULL, NULL, 1, NULL, '{}', 'now', 'now', 'now')`,
		`INSERT INTO workspace_runtime_sessions VALUES ('ws_shared', 'rts_original', 'now')`,
		`INSERT INTO workspaces VALUES ('ws_empty', 'No review', 'now', 'now', '/source/empty')`,
		`INSERT INTO runtime_sessions VALUES ('rts_unowned', 'sb_fixture', 'unowned', 'ONLINE', 'Still running', NULL, NULL, 1, NULL, '{}', 'now', 'now', 'now')`,
		`INSERT INTO workspace_runtime_sessions VALUES ('ws_empty', 'rts_unowned', 'now')`,
		`INSERT INTO idempotency_keys VALUES ('caller', 'comment', 'hash', 201, '{}', CAST('{"id":"d_original","messages":[{"body":"Keep this message"}]}' AS BLOB), 'now', 'ws_shared')`,
		`INSERT INTO idempotency_keys VALUES ('caller', 'workspace', 'hash', 201, '{}', CAST('{"id":"ws_shared"}' AS BLOB), 'now', 'ws_shared')`,
		`INSERT INTO idempotency_keys VALUES ('caller', 'pending', 'hash', NULL, NULL, NULL, 'now', NULL)`,
		`INSERT INTO idempotency_keys VALUES ('caller', 'global', 'global-hash', 201, '{"ETag":"original"}', CAST('{"id":"rts_unowned","label":"Still running"}' AS BLOB), 'now', NULL)`,
		`PRAGMA user_version = 4`,
	} {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			t.Fatalf("legacy fixture: %v: %s", err, statement)
		}
	}
	legacy.Close()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM review_repositories WHERE repository_id = 'repo_original' AND label = 'api' AND include_untracked = 0`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("shared identities/settings: %d %v", count, err)
	}
	if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM review_runtime_sessions WHERE runtime_session_id = 'rts_original'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("association fan-out: %d %v", count, err)
	}
	var runtimeState string
	if err := db.Reader().QueryRowContext(ctx, "SELECT state FROM runtime_sessions WHERE id = 'rts_unowned'").Scan(&runtimeState); err != nil || runtimeState != "ONLINE" {
		t.Fatalf("unowned runtime changed: %q %v", runtimeState, err)
	}
	if err := db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM review_runtime_sessions WHERE runtime_session_id = 'rts_unowned'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("runtime gained an invented review association: %d %v", count, err)
	}
	var hash, headers, body string
	if err := db.Reader().QueryRowContext(ctx, "SELECT request_hash, headers, CAST(body AS TEXT) FROM idempotency_keys WHERE key = 'global'").Scan(&hash, &headers, &body); err != nil || hash != "global-hash" || headers != `{"ETag":"original"}` || body != `{"id":"rts_unowned","label":"Still running"}` {
		t.Fatalf("unrelated completed receipt changed: %q %q %q %v", hash, headers, body, err)
	}
	var repoID, text string
	if err := db.Reader().QueryRowContext(ctx, `SELECT repository_id, selected_text FROM discussion_code_ranges WHERE discussion_id = 'd_original'`).Scan(&repoID, &text); err != nil || repoID != "repo_original" || text != "code" {
		t.Fatalf("anchor changed: %s %q %v", repoID, text, err)
	}
	var owner string
	if err := db.Reader().QueryRowContext(ctx, `SELECT review_id FROM idempotency_keys WHERE key = 'comment'`).Scan(&owner); err != nil || owner != "rvw_a" {
		t.Fatalf("receipt owner: %q %v", owner, err)
	}
	if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM idempotency_keys WHERE key = 'workspace'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired receipt: %d %v", count, err)
	}
	if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM idempotency_keys WHERE key = 'pending' AND body IS NULL AND status IS NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pending claim: %d %v", count, err)
	}
	var retiredRef string
	if err := db.Reader().QueryRowContext(ctx, "SELECT legacy_base_ref FROM repositories WHERE id = ?", repoID).Scan(&retiredRef); err != nil || retiredRef != "origin/develop" {
		t.Fatalf("legacy preference audit lost: %q %v", retiredRef, err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO review_repositories VALUES ('missing', 'repo_original', 'bad', 1)`)
		return err
	}); err == nil {
		t.Fatal("foreign keys not restored")
	}
}
