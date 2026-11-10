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
	finding := graphAt(t, e, second.ID).Findings[0]
	status := "RESOLVED"
	must[review.Finding](t)(e.svc.UpdateFinding(e.ctx, finding.ID, finding.Version, review.UpdateFindingRequest{RevisionID: second.ID, Status: &status}, reviewer))
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
	if len(edited.TopicIDs) != 2 || !slices.Contains(edited.TopicIDs, req.TopicIDs[0]) || !slices.Contains(edited.TopicIDs, req.TopicIDs[1]) || edited.AssignmentMode != "EXPLICIT" {
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
	nextFiles := graphAt(t, e, second.ID).SourceChanges.Repositories[0].Files
	next := nextFiles[slices.IndexFunc(nextFiles, func(v review.SourceFile) bool { return v.Path == file.Path })]
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
	files := graphAt(t, e, third.ID).SourceChanges.Repositories[0].Files
	index := slices.IndexFunc(files, func(file review.SourceFile) bool { return file.Path == req.Path })
	if index < 0 || files[index].AssignmentMode != "DERIVED" || !files[index].Unassigned {
		t.Fatal("a same-named new file is missing or resurrected an absent correction")
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
	title := "Still editable after failed capture"
	must[review.Topic](t)(e.svc.UpdateTopic(e.ctx, before.Topics[0].ID, before.Topics[0].Version, review.UpdateTopicRequest{Title: &title}, reviewer))
	expectedFrozen := graphAt(t, e, e.revision.ID)
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
	if !reflect.DeepEqual(graphAt(t, e, e.revision.ID), expectedFrozen) {
		t.Fatal("retry did not freeze the still-editable predecessor faithfully")
	}
}

func TestLifecycleChangesStayExplicitAndIndependent(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	findingID := ids["findings"]["F-1"]
	thread := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Body: "Original concern", Anchor: review.AnchorInput{Type: "FINDING", ID: &findingID}}, reviewer))
	baselineID := e.revision.ID
	for _, test := range []struct{ findingStatus, findingChange, topicState, topicChange string }{
		{"RESOLVED", "RESOLVED", "OBSOLETE", "OBSOLETE"},
		{"OPEN", "REOPENED", "ACTIVE", "REACTIVATED"},
		{"DISMISSED", "DISMISSED", "ACTIVE", "UNCHANGED"},
	} {
		next := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
		current := graphAt(t, e, next.ID)
		finding := current.Findings[slices.IndexFunc(current.Findings, func(v review.Finding) bool { return v.ID == findingID })]
		topic := current.Topics[slices.IndexFunc(current.Topics, func(v review.Topic) bool { return v.ID == ids["topics"]["T-worker"] })]
		if topic.State != test.topicState {
			must[review.Topic](t)(e.svc.UpdateTopic(e.ctx, topic.ID, topic.Version, review.UpdateTopicRequest{State: &test.topicState}, reviewer))
			afterTopic := graphAt(t, e, next.ID)
			if afterTopic.Findings[slices.IndexFunc(afterTopic.Findings, func(v review.Finding) bool { return v.ID == findingID })].Status != finding.Status {
				t.Fatal("topic lifecycle changed finding status")
			}
		}
		updated := must[review.Finding](t)(e.svc.UpdateFinding(e.ctx, finding.ID, finding.Version, review.UpdateFindingRequest{RevisionID: next.ID, Status: &test.findingStatus}, reviewer))
		if updated.Status != test.findingStatus || updated.FirstSeenRevisionID != e.revision.ID || updated.Author != finding.Author {
			t.Fatalf("finding lifecycle lost its explicit status or source identity: %+v", updated)
		}
		comparison := must[review.RevisionComparison](t)(e.svc.CompareRevisions(e.ctx, e.review.ID, baselineID, next.ID))
		findingChange := comparison.Findings[slices.IndexFunc(comparison.Findings, func(v review.EntityComparison) bool { return v.ID == finding.ID })]
		topicChange := comparison.Topics[slices.IndexFunc(comparison.Topics, func(v review.EntityComparison) bool { return v.ID == topic.ID })]
		if findingChange.Change != test.findingChange || topicChange.Change != test.topicChange {
			t.Fatalf("independent lifecycle comparison: finding=%+v topic=%+v", findingChange, topicChange)
		}
		baselineID = next.ID
	}
	if must[review.Discussion](t)(e.svc.GetDiscussion(e.ctx, thread.ID)).Status != "OPEN" {
		t.Fatal("finding status automatically resolved its thread")
	}
	if len(must[[]review.Decision](t)(e.svc.ListDecisions(e.ctx, e.review.ID))) != 0 {
		t.Fatal("lifecycle changes fabricated a review verdict")
	}
	must[review.Decision](t)(e.svc.CreateDecision(e.ctx, e.review.ID, review.CreateDecisionRequest{Decision: "REQUEST_CHANGES", DiscussionIDs: []string{thread.ID}}, reviewer))
}

func TestRevisionViewAndComparisonUseBoundedReader(t *testing.T) {
	e := newEnv(t)
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	empty := graphAt(t, e, e.revision.ID)
	if len(empty.Topics) != 0 || !empty.SourceChanges.AssignmentsAvailable {
		t.Fatal("known empty history was treated as unavailable")
	}
	e.importFixture(t)
	third := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer))
	e.db.Reader().SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	must[review.RevisionView](t)(e.svc.GetRevisionView(ctx, e.review.ID, &second.ID))
	must[review.RevisionComparison](t)(e.svc.CompareRevisions(ctx, e.review.ID, second.ID, third.ID))
	_, err := e.svc.CompareRevisions(ctx, e.review.ID, third.ID, second.ID)
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
	transientConfig := *diagram.Diagram
	transientConfig.Nodes = append(slices.Clone(transientConfig.Nodes), review.DiagramNode{ID: "transient", Label: "Transient node", Kind: "STATE", EvidenceIDs: []string{}})
	edited := must[review.Representation](t)(e.svc.UpdateRepresentation(e.ctx, *diagram.ID, *diagram.Version, review.UpdateRepresentationRequest{RepresentationConfig: review.RepresentationConfig{Diagram: &transientConfig}}, reviewer))
	transientThread := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "DIAGRAM_ELEMENT", ID: diagram.ID, ElementID: id("transient")}, Body: "Discussed before removal"}, reviewer))
	must[review.Representation](t)(e.svc.UpdateRepresentation(e.ctx, *diagram.ID, *edited.Version, review.UpdateRepresentationRequest{RepresentationConfig: review.RepresentationConfig{Diagram: diagram.Diagram}}, reviewer))
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
	for _, revisionID := range []string{e.revision.ID, second.ID} {
		statuses := must[[]review.DiscussionAnchorStatus](t)(e.svc.DiscussionAnchorStatuses(e.ctx, e.review.ID, revisionID))
		status := statuses[slices.IndexFunc(statuses, func(v review.DiscussionAnchorStatus) bool { return v.DiscussionID == transientThread.ID })]
		if status.Status != "UNAVAILABLE" {
			t.Fatalf("original node removed before freeze is %s against %s, want UNAVAILABLE", status.Status, revisionID)
		}
	}
	laterFinding := e.importFixture(t).IDs["findings"]["F-1"]
	_, err := e.svc.CreateDecision(e.ctx, e.review.ID, review.CreateDecisionRequest{RevisionID: &e.revision.ID, Decision: "REQUEST_CHANGES", FindingIDs: []string{laterFinding}}, reviewer)
	expectError(t, err, review.CodeInvalidArgument)

	storedSource := must[review.FileContent](t)(e.svc.GetFile(e.ctx, e.revision.ID, e.revision.Snapshots[0].RepositoryID, "src/routes/upload.ts"))
	if err := e.db.Write(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, "DELETE FROM revision_review_states WHERE revision_id = ?", e.revision.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	unknown := must[review.RevisionView](t)(e.svc.GetRevisionView(e.ctx, e.review.ID, &e.revision.ID))
	if unknown.Graph != nil || unknown.UnavailableReason == nil || unknown.Editable {
		t.Fatal("missing history invented a semantic graph")
	}
	unavailableStatuses := must[[]review.DiscussionAnchorStatus](t)(e.svc.DiscussionAnchorStatuses(e.ctx, e.review.ID, second.ID))
	for _, discussion := range original {
		if unavailableStatuses[slices.IndexFunc(unavailableStatuses, func(v review.DiscussionAnchorStatus) bool { return v.DiscussionID == discussion.ID })].Status != "UNAVAILABLE" {
			t.Fatal("missing original semantics were treated as known or changed")
		}
	}
	source := must[review.SourceChanges](t)(e.svc.SourceChanges(e.ctx, e.revision.ID))
	if source.AssignmentsAvailable || source.UnassignedCount != 0 {
		t.Fatal("unknown ownership was presented as unassigned source")
	}
	for _, repo := range source.Repositories {
		for _, file := range repo.Files {
			if file.Unassigned || len(file.TopicIDs) != 0 {
				t.Fatal("unknown file ownership was guessed")
			}
		}
	}
	if got := must[review.FileContent](t)(e.svc.GetFile(e.ctx, e.revision.ID, storedSource.RepositoryID, storedSource.Path)); !reflect.DeepEqual(got, storedSource) {
		t.Fatal("unavailable semantic history hid or changed immutable source")
	}
	must[review.Message](t)(e.svc.Reply(e.ctx, original[0].ID, review.CreateMessageRequest{Body: "Still replyable without semantic history"}, reviewer))
	_, err = e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{RevisionID: &e.revision.ID, Anchor: anchors[0], Body: "Do not guess missing context"}, reviewer)
	expectError(t, err, review.CodeInvalidArgument)
	must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{RevisionID: &e.revision.ID, Anchor: review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{RepositoryID: storedSource.RepositoryID, Path: storedSource.Path, Side: "HEAD", StartLine: 1, EndLine: 1}}, Body: "Stored source is still known"}, reviewer))
	comparison := must[review.RevisionComparison](t)(e.svc.CompareRevisions(e.ctx, e.review.ID, e.revision.ID, second.ID))
	if comparison.UnavailableReason == nil || len(comparison.Topics) != 0 || len(comparison.Findings) != 0 {
		t.Fatal("unknown baseline was presented as everything added")
	}
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
