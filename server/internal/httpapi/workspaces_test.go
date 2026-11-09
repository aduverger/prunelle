package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/sessions"
	"github.com/aduverger/prunelle/server/internal/sessions/sessionstest"
)

func TestAutomaticWorkspaceContextAndHardDeleteOverHTTP(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	repos := must(fixture.BuildRepos(s.reposDir))
	candidates := must(s.client.DiscoverWorkspacesWithResponse(ctx))
	expectStatus(t, candidates.StatusCode(), http.StatusOK, candidates.Body)
	if len(*candidates.JSON200) != 2 || (*candidates.JSON200)[0].WorkspaceID != nil {
		t.Fatalf("candidates = %s", candidates.Body)
	}
	registered := must(s.client.ListWorkspacesWithResponse(ctx))
	if len(*registered.JSON200) != 0 {
		t.Fatalf("discovery registered workspace: %s", registered.Body)
	}
	missing := must(s.client.ResolveReviewWithResponse(ctx, &api.ResolveReviewParams{Path: repos.API}))
	expectStatus(t, missing.StatusCode(), http.StatusBadRequest, missing.Body)
	key := "inferred-create"
	request := review.CreateReviewRequest{WorkspacePath: filepath.Join(repos.API, "src")}
	created := must(s.client.CreateReviewWithResponse(ctx, &api.CreateReviewParams{IdempotencyKey: &key}, request))
	expectStatus(t, created.StatusCode(), http.StatusCreated, created.Body)
	if created.JSON201.Title != "api" {
		t.Fatalf("title = %s", created.Body)
	}
	replayed := must(s.client.CreateReviewWithResponse(ctx, &api.CreateReviewParams{IdempotencyKey: &key}, request))
	if replayed.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" || replayed.JSON201.ID != created.JSON201.ID {
		t.Fatalf("inferred creation is not idempotent: %s", replayed.Body)
	}
	resolved := must(s.client.ResolveReviewWithResponse(ctx, &api.ResolveReviewParams{Path: filepath.Join(repos.API, "src")}))
	expectStatus(t, resolved.StatusCode(), http.StatusOK, resolved.Body)
	if resolved.JSON200.ID != created.JSON201.ID {
		t.Fatalf("wrong review: %s", resolved.Body)
	}
	capture := must(s.client.CaptureRevisionWithResponse(ctx, resolved.JSON200.ID, nil))
	expectStatus(t, capture.StatusCode(), http.StatusCreated, capture.Body)
	second := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{WorkspaceID: created.JSON201.WorkspaceID, Title: "Another review"}))
	expectStatus(t, second.StatusCode(), http.StatusCreated, second.Body)
	ambiguous := must(s.client.ResolveReviewWithResponse(ctx, &api.ResolveReviewParams{Path: repos.API}))
	expectStatus(t, ambiguous.StatusCode(), http.StatusConflict, ambiguous.Body)
	for _, invalid := range []review.CreateReviewRequest{
		{}, {WorkspaceID: created.JSON201.WorkspaceID, WorkspacePath: repos.API},
		{WorkspacePath: "relative"}, {WorkspacePath: s.reposDir},
		{WorkspaceID: created.JSON201.WorkspaceID, BaseRef: "origin/main"},
	} {
		response := must(s.client.CreateReviewWithResponse(ctx, nil, invalid))
		expectStatus(t, response.StatusCode(), http.StatusBadRequest, response.Body)
	}
	otherKey := "unrelated-create"
	otherRequest := review.CreateReviewRequest{WorkspacePath: repos.Worker}
	other := must(s.client.CreateReviewWithResponse(ctx, &api.CreateReviewParams{IdempotencyKey: &otherKey}, otherRequest))
	expectStatus(t, other.StatusCode(), http.StatusCreated, other.Body)
	// Cached discussion bodies and review receipts belong to the workspace too.
	discussionKey := "discussion-create"
	discussion := must(s.client.CreateDiscussionWithResponse(ctx, created.JSON201.ID, &api.CreateDiscussionParams{IdempotencyKey: &discussionKey}, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Delete this stored text"}))
	expectStatus(t, discussion.StatusCode(), http.StatusCreated, discussion.Body)
	deleted := must(s.client.DeleteWorkspaceWithResponse(ctx, created.JSON201.WorkspaceID))
	expectStatus(t, deleted.StatusCode(), http.StatusNoContent, deleted.Body)
	gone := must(s.client.GetReviewWithResponse(ctx, created.JSON201.ID))
	expectStatus(t, gone.StatusCode(), http.StatusNotFound, gone.Body)
	oldReceipt := must(s.client.CreateDiscussionWithResponse(ctx, created.JSON201.ID, &api.CreateDiscussionParams{IdempotencyKey: &discussionKey}, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Delete this stored text"}))
	expectStatus(t, oldReceipt.StatusCode(), http.StatusNotFound, oldReceipt.Body)
	if oldReceipt.HTTPResponse.Header.Get("Idempotent-Replayed") != "" {
		t.Fatal("deleted text was replayed from cached receipt")
	}
	otherReplay := must(s.client.CreateReviewWithResponse(ctx, &api.CreateReviewParams{IdempotencyKey: &otherKey}, otherRequest))
	if otherReplay.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" || otherReplay.JSON201.ID != other.JSON201.ID {
		t.Fatalf("deletion damaged unrelated retry: %s", otherReplay.Body)
	}
	discoveredAgain := must(s.client.DiscoverWorkspacesWithResponse(ctx))
	if len(*discoveredAgain.JSON200) != 2 || (*discoveredAgain.JSON200)[0].WorkspaceID != nil {
		t.Fatalf("rediscovery restored deleted metadata: %s", discoveredAgain.Body)
	}
	fresh := must(s.client.CreateReviewWithResponse(ctx, nil, request))
	expectStatus(t, fresh.StatusCode(), http.StatusCreated, fresh.Body)
	if fresh.JSON201.WorkspaceID == created.JSON201.WorkspaceID {
		t.Fatal("fresh creation reused deleted identity")
	}
	expectStatus(t, must(s.client.DeleteWorkspaceWithResponse(ctx, created.JSON201.WorkspaceID)).StatusCode(), http.StatusNoContent, nil)
	stillHere := must(s.client.GetReviewWithResponse(ctx, fresh.JSON201.ID))
	expectStatus(t, stillHere.StatusCode(), http.StatusOK, stillHere.Body)
}

func TestEventStreamResumesAcrossDeletedWorkspaceHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, _, _ := s.seed(t)
	stream := s.openEvents(t, "")
	checkpoint := stream.next(t)
	if checkpoint.event != "ready" {
		t.Fatalf("missing initial checkpoint: %+v", checkpoint)
	}
	stream.stop()
	deletedDiscussion := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, nil, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "A deleted discussion while disconnected"}))
	expectStatus(t, deletedDiscussion.StatusCode(), http.StatusCreated, deletedDiscussion.Body)
	other := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{WorkspacePath: filepath.Join(s.reposDir, "api"), Title: "Keep this review"}))
	expectStatus(t, other.StatusCode(), http.StatusCreated, other.Body)
	deleted := must(s.client.DeleteWorkspaceWithResponse(ctx, rv.WorkspaceID))
	expectStatus(t, deleted.StatusCode(), http.StatusNoContent, deleted.Body)
	s.stop()
	restarted := startTestServer(t, s.root, nil)
	resumed := restarted.openEvents(t, strconv.FormatInt(checkpoint.id, 10))
	if ready := resumed.next(t); ready.event != "ready" || ready.id != checkpoint.id {
		t.Fatalf("invalid resume checkpoint: %+v", ready)
	}
	seenOtherReview := false
	previousSequence := checkpoint.id
	for {
		next := resumed.next(t)
		event := next.decode(t)
		if event.Sequence <= previousSequence || event.Sequence != next.id {
			t.Fatalf("event sequence did not advance across deletion: %+v", next)
		}
		previousSequence = event.Sequence
		if event.EntityID == deletedDiscussion.JSON201.ID || (event.ReviewID != nil && *event.ReviewID == rv.ID) {
			t.Fatalf("deleted workspace history was replayed: %+v", event)
		}
		if next.event == "review.created" && event.EntityID == other.JSON201.ID {
			seenOtherReview = true
		}
		if next.event == "workspace.deleted" {
			if event.EntityID != rv.WorkspaceID || event.ReviewID != nil {
				t.Fatalf("wrong deletion notification: %+v", event)
			}
			break
		}
	}
	if !seenOtherReview {
		t.Fatal("deletion discarded an unrelated missed review event")
	}
}

func TestWorkspaceDeleteUnlinksButDoesNotStopRuntime(t *testing.T) {
	ctx := context.Background()
	fake := sessionstest.NewFake()
	s := newTestServer(t, nil, sessions.Registration{Name: "rex", Config: json.RawMessage(`{}`), Backend: fake})
	rv, _, _ := s.seed(t)
	fake.Set(sessions.Observed{ExternalID: "session:a/block:a1", State: sessions.StateOnline, Label: "Running fixture", Local: true, Metadata: json.RawMessage(`{}`)})
	backends := must(s.client.ListSessionBackendsWithResponse(ctx))
	if err := s.sessions.Sync(ctx, (*backends.JSON200)[0].ID); err != nil {
		t.Fatal(err)
	}
	listed := must(s.client.ListRuntimeSessionsWithResponse(ctx, &api.ListRuntimeSessionsParams{}))
	runtime := (*listed.JSON200)[0]
	linked := must(s.client.AssociateRuntimeSessionWithResponse(ctx, rv.WorkspaceID, runtime.ID))
	expectStatus(t, linked.StatusCode(), http.StatusOK, linked.Body)
	deleted := must(s.client.DeleteWorkspaceWithResponse(ctx, rv.WorkspaceID))
	expectStatus(t, deleted.StatusCode(), http.StatusNoContent, deleted.Body)
	remaining := must(s.client.GetRuntimeSessionWithResponse(ctx, runtime.ID))
	expectStatus(t, remaining.StatusCode(), http.StatusOK, remaining.Body)
	if remaining.JSON200.State != sessions.StateOnline || len(remaining.JSON200.WorkspaceIDs) != 0 {
		t.Fatalf("runtime stopped or association retained: %s", remaining.Body)
	}
}
