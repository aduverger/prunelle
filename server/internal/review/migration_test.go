package review_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

func TestVersionFourHistorySurvivesOwnershipMigrationAndScopedDeletion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	legacy := must[*sql.DB](t)(sql.Open("sqlite", "file:"+filepath.Join(dir, "prunelle.db")+"?_pragma=foreign_keys(1)"))
	for _, name := range []string{"0001_init.sql", "0002_sessions.sql", "0003_mutations.sql", "0004_workspaces.sql"} {
		migration := must[[]byte](t)(os.ReadFile(filepath.Join("..", "store", "migrations", name)))
		if _, err := legacy.ExecContext(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO workspaces VALUES ('ws_old','Old shared context','2026-11-01T10:00:00Z','2026-11-01T10:00:00Z','/historical/source')`,
		`INSERT INTO repositories VALUES ('repo_old','ws_old','api','/historical/source/api','origin/develop',1,'2026-11-01T10:00:00Z','2026-11-01T10:00:00Z')`,
		`INSERT INTO reviews VALUES ('rvw_a','ws_old','A','','OPEN',NULL,'2026-11-01T10:00:00Z','2026-11-01T10:00:00Z')`,
		`INSERT INTO reviews VALUES ('rvw_b','ws_old','B','','OPEN',NULL,'2026-11-01T10:00:00Z','2026-11-01T10:00:00Z')`,
		`INSERT INTO revisions VALUES ('rev_a','rvw_a',1,'2026-11-01T10:00:00Z','fingerprint-a','CONSISTENT')`,
		`INSERT INTO revisions VALUES ('rev_b','rvw_b',1,'2026-11-01T10:00:00Z','fingerprint-b','CONSISTENT')`,
		`UPDATE reviews SET current_revision_id='rev_a' WHERE id='rvw_a'`,
		`UPDATE reviews SET current_revision_id='rev_b' WHERE id='rvw_b'`,
		`INSERT INTO repo_snapshots VALUES ('snap_a','rev_a','repo_old','origin/develop','base-a','head-a','feature',0,1,0,'source-a','CONSISTENT','[]')`,
		`INSERT INTO repo_snapshots VALUES ('snap_b','rev_b','repo_old','origin/develop','base-b','head-b','feature',0,1,0,'source-b','CONSISTENT','[]')`,
		`INSERT INTO imports VALUES ('imp_old','rvw_a','rev_a','AGENT','pi:legacy','2026-11-01T10:00:00Z')`,
		`INSERT INTO topics VALUES ('top_old','rvw_a',NULL,'Topic','Summary','IMPORTED','ACTIVE',0,'AGENT','pi:legacy','imp_old','2026-11-01T10:00:00Z','2026-11-01T10:00:00Z',1)`,
		`INSERT INTO topics VALUES ('top_child','rvw_a','top_old','Child','','IMPORTED','ACTIVE',0,'AGENT','pi:legacy','imp_old','2026-11-01T10:00:00Z','2026-11-01T10:00:00Z',1)`,
		`INSERT INTO intents VALUES ('int_old','rvw_a','BEHAVIOR','Intent','source',NULL,'OBSERVED','AGENT','pi:legacy','imp_old','2026-11-01T10:00:00Z','2026-11-01T10:00:00Z')`,
		`INSERT INTO observed_changes VALUES ('chg_old','rev_a','BEHAVIOR','Change','before','after','[]','SUPPORTED','OBSERVED','AGENT','pi:legacy','imp_old','2026-11-01T10:00:00Z','2026-11-01T10:00:00Z')`,
		`INSERT INTO findings VALUES ('fnd_old','rvw_a','rev_a','CORRECTNESS','HIGH','OPEN','Finding','Reason','OBSERVED','AGENT','pi:legacy','imp_old','2026-11-01T10:00:00Z','2026-11-01T10:00:00Z')`,
		`INSERT INTO evidence VALUES ('evd_old','rev_a','CODE_RANGE','Code',NULL,'source','OBSERVED','AGENT','pi:legacy','imp_old','2026-11-01T10:00:00Z')`,
		`INSERT INTO representations VALUES ('rep_old','top_old','rev_a','DIAGRAM','Diagram','{"diagram":{"nodes":[{"id":"node","label":"Node","kind":"COMPONENT","ref":null,"evidence_ids":[]}],"edges":[]}}','IMPORTED',0,'AGENT','pi:legacy','imp_old','2026-11-01T10:00:00Z','2026-11-01T10:00:00Z',1)`,
		`INSERT INTO attention_signals VALUES ('att_old','rev_a',NULL,'fnd_old',NULL,'HIGH','Reason','Unknown','OBSERVED','AGENT','pi:legacy','imp_old','2026-11-01T10:00:00Z')`,
		`INSERT INTO topic_intents VALUES ('top_old','int_old')`,
		`INSERT INTO topic_changes VALUES ('top_old','chg_old')`,
		`INSERT INTO topic_findings VALUES ('top_old','fnd_old')`,
		`INSERT INTO topic_evidence VALUES ('top_old','evd_old')`,
		`INSERT INTO intent_evidence VALUES ('int_old','evd_old')`,
		`INSERT INTO change_evidence VALUES ('chg_old','evd_old')`,
		`INSERT INTO finding_evidence VALUES ('fnd_old','evd_old')`,
		`INSERT INTO attention_evidence VALUES ('att_old','evd_old')`,
		`INSERT INTO decisions VALUES ('dec_old','rvw_a','rev_a','REQUEST_CHANGES','Fix this','HUMAN','human','2026-11-01T10:00:00Z')`,
		`INSERT INTO decision_findings VALUES ('dec_old','fnd_old')`,
	} {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			t.Fatalf("legacy fixture: %v: %s", err, statement)
		}
	}
	content := []byte("alpha\nbeta\n")
	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	if _, err := legacy.ExecContext(ctx, "INSERT INTO blobs VALUES (?, ?, ?)", hash, len(content), content); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []string{"snap_a", "snap_b"} {
		if _, err := legacy.ExecContext(ctx, `INSERT INTO snapshot_files VALUES (?, 'file.ts',NULL,'MODIFIED','100644','100644','oid-base','oid-head',?,?,0,NULL)`, snapshot, hash, hash); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO evidence_code_ranges VALUES ('evd_old','repo_old','file.ts','HEAD',1,2,?,'alpha'||char(10)||'beta')`, hash); err != nil {
		t.Fatal(err)
	}
	for _, anchor := range []struct{ kind, column, target string }{
		{"REVIEW", "", ""}, {"TOPIC", "topic_id", "top_old"}, {"INTENT", "intent_id", "int_old"}, {"CHANGE", "change_id", "chg_old"}, {"FINDING", "finding_id", "fnd_old"}, {"EVIDENCE", "evidence_id", "evd_old"}, {"CODE_RANGE", "", ""}, {"DIAGRAM_ELEMENT", "representation_id", "rep_old"},
	} {
		id := "d_" + anchor.kind
		if _, err := legacy.ExecContext(ctx, `INSERT INTO discussions (id,review_id,revision_id,anchor_type,status,author_kind,author_id,version,created_at,updated_at,topic_id,intent_id,change_id,finding_id,evidence_id,representation_id,element_id)
   VALUES (?,'rvw_a','rev_a',?,'OPEN','HUMAN','human',1,'2026-11-01T10:00:00Z','2026-11-01T10:00:00Z',?,?,?,?,?,?,?)`, id, anchor.kind, nullableTarget(anchor.column, "topic_id", anchor.target), nullableTarget(anchor.column, "intent_id", anchor.target), nullableTarget(anchor.column, "change_id", anchor.target), nullableTarget(anchor.column, "finding_id", anchor.target), nullableTarget(anchor.column, "evidence_id", anchor.target), nullableTarget(anchor.column, "representation_id", anchor.target), nullableTarget(anchor.kind, "DIAGRAM_ELEMENT", "node")); err != nil {
			t.Fatal(err)
		}
		if anchor.kind == "CODE_RANGE" {
			if _, err := legacy.ExecContext(ctx, `INSERT INTO discussion_code_ranges VALUES (?,'repo_old','file.ts','HEAD',1,2,?,'alpha'||char(10)||'beta')`, id, hash); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := legacy.ExecContext(ctx, `INSERT INTO discussion_messages VALUES (?,?,'HUMAN','human','Original text','2026-11-01T10:00:00Z')`, "msg_"+anchor.kind, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO decision_discussions VALUES ('dec_old','d_CODE_RANGE'); PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	db := must[*store.DB](t)(store.Open(ctx, dir))
	defer db.Close()
	svc := review.NewService(db, "main")
	e := &env{ctx: ctx, db: db, svc: svc}
	first := e.reviewContent(t, "rvw_a")
	second := e.reviewContent(t, "rvw_b")
	if len(first.discussions) != 8 || first.decisions[0].ID != "dec_old" || first.topics[0].ID != "top_old" || first.imports[0].ID != "imp_old" {
		t.Fatalf("history lost during migration: %+v", first)
	}
	for _, r := range []review.Review{first.review, second.review} {
		if len(r.Repositories) != 1 || r.Repositories[0].ID != "repo_old" || r.ContextPath == nil || *r.ContextPath != "/historical/source" {
			t.Fatalf("source identity changed: %+v", r)
		}
	}
	snapshot := must[review.Revision](t)(svc.GetRevision(ctx, "rev_a"))
	if snapshot.Fingerprint != "fingerprint-a" || snapshot.Snapshots[0].BaseRef != "origin/develop" || snapshot.Snapshots[0].BaseSHA != "base-a" || snapshot.Snapshots[0].HeadSHA != "head-a" {
		t.Fatalf("captured comparison changed: %+v", snapshot)
	}
	file := must[review.FileContent](t)(svc.GetFile(ctx, "rev_a", "repo_old", "file.ts"))
	if file.HeadText == nil || *file.HeadText != string(content) {
		t.Fatal("stored source changed")
	}
	thread := must[review.Discussion](t)(svc.GetDiscussion(ctx, "d_CODE_RANGE"))
	if thread.Anchor.CodeRange.RepositoryID != "repo_old" || thread.Anchor.CodeRange.BlobHash != hash || thread.Anchor.CodeRange.SelectedText != "alpha\nbeta" || thread.Messages[0].ID != "msg_CODE_RANGE" {
		t.Fatalf("immutable anchor changed: %+v", thread)
	}
	if err := svc.DeleteReview(ctx, "rvw_a", reviewer); err != nil {
		t.Fatal(err)
	}
	if after := e.reviewContent(t, "rvw_b"); !reflect.DeepEqual(after, second) {
		t.Fatal("deleting migrated review changed shared source owner")
	}
	must[review.FileContent](t)(svc.GetFile(ctx, "rev_b", "repo_old", "file.ts"))
	var retiredRef string
	if err := db.Reader().QueryRowContext(ctx, "SELECT legacy_base_ref FROM repositories WHERE id = 'repo_old'").Scan(&retiredRef); err != nil || retiredRef != "origin/develop" {
		t.Fatalf("surviving owner's migration audit lost: %q %v", retiredRef, err)
	}
	if err := svc.DeleteReview(ctx, "rvw_b", reviewer); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM repositories").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("unreferenced identity retained: %d %v", remaining, err)
	}
}

func nullableTarget(actual, expected, value string) any {
	if actual == expected {
		return value
	}
	return nil
}
