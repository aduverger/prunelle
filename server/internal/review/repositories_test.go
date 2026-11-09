package review_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

func fixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func repositoryState(t *testing.T, root string) string {
	t.Helper()
	var snapshot strings.Builder
	snapshot.WriteString(fixtureGit(t, root, "status", "--porcelain=v1", "--untracked-files=all"))
	snapshot.WriteString(fixtureGit(t, root, "show-ref"))
	snapshot.WriteString(fixtureGit(t, root, "log", "--all", "--format=%H"))
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&snapshot, "%s:%x\n", path, sha256.Sum256(contents))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot.String()
}

func sourceEnv(t *testing.T) (*review.Service, *store.DB, string, fixture.Repos) {
	t.Helper()
	ctx := context.Background()
	root := must[string](t)(filepath.EvalSymlinks(t.TempDir()))
	db := must[*store.DB](t)(store.Open(ctx, t.TempDir()))
	t.Cleanup(func() { db.Close() })
	repos := must[fixture.Repos](t)(fixture.BuildRepos(filepath.Join(root, "unblock_elems")))
	fixtureGit(t, repos.API, "worktree", "add", "--detach", filepath.Join(root, "unblock_elems", "repo-c"), "HEAD")
	fixtureGit(t, repos.Worker, "worktree", "add", "--detach", filepath.Join(root, "unblock_elems", "repo-d"), "HEAD")
	standalone := must[fixture.Repos](t)(fixture.BuildRepos(filepath.Join(t.TempDir(), "repos")))
	if err := os.Rename(standalone.API, filepath.Join(root, "ecomapping")); err != nil {
		t.Fatal(err)
	}
	return review.NewService(db, "main"), db, root, repos
}

type reviewContentSnapshot struct {
	review          review.Review
	topics          []review.Topic
	intents         []review.Intent
	changes         []review.ObservedChange
	findings        []review.Finding
	evidence        []review.Evidence
	attention       []review.AttentionSignal
	representations []review.Representation
	imports         []review.Import
	discussions     []review.Discussion
	decisions       []review.Decision
}

func (e *env) reviewContent(t *testing.T, reviewID string) reviewContentSnapshot {
	t.Helper()
	topics := must[[]review.Topic](t)(e.svc.ListTopics(e.ctx, reviewID))
	var representations []review.Representation
	for _, topic := range topics {
		representations = append(representations, must[[]review.Representation](t)(e.svc.TopicRepresentations(e.ctx, topic.ID))...)
	}
	return reviewContentSnapshot{
		review: must[review.Review](t)(e.svc.GetReview(e.ctx, reviewID)), topics: topics,
		intents:         must[[]review.Intent](t)(e.svc.ListIntents(e.ctx, reviewID)),
		changes:         must[[]review.ObservedChange](t)(e.svc.ListChanges(e.ctx, reviewID, nil)),
		findings:        must[[]review.Finding](t)(e.svc.ListFindings(e.ctx, reviewID)),
		evidence:        must[[]review.Evidence](t)(e.svc.ListEvidence(e.ctx, reviewID)),
		attention:       must[[]review.AttentionSignal](t)(e.svc.ListAttention(e.ctx, reviewID)),
		representations: representations,
		imports:         must[[]review.Import](t)(e.svc.ListImports(e.ctx, reviewID)),
		discussions:     must[[]review.Discussion](t)(e.svc.ListDiscussions(e.ctx, reviewID)),
		decisions:       must[[]review.Decision](t)(e.svc.ListDecisions(e.ctx, reviewID)),
	}
}

func TestHardDeletePreservesOtherReviewContentAndFiles(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	id := func(value string) *string { return &value }
	representations := must[[]review.Representation](t)(e.svc.TopicRepresentations(e.ctx, ids["topics"]["T-status"]))
	diagram := representations[slices.IndexFunc(representations, func(r review.Representation) bool { return r.Type == "DIAGRAM" })].ID
	anchors := []review.AnchorInput{
		{Type: "REVIEW"}, {Type: "TOPIC", ID: id(ids["topics"]["T-tests"])},
		{Type: "INTENT", ID: id(ids["intents"]["I-1"])}, {Type: "CHANGE", ID: id(ids["changes"]["C-1"])},
		{Type: "FINDING", ID: id(ids["findings"]["F-1"])}, {Type: "EVIDENCE", ID: id(ids["evidence"]["E-retry"])},
		{Type: "DIAGRAM_ELEMENT", ID: diagram, ElementID: id("failed")},
		{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{RepositoryID: e.revision.Snapshots[1].RepositoryID, Path: "worker/consumer.py", Side: "HEAD", StartLine: 1, EndLine: 2}},
	}
	var discussionID string
	for _, anchor := range anchors {
		discussion := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Anchor: anchor, Body: "Deletion fixture"}, reviewer))
		discussionID = discussion.ID
		must[review.Message](t)(e.svc.Reply(e.ctx, discussion.ID, review.CreateMessageRequest{Body: "A reply"}, reviewer))
	}
	must[review.Decision](t)(e.svc.CreateDecision(e.ctx, e.review.ID, review.CreateDecisionRequest{Decision: "REQUEST_CHANGES", FindingIDs: []string{ids["findings"]["F-1"]}, DiscussionIDs: []string{discussionID}}, reviewer))
	must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	other := must[review.Review](t)(e.svc.CreateReview(e.ctx, review.CreateReviewRequest{Path: *e.review.ContextPath, Title: "Other"}, reviewer))
	otherRevision := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, other.ID, review.CaptureRequest{}, reviewer))
	must[review.ImportResult](t)(e.svc.Import(e.ctx, other.ID, review.ImportRequest{Manifest: fixture.Manifest()}, reviewer))
	must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, other.ID, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Keep this other review's discussion"}, reviewer))
	must[review.Decision](t)(e.svc.CreateDecision(e.ctx, other.ID, review.CreateDecisionRequest{Decision: "COMMENT", Body: "Keep this other review's decision"}, reviewer))
	otherContent := e.reviewContent(t, other.ID)
	otherFile := must[review.FileContent](t)(e.svc.GetFile(e.ctx, otherRevision.ID, otherRevision.Snapshots[0].RepositoryID, "src/routes/upload.ts"))
	before := map[string]string{}
	for _, path := range []string{e.repos.API, e.repos.Worker} {
		before[path] = repositoryState(t, path)
	}
	if err := e.svc.DeleteReview(e.ctx, e.review.ID, reviewer); err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		got := repositoryState(t, path)
		if got != want {
			t.Fatalf("repository changed during deletion: %s", path)
		}
	}
	_, err := e.svc.GetReview(e.ctx, e.review.ID)
	expectError(t, err, review.CodeNotFound)
	remaining := must[[]review.Review](t)(e.svc.ListReviews(e.ctx))
	if len(remaining) != 1 || remaining[0].ID != other.ID {
		t.Fatalf("remaining reviews = %+v", remaining)
	}
	after := must[review.FileContent](t)(e.svc.GetFile(e.ctx, otherRevision.ID, otherRevision.Snapshots[0].RepositoryID, "src/routes/upload.ts"))
	if !reflect.DeepEqual(after, otherFile) {
		t.Fatal("shared blob/other review content lost")
	}
	rows := must[*sql.Rows](t)(e.db.Reader().QueryContext(e.ctx, "PRAGMA foreign_key_check"))
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation after deletion")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := e.reviewContent(t, other.ID); !reflect.DeepEqual(got, otherContent) {
		t.Fatal("other review's semantic content, discussions or decisions changed")
	}
	for _, topicID := range ids["topics"] {
		_, err := e.svc.GetTopic(e.ctx, topicID)
		expectError(t, err, review.CodeNotFound)
	}
	for _, evidenceID := range ids["evidence"] {
		_, err := e.svc.GetEvidence(e.ctx, evidenceID)
		expectError(t, err, review.CodeNotFound)
	}
	_, err = e.svc.GetDiscussion(e.ctx, discussionID)
	expectError(t, err, review.CodeNotFound)
	if err := e.svc.DeleteReview(e.ctx, e.review.ID, reviewer); err != nil {
		t.Fatal("repeated deletion failed:", err)
	}
}

func TestReviewSourcesAreIndependentAndSelectedOnDemand(t *testing.T) {
	svc, _, root, repos := sourceEnv(t)
	ctx := context.Background()
	single := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{Path: filepath.Join(repos.API, "src")}, reviewer))
	if len(single.Repositories) != 1 || single.Repositories[0].RootPath != repos.API {
		t.Fatalf("unknown first context guessed a parent: %+v", single)
	}
	groupPath := filepath.Dir(repos.API)
	fixtureGit(t, root, "init", "-b", "main", filepath.Join(groupPath, "nested", "deeper-repo"))
	if err := os.Symlink(repos.API, filepath.Join(groupPath, "api-alias")); err != nil {
		t.Fatal(err)
	}
	group := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{Path: groupPath}, reviewer))
	if group.Title != "unblock_elems" || len(group.Repositories) != 4 {
		t.Fatalf("source set = %+v", group)
	}
	fixtureGit(t, root, "init", "-b", "main", filepath.Join(groupPath, "new-repo"))
	_, err := svc.CreateReview(ctx, review.CreateReviewRequest{Path: filepath.Join(repos.API, "src"), InferContext: true}, reviewer)
	expectError(t, err, review.CodeConflict, "multiple source contexts")
	next := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{Path: filepath.Join(repos.Worker, "worker"), InferContext: true}, reviewer))
	if len(next.Repositories) != 5 || next.ID == group.ID {
		t.Fatalf("new review did not rescan independently: %+v", next)
	}
	original := must[review.Review](t)(svc.GetReview(ctx, group.ID))
	if len(original.Repositories) != 4 {
		t.Fatal("creating a review changed an older membership")
	}
	must[review.Revision](t)(svc.CaptureRevision(ctx, group.ID, review.CaptureRequest{}, reviewer))
	_, err = svc.ResolveReview(ctx, repos.API)
	expectError(t, err, review.CodeConflict, single.ID, group.ID, next.ID)
	outside := must[fixture.Repos](t)(fixture.BuildRepos(filepath.Join(t.TempDir(), "outside")))
	includeUntracked := false
	explicit := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{Repositories: []review.CreateRepositoryRequest{{Label: "outside", Path: outside.API}, {Label: "worker", Path: repos.Worker, IncludeUntracked: &includeUntracked}}}, reviewer))
	if explicit.ContextPath != nil || len(explicit.Repositories) != 2 || explicit.Repositories[1].IncludeUntracked {
		t.Fatalf("explicit source set: %+v", explicit)
	}
	reloaded := must[review.Review](t)(svc.GetReview(ctx, group.ID))
	workerIndex := slices.IndexFunc(reloaded.Repositories, func(repository review.Repository) bool { return repository.RootPath == repos.Worker })
	if workerIndex < 0 || !reloaded.Repositories[workerIndex].IncludeUntracked {
		t.Fatal("another review changed the original capture settings")
	}
}

func TestSourceValidationDoesNotReinterpretBrokenGitAsAContainer(t *testing.T) {
	svc, _, root, repos := sourceEnv(t)
	broken := filepath.Join(root, "broken")
	if err := os.Mkdir(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: /missing-prunelle-fixture-repository\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, repos.API, "worktree", "add", "--detach", filepath.Join(broken, "valid-child"), "HEAD")
	_, err := svc.CreateReview(context.Background(), review.CreateReviewRequest{Path: broken}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "repository")
	if listed := must[[]review.Review](t)(svc.ListReviews(context.Background())); len(listed) != 0 {
		t.Fatal("broken source registered a partial review")
	}
}

func TestReviewContextAndCreationRollbackRecover(t *testing.T) {
	svc, db, root, _ := sourceEnv(t)
	ctx := context.Background()
	path := filepath.Join(root, "ecomapping")
	_, err := svc.ResolveReview(ctx, path)
	expectError(t, err, review.CodeInvalidArgument, "no reviews")
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TRIGGER reject_membership BEFORE INSERT ON review_repositories BEGIN SELECT RAISE(ABORT, 'fixture failure'); END")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateReview(ctx, review.CreateReviewRequest{Path: path}, reviewer)
	if err == nil || !strings.Contains(err.Error(), "fixture failure") {
		t.Fatalf("expected injected membership failure: %v", err)
	}
	if got := must[[]review.Review](t)(svc.ListReviews(ctx)); len(got) != 0 {
		t.Fatal("partial review after failed creation")
	}
	var orphanSources int
	if err := db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM repositories").Scan(&orphanSources); err != nil || orphanSources != 0 {
		t.Fatalf("failed creation leaked source identities: %d %v", orphanSources, err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, "DROP TRIGGER reject_membership"); return err }); err != nil {
		t.Fatal(err)
	}
	first := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{Path: path}, reviewer))
	if first.Title != "ecomapping" {
		t.Fatalf("default title: %s", first.Title)
	}
	if got := must[review.Review](t)(svc.ResolveReview(ctx, filepath.Join(path, "src"))); got.ID != first.ID {
		t.Fatal("sole review not resolved")
	}
	second := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{Path: path}, reviewer))
	if second.ID == first.ID {
		t.Fatal("new review reused a review identity")
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE reviews SET status = 'CLOSED' WHERE id = ?", first.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ResolveReview(ctx, path)
	expectError(t, err, review.CodeConflict, first.ID, second.ID)
	if err := svc.DeleteReview(ctx, first.ID, reviewer); err != nil {
		t.Fatal(err)
	}
	if got := must[review.Review](t)(svc.ResolveReview(ctx, path)); got.ID != second.ID {
		t.Fatal("deleting one review removed the other")
	}
}

func TestConcurrentCreationKeepsEveryIndependentReview(t *testing.T) {
	svc, _, root, _ := sourceEnv(t)
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, err := svc.CreateReview(context.Background(), review.CreateReviewRequest{Path: filepath.Join(root, "ecomapping")}, reviewer)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	reviews := must[[]review.Review](t)(svc.ListReviews(context.Background()))
	if len(reviews) != 4 {
		t.Fatalf("lost creations: %d", len(reviews))
	}
}

func TestCaptureDefaultsAndOverridesBelongToSnapshots(t *testing.T) {
	svc, db, root, _ := sourceEnv(t)
	ctx := context.Background()
	path := filepath.Join(root, "ecomapping")
	fixtureGit(t, path, "update-ref", "refs/remotes/origin/main", "HEAD")
	r := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{Path: path}, reviewer))
	old := must[review.Revision](t)(svc.CaptureRevision(ctx, r.ID, review.CaptureRequest{}, reviewer))
	configured := review.NewService(db, "origin/main")
	next := must[review.Revision](t)(configured.CaptureRevision(ctx, r.ID, review.CaptureRequest{}, reviewer))
	explicit := must[review.Revision](t)(configured.CaptureRevision(ctx, r.ID, review.CaptureRequest{BaseRef: "missing", RepositoryBaseRefs: []review.RepositoryBaseRef{{RepositoryID: r.Repositories[0].ID, BaseRef: "main"}}}, reviewer))
	if old.Snapshots[0].BaseRef != "main" || next.Snapshots[0].BaseRef != "origin/main" || explicit.Snapshots[0].BaseRef != "main" {
		t.Fatal("capture precedence/default was not applied")
	}
	if old.Snapshots[0].BaseSHA == next.Snapshots[0].BaseSHA || next.Snapshots[0].BaseSHA != next.Snapshots[0].HeadSHA || explicit.Snapshots[0].BaseSHA != old.Snapshots[0].BaseSHA {
		t.Fatal("requested comparison refs were recorded but not resolved")
	}
	persisted := must[review.Revision](t)(configured.GetRevision(ctx, old.ID))
	if !reflect.DeepEqual(persisted, old) {
		t.Fatal("default change rewrote old snapshots")
	}
	for name, invalid := range map[string]struct {
		input review.CaptureRequest
		code  string
	}{
		"missing ref":        {review.CaptureRequest{BaseRef: "refs/heads/missing"}, review.CodeCaptureFailed},
		"foreign repository": {review.CaptureRequest{RepositoryBaseRefs: []review.RepositoryBaseRef{{RepositoryID: "foreign", BaseRef: "main"}}}, review.CodeInvalidArgument},
		"duplicate override": {review.CaptureRequest{RepositoryBaseRefs: []review.RepositoryBaseRef{{RepositoryID: r.Repositories[0].ID, BaseRef: "main"}, {RepositoryID: r.Repositories[0].ID, BaseRef: "main"}}}, review.CodeInvalidArgument},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := configured.CaptureRevision(ctx, r.ID, invalid.input, reviewer)
			expectError(t, err, invalid.code)
		})
	}
	if revisions := must[[]review.Revision](t)(svc.ListRevisions(ctx, r.ID)); len(revisions) != 3 {
		t.Fatal("failed capture left a partial revision")
	}
	current := must[review.Review](t)(svc.GetReview(ctx, r.ID))
	if current.CurrentRevisionID == nil || *current.CurrentRevisionID != explicit.ID {
		t.Fatal("failed capture changed the current revision")
	}
}

func TestListReviewsReleasesReaderBeforeLoadingMembers(t *testing.T) {
	e := newEnv(t)
	e.db.Reader().SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(e.ctx, time.Second)
	defer cancel()
	listed := must[[]review.Review](t)(e.svc.ListReviews(ctx))
	if len(listed) != 1 || len(listed[0].Repositories) != 2 {
		t.Fatalf("review source context missing: %+v", listed)
	}
}

func TestCaptureSupportsMixedRepositoryRefs(t *testing.T) {
	svc, db, root, repos := sourceEnv(t)
	ctx := context.Background()
	apiPath := filepath.Join(root, "ecomapping")
	fixtureGit(t, repos.Worker, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	r := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{Repositories: []review.CreateRepositoryRequest{{Label: "api", Path: apiPath}, {Label: "worker", Path: repos.Worker}}}, reviewer))
	configured := review.NewService(db, "origin/main")
	_, err := configured.CaptureRevision(ctx, r.ID, review.CaptureRequest{RepositoryBaseRefs: []review.RepositoryBaseRef{{RepositoryID: r.Repositories[1].ID, BaseRef: "refs/heads/missing"}}}, reviewer)
	expectError(t, err, review.CodeCaptureFailed)
	if revisions := must[[]review.Revision](t)(svc.ListRevisions(ctx, r.ID)); len(revisions) != 0 {
		t.Fatal("later repository failure published an earlier partial snapshot")
	}
	if failed := must[review.Review](t)(svc.GetReview(ctx, r.ID)); failed.CurrentRevisionID != nil {
		t.Fatal("failed first capture set a current revision")
	}
	snapshot := must[review.Revision](t)(configured.CaptureRevision(ctx, r.ID, review.CaptureRequest{RepositoryBaseRefs: []review.RepositoryBaseRef{{RepositoryID: r.Repositories[0].ID, BaseRef: "main"}}}, reviewer))
	if snapshot.Snapshots[0].BaseRef != "main" || snapshot.Snapshots[1].BaseRef != "origin/main" {
		t.Fatalf("mixed comparison inputs were lost: %+v", snapshot.Snapshots)
	}
}

func TestDeleteReviewRollbackAndOldIDRetry(t *testing.T) {
	e := newEnv(t)
	e.importFixture(t)
	before := e.reviewContent(t, e.review.ID)
	if err := e.db.Write(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, "CREATE TRIGGER reject_delete BEFORE DELETE ON reviews BEGIN SELECT RAISE(ABORT, 'fixture failure'); END")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteReview(e.ctx, e.review.ID, reviewer); err == nil {
		t.Fatal("injected deletion failure ignored")
	}
	if after := e.reviewContent(t, e.review.ID); !reflect.DeepEqual(after, before) {
		t.Fatal("deletion partially committed")
	}
	if err := e.db.Write(e.ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(e.ctx, "DROP TRIGGER reject_delete"); return err }); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteReview(e.ctx, e.review.ID, reviewer); err != nil {
		t.Fatal(err)
	}
	fresh := must[review.Review](t)(e.svc.CreateReview(e.ctx, review.CreateReviewRequest{Path: *e.review.ContextPath}, reviewer))
	if err := e.svc.DeleteReview(e.ctx, e.review.ID, reviewer); err != nil {
		t.Fatal(err)
	}
	must[review.Review](t)(e.svc.GetReview(e.ctx, fresh.ID))
	var blobs int
	if err := e.db.Reader().QueryRowContext(e.ctx, "SELECT COUNT(*) FROM blobs").Scan(&blobs); err != nil || blobs != 0 {
		t.Fatalf("unused source content survived: %d %v", blobs, err)
	}
}

func TestCaptureRejectsRetargetedRepository(t *testing.T) {
	e := newEnv(t)
	other := must[fixture.Repos](t)(fixture.BuildRepos(filepath.Join(t.TempDir(), "other")))
	if err := os.Rename(e.repos.API, e.repos.API+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other.API, e.repos.API); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer)
	expectError(t, err, review.CodeCaptureFailed, "canonical root")
	must[review.FileContent](t)(e.svc.GetFile(e.ctx, e.revision.ID, e.revision.Snapshots[0].RepositoryID, "src/routes/upload.ts"))
}
