package review_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aduverger/prunelle/server/internal/events"
	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
)

// expectError fails unless err is a review error with code whose text contains every fragment.
func expectError(t *testing.T, err error, code string, fragments ...string) {
	t.Helper()
	var reviewErr *review.Error
	if !errors.As(err, &reviewErr) || reviewErr.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q does not mention %q", err, f)
		}
	}
}

func must[T any](t *testing.T) func(T, error) T {
	return func(value T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

// rootTitles lists root topics in order, checking that their positions are 0, 1, 2...
func (e *env) rootTitles(t *testing.T) []string {
	t.Helper()
	var titles []string
	for _, topic := range must[[]review.Topic](t)(e.svc.ListTopics(e.ctx, e.review.ID)) {
		if topic.ParentID == nil {
			if topic.Position != len(titles) {
				t.Errorf("root %q at position %d, want %d", topic.Title, topic.Position, len(titles))
			}
			titles = append(titles, topic.Title)
		}
	}
	return titles
}

func TestTopicEditsRequireTheCurrentVersion(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	created := must[review.Topic](t)(e.svc.CreateTopic(e.ctx, e.review.ID, review.CreateTopicRequest{
		ParentID: &[]string{ids["topics"]["T-async"]}[0], Title: " Upload limits ", Summary: "Size limits on upload.",
		IntentIDs: []string{ids["intents"]["I-1"]}, FindingIDs: []string{ids["findings"]["F-1"], ids["findings"]["F-1"]},
	}, reviewer))
	if created.Origin != "MANUAL" || created.Author != reviewer || created.Title != "Upload limits" || created.Position != 1 ||
		created.Version != 1 || !slices.Equal(created.FindingIDs, []string{ids["findings"]["F-1"]}) {
		t.Fatalf("created = %+v", created)
	}

	newTitle := "Upload size limits"
	edit := review.UpdateTopicRequest{Title: &newTitle, FindingIDs: &[]string{ids["findings"]["F-2"]}}
	updated := must[review.Topic](t)(e.svc.UpdateTopic(e.ctx, created.ID, 1, edit, reviewer))
	if updated.Title != newTitle || updated.Version != 2 || !slices.Equal(updated.FindingIDs, []string{ids["findings"]["F-2"]}) ||
		!slices.Equal(updated.IntentIDs, created.IntentIDs) {
		t.Errorf("updated = %+v", updated)
	}
	_, err := e.svc.UpdateTopic(e.ctx, created.ID, 1, edit, reviewer)
	expectError(t, err, review.CodePreconditionFailed, "version 2, not 1")
}

func TestMoveKeepsSiblingOrderDenseAndRejectsCycles(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	tests := must[review.Topic](t)(e.svc.GetTopic(e.ctx, ids["topics"]["T-tests"]))
	status := must[review.Topic](t)(e.svc.GetTopic(e.ctx, ids["topics"]["T-status"]))

	moved := must[review.Topic](t)(e.svc.MoveTopic(e.ctx, tests.ID, tests.Version, review.MoveTopicRequest{Position: 0}, reviewer))
	if moved.Position != 0 {
		t.Errorf("moved position = %d", moved.Position)
	}
	if got := e.rootTitles(t); !slices.Equal(got, []string{"Configuration and tests", "Asynchronous upload", "Processing status flow"}) {
		t.Errorf("roots = %v", got)
	}
	shifted := must[review.Topic](t)(e.svc.GetTopic(e.ctx, status.ID))
	if shifted.Position != 2 || shifted.Version == status.Version {
		t.Errorf("a sibling whose position changed keeps its version: %+v", shifted)
	}

	// Moving the first root under another topic, past the end, clamps to the end; the old siblings close the gap.
	async := ids["topics"]["T-async"]
	under := must[review.Topic](t)(e.svc.MoveTopic(e.ctx, tests.ID, moved.Version, review.MoveTopicRequest{ParentID: &async, Position: 9}, reviewer))
	if *under.ParentID != async || under.Position != 1 {
		t.Errorf("moved under T-async = %+v", under)
	}
	if got := e.rootTitles(t); !slices.Equal(got, []string{"Asynchronous upload", "Processing status flow"}) {
		t.Errorf("roots after move = %v", got)
	}

	parent := must[review.Topic](t)(e.svc.GetTopic(e.ctx, async))
	worker := ids["topics"]["T-worker"]
	_, err := e.svc.MoveTopic(e.ctx, async, parent.Version, review.MoveTopicRequest{ParentID: &worker}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "descendants")
	_, err = e.svc.MoveTopic(e.ctx, async, parent.Version, review.MoveTopicRequest{ParentID: &async}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "descendants")
}

func TestObsoleteTopicReleasesItsFiles(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	before := must[review.SourceChanges](t)(e.svc.SourceChanges(e.ctx, e.revision.ID))
	tests := must[review.Topic](t)(e.svc.GetTopic(e.ctx, ids["topics"]["T-tests"]))
	onlyTests := 0
	for _, repo := range before.Repositories {
		for _, f := range repo.Files {
			if slices.Equal(f.TopicIDs, []string{tests.ID}) {
				onlyTests++
			}
		}
	}
	if onlyTests == 0 {
		t.Fatal("fixture has no file owned only by T-tests")
	}
	obsolete := "OBSOLETE"
	must[review.Topic](t)(e.svc.UpdateTopic(e.ctx, tests.ID, tests.Version, review.UpdateTopicRequest{State: &obsolete}, reviewer))

	after := must[review.SourceChanges](t)(e.svc.SourceChanges(e.ctx, e.revision.ID))
	if after.UnassignedCount != before.UnassignedCount+onlyTests {
		t.Errorf("unassigned %d → %d", before.UnassignedCount, after.UnassignedCount)
	}
	for _, repo := range after.Repositories {
		for _, f := range repo.Files {
			if slices.Contains(f.TopicIDs, tests.ID) {
				t.Errorf("%s still claimed by the obsolete topic", f.Path)
			}
		}
	}
}

func TestMutationsRejectRecordsOfAnotherReview(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	other := must[review.Review](t)(e.svc.CreateReview(e.ctx, review.CreateReviewRequest{WorkspaceID: e.review.WorkspaceID, Title: "Other"}, reviewer))
	must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, other.ID, reviewer))
	topic := must[review.Topic](t)(e.svc.CreateTopic(e.ctx, other.ID, review.CreateTopicRequest{Title: "Elsewhere"}, reviewer))
	foreignFinding := ids["findings"]["F-1"]

	_, err := e.svc.UpdateTopic(e.ctx, topic.ID, topic.Version, review.UpdateTopicRequest{FindingIDs: &[]string{foreignFinding}}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "finding_ids[0]", "not a finding of this review")
	_, err = e.svc.CreateTopic(e.ctx, other.ID, review.CreateTopicRequest{Title: "Child", ParentID: &[]string{ids["topics"]["T-async"]}[0]}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "parent_id")
	_, err = e.svc.CreateDiscussion(e.ctx, other.ID, review.CreateDiscussionRequest{Body: "?",
		Anchor: review.AnchorInput{Type: "FINDING", ID: &foreignFinding}}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "anchor.id")
	_, err = e.svc.CreateDecision(e.ctx, other.ID, review.CreateDecisionRequest{Decision: "REQUEST_CHANGES", FindingIDs: []string{foreignFinding}}, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "finding_ids[0]")
}

func TestRepresentationsAreValidatedLikeImports(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	tests := ids["topics"]["T-tests"]
	diagram := review.CreateRepresentationRequest{Type: "DIAGRAM", RepresentationConfig: review.RepresentationConfig{Diagram: &review.DiagramConfig{
		Nodes: []review.DiagramNode{
			{ID: "suite", Label: "Test suite", Kind: "COMPONENT", EvidenceIDs: []string{ids["evidence"]["E-test-run"]}},
			{ID: "consumer", Label: "Consumer", Kind: "SERVICE", Ref: &review.EntityRef{Type: "TOPIC", ID: ids["topics"]["T-worker"]}},
		},
		Edges: []review.DiagramEdge{{From: "suite", To: "consumer", Label: "covers"}},
	}}}
	created := must[review.Representation](t)(e.svc.CreateRepresentation(e.ctx, tests, diagram, reviewer))
	if created.Origin != "MANUAL" || created.Title != "Diagram" || !created.Available || *created.Version != 1 || *created.RevisionID != e.revision.ID {
		t.Fatalf("created = %+v", created)
	}

	invalid := review.CreateRepresentationRequest{Type: "CODE_DIFF", RepresentationConfig: review.RepresentationConfig{
		CodeDiff: &review.EvidenceListConfig{EvidenceIDs: []string{ids["evidence"]["E-test-run"]}}}}
	_, err := e.svc.CreateRepresentation(e.ctx, tests, invalid, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "is not CODE_RANGE evidence")

	badEdge := review.UpdateRepresentationRequest{RepresentationConfig: review.RepresentationConfig{Diagram: &review.DiagramConfig{
		Nodes: diagram.Diagram.Nodes, Edges: []review.DiagramEdge{{From: "suite", To: "nowhere"}}}}}
	_, err = e.svc.UpdateRepresentation(e.ctx, *created.ID, 1, badEdge, reviewer)
	expectError(t, err, review.CodeInvalidArgument, "unknown node")

	title := "Test coverage map"
	updated := must[review.Representation](t)(e.svc.UpdateRepresentation(e.ctx, *created.ID, 1, review.UpdateRepresentationRequest{Title: &title}, reviewer))
	if updated.Title != title || *updated.Version != 2 || len(updated.Diagram.Nodes) != 2 {
		t.Errorf("updated = %+v", updated)
	}
	_, err = e.svc.UpdateRepresentation(e.ctx, *created.ID, 1, review.UpdateRepresentationRequest{Title: &title}, reviewer)
	expectError(t, err, review.CodePreconditionFailed)
}

func TestDiscussionAnchorsReachTheirTopics(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	topics := ids["topics"]
	workerRepo := e.revision.Snapshots[1].RepositoryID
	reps := must[[]review.Representation](t)(e.svc.TopicRepresentations(e.ctx, topics["T-status"]))
	statusDiagram := reps[slices.IndexFunc(reps, func(r review.Representation) bool { return r.Type == "DIAGRAM" })].ID
	id := func(s string) *string { return &s }

	cases := []struct {
		name   string
		anchor review.AnchorInput
		topics []string
	}{
		{"review", review.AnchorInput{Type: "REVIEW"}, nil},
		{"topic", review.AnchorInput{Type: "TOPIC", ID: id(topics["T-tests"])}, []string{topics["T-tests"]}},
		{"finding", review.AnchorInput{Type: "FINDING", ID: id(ids["findings"]["F-1"])}, []string{topics["T-worker"]}},
		{"evidence through a finding and a change", review.AnchorInput{Type: "EVIDENCE", ID: id(ids["evidence"]["E-retry"])},
			[]string{topics["T-status"], topics["T-worker"]}},
		{"diagram element", review.AnchorInput{Type: "DIAGRAM_ELEMENT", ID: statusDiagram, ElementID: id("failed")}, []string{topics["T-status"]}},
		{"code range in a file owned by two topics", review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{
			RepositoryID: workerRepo, Path: "worker/consumer.py", Side: "HEAD", StartLine: 1, EndLine: 2}},
			[]string{topics["T-status"], topics["T-worker"]}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Anchor: tc.anchor, Body: "Why?"}, reviewer))
			want := tc.topics
			if want == nil {
				want = []string{}
			}
			slices.Sort(want)
			if !slices.Equal(d.TopicIDs, want) {
				t.Errorf("topic_ids = %v, want %v", d.TopicIDs, want)
			}
			if d.RevisionID != e.revision.ID || d.Status != "OPEN" || d.Author != reviewer || len(d.Messages) != 1 || d.Messages[0].Body != "Why?" {
				t.Errorf("discussion = %+v", d)
			}
		})
	}

	codeRange := must[[]review.Discussion](t)(e.svc.ListDiscussions(e.ctx, e.review.ID))[5].Anchor.CodeRange
	if codeRange == nil || codeRange.BlobHash == "" || !strings.HasPrefix(codeRange.SelectedText, "from worker.config import") {
		t.Errorf("code range anchor = %+v", codeRange)
	}
}

func TestInvalidAnchorsAreRejected(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	workerRepo := e.revision.Snapshots[1].RepositoryID
	topic := ids["topics"]["T-tests"]
	reps := must[[]review.Representation](t)(e.svc.TopicRepresentations(e.ctx, ids["topics"]["T-status"]))
	element := "nowhere"
	cases := []struct {
		name     string
		anchor   review.AnchorInput
		fragment string
	}{
		{"lines outside the file", review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{
			RepositoryID: workerRepo, Path: "worker/consumer.py", Side: "HEAD", StartLine: 1, EndLine: 999}}, "are outside"},
		{"unchanged file", review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{
			RepositoryID: workerRepo, Path: "README.md", Side: "HEAD", StartLine: 1, EndLine: 1}}, "not found"},
		{"record of another type", review.AnchorInput{Type: "FINDING", ID: &topic}, "not a finding"},
		{"missing target", review.AnchorInput{Type: "TOPIC"}, "anchor.id: is required"},
		{"unknown diagram node", review.AnchorInput{Type: "DIAGRAM_ELEMENT", ID: reps[1].ID, ElementID: &element}, "no node"},
		{"text is not a diagram", review.AnchorInput{Type: "DIAGRAM_ELEMENT", ID: reps[0].ID, ElementID: &element}, "is not a diagram"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Anchor: tc.anchor, Body: "?"}, reviewer)
			expectError(t, err, review.CodeInvalidArgument, tc.fragment)
		})
	}
	if got := must[[]review.Discussion](t)(e.svc.ListDiscussions(e.ctx, e.review.ID)); len(got) != 0 {
		t.Errorf("rejected discussions were stored: %d", len(got))
	}
}

func TestReplyInvalidatesTheVersionUsedToResolve(t *testing.T) {
	e := newEnv(t)
	d := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Ship it?"}, reviewer))
	agentOwner := review.Author{Kind: "HUMAN", ID: "author@example.com"}
	reply := must[review.Message](t)(e.svc.Reply(e.ctx, d.ID, review.CreateMessageRequest{Body: "Not yet, see F-1."}, agentOwner))
	if reply.DiscussionID != d.ID || reply.Author != agentOwner {
		t.Errorf("reply = %+v", reply)
	}

	_, err := e.svc.UpdateDiscussion(e.ctx, d.ID, d.Version, review.UpdateDiscussionRequest{Status: "RESOLVED"}, reviewer)
	expectError(t, err, review.CodePreconditionFailed)
	current := must[review.Discussion](t)(e.svc.GetDiscussion(e.ctx, d.ID))
	resolved := must[review.Discussion](t)(e.svc.UpdateDiscussion(e.ctx, d.ID, current.Version, review.UpdateDiscussionRequest{Status: "RESOLVED"}, reviewer))
	if resolved.Status != "RESOLVED" || len(resolved.Messages) != 2 || resolved.Messages[1].ID != reply.ID {
		t.Errorf("resolved = %+v", resolved)
	}
}

func TestDecisionsStayTiedToTheirRevision(t *testing.T) {
	e := newEnv(t)
	ids := e.importFixture(t).IDs
	thread := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{
		Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Add a FAILED status."}, reviewer))

	invalid := []struct {
		name     string
		req      review.CreateDecisionRequest
		fragment string
	}{
		{"request changes without selection", review.CreateDecisionRequest{Decision: "REQUEST_CHANGES", Body: "Fix it"}, "at least one"},
		{"approve with selection", review.CreateDecisionRequest{Decision: "APPROVE", FindingIDs: []string{ids["findings"]["F-1"]}}, "only REQUEST_CHANGES"},
		{"comment without body", review.CreateDecisionRequest{Decision: "COMMENT"}, "body: is required"},
	}
	for _, tc := range invalid {
		_, err := e.svc.CreateDecision(e.ctx, e.review.ID, tc.req, reviewer)
		expectError(t, err, review.CodeInvalidArgument, tc.fragment)
	}

	requested := must[review.Decision](t)(e.svc.CreateDecision(e.ctx, e.review.ID, review.CreateDecisionRequest{
		Decision: "REQUEST_CHANGES", Body: "Guard retries.", FindingIDs: []string{ids["findings"]["F-1"]}, DiscussionIDs: []string{thread.ID}}, reviewer))
	second := must[review.Revision](t)(e.svc.CaptureRevision(e.ctx, e.review.ID, reviewer))
	approved := must[review.Decision](t)(e.svc.CreateDecision(e.ctx, e.review.ID, review.CreateDecisionRequest{Decision: "APPROVE"}, reviewer))

	decisions := must[[]review.Decision](t)(e.svc.ListDecisions(e.ctx, e.review.ID))
	if len(decisions) != 2 || decisions[0].ID != requested.ID || decisions[0].RevisionID != e.revision.ID ||
		!slices.Equal(decisions[0].DiscussionIDs, []string{thread.ID}) || decisions[1].ID != approved.ID || decisions[1].RevisionID != second.ID {
		t.Errorf("decisions = %+v", decisions)
	}
}

func TestEveryChangeAppendsOneEventWithItsActor(t *testing.T) {
	e := newEnv(t)
	importer := review.Author{Kind: "HUMAN", ID: "importer@example.com"}
	must[review.ImportResult](t)(e.svc.Import(e.ctx, e.review.ID, review.ImportRequest{Manifest: fixture.Manifest()}, importer))
	topic := must[review.Topic](t)(e.svc.CreateTopic(e.ctx, e.review.ID, review.CreateTopicRequest{Title: "Notes"}, reviewer))
	_, err := e.svc.CreateTopic(e.ctx, e.review.ID, review.CreateTopicRequest{Title: " "}, reviewer)
	expectError(t, err, review.CodeInvalidArgument)
	d := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{
		Anchor: review.AnchorInput{Type: "TOPIC", ID: &topic.ID}, Body: "Draft"}, reviewer))
	must[review.Message](t)(e.svc.Reply(e.ctx, d.ID, review.CreateMessageRequest{Body: "Agreed"}, reviewer))

	log := must[[]events.Event](t)(events.After(e.ctx, e.db, 0, 100))
	var got []string
	for i, ev := range log {
		got = append(got, ev.Type)
		if i > 0 && ev.Sequence <= log[i-1].Sequence {
			t.Errorf("sequence %d after %d", ev.Sequence, log[i-1].Sequence)
		}
	}
	want := []string{"workspace.created", "review.created", "revision.created", "import.created", "topic.created", "discussion.created", "discussion.updated"}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if imported := log[3]; imported.Actor != events.Actor(importer) || *imported.ReviewID != e.review.ID {
		t.Errorf("import event = %+v", imported)
	}
	if reply := log[6]; reply.EntityID != d.ID || reply.Actor != events.Actor(reviewer) {
		t.Errorf("reply event = %+v", reply)
	}
}
