package httpapi_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
)

func TestReviewSourcesCaptureAndHardDeleteOverHTTP(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	repos := must(fixture.BuildRepos(s.reposDir))
	listed := must(s.client.ListReviewsWithResponse(ctx))
	if len(*listed.JSON200) != 0 {
		t.Fatal("filesystem presence created a review")
	}
	key := "create-review"
	request := review.CreateReviewRequest{Path: filepath.Join(repos.API, "src")}
	created := must(s.client.CreateReviewWithResponse(ctx, &api.CreateReviewParams{IdempotencyKey: &key}, request))
	expectStatus(t, created.StatusCode(), 201, created.Body)
	if created.JSON201.Title != "api" || len(created.JSON201.Repositories) != 1 {
		t.Fatalf("source context: %s", created.Body)
	}
	replayed := must(s.client.CreateReviewWithResponse(ctx, &api.CreateReviewParams{IdempotencyKey: &key}, request))
	if replayed.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" || replayed.JSON201.ID != created.JSON201.ID {
		t.Fatal("creation replay changed identity")
	}
	capture := must(s.client.CaptureRevisionWithResponse(ctx, created.JSON201.ID, nil, review.CaptureRequest{}))
	expectStatus(t, capture.StatusCode(), 201, capture.Body)
	second := must(s.client.CreateReviewWithResponse(ctx, nil, request))
	expectStatus(t, second.StatusCode(), 201, second.Body)
	if second.JSON201.ID == created.JSON201.ID {
		t.Fatal("creation reused another review")
	}
	ambiguous := must(s.client.ResolveReviewWithResponse(ctx, &api.ResolveReviewParams{Path: repos.API}))
	expectStatus(t, ambiguous.StatusCode(), 409, ambiguous.Body)
	for _, invalid := range []string{
		`{}`, `{"path":"relative"}`, `{"path":"` + repos.API + `","repositories":[{"label":"api","path":"` + repos.API + `"}]}`,
		`{"workspace_id":"old"}`, `{"path":"` + repos.API + `","base_ref":"main"}`,
	} {
		response := must(s.client.CreateReviewWithBodyWithResponse(ctx, nil, "application/json", strings.NewReader(invalid)))
		expectStatus(t, response.StatusCode(), 400, response.Body)
	}
	oldAPI := must(http.Get(s.url + "/v1/workspaces"))
	oldAPI.Body.Close()
	if oldAPI.StatusCode != 404 {
		t.Fatal("retired workspace API still exposed")
	}
	otherKey := "other-review"
	otherRequest := review.CreateReviewRequest{Path: repos.Worker}
	other := must(s.client.CreateReviewWithResponse(ctx, &api.CreateReviewParams{IdempotencyKey: &otherKey}, otherRequest))
	expectStatus(t, other.StatusCode(), 201, other.Body)
	commentKey := "comment"
	commentRequest := review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Delete this stored text"}
	comment := must(s.client.CreateDiscussionWithResponse(ctx, created.JSON201.ID, &api.CreateDiscussionParams{IdempotencyKey: &commentKey}, commentRequest))
	expectStatus(t, comment.StatusCode(), 201, comment.Body)
	deleted := must(s.client.DeleteReviewWithResponse(ctx, created.JSON201.ID))
	expectStatus(t, deleted.StatusCode(), 204, deleted.Body)
	gone := must(s.client.GetReviewWithResponse(ctx, created.JSON201.ID))
	expectStatus(t, gone.StatusCode(), 404, gone.Body)
	oldReceipt := must(s.client.CreateDiscussionWithResponse(ctx, created.JSON201.ID, &api.CreateDiscussionParams{IdempotencyKey: &commentKey}, commentRequest))
	expectStatus(t, oldReceipt.StatusCode(), 404, oldReceipt.Body)
	if oldReceipt.HTTPResponse.Header.Get("Idempotent-Replayed") != "" {
		t.Fatal("deleted private text replayed")
	}
	otherReplay := must(s.client.CreateReviewWithResponse(ctx, &api.CreateReviewParams{IdempotencyKey: &otherKey}, otherRequest))
	if otherReplay.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" || otherReplay.JSON201.ID != other.JSON201.ID {
		t.Fatal("unrelated receipt removed")
	}
	remaining := must(s.client.GetReviewWithResponse(ctx, second.JSON201.ID))
	expectStatus(t, remaining.StatusCode(), 200, remaining.Body)
	expectStatus(t, must(s.client.DeleteReviewWithResponse(ctx, created.JSON201.ID)).StatusCode(), 204, nil)
	remaining = must(s.client.GetReviewWithResponse(ctx, second.JSON201.ID))
	expectStatus(t, remaining.StatusCode(), 200, remaining.Body)
}

func TestEventStreamResumesAcrossDeletedReviewHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	r, _, _ := s.seed(t)
	stream := s.openEvents(t, "")
	checkpoint := stream.next(t)
	stream.stop()
	removed := must(s.client.CreateDiscussionWithResponse(ctx, r.ID, nil, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Removed history"}))
	expectStatus(t, removed.StatusCode(), 201, removed.Body)
	other := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{Path: s.reposDir, Title: "Keep this review"}))
	expectStatus(t, other.StatusCode(), 201, other.Body)
	deleted := must(s.client.DeleteReviewWithResponse(ctx, r.ID))
	expectStatus(t, deleted.StatusCode(), 204, deleted.Body)
	s.stop()
	restarted := startTestServer(t, s.root, nil)
	resumed := restarted.openEvents(t, strconv.FormatInt(checkpoint.id, 10))
	if ready := resumed.next(t); ready.event != "ready" || ready.id != checkpoint.id {
		t.Fatalf("checkpoint changed: %+v", ready)
	}
	seenOther := false
	previous := checkpoint.id
	for {
		next := resumed.next(t)
		event := next.decode(t)
		if event.Sequence <= previous || event.Sequence != next.id {
			t.Fatal("sequence did not advance")
		}
		previous = event.Sequence
		if event.EntityID == removed.JSON201.ID || (event.ReviewID != nil && *event.ReviewID == r.ID && next.event != "review.deleted") {
			t.Fatal("deleted history replayed")
		}
		if next.event == "review.created" && event.EntityID == other.JSON201.ID {
			seenOther = true
		}
		if next.event == "review.deleted" {
			if event.EntityID != r.ID {
				t.Fatal("wrong deletion event")
			}
			break
		}
	}
	if !seenOther {
		t.Fatal("unrelated missed event lost")
	}
}

func TestAuthorizedCallerCanSelectAnyReadableGitSource(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, []string{"friend@example.com"})
	outside := must(fixture.BuildRepos(filepath.Join(t.TempDir(), "outside")))
	request := review.CreateReviewRequest{Path: outside.API}
	allowed := must(s.client.CreateReviewWithResponse(ctx, nil, request, throughServe))
	expectStatus(t, allowed.StatusCode(), http.StatusCreated, allowed.Body)
	if allowed.JSON201.Repositories[0].RootPath != outside.API {
		t.Fatal("server did not validate the chosen source")
	}
	denied := must(s.client.CreateReviewWithResponse(ctx, nil, request, func(ctx context.Context, req *http.Request) error {
		_ = throughServe(ctx, req)
		req.Header.Set("Tailscale-User-Login", "stranger@example.com")
		return nil
	}))
	expectStatus(t, denied.StatusCode(), http.StatusForbidden, denied.Body)
}

func TestCaptureReplayKeepsOriginalDefaultAfterRestart(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	r, _, _ := s.seed(t)
	key := "capture-with-default"
	params := &api.CaptureRevisionParams{IdempotencyKey: &key}
	first := must(s.client.CaptureRevisionWithResponse(ctx, r.ID, params, review.CaptureRequest{}))
	expectStatus(t, first.StatusCode(), http.StatusCreated, first.Body)
	s.stop()
	restarted := startTestServerWithDefault(t, s.root, "refs/heads/missing-default", nil)
	replayed := must(restarted.client.CaptureRevisionWithResponse(ctx, r.ID, params, review.CaptureRequest{}))
	expectStatus(t, replayed.StatusCode(), http.StatusCreated, replayed.Body)
	if replayed.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" || string(first.Body) != string(replayed.Body) {
		t.Fatal("config change altered committed capture replay")
	}
	failed := must(restarted.client.CaptureRevisionWithResponse(ctx, r.ID, nil, review.CaptureRequest{}))
	expectStatus(t, failed.StatusCode(), http.StatusUnprocessableEntity, failed.Body)
	recovered := must(restarted.client.CaptureRevisionWithResponse(ctx, r.ID, nil, review.CaptureRequest{BaseRef: "main"}))
	expectStatus(t, recovered.StatusCode(), http.StatusCreated, recovered.Body)
}
