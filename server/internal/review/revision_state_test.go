package review_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
)

func graphAt(t *testing.T, e *env, revisionID string) *review.ReviewGraph {
	t.Helper()
	view := must[review.RevisionView](t)(e.svc.GetRevisionView(e.ctx, e.review.ID, &revisionID))
	if view.Graph == nil {
		t.Fatalf("semantic view unavailable: %v", view.UnavailableReason)
	}
	return view.Graph
}

func TestRevisionViewsFreezeSemanticContentWithoutChangingIdentity(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	before := graphAt(t, e, e.revision.ID)
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	unchanged := must[review.RevisionComparison](t)(e.svc.CompareRevisions(e.ctx, e.review.ID, e.revision.ID, second.ID))
	for _, topic := range unchanged.Topics {
		if topic.Change != "UNCHANGED" {
			t.Fatalf("capture alone changed topic: %+v", topic)
		}
	}
	for _, finding := range unchanged.Findings {
		if finding.Change != "UNCHANGED" {
			t.Fatalf("capture alone changed finding: %+v", finding)
		}
	}
	topic := must[review.Topic](t)(e.svc.GetTopic(e.ctx, ids["topics"]["T-status"]))
	title := "Human edited status flow"
	must[review.Topic](t)(e.svc.UpdateTopic(e.ctx, topic.ID, topic.Version, review.UpdateTopicRequest{Title: &title}, reviewer))
	reps := must[[]review.Representation](t)(e.svc.TopicRepresentations(e.ctx, topic.ID))
	diagram := reps[slices.IndexFunc(reps, func(r review.Representation) bool { return r.Type == "DIAGRAM" })]
	config := *diagram.Diagram
	config.Nodes = slices.Clone(config.Nodes)
	config.Nodes[0].Label = "Human edited diagram"
	must[review.Representation](t)(e.svc.UpdateRepresentation(e.ctx, *diagram.ID, *diagram.Version, review.UpdateRepresentationRequest{RepresentationConfig: review.RepresentationConfig{Diagram: &config}}, reviewer))
	finding := before.Findings[0]
	status := "RESOLVED"
	must[review.Finding](t)(e.svc.UpdateFinding(e.ctx, finding.ID, finding.Version+1, review.UpdateFindingRequest{RevisionID: second.ID, Status: &status}, reviewer))
	added := must[review.Topic](t)(e.svc.CreateTopic(e.ctx, e.review.ID, review.CreateTopicRequest{Title: "New manual topic"}, reviewer))
	changed := graphAt(t, e, second.ID)
	third := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	if got := graphAt(t, e, e.revision.ID); !reflect.DeepEqual(got, before) {
		t.Fatal("old semantic view changed after later edits/captures")
	}
	if got := graphAt(t, e, second.ID); !reflect.DeepEqual(got, changed) {
		t.Fatal("second revision was not frozen faithfully")
	}
	latest := graphAt(t, e, third.ID)
	if latest.Topics[slices.IndexFunc(latest.Topics, func(v review.Topic) bool { return v.ID == topic.ID })].Title != title {
		t.Fatal("capture discarded human title")
	}
	comparison := must[review.RevisionComparison](t)(e.svc.CompareRevisions(e.ctx, e.review.ID, e.revision.ID, third.ID))
	edited := comparison.Topics[slices.IndexFunc(comparison.Topics, func(v review.EntityComparison) bool { return v.ID == topic.ID })]
	if edited.Change != "CHANGED" || !slices.Contains(edited.ChangedFields, "title") || !slices.Contains(edited.ChangedFields, "representations") {
		t.Fatalf("changed reasons: %+v", edited)
	}
	if comparison.Topics[slices.IndexFunc(comparison.Topics, func(v review.EntityComparison) bool { return v.ID == added.ID })].Change != "ADDED" {
		t.Fatal("new topic not identified")
	}
	if comparison.Findings[slices.IndexFunc(comparison.Findings, func(v review.EntityComparison) bool { return v.ID == finding.ID })].Change != "RESOLVED" {
		t.Fatal("finding was not explicitly resolved")
	}
	_, err := e.svc.UpdateTopic(e.ctx, topic.ID, topic.Version, review.UpdateTopicRequest{Title: &title}, reviewer)
	expectError(t, err, review.CodePreconditionFailed)
	_, err = e.svc.Import(e.ctx, e.review.ID, review.ImportRequest{RevisionID: &e.revision.ID, Manifest: fixture.Manifest()}, reviewer)
	expectError(t, err, review.CodeConflict)
	_, err = e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{RevisionID: &e.revision.ID, Anchor: review.AnchorInput{Type: "TOPIC", ID: &added.ID}, Body: "Future topic"}, reviewer)
	expectError(t, err, review.CodeInvalidArgument)
}

func TestSourceCorrectionsAreCanonicalConditionalAndRevisionScoped(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	graph := graphAt(t, e, e.revision.ID)
	source := graph.SourceChanges
	repo := source.Repositories[0]
	file := repo.Files[slices.IndexFunc(repo.Files, func(v review.SourceFile) bool { return v.Path == "src/routes/upload.ts" })]
	originalIDs := slices.Clone(file.TopicIDs)
	req := review.UpdateSourceTopicsRequest{RepositoryID: repo.RepositoryID, Path: file.Path, Mode: "EXPLICIT", TopicIDs: []string{ids["topics"]["T-tests"], ids["topics"]["T-worker"]}}
	edited := must[review.SourceFile](t)(e.svc.UpdateSourceTopics(e.ctx, e.revision.ID, file.AssignmentVersion, req, reviewer))
	if len(edited.TopicIDs) != 2 || edited.AssignmentMode != "EXPLICIT" {
		t.Fatalf("assignment: %+v", edited)
	}
	discussion := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Body: "Corrected context", Anchor: review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{RepositoryID: repo.RepositoryID, Path: file.Path, Side: "HEAD", StartLine: 1, EndLine: 1}}}, reviewer))
	if !slices.Equal(discussion.TopicIDs, edited.TopicIDs) {
		t.Fatal("thread disagrees with canonical file assignment")
	}
	_, err := e.svc.UpdateSourceTopics(e.ctx, e.revision.ID, file.AssignmentVersion, req, reviewer)
	expectError(t, err, review.CodePreconditionFailed)
	req.TopicIDs = []string{}
	empty := must[review.SourceFile](t)(e.svc.UpdateSourceTopics(e.ctx, e.revision.ID, edited.AssignmentVersion, req, reviewer))
	if !empty.Unassigned {
		t.Fatal("explicit empty assignment was treated as derived")
	}
	current := graphAt(t, e, e.revision.ID)
	for _, rep := range current.Representations {
		if rep.Type == "CODE_DIFF" && slices.Contains(originalIDs, rep.TopicID) {
			if slices.ContainsFunc(*rep.SourceFiles, func(v review.SourceFileRef) bool { return v.RepositoryID == repo.RepositoryID && v.Path == file.Path }) {
				t.Fatal("topic code ignored explicit removal")
			}
			for _, id := range rep.CodeDiff.EvidenceIDs {
				evidence := current.Evidence[slices.IndexFunc(current.Evidence, func(v review.Evidence) bool { return v.ID == id })]
				if evidence.CodeRange.Path == file.Path && evidence.CodeRange.RepositoryID == repo.RepositoryID {
					t.Fatal("code range bypassed explicit removal")
				}
			}
		}
	}
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	next := graphAt(t, e, second.ID).SourceChanges.Repositories[0].Files[slices.IndexFunc(repo.Files, func(v review.SourceFile) bool { return v.Path == file.Path })]
	if !next.Unassigned || next.AssignmentMode != "EXPLICIT" {
		t.Fatal("capture lost deliberate unassignment")
	}
	req.Mode = "DERIVED"
	_, err = e.svc.UpdateSourceTopics(e.ctx, e.revision.ID, empty.AssignmentVersion, req, reviewer)
	expectError(t, err, review.CodeConflict)
}

func TestSourceCorrectionResetAndMissingPathDoNotResurrectChoices(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	source := graphAt(t, e, e.revision.ID).SourceChanges
	repo := source.Repositories[0]
	req := review.UpdateSourceTopicsRequest{RepositoryID: repo.RepositoryID, Path: "docs/async-upload.md", Mode: "EXPLICIT", TopicIDs: []string{ids["topics"]["T-tests"]}}
	assigned := must[review.SourceFile](t)(e.svc.UpdateSourceTopics(e.ctx, e.revision.ID, 1, req, reviewer))
	req.Mode = "DERIVED"
	req.TopicIDs = []string{}
	reset := must[review.SourceFile](t)(e.svc.UpdateSourceTopics(e.ctx, e.revision.ID, assigned.AssignmentVersion, req, reviewer))
	if !reset.Unassigned || reset.AssignmentVersion <= assigned.AssignmentVersion {
		t.Fatal("reset lost version or revived explicit association")
	}
	req.Mode = "EXPLICIT"
	req.TopicIDs = []string{ids["topics"]["T-tests"]}
	must[review.SourceFile](t)(e.svc.UpdateSourceTopics(e.ctx, e.revision.ID, reset.AssignmentVersion, req, reviewer))
	if err := os.Remove(filepath.Join(e.repos.API, req.Path)); err != nil {
		t.Fatal(err)
	}
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	if err := os.WriteFile(filepath.Join(e.repos.API, req.Path), []byte("A different document"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	for _, file := range graphAt(t, e, third.ID).SourceChanges.Repositories[0].Files {
		if file.Path == req.Path && file.AssignmentMode != "DERIVED" {
			t.Fatal("an absent correction resurrected on a later same-named file")
		}
	}
	_, err := e.svc.UpdateSourceTopics(e.ctx, second.ID, 1, req, reviewer)
	expectError(t, err, review.CodeConflict)
}

func TestCaptureArchiveRollsBackAndConcurrentCapturesKeepPredecessors(t *testing.T) {
	e := newEnv(t)
	e.importFixture(t)
	before := graphAt(t, e, e.revision.ID)
	if err := e.db.Write(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, `CREATE TRIGGER fail_snapshot BEFORE INSERT ON repo_snapshots BEGIN SELECT RAISE(ABORT,'fixture snapshot failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer)
	if err == nil || !strings.Contains(err.Error(), "fixture snapshot failure") {
		t.Fatalf("expected injected failure: %v", err)
	}
	if got := graphAt(t, e, e.revision.ID); !reflect.DeepEqual(got, before) {
		t.Fatal("failed capture altered current graph")
	}
	var frozen int
	if err := e.db.Reader().QueryRowContext(e.ctx, "SELECT COUNT(*) FROM revision_review_states").Scan(&frozen); err != nil || frozen != 0 {
		t.Fatalf("failed capture sealed history: %d %v", frozen, err)
	}
	if err := e.db.Write(e.ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(e.ctx, "DROP TRIGGER fail_snapshot"); return err }); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer)
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	revisions := must[[]review.Revision](t)(e.svc.ListRevisions(e.ctx, e.review.ID))
	if len(revisions) != 3 {
		t.Fatal("concurrent capture lost a revision")
	}
	for _, revision := range revisions {
		graphAt(t, e, revision.ID)
	}
}

func TestOriginalAnchorsAndFindingLifecycleRemainExplicit(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	repo := e.revision.Snapshots[0].RepositoryID
	code := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Body: "Original line", Anchor: review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{RepositoryID: repo, Path: "src/routes/upload.ts", Side: "HEAD", StartLine: 2, EndLine: 3}}}, reviewer))
	if err := os.WriteFile(filepath.Join(e.repos.API, "src/routes/upload.ts"), []byte("// added prefix\n"+*must[review.FileContent](t)(e.svc.GetFile(e.ctx, e.revision.ID, repo, "src/routes/upload.ts")).HeadText), 0o644); err != nil {
		t.Fatal(err)
	}
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	statuses := must[[]review.DiscussionAnchorStatus](t)(e.svc.DiscussionAnchorStatuses(e.ctx, e.review.ID, second.ID))
	if statuses[0].Status != "OUTDATED" {
		t.Fatalf("shifted line was silently mapped: %+v", statuses)
	}
	stored := must[review.Discussion](t)(e.svc.GetDiscussion(e.ctx, code.ID))
	if !reflect.DeepEqual(stored.Anchor, code.Anchor) {
		t.Fatal("original anchor changed")
	}
	must[review.Decision](t)(e.svc.CreateDecision(e.ctx, e.review.ID, review.CreateDecisionRequest{RevisionID: &second.ID, Decision: "REQUEST_CHANGES", DiscussionIDs: []string{code.ID}}, reviewer))
	findingID := ids["findings"]["F-1"]
	for _, status := range []string{"RESOLVED", "OPEN", "DISMISSED"} {
		g := graphAt(t, e, second.ID)
		finding := g.Findings[slices.IndexFunc(g.Findings, func(v review.Finding) bool { return v.ID == findingID })]
		updated := must[review.Finding](t)(e.svc.UpdateFinding(e.ctx, finding.ID, finding.Version, review.UpdateFindingRequest{RevisionID: second.ID, Status: &status}, reviewer))
		if updated.FirstSeenRevisionID != e.revision.ID || updated.Author != finding.Author {
			t.Fatal("finding lifecycle rewrote source identity")
		}
	}
	if must[review.Discussion](t)(e.svc.GetDiscussion(e.ctx, code.ID)).Status != "OPEN" {
		t.Fatal("finding status automatically resolved a thread")
	}
}

func TestRevisionViewAndComparisonUseBoundedReader(t *testing.T) {
	e := newEnv(t)
	e.importFixture(t)
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	e.db.Reader().SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	must[review.RevisionView](t)(e.svc.GetRevisionView(ctx, e.review.ID, &e.revision.ID))
	must[review.RevisionComparison](t)(e.svc.CompareRevisions(ctx, e.review.ID, e.revision.ID, second.ID))
	_, err := e.svc.CompareRevisions(ctx, e.review.ID, second.ID, e.revision.ID)
	expectError(t, err, review.CodeInvalidArgument)
}

func TestFrozenSemanticAnchorsUseOriginalGraphAndMembership(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	id := func(value string) *string { return &value }
	reps := must[[]review.Representation](t)(e.svc.TopicRepresentations(e.ctx, ids["topics"]["T-status"]))
	diagram := reps[slices.IndexFunc(reps, func(v review.Representation) bool { return v.Type == "DIAGRAM" })]
	anchors := []review.AnchorInput{
		{Type: "TOPIC", ID: id(ids["topics"]["T-status"])},
		{Type: "INTENT", ID: id(ids["intents"]["I-1"])},
		{Type: "CHANGE", ID: id(ids["changes"]["C-1"])},
		{Type: "FINDING", ID: id(ids["findings"]["F-1"])},
		{Type: "EVIDENCE", ID: id(ids["evidence"]["E-retry"])},
		{Type: "DIAGRAM_ELEMENT", ID: diagram.ID, ElementID: id("failed")},
	}
	original := []review.Discussion{}
	for _, anchor := range anchors {
		original = append(original, must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Anchor: anchor, Body: "Preserve original semantic context"}, reviewer)))
	}
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	for _, topic := range graphAt(t, e, second.ID).Topics {
		empty := []string{}
		must[review.Topic](t)(e.svc.UpdateTopic(e.ctx, topic.ID, topic.Version, review.UpdateTopicRequest{IntentIDs: &empty, ChangeIDs: &empty, FindingIDs: &empty, EvidenceIDs: &empty}, reviewer))
	}
	latestReps := must[[]review.Representation](t)(e.svc.TopicRepresentations(e.ctx, ids["topics"]["T-status"]))
	latestDiagram := latestReps[slices.IndexFunc(latestReps, func(v review.Representation) bool { return v.Type == "DIAGRAM" })]
	replacement := review.DiagramConfig{Nodes: []review.DiagramNode{{ID: "different", Label: "New diagram", Kind: "STATE", EvidenceIDs: []string{}}}, Edges: []review.DiagramEdge{}}
	must[review.Representation](t)(e.svc.UpdateRepresentation(e.ctx, *latestDiagram.ID, *latestDiagram.Version, review.UpdateRepresentationRequest{RepresentationConfig: review.RepresentationConfig{Diagram: &replacement}}, reviewer))
	for _, before := range original {
		after := must[review.Discussion](t)(e.svc.GetDiscussion(e.ctx, before.ID))
		if !reflect.DeepEqual(before.Anchor, after.Anchor) || !slices.Equal(before.TopicIDs, after.TopicIDs) {
			t.Fatalf("old %s context was reprojected through current links", before.Anchor.Type)
		}
	}
	must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{RevisionID: &e.revision.ID, Anchor: anchors[len(anchors)-1], Body: "The original diagram node remains valid"}, reviewer))
	statuses := must[[]review.DiscussionAnchorStatus](t)(e.svc.DiscussionAnchorStatuses(e.ctx, e.review.ID, second.ID))
	diagramStatus := statuses[slices.IndexFunc(statuses, func(v review.DiscussionAnchorStatus) bool { return v.DiscussionID == original[len(original)-1].ID })]
	if diagramStatus.Status != "OUTDATED" {
		t.Fatal("missing diagram element was not flagged")
	}
	laterFinding := e.importFixture(t).IDs["findings"]["F-1"]
	_, err := e.svc.CreateDecision(e.ctx, e.review.ID, review.CreateDecisionRequest{RevisionID: &e.revision.ID, Decision: "REQUEST_CHANGES", FindingIDs: []string{laterFinding}}, reviewer)
	expectError(t, err, review.CodeInvalidArgument)
}

func TestEmptyAndBinarySourceAssignmentsAreNotSyntheticEvidence(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	for path, content := range map[string][]byte{"empty.txt": {}, "binary.dat": {0, 1, 2, 0}} {
		if err := os.WriteFile(filepath.Join(e.repos.API, path), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	before := graphAt(t, e, second.ID)
	for _, path := range []string{"empty.txt", "binary.dat"} {
		must[review.SourceFile](t)(e.svc.UpdateSourceTopics(e.ctx, second.ID, 1, review.UpdateSourceTopicsRequest{RepositoryID: second.Snapshots[0].RepositoryID, Path: path, Mode: "EXPLICIT", TopicIDs: []string{ids["topics"]["T-tests"]}}, reviewer))
	}
	after := graphAt(t, e, second.ID)
	if len(after.Evidence) != len(before.Evidence) {
		t.Fatal("source grouping fabricated evidence")
	}
	rep := after.Representations[slices.IndexFunc(after.Representations, func(v review.Representation) bool {
		return v.Type == "CODE_DIFF" && v.TopicID == ids["topics"]["T-tests"]
	})]
	for _, path := range []string{"empty.txt", "binary.dat"} {
		if !slices.ContainsFunc(*rep.SourceFiles, func(v review.SourceFileRef) bool { return v.Path == path }) {
			t.Fatal("assigned file absent from canonical code selection")
		}
	}
	comparison := must[review.RevisionComparison](t)(e.svc.CompareRevisions(e.ctx, e.review.ID, e.revision.ID, second.ID))
	topic := comparison.Topics[slices.IndexFunc(comparison.Topics, func(v review.EntityComparison) bool { return v.ID == ids["topics"]["T-tests"] })]
	if !slices.Contains(topic.ChangedFields, "source_topics") {
		t.Fatal("manual source grouping was omitted from semantic comparison")
	}
}

func TestAnchorValidityUsesStoredSidesWithoutGuessingLinesOrDeletion(t *testing.T) {
	e := newEnv(t)
	e.importFixture(t)
	original := must[review.FileContent](t)(e.svc.GetFile(e.ctx, e.revision.ID, e.revision.Snapshots[0].RepositoryID, "src/routes/upload.ts"))
	threads := map[string]review.Discussion{}
	for _, side := range []string{"BASE", "HEAD"} {
		threads[side] = must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Body: "Keep original " + side, Anchor: review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{RepositoryID: original.RepositoryID, Path: original.Path, Side: side, StartLine: 1, EndLine: 2}}}, reviewer))
	}
	for _, test := range []struct{ name, content, head, base string }{
		{"identical", *original.HeadText, "UNCHANGED", "UNCHANGED"},
		{"inserted lines", "// added line\n" + *original.HeadText, "OUTDATED", "UNCHANGED"},
		{"repeated text", *original.HeadText + *original.HeadText, "OUTDATED", "UNCHANGED"},
		{"binary", "\x00binary\x00", "UNAVAILABLE", "UNAVAILABLE"},
		{"not in changed files", *original.BaseText, "UNAVAILABLE", "UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(e.repos.API, original.Path), []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			next := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
			statuses := must[[]review.DiscussionAnchorStatus](t)(e.svc.DiscussionAnchorStatuses(e.ctx, e.review.ID, next.ID))
			for side, thread := range threads {
				status := statuses[slices.IndexFunc(statuses, func(v review.DiscussionAnchorStatus) bool { return v.DiscussionID == thread.ID })]
				want := test.head
				if side == "BASE" {
					want = test.base
				}
				if status.Status != want {
					t.Fatalf("%s validity=%s, want %s", side, status.Status, want)
				}
				persisted := must[review.Discussion](t)(e.svc.GetDiscussion(e.ctx, thread.ID))
				if !reflect.DeepEqual(persisted.Anchor, thread.Anchor) {
					t.Fatal("validity check rewrote the original anchor")
				}
			}
		})
	}
}
