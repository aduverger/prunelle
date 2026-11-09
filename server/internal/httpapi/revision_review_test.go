package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/events"
	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
)

func TestRevisionReviewHTTPHistoryCorrectionsAndRestart(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, first, ids := s.seed(t)
	before := must(s.client.GetRevisionViewWithResponse(ctx, rv.ID, &api.GetRevisionViewParams{RevisionId: &first.ID}))
	expectStatus(t, before.StatusCode(), 200, before.Body)
	if before.JSON200.Graph == nil || !before.JSON200.Editable {
		t.Fatal("current graph unavailable")
	}
	file := before.JSON200.Graph.SourceChanges.Repositories[0].Files[0]
	repoID := first.Snapshots[0].RepositoryID
	tag := fmt.Sprintf(`"%d"`, file.AssignmentVersion)
	request := review.UpdateSourceTopicsRequest{RepositoryID: repoID, Path: file.Path, Mode: "EXPLICIT", TopicIDs: []string{ids.IDs["topics"]["T-tests"]}}
	missing := must(s.client.UpdateSourceTopicsWithResponse(ctx, first.ID, nil, request))
	expectStatus(t, missing.StatusCode(), 428, missing.Body)
	sourceEvents := s.openEvents(t, "")
	sourceEvents.next(t)
	corrected := must(s.client.UpdateSourceTopicsWithResponse(ctx, first.ID, &api.UpdateSourceTopicsParams{IfMatch: &tag}, request))
	expectStatus(t, corrected.StatusCode(), 200, corrected.Body)
	if event := sourceEvents.next(t).decode(t); event.Type != "source.topics.updated" || event.ReviewID == nil || *event.ReviewID != rv.ID || event.Actor != (events.Actor{Kind: "HUMAN", ID: ownerLogin}) || event.Login != ownerLogin {
		t.Fatalf("source correction event lost its scope or updater: %+v", event)
	}
	sourceEvents.stop()
	stale := must(s.client.UpdateSourceTopicsWithResponse(ctx, first.ID, &api.UpdateSourceTopicsParams{IfMatch: &tag}, request))
	expectStatus(t, stale.StatusCode(), 412, stale.Body)
	firstGraph := must(s.client.GetRevisionViewWithResponse(ctx, rv.ID, &api.GetRevisionViewParams{RevisionId: &first.ID}))
	second := must(s.client.CaptureRevisionWithResponse(ctx, rv.ID, nil, review.CaptureRequest{}))
	expectStatus(t, second.StatusCode(), 201, second.Body)
	latest := must(s.client.GetRevisionViewWithResponse(ctx, rv.ID, &api.GetRevisionViewParams{RevisionId: &second.JSON201.ID}))
	expectStatus(t, latest.StatusCode(), 200, latest.Body)
	finding := latest.JSON200.Graph.Findings[0]
	status := "RESOLVED"
	findingTag := fmt.Sprintf(`"%d"`, finding.Version)
	findingRequest := review.UpdateFindingRequest{RevisionID: second.JSON201.ID, Status: &status}
	noTag := must(s.client.UpdateFindingWithResponse(ctx, finding.ID, nil, findingRequest))
	expectStatus(t, noTag.StatusCode(), 428, noTag.Body)
	findingEvents := s.openEvents(t, "")
	findingEvents.next(t)
	resolved := must(s.client.UpdateFindingWithResponse(ctx, finding.ID, &api.UpdateFindingParams{IfMatch: &findingTag}, findingRequest))
	expectStatus(t, resolved.StatusCode(), 200, resolved.Body)
	if event := findingEvents.next(t).decode(t); event.Type != "finding.updated" || event.EntityID != finding.ID || event.ReviewID == nil || *event.ReviewID != rv.ID || event.Actor != (events.Actor{Kind: "HUMAN", ID: ownerLogin}) || event.Login != ownerLogin {
		t.Fatalf("finding event lost its scope or updater: %+v", event)
	}
	findingEvents.stop()
	staleFinding := must(s.client.UpdateFindingWithResponse(ctx, finding.ID, &api.UpdateFindingParams{IfMatch: &findingTag}, findingRequest))
	expectStatus(t, staleFinding.StatusCode(), 412, staleFinding.Body)
	findingRequest.RevisionID = first.ID
	resolvedTag := fmt.Sprintf(`"%d"`, resolved.JSON200.Version)
	oldFinding := must(s.client.UpdateFindingWithResponse(ctx, finding.ID, &api.UpdateFindingParams{IfMatch: &resolvedTag}, findingRequest))
	expectStatus(t, oldFinding.StatusCode(), 409, oldFinding.Body)
	oldTarget := must(s.client.UpdateSourceTopicsWithResponse(ctx, first.ID, &api.UpdateSourceTopicsParams{IfMatch: &tag}, request))
	expectStatus(t, oldTarget.StatusCode(), 409, oldTarget.Body)
	comparison := must(s.client.CompareRevisionsWithResponse(ctx, rv.ID, &api.CompareRevisionsParams{FromRevisionId: first.ID, ToRevisionId: second.JSON201.ID}))
	expectStatus(t, comparison.StatusCode(), 200, comparison.Body)
	changed := comparison.JSON200.Findings[slices.IndexFunc(comparison.JSON200.Findings, func(v review.EntityComparison) bool { return v.ID == finding.ID })]
	if changed.Change != "RESOLVED" || !slices.Contains(changed.ChangedFields, "status") {
		t.Fatalf("finding comparison: %+v", changed)
	}
	root := s.root
	s.stop()
	s = startTestServer(t, root, nil)
	frozen := must(s.client.GetRevisionViewWithResponse(ctx, rv.ID, &api.GetRevisionViewParams{RevisionId: &first.ID}))
	expectStatus(t, frozen.StatusCode(), 200, frozen.Body)
	if frozen.JSON200.Editable || !reflect.DeepEqual(frozen.JSON200.Graph, firstGraph.JSON200.Graph) {
		t.Fatal("historical graph changed after advance/restart")
	}
	deleted := must(s.client.DeleteReviewWithResponse(ctx, rv.ID))
	expectStatus(t, deleted.StatusCode(), 204, deleted.Body)
	absent := must(s.client.GetRevisionViewWithResponse(ctx, rv.ID, &api.GetRevisionViewParams{RevisionId: &first.ID}))
	expectStatus(t, absent.StatusCode(), 404, absent.Body)
}

func TestCommittedImportReplaysAfterSemanticHistoryFreezes(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, first, _ := s.seed(t)
	key := "frozen-import-replay"
	request := review.ImportRequest{RevisionID: &first.ID, Manifest: fixture.Manifest()}
	imported := must(s.client.ImportManifestWithResponse(ctx, rv.ID, &api.ImportManifestParams{IdempotencyKey: &key}, request))
	expectStatus(t, imported.StatusCode(), 201, imported.Body)
	captured := must(s.client.CaptureRevisionWithResponse(ctx, rv.ID, nil, review.CaptureRequest{}))
	expectStatus(t, captured.StatusCode(), 201, captured.Body)
	replay := must(s.client.ImportManifestWithResponse(ctx, rv.ID, &api.ImportManifestParams{IdempotencyKey: &key}, request))
	expectStatus(t, replay.StatusCode(), 201, replay.Body)
	if !bytes.Equal(imported.Body, replay.Body) || replay.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("committed import was re-executed after freeze")
	}
	rejected := must(s.client.ImportManifestWithResponse(ctx, rv.ID, nil, request))
	expectStatus(t, rejected.StatusCode(), 409, rejected.Body)
}

func TestRevisionReadsRejectForeignRevisions(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, first, _ := s.seed(t)
	thread := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, nil, review.CreateDiscussionRequest{Body: "Original stored source", Anchor: review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{RepositoryID: first.Snapshots[0].RepositoryID, Path: "src/routes/upload.ts", Side: "HEAD", StartLine: 1, EndLine: 2}}}))
	expectStatus(t, thread.StatusCode(), 201, thread.Body)
	second := must(s.client.CaptureRevisionWithResponse(ctx, rv.ID, nil, review.CaptureRequest{}))
	expectStatus(t, second.StatusCode(), 201, second.Body)
	statuses := must(s.client.GetDiscussionAnchorStatusesWithResponse(ctx, rv.ID, &api.GetDiscussionAnchorStatusesParams{RevisionId: second.JSON201.ID}))
	expectStatus(t, statuses.StatusCode(), 200, statuses.Body)
	if (*statuses.JSON200)[0].Status != "UNCHANGED" {
		t.Fatal("identical blob was not recognized")
	}
	other := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{Path: *rv.ContextPath, Title: "Other"}))
	expectStatus(t, other.StatusCode(), 201, other.Body)
	foreign := must(s.client.GetRevisionViewWithResponse(ctx, other.JSON201.ID, &api.GetRevisionViewParams{RevisionId: &first.ID}))
	expectStatus(t, foreign.StatusCode(), http.StatusBadRequest, foreign.Body)
	foreignAnchors := must(s.client.GetDiscussionAnchorStatusesWithResponse(ctx, other.JSON201.ID, &api.GetDiscussionAnchorStatusesParams{RevisionId: first.ID}))
	expectStatus(t, foreignAnchors.StatusCode(), http.StatusBadRequest, foreignAnchors.Body)
	foreignComparison := must(s.client.CompareRevisionsWithResponse(ctx, other.JSON201.ID, &api.CompareRevisionsParams{FromRevisionId: first.ID, ToRevisionId: second.JSON201.ID}))
	expectStatus(t, foreignComparison.StatusCode(), http.StatusBadRequest, foreignComparison.Body)
}
