package review_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

func TestVersionFiveUpgradeDoesNotInventEarlierSemanticHistory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	legacy := must[*sql.DB](t)(sql.Open("sqlite", filepath.Join(dir, "prunelle.db")))
	paths := must[[]string](t)(filepath.Glob("../store/migrations/000[1-5]_*.sql"))
	if len(paths) != 5 {
		t.Fatal("released v5 fixture needs exactly migrations 1–5")
	}
	for _, path := range paths {
		migration := must[[]byte](t)(os.ReadFile(path))
		if _, err := legacy.ExecContext(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	const at = "2026-11-01T10:00:00Z"
	for _, statement := range []string{
		`INSERT INTO repositories VALUES ('repo_legacy','/fixture/api','` + at + `',NULL)`,
		`INSERT INTO reviews VALUES ('rvw_legacy',NULL,'Review','','OPEN',NULL,'` + at + `','` + at + `')`,
		`INSERT INTO review_repositories VALUES ('rvw_legacy','repo_legacy','api',1)`,
		`INSERT INTO revisions VALUES ('rev_old','rvw_legacy',1,'` + at + `','first','CONSISTENT')`,
		`INSERT INTO revisions VALUES ('rev_current','rvw_legacy',2,'` + at + `','second','CONSISTENT')`,
		`UPDATE reviews SET current_revision_id='rev_current' WHERE id='rvw_legacy'`,
		`INSERT INTO imports VALUES ('imp_legacy','rvw_legacy','rev_old','AGENT','pi:legacy','` + at + `')`,
		`INSERT INTO topics VALUES ('top_legacy','rvw_legacy',NULL,'Edited current title','Edited current explanation','IMPORTED','ACTIVE',0,'AGENT','pi:legacy','imp_legacy','` + at + `','` + at + `',7)`,
		`INSERT INTO discussions VALUES ('dsc_legacy','rvw_legacy','rev_old','TOPIC','top_legacy',NULL,NULL,NULL,NULL,NULL,NULL,'OPEN','HUMAN','human',1,'` + at + `','` + at + `')`,
		`INSERT INTO discussion_messages VALUES ('msg_legacy','dsc_legacy','HUMAN','human','Original discussion survives','` + at + `')`,
		`PRAGMA user_version=5`,
	} {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			t.Fatalf("v5 fixture: %v", err)
		}
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("old source\n")))
	if _, err := legacy.ExecContext(ctx, `INSERT INTO blobs VALUES (?,11,CAST('old source'||char(10) AS BLOB))`, hash); err != nil {
		t.Fatal(err)
	}
	for i, revisionID := range []string{"rev_old", "rev_current"} {
		snapshotID := fmt.Sprintf("snap_%d", i)
		if _, err := legacy.ExecContext(ctx, `INSERT INTO repo_snapshots VALUES (?,?, 'repo_legacy','main','base','head','feature',0,1,0,'fingerprint','CONSISTENT','[]')`, snapshotID, revisionID); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.ExecContext(ctx, `INSERT INTO snapshot_files VALUES (?,'legacy.ts',NULL,'MODIFIED','100644','100644','base','head',?,?,0,NULL)`, snapshotID, hash, hash); err != nil {
			t.Fatal(err)
		}
	}
	legacy.Close()
	db := must[*store.DB](t)(store.Open(ctx, dir))
	defer db.Close()
	svc := review.NewService(db, "main")
	current := must[review.RevisionView](t)(svc.GetRevisionView(ctx, "rvw_legacy", nil))
	if current.Graph == nil || current.Graph.Topics[0].Title != "Edited current title" || current.Graph.Topics[0].Version != 7 {
		t.Fatal("upgrade lost current metadata")
	}
	oldID := "rev_old"
	old := must[review.RevisionView](t)(svc.GetRevisionView(ctx, "rvw_legacy", &oldID))
	if old.Graph != nil || old.UnavailableReason == nil || old.Editable {
		t.Fatal("upgrade invented an earlier graph")
	}
	content := must[review.FileContent](t)(svc.GetFile(ctx, oldID, "repo_legacy", "legacy.ts"))
	if content.HeadText == nil || *content.HeadText != "old source\n" {
		t.Fatal("legacy source lost")
	}
	source := must[review.SourceChanges](t)(svc.SourceChanges(ctx, oldID))
	if source.AssignmentsAvailable || source.UnassignedCount != 0 || source.Repositories[0].Files[0].Unassigned {
		t.Fatal("unknown history was presented as unassigned source")
	}
	thread := must[review.Discussion](t)(svc.GetDiscussion(ctx, "dsc_legacy"))
	if thread.Messages[0].Body != "Original discussion survives" {
		t.Fatal("legacy messages lost")
	}
	must[review.Message](t)(svc.Reply(ctx, thread.ID, review.CreateMessageRequest{Body: "Still replyable"}, reviewer))
	topicID := "top_legacy"
	_, err := svc.CreateDiscussion(ctx, "rvw_legacy", review.CreateDiscussionRequest{RevisionID: &oldID, Body: "Do not guess context", Anchor: review.AnchorInput{Type: "TOPIC", ID: &topicID}}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "history")
	must[review.Discussion](t)(svc.CreateDiscussion(ctx, "rvw_legacy", review.CreateDiscussionRequest{RevisionID: &oldID, Body: "Known stored source", Anchor: review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{RepositoryID: "repo_legacy", Path: "legacy.ts", Side: "HEAD", StartLine: 1, EndLine: 1}}}, reviewer))
	comparison := must[review.RevisionComparison](t)(svc.CompareRevisions(ctx, "rvw_legacy", oldID, "rev_current"))
	if comparison.UnavailableReason == nil || len(comparison.Topics) != 0 {
		t.Fatal("unknown baseline became everything-added")
	}
}
