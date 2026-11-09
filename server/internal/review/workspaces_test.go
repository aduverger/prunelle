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

func discoveryEnv(t *testing.T) (*review.Service, *store.DB, string, fixture.Repos) {
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
	return review.NewService(db, []string{root}), db, root, repos
}

func TestWorkspaceDiscoveryIsOneLevelReadOnlyAndRecognizesWorktrees(t *testing.T) {
	svc, db, root, repos := discoveryEnv(t)
	ctx := context.Background()
	must[fixture.Repos](t)(fixture.BuildRepos(filepath.Join(root, "too-deep", "nested")))
	fixtureGit(t, root, "init", "--bare", filepath.Join(root, "bare"))
	outside := must[fixture.Repos](t)(fixture.BuildRepos(filepath.Join(t.TempDir(), "outside")))
	if err := os.Symlink(outside.API, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	candidates := must[[]review.WorkspaceCandidate](t)(svc.DiscoverWorkspaces(ctx))
	if len(candidates) != 2 || candidates[0].Name != "ecomapping" || candidates[1].Name != "unblock_elems" || len(candidates[0].Repositories) != 1 || len(candidates[1].Repositories) != 4 {
		t.Fatalf("candidates = %+v", candidates)
	}
	for _, candidate := range candidates {
		if candidate.WorkspaceID != nil || filepath.Dir(candidate.Path) != root {
			t.Fatalf("unexpected registration/path: %+v", candidate)
		}
	}
	if got := must[[]review.Workspace](t)(svc.ListWorkspaces(ctx)); len(got) != 0 {
		t.Fatalf("discovery registered workspaces: %+v", got)
	}
	if got := must[[]review.Review](t)(svc.ListReviews(ctx, nil)); len(got) != 0 {
		t.Fatalf("discovery created reviews: %+v", got)
	}
	for _, path := range []string{root, filepath.Join(root, "escape"), filepath.Join(root, "too-deep", "nested", "api"), "relative"} {
		_, err := svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: path}, reviewer)
		expectError(t, err, review.CodeInvalidArgument)
	}
	// A configured root stays excluded even when it is another root's immediate child.
	nestedRoots := review.NewService(db, []string{root, root, filepath.Dir(repos.API)})
	for _, candidate := range must[[]review.WorkspaceCandidate](t)(nestedRoots.DiscoverWorkspaces(ctx)) {
		if candidate.Path == root || candidate.Path == filepath.Dir(repos.API) {
			t.Fatalf("configured root became workspace: %+v", candidate)
		}
	}
}

func TestReviewContextCreatesAtomicallyDefaultsTitleAndDoesNotGuess(t *testing.T) {
	svc, db, root, repos := discoveryEnv(t)
	ctx := context.Background()
	single := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: filepath.Join(root, "ecomapping")}, reviewer))
	_, err := svc.ResolveReview(ctx, repos.API)
	expectError(t, err, review.CodeInvalidArgument, "has no reviews")
	candidate := must[[]review.WorkspaceCandidate](t)(svc.DiscoverWorkspaces(ctx))[1]
	must[review.Workspace](t)(svc.CreateWorkspace(ctx, review.CreateWorkspaceRequest{Name: candidate.Name, Repositories: candidate.Repositories}, reviewer))
	_, err = svc.ResolveReview(ctx, repos.API)
	expectError(t, err, review.CodeInvalidArgument, "has no reviews")
	group := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: filepath.Join(repos.API, "src")}, reviewer))
	if group.Title != "unblock_elems" || group.WorkspaceID == single.WorkspaceID {
		t.Fatalf("group review = %+v", group)
	}
	ws := must[review.Workspace](t)(svc.GetWorkspace(ctx, group.WorkspaceID))
	if ws.RootPath == nil || *ws.RootPath != filepath.Dir(repos.API) || len(ws.Repositories) != 4 {
		t.Fatalf("workspace = %+v", ws)
	}
	resolved := must[review.Review](t)(svc.ResolveReview(ctx, filepath.Join(repos.Worker, "worker")))
	if resolved.ID != group.ID {
		t.Fatalf("resolved = %+v", resolved)
	}
	captured := must[review.Revision](t)(svc.CaptureRevision(ctx, resolved.ID, reviewer))
	if len(captured.Snapshots) != 4 {
		t.Fatalf("capture omitted members: %+v", captured)
	}
	other := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: repos.API, Title: "Second"}, reviewer))
	if other.WorkspaceID != group.WorkspaceID {
		t.Fatal("repeated creation duplicated workspace")
	}
	_, err = svc.ResolveReview(ctx, repos.API)
	expectError(t, err, review.CodeConflict, group.ID, other.ID)
	if got := must[review.Review](t)(svc.ResolveReview(ctx, filepath.Join(root, "ecomapping", "src"))); got.ID != single.ID {
		t.Fatalf("unrelated reviews changed context: %+v", got)
	}
	// A failed first-review insert must roll back registration and its event as well.
	if err := svc.DeleteWorkspace(ctx, single.WorkspaceID, reviewer); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER reject_review BEFORE INSERT ON reviews BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: filepath.Join(root, "ecomapping")}, reviewer)
	if err == nil || !strings.Contains(err.Error(), "fixture failure") {
		t.Fatalf("expected injected failure: %v", err)
	}
	if got := must[[]review.Workspace](t)(svc.ListWorkspaces(ctx)); len(got) != 1 || got[0].ID != group.WorkspaceID {
		t.Fatalf("failed creation left registration: %+v", got)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, `DROP TRIGGER reject_review`); return err }); err != nil {
		t.Fatal(err)
	}
	retried := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: filepath.Join(root, "ecomapping")}, reviewer))
	if resolved := must[review.Review](t)(svc.ResolveReview(ctx, filepath.Join(root, "ecomapping"))); resolved.ID != retried.ID {
		t.Fatalf("creation did not recover after rollback: %+v", resolved)
	}
}

func TestConcurrentReviewCreationReusesOneWorkspace(t *testing.T) {
	svc, _, root, _ := discoveryEnv(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for i := range 4 {
		wg.Go(func() {
			_, err := svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: filepath.Join(root, "ecomapping"), Title: fmt.Sprint(i)}, reviewer)
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := must[[]review.Workspace](t)(svc.ListWorkspaces(ctx)); len(got) != 1 {
		t.Fatalf("workspaces = %+v", got)
	}
	if got := must[[]review.Review](t)(svc.ListReviews(ctx, nil)); len(got) != 4 {
		t.Fatalf("reviews = %+v", got)
	}
}

func TestDiscoveryReusesLegacyMembershipWithoutChangingSettings(t *testing.T) {
	svc, _, root, _ := discoveryEnv(t)
	ctx := context.Background()
	candidate := must[[]review.WorkspaceCandidate](t)(svc.DiscoverWorkspaces(ctx))[0]
	include := false
	candidate.Repositories[0].BaseRef = "origin/main"
	candidate.Repositories[0].IncludeUntracked = &include
	legacy := must[review.Workspace](t)(svc.CreateWorkspace(ctx, review.CreateWorkspaceRequest{Name: "Existing manual", Repositories: candidate.Repositories}, reviewer))
	rv := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: candidate.Path}, reviewer))
	if rv.WorkspaceID != legacy.ID || rv.Title != legacy.Name {
		t.Fatalf("legacy registration not reused: %+v", rv)
	}
	ws := must[review.Workspace](t)(svc.GetWorkspace(ctx, legacy.ID))
	if ws.Repositories[0].BaseRef != "origin/main" || ws.Repositories[0].IncludeUntracked || ws.Repositories[0].ID != legacy.Repositories[0].ID {
		t.Fatalf("registration settings rewritten: %+v", ws)
	}
	_, err := svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: candidate.Path, BaseRef: "main"}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "existing settings are not changed")
	// Discovering extra repositories must not silently rewrite registered membership.
	fixtureGit(t, root, "init", "-b", "main", filepath.Join(root, "unblock_elems", "new-repo"))
	group := filepath.Join(root, "unblock_elems")
	must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: group}, reviewer))
	fixtureGit(t, root, "init", "-b", "main", filepath.Join(group, "another-repo"))
	_, err = svc.ResolveReview(ctx, group)
	expectError(t, err, review.CodeConflict, "repositories changed")
}

func TestAmbiguousLegacyWorkspacesRequireExplicitID(t *testing.T) {
	svc, _, _, _ := discoveryEnv(t)
	ctx := context.Background()
	candidate := must[[]review.WorkspaceCandidate](t)(svc.DiscoverWorkspaces(ctx))[0]
	req := review.CreateWorkspaceRequest{Name: "Manual", Repositories: candidate.Repositories}
	first := must[review.Workspace](t)(svc.CreateWorkspace(ctx, req, reviewer))
	second := must[review.Workspace](t)(svc.CreateWorkspace(ctx, req, reviewer))
	_, err := svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: candidate.Path}, reviewer)
	expectError(t, err, review.CodeConflict, first.ID, second.ID)
	rv := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspaceID: second.ID}, reviewer))
	if rv.WorkspaceID != second.ID {
		t.Fatal("explicit override ignored")
	}
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

func TestHardDeleteRemovesHistoryButPreservesFilesSharedBlobsAndOtherWorkspaces(t *testing.T) {
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
	must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, reviewer))
	second := must[review.Review](t)(e.svc.CreateReview(e.ctx, review.CreateReviewRequest{WorkspaceID: e.review.WorkspaceID}, reviewer))
	must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, second.ID, reviewer))
	must[review.ImportResult](t)(e.svc.Import(e.ctx, second.ID, review.ImportRequest{Manifest: fixture.Manifest()}, reviewer))
	otherWS := must[review.Workspace](t)(e.svc.CreateWorkspace(e.ctx, fixture.WorkspaceRequest(e.repos), reviewer))
	other := must[review.Review](t)(e.svc.CreateReview(e.ctx, review.CreateReviewRequest{WorkspaceID: otherWS.ID}, reviewer))
	otherRevision := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, other.ID, reviewer))
	must[review.ImportResult](t)(e.svc.Import(e.ctx, other.ID, review.ImportRequest{Manifest: fixture.Manifest()}, reviewer))
	must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, other.ID, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Keep this other workspace's discussion"}, reviewer))
	must[review.Decision](t)(e.svc.CreateDecision(e.ctx, other.ID, review.CreateDecisionRequest{Decision: "COMMENT", Body: "Keep this other workspace's decision"}, reviewer))
	otherContent := e.reviewContent(t, other.ID)
	otherFile := must[review.FileContent](t)(e.svc.GetFile(e.ctx, otherRevision.ID, otherRevision.Snapshots[0].RepositoryID, "src/routes/upload.ts"))
	before := map[string]string{}
	for _, path := range []string{e.repos.API, e.repos.Worker} {
		before[path] = repositoryState(t, path)
	}
	if err := e.svc.DeleteWorkspace(e.ctx, e.review.WorkspaceID, reviewer); err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		got := repositoryState(t, path)
		if got != want {
			t.Fatalf("repository changed during deletion: %s", path)
		}
	}
	_, err := e.svc.GetWorkspace(e.ctx, e.review.WorkspaceID)
	expectError(t, err, review.CodeNotFound)
	remaining := must[[]review.Review](t)(e.svc.ListReviews(e.ctx, nil))
	if len(remaining) != 1 || remaining[0].ID != other.ID {
		t.Fatalf("remaining reviews = %+v", remaining)
	}
	after := must[review.FileContent](t)(e.svc.GetFile(e.ctx, otherRevision.ID, otherRevision.Snapshots[0].RepositoryID, "src/routes/upload.ts"))
	if !reflect.DeepEqual(after, otherFile) {
		t.Fatal("shared blob/other workspace content lost")
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
		t.Fatal("other workspace's semantic content, discussions or decisions changed")
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
	if err := e.svc.DeleteWorkspace(e.ctx, e.review.WorkspaceID, reviewer); err != nil {
		t.Fatal("repeated deletion failed:", err)
	}
}

func TestCaptureRejectsRepositoryReplacedWithEscapingSymlink(t *testing.T) {
	e := newEnv(t)
	outside := must[fixture.Repos](t)(fixture.BuildRepos(filepath.Join(t.TempDir(), "outside")))
	if err := os.Rename(e.repos.API, e.repos.API+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside.API, e.repos.API); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CaptureRevision(e.ctx, e.review.ID, reviewer)
	expectError(t, err, review.CodeCaptureFailed, "outside the configured allowed roots")
	if revisions := must[[]review.Revision](t)(e.svc.ListRevisions(e.ctx, e.review.ID)); len(revisions) != 1 {
		t.Fatal("escaping capture stored a partial revision")
	}
	must[review.FileContent](t)(e.svc.GetFile(e.ctx, e.revision.ID, e.revision.Snapshots[0].RepositoryID, "src/routes/upload.ts"))
}

func TestDeleteRollbackAndReadOnlyRediscovery(t *testing.T) {
	svc, db, root, _ := discoveryEnv(t)
	ctx := context.Background()
	path := filepath.Join(root, "ecomapping")
	rv := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: path}, reviewer))
	must[review.Revision](t)(svc.CaptureRevision(ctx, rv.ID, reviewer))
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER reject_delete BEFORE DELETE ON workspaces BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteWorkspace(ctx, rv.WorkspaceID, reviewer); err == nil {
		t.Fatal("injected deletion failure ignored")
	}
	if revisions := must[[]review.Revision](t)(svc.ListRevisions(ctx, rv.ID)); len(revisions) != 1 {
		t.Fatal("deletion partially committed")
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, `DROP TRIGGER reject_delete`); return err }); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteWorkspace(ctx, rv.WorkspaceID, reviewer); err != nil {
		t.Fatal(err)
	}
	var blobs int
	if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM blobs`).Scan(&blobs); err != nil || blobs != 0 {
		t.Fatalf("unreferenced stored content survived deletion: %d, %v", blobs, err)
	}
	candidates := must[[]review.WorkspaceCandidate](t)(svc.DiscoverWorkspaces(ctx))
	if len(candidates) != 2 || candidates[0].WorkspaceID != nil {
		t.Fatalf("rediscovery restored registration: %+v", candidates)
	}
	if workspaces := must[[]review.Workspace](t)(svc.ListWorkspaces(ctx)); len(workspaces) != 0 {
		t.Fatal("discovery resurrected workspace")
	}
	fresh := must[review.Review](t)(svc.CreateReview(ctx, review.CreateReviewRequest{WorkspacePath: path}, reviewer))
	if fresh.WorkspaceID == rv.WorkspaceID || fresh.ID == rv.ID {
		t.Fatal("fresh creation reused deleted history identity")
	}
	if err := svc.DeleteWorkspace(ctx, rv.WorkspaceID, reviewer); err != nil {
		t.Fatal(err)
	}
	must[review.Review](t)(svc.GetReview(ctx, fresh.ID))
}
