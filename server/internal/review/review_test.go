package review_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

var reviewer = review.Actor{Author: review.Author{Kind: "HUMAN", ID: "reviewer@example.com"}, Login: "reviewer@example.com"}

type env struct {
	ctx      context.Context
	db       *store.DB
	svc      *review.Service
	repos    fixture.Repos
	review   review.Review
	revision review.Revision
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, filepath.Join(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reposDir := filepath.Join(root, "repos")
	repos, err := fixture.BuildRepos(reposDir)
	if err != nil {
		t.Fatal(err)
	}
	svc := review.NewService(db, "main")
	rv, err := svc.CreateReview(ctx, fixture.ReviewRequest(repos), reviewer)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := svc.CaptureRevision(ctx, rv.ID, review.CaptureRequest{}, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	return &env{ctx: ctx, db: db, svc: svc, repos: repos, review: rv, revision: revision}
}

func (e *env) importFixture(t *testing.T) review.ImportResult {
	t.Helper()
	result, err := e.svc.Import(e.ctx, e.review.ID, review.ImportRequest{Manifest: fixture.Manifest()}, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCaptureSpansBothRepositories(t *testing.T) {
	e := newEnv(t)
	if e.revision.Status != "CONSISTENT" || e.revision.Ordinal != 1 || len(e.revision.Snapshots) != 2 {
		t.Fatalf("revision = %+v", e.revision)
	}
	for _, snap := range e.revision.Snapshots {
		if snap.Branch != fixture.FeatureBranch || snap.BaseSHA == snap.HeadSHA {
			t.Errorf("snapshot %s = %+v", snap.RepositoryLabel, snap)
		}
	}
	changes, err := e.svc.SourceChanges(e.ctx, e.revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, repo := range changes.Repositories {
		for _, f := range repo.Files {
			got[repo.RepositoryLabel+"/"+f.Path] = f.Status
		}
	}
	want := map[string]string{
		"api/docs/async-upload.md": "UNTRACKED", "api/package.json": "MODIFIED", "api/src/processing.ts": "DELETED",
		"api/src/queue.ts": "ADDED", "api/src/routes/status.ts": "ADDED", "api/src/routes/upload.ts": "MODIFIED",
		"worker/tests/test_consumer.py": "ADDED", "worker/worker/config.py": "MODIFIED",
		"worker/worker/consumer.py": "ADDED", "worker/worker/processing.py": "ADDED",
	}
	if len(got) != len(want) {
		t.Fatalf("changed files = %v", got)
	}
	for path, status := range want {
		if got[path] != status {
			t.Errorf("%s = %q, want %q", path, got[path], status)
		}
	}
	if changes.UnassignedCount != len(want) {
		t.Errorf("before import every file is unassigned, got %d", changes.UnassignedCount)
	}
}

func TestImportLinksTopicsToCanonicalRecords(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	agent := review.Author{Kind: "AGENT", ID: "pi:fixture-session"}

	topics, err := e.svc.ListTopics(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 4 {
		t.Fatalf("topics = %d", len(topics))
	}
	worker, err := e.svc.GetTopic(e.ctx, ids["topics"]["T-worker"])
	if err != nil {
		t.Fatal(err)
	}
	if *worker.ParentID != ids["topics"]["T-async"] || !slices.Equal(worker.FindingIDs, []string{ids["findings"]["F-1"]}) {
		t.Errorf("worker topic = %+v", worker)
	}

	findings, err := e.svc.ListFindings(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Severity != "HIGH" || findings[0].Provenance != "INFERRED" || findings[0].Author != agent {
		t.Fatalf("findings = %+v", findings)
	}

	evidence, err := e.svc.ListEvidence(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	retryUses := 0
	for _, list := range [][]string{findings[0].EvidenceIDs, findings[1].EvidenceIDs} {
		if slices.Contains(list, ids["evidence"]["E-retry"]) {
			retryUses++
		}
	}
	if len(evidence) != 10 || retryUses != 2 {
		t.Errorf("evidence rows = %d, E-retry shared by %d findings", len(evidence), retryUses)
	}

	retry, err := e.svc.GetEvidence(e.ctx, ids["evidence"]["E-retry"])
	if err != nil {
		t.Fatal(err)
	}
	if retry.Provenance != "OBSERVED" || retry.Author != agent ||
		!strings.HasPrefix(retry.CodeRange.SelectedText, "    for attempt in range") || !strings.HasSuffix(retry.CodeRange.SelectedText, "return") {
		t.Errorf("code evidence = %+v", retry)
	}
	testRun, err := e.svc.GetEvidence(e.ctx, ids["evidence"]["E-test-run"])
	if err != nil {
		t.Fatal(err)
	}
	if testRun.Provenance != "MEASURED" || testRun.Author != agent || testRun.CodeRange != nil {
		t.Errorf("reported evidence = %+v", testRun)
	}
	deleted, err := e.svc.GetEvidence(e.ctx, ids["evidence"]["E-sync"])
	if err != nil {
		t.Fatal(err)
	}
	if deleted.CodeRange.Side != "BASE" || !strings.Contains(deleted.CodeRange.SelectedText, "processDocument") {
		t.Errorf("base-side evidence = %+v", deleted.CodeRange)
	}

	changes, err := e.svc.ListChanges(e.ctx, e.review.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.Provenance != "OBSERVED" || c.Author != agent {
			t.Errorf("change %s = %s by %+v", c.Summary, c.Provenance, c.Author)
		}
	}
	if changes[1].VerificationStatus != "SUPPORTED" || changes[0].VerificationStatus != "UNVERIFIED" {
		t.Errorf("verification stored as declared: %s, %s", changes[0].VerificationStatus, changes[1].VerificationStatus)
	}
	topic, err := e.svc.GetTopic(e.ctx, ids["topics"]["T-async"])
	if err != nil || topic.Author != agent {
		t.Errorf("topic author = %+v, %v", topic.Author, err)
	}
	attention, err := e.svc.ListAttention(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attention) != 1 || attention[0].Target != (review.AttentionTarget{Type: "FINDING", ID: ids["findings"]["F-1"]}) {
		t.Errorf("attention = %+v", attention)
	}
}

func TestSourceChangesListUnassignedFiles(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	changes, err := e.svc.SourceChanges(e.ctx, e.revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	unassigned := map[string][]string{}
	topicsByPath := map[string][]string{}
	for _, repo := range changes.Repositories {
		for _, f := range repo.Files {
			if f.Unassigned {
				unassigned[repo.RepositoryLabel] = append(unassigned[repo.RepositoryLabel], f.Path)
			}
			topicsByPath[repo.RepositoryLabel+"/"+f.Path] = f.TopicIDs
		}
	}
	if len(unassigned) != 1 || !slices.Equal(unassigned["api"], fixture.UnassignedFiles["api"]) {
		t.Fatalf("unassigned = %v", unassigned)
	}
	if changes.UnassignedCount != 2 {
		t.Errorf("unassigned count = %d", changes.UnassignedCount)
	}
	// worker/consumer.py is reached through a change (T-worker), a finding (T-worker, T-status), and directly (none).
	consumer := topicsByPath["worker/worker/consumer.py"]
	for _, key := range []string{"T-worker", "T-status"} {
		if !slices.Contains(consumer, ids["topics"][key]) {
			t.Errorf("consumer.py topics %v miss %s", consumer, key)
		}
	}
}

func TestTopicRepresentationsReportAvailability(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	byType := func(topicKey string) map[string]review.Representation {
		reps, err := e.svc.TopicRepresentations(e.ctx, ids["topics"][topicKey])
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]review.Representation{}
		for _, r := range reps {
			m[r.Type] = r
		}
		if len(m) != 4 {
			t.Fatalf("%s representations = %+v", topicKey, reps)
		}
		return m
	}

	async := byType("T-async")
	for _, typ := range []string{"TEXT", "DIAGRAM", "CODE_DIFF", "EVIDENCE_LIST"} {
		if async[typ].ID == nil || !async[typ].Available {
			t.Errorf("T-async %s = %+v", typ, async[typ])
		}
	}
	if got := async["CODE_DIFF"].CodeDiff.EvidenceIDs; len(got) != 3 {
		t.Errorf("T-async code evidence = %v", got)
	}
	node := async["DIAGRAM"].Diagram.Nodes[3]
	if node.Ref == nil || node.Ref.ID != ids["topics"]["T-worker"] {
		t.Errorf("diagram node ref = %+v", node.Ref)
	}

	worker := byType("T-worker")
	if got := worker["CODE_DIFF"].CodeDiff.EvidenceIDs; len(got) != 2 || worker["CODE_DIFF"].Title != "Retry path" {
		t.Errorf("narrowed code diff = %+v", worker["CODE_DIFF"])
	}

	status := byType("T-status")
	if status["TEXT"].Author == nil || status["TEXT"].Author.Kind != "AGENT" {
		t.Errorf("stored representation author = %+v", status["TEXT"].Author)
	}
	if status["CODE_DIFF"].ID != nil || status["CODE_DIFF"].Author != nil || !status["CODE_DIFF"].Available || status["CODE_DIFF"].Origin != "DERIVED" {
		t.Errorf("derived code diff = %+v", status["CODE_DIFF"])
	}

	tests := byType("T-tests")
	if tests["DIAGRAM"].Available || tests["DIAGRAM"].UnavailableReason == nil {
		t.Errorf("missing diagram = %+v", tests["DIAGRAM"])
	}
}

func TestOldRevisionKeepsItsContentAfterWorktreeChanges(t *testing.T) {
	e := newEnv(t)
	upload := filepath.Join(e.repos.API, "src/routes/upload.ts")
	before, err := os.ReadFile(upload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upload, []byte("// rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	if second.Ordinal != 2 || second.Fingerprint == e.revision.Fingerprint {
		t.Fatalf("second revision = %+v", second)
	}
	apiID := e.revision.Snapshots[0].RepositoryID
	old, err := e.svc.GetFile(e.ctx, e.revision.ID, apiID, "src/routes/upload.ts")
	if err != nil {
		t.Fatal(err)
	}
	if *old.HeadText != string(before) || !strings.Contains(*old.BaseText, "processDocument(doc)") {
		t.Errorf("first revision content changed: %q", *old.HeadText)
	}
	current, err := e.svc.GetFile(e.ctx, second.ID, apiID, "src/routes/upload.ts")
	if err != nil {
		t.Fatal(err)
	}
	if *current.HeadText != "// rewritten\n" {
		t.Errorf("second revision head = %q", *current.HeadText)
	}
	rv, err := e.svc.GetReview(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *rv.CurrentRevisionID != second.ID {
		t.Errorf("current revision = %s", *rv.CurrentRevisionID)
	}
	deleted, err := e.svc.GetFile(e.ctx, second.ID, apiID, "src/processing.ts")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.HeadText != nil || deleted.BaseText == nil {
		t.Errorf("deleted file = %+v", deleted)
	}
}

func TestImportRejectsInvalidManifestWithoutWriting(t *testing.T) {
	e := newEnv(t)
	m := fixture.Manifest()
	m.Topics[1].Changes = append(m.Topics[1].Changes, "C-missing")
	m.Evidence[0].CodeRange.EndLine = 999
	m.Findings[0].Severity = "CRITICAL"
	m.Findings[1].Provenance = "GUESSED"
	m.Topics = append([]review.ManifestTopic{{Key: "T-orphan", Parent: "T-later", Title: "Orphan"}}, m.Topics...)

	_, err := e.svc.Import(e.ctx, e.review.ID, review.ImportRequest{Manifest: m}, reviewer)
	var reviewErr *review.Error
	if !errors.As(err, &reviewErr) || reviewErr.Code != review.CodeInvalidArgument {
		t.Fatalf("err = %v", err)
	}
	joined := strings.Join(reviewErr.Details, "\n")
	for _, want := range []string{`unknown changes key "C-missing"`, "-999 are outside src/routes/upload.ts", `"CRITICAL" is not one of`, `findings[1].provenance: "GUESSED" is not one of`, `parent "T-later" must be a topic listed earlier`} {
		if !strings.Contains(joined, want) {
			t.Errorf("details miss %q:\n%s", want, joined)
		}
	}
	topics, err := e.svc.ListTopics(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := e.svc.ListEvidence(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	imports, err := e.svc.ListImports(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(topics)+len(evidence)+len(imports) != 0 {
		t.Errorf("invalid import wrote %d topics, %d evidence, %d imports", len(topics), len(evidence), len(imports))
	}
}

func TestCreateReviewRejectsInvalidRepositorySets(t *testing.T) {
	e := newEnv(t)
	alias := filepath.Join(t.TempDir(), "api-alias")
	if err := os.Symlink(e.repos.API, alias); err != nil {
		t.Fatal(err)
	}
	for name, repositories := range map[string][]review.CreateRepositoryRequest{
		"non-repository":             {{Label: "invalid", Path: t.TempDir()}},
		"duplicate labels":           {{Label: "same", Path: e.repos.API}, {Label: "same", Path: e.repos.Worker}},
		"duplicate canonical source": {{Label: "api", Path: e.repos.API}, {Label: "alias", Path: alias}},
		"blank label":                {{Label: " ", Path: e.repos.API}},
		"relative server path":       {{Label: "api", Path: "relative/api"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.CreateReview(e.ctx, review.CreateReviewRequest{Repositories: repositories}, reviewer)
			expectError(t, err, review.CodeInvalidArgument)
		})
	}
	remaining := must[[]review.Review](t)(e.svc.ListReviews(e.ctx))
	if len(remaining) != 1 || remaining[0].ID != e.review.ID {
		t.Fatal("invalid repository sets created review metadata")
	}
}

func TestImportRequiresARevisionOfTheSameReview(t *testing.T) {
	e := newEnv(t)
	other, err := e.svc.CreateReview(e.ctx, review.CreateReviewRequest{Path: *e.review.ContextPath, Title: "Other"}, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	var reviewErr *review.Error
	_, err = e.svc.Import(e.ctx, other.ID, review.ImportRequest{Manifest: fixture.Manifest()}, reviewer)
	if !errors.As(err, &reviewErr) || reviewErr.Code != review.CodeInvalidArgument || !strings.Contains(err.Error(), "no revision") {
		t.Fatalf("import before capture: %v", err)
	}
	_, err = e.svc.Import(e.ctx, other.ID, review.ImportRequest{RevisionID: &e.revision.ID, Manifest: fixture.Manifest()}, reviewer)
	if !errors.As(err, &reviewErr) || reviewErr.Code != review.CodeInvalidArgument || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("import into another review's revision: %v", err)
	}
}

func TestEmptyFileHasEmptyContentNotMissingContent(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(e.repos.Worker, "worker", "__init__.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	revision, err := e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	workerID := revision.Snapshots[1].RepositoryID
	file, err := e.svc.GetFile(e.ctx, revision.ID, workerID, "worker/__init__.py")
	if err != nil {
		t.Fatal(err)
	}
	if file.HeadText == nil || *file.HeadText != "" || file.BaseText != nil {
		t.Fatalf("empty added file: base=%v head=%v", file.BaseText, file.HeadText)
	}
}

func TestCaptureOverRevisionLimitFailsWithoutStoringARevision(t *testing.T) {
	e := newEnv(t)
	chunk := bytes.Repeat([]byte("0123456789abcde\n"), (1<<20)/16) // exactly 1 MiB of text
	for i := 0; i*len(chunk) <= review.MaxRevisionContentBytes; i++ {
		if err := os.WriteFile(filepath.Join(e.repos.Worker, fmt.Sprintf("bulk-%02d.txt", i)), chunk, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer)
	var reviewErr *review.Error
	if !errors.As(err, &reviewErr) || reviewErr.Code != review.CodeCaptureFailed {
		t.Fatalf("err = %v", err)
	}
	revisions, err := e.svc.ListRevisions(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	rv, err := e.svc.GetReview(e.ctx, e.review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || *rv.CurrentRevisionID != e.revision.ID {
		t.Errorf("revisions = %d, current = %s", len(revisions), *rv.CurrentRevisionID)
	}
}
