package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/agents"
	"github.com/aduverger/prunelle/server/internal/review"
)

func queueQuestion(t *testing.T, s *testServer, rv review.Review, revision review.Revision, peer piPeer, body, key string) *api.CreateAgentInvocationResponse {
	t.Helper()
	ctx := context.Background()
	question := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, nil, review.CreateDiscussionRequest{Body: body, Anchor: review.AnchorInput{Type: "REVIEW"}, RevisionID: &revision.ID}))
	expectStatus(t, question.StatusCode(), 201, question.Body)
	queued := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, &api.CreateAgentInvocationParams{IdempotencyKey: &key}, review.CreateAgentInvocationRequest{AgentSessionID: peer.session.ID, RevisionID: revision.ID, Intent: "DISCUSS", DiscussionID: &question.JSON201.ID, MessageID: &question.JSON201.Messages[0].ID}))
	expectStatus(t, queued.StatusCode(), 202, queued.Body)
	return queued
}

func finishQuestion(t *testing.T, s *testServer, peer piPeer, invocation review.AgentInvocation) {
	t.Helper()
	completed := must(s.client.CompleteAgentInvocationWithResponse(context.Background(), invocation.ID, nil, review.CompleteAgentInvocationRequest{DiscussionID: invocation.DiscussionID, Body: "Answer: " + invocation.ID}, piActor(peer.external)))
	expectStatus(t, completed.StatusCode(), 200, completed.Body)
}

func TestPiQueueSerializesReviewsAndCancellationPreservesComments(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, revision, _ := s.seed(t)
	peer := s.connectPi(t, "fifo", rv.Repositories[0].RootPath)
	s.waitPiBinding(t, rv.ID, peer.session.ID)
	busy := must(s.client.UpdateAgentSessionStateWithResponse(ctx, peer.session.ID, agents.StateRequest{ConnectionID: peer.connection, Ready: false, Model: "fixture/captured-reviewer", ContextID: "local-task"}, piActor(peer.external)))
	expectStatus(t, busy.StatusCode(), 200, busy.Body)
	other := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{Path: rv.Repositories[0].RootPath, Title: "Other queued review"}))
	expectStatus(t, other.StatusCode(), 201, other.Body)
	otherRevision := must(s.client.CaptureRevisionWithResponse(ctx, other.JSON201.ID, nil, review.CaptureRequest{}))
	expectStatus(t, otherRevision.StatusCode(), 201, otherRevision.Body)
	first := queueQuestion(t, s, rv, revision, peer, "First", "fifo-first")
	second := queueQuestion(t, s, *other.JSON201, *otherRevision.JSON201, peer, "Second from another review", "fifo-second")
	third := queueQuestion(t, s, rv, revision, peer, "Cancelled", "fifo-third")
	fourth := queueQuestion(t, s, rv, revision, peer, "Cancel on changing Pi", "fifo-fourth")
	generation := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, nil, review.CreateAgentInvocationRequest{AgentSessionID: peer.session.ID, RevisionID: revision.ID, Intent: "PREPARE_REVIEW"}))
	expectStatus(t, generation.StatusCode(), 409, generation.Body)
	if !bytes.Contains(generation.Body, []byte("queued comments")) {
		t.Fatal("generation bypassed comment ordering")
	}
	queue := must(s.client.ListAgentQueueWithResponse(ctx, peer.session.ID))
	expectStatus(t, queue.StatusCode(), 200, queue.Body)
	for i, expected := range []string{first.JSON202.ID, second.JSON202.ID, third.JSON202.ID, fourth.JSON202.ID} {
		if (*queue.JSON200)[i].ID != expected || (*queue.JSON200)[i].Status != "QUEUED" {
			t.Fatal("queue lost cross-review acceptance order")
		}
	}
	key := "fifo-first"
	replayed := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, &api.CreateAgentInvocationParams{IdempotencyKey: &key}, review.CreateAgentInvocationRequest{AgentSessionID: peer.session.ID, RevisionID: revision.ID, Intent: "DISCUSS", DiscussionID: first.JSON202.DiscussionID, MessageID: first.JSON202.MessageID}))
	if !bytes.Equal(first.Body, replayed.Body) {
		t.Fatal("enqueue replay changed the committed receipt")
	}
	tooEarly := must(s.client.CompleteAgentInvocationWithResponse(ctx, first.JSON202.ID, nil, review.CompleteAgentInvocationRequest{DiscussionID: first.JSON202.DiscussionID, Body: "Not dispatched"}, piActor(peer.external)))
	expectStatus(t, tooEarly.StatusCode(), 409, tooEarly.Body)
	forbidden := must(s.client.CancelAgentInvocationWithResponse(ctx, third.JSON202.ID, piActor(peer.external)))
	expectStatus(t, forbidden.StatusCode(), 403, forbidden.Body)
	cancelled := must(s.client.CancelAgentInvocationWithResponse(ctx, third.JSON202.ID))
	expectStatus(t, cancelled.StatusCode(), 200, cancelled.Body)
	again := must(s.client.CancelAgentInvocationWithResponse(ctx, third.JSON202.ID))
	expectStatus(t, again.StatusCode(), 200, again.Body)
	thread := must(s.client.GetDiscussionWithResponse(ctx, *third.JSON202.DiscussionID))
	if thread.JSON200.Messages[0].Body != "Cancelled" {
		t.Fatal("cancelling removed the saved comment")
	}
	peer.ready(t, s)
	if sent := peer.next(t); sent.ID != first.JSON202.ID {
		t.Fatal("FIFO head was not dispatched")
	}
	if ready := peer.ready(t, s); ready.State != "BUSY" {
		t.Fatal("premature readiness freed the queue")
	}
	finishQuestion(t, s, peer, *first.JSON202)
	waiting := must(s.client.GetAgentInvocationWithResponse(ctx, second.JSON202.ID))
	if waiting.JSON200.Status != "QUEUED" {
		t.Fatal("completion alone dispatched before Pi settled")
	}
	peer.ready(t, s)
	if sent := peer.next(t); sent.ID != second.JSON202.ID {
		t.Fatal("cross-review FIFO advanced incorrectly")
	}
	tooLate := must(s.client.CancelAgentInvocationWithResponse(ctx, second.JSON202.ID))
	expectStatus(t, tooLate.StatusCode(), 409, tooLate.Body)
	binding := must(s.client.GetAgentBindingWithResponse(ctx, rv.ID))
	etag := binding.HTTPResponse.Header.Get("ETag")
	detached := must(s.client.SetAgentBindingWithResponse(ctx, rv.ID, &api.SetAgentBindingParams{IfMatch: &etag}, agents.BindingRequest{Mode: "DISCONNECTED"}))
	expectStatus(t, detached.StatusCode(), 200, detached.Body)
	removed := must(s.client.GetAgentInvocationWithResponse(ctx, fourth.JSON202.ID))
	if removed.JSON200.Status != "CANCELLED" {
		t.Fatal("changing Pi did not cancel unsent work")
	}
	finishQuestion(t, s, peer, *second.JSON202)
	if ready := peer.ready(t, s); ready.State != "READY" {
		t.Fatal("cancelled work occupied the live slot")
	}
	queue = must(s.client.ListAgentQueueWithResponse(ctx, peer.session.ID))
	if len(*queue.JSON200) != 0 {
		t.Fatal("completed/cancelled requests remained in the pending queue")
	}
}

func TestPiQueueSurvivesRestartAndRequiresHumanResumeAfterUncertainty(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, revision, _ := s.seed(t)
	peer := s.connectPi(t, "queue-restart", rv.Repositories[0].RootPath)
	s.waitPiBinding(t, rv.ID, peer.session.ID)
	first := queueQuestion(t, s, rv, revision, peer, "Dispatched only once", "queue-restart-first")
	peer.next(t)
	second := queueQuestion(t, s, rv, revision, peer, "Survive restart", "queue-restart-second")
	peer.stream.stop()
	s.stop()
	s = startTestServer(t, s.root, nil)
	resumed := s.connectPi(t, peer.external, peer.session.Cwd)
	state := resumed.ready(t, s)
	if !state.QueuePaused {
		t.Fatal("uncertain queue resumed automatically")
	}
	generation := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, nil, review.CreateAgentInvocationRequest{AgentSessionID: peer.session.ID, RevisionID: revision.ID, Intent: "PREPARE_REVIEW"}))
	expectStatus(t, generation.StatusCode(), 409, generation.Body)
	if !bytes.Contains(generation.Body, []byte("paused")) {
		t.Fatal("generation bypassed queue uncertainty")
	}
	waiting := must(s.client.GetAgentInvocationWithResponse(ctx, second.JSON202.ID))
	if waiting.JSON200.Status != "QUEUED" {
		t.Fatal("restart lost or dispatched pending work")
	}
	unknown := must(s.client.GetAgentInvocationWithResponse(ctx, first.JSON202.ID))
	if unknown.JSON200.Status != "UNKNOWN" {
		t.Fatal("dispatched work was reset for replay")
	}
	etag := fmt.Sprintf(`"%d"`, state.QueueVersion)
	forbidden := must(s.client.ResumeAgentQueueWithResponse(ctx, peer.session.ID, &api.ResumeAgentQueueParams{IfMatch: &etag}, piActor(peer.external)))
	expectStatus(t, forbidden.StatusCode(), 403, forbidden.Body)
	missing := must(s.client.ResumeAgentQueueWithResponse(ctx, peer.session.ID, nil))
	expectStatus(t, missing.StatusCode(), 428, missing.Body)
	continued := must(s.client.ResumeAgentQueueWithResponse(ctx, peer.session.ID, &api.ResumeAgentQueueParams{IfMatch: &etag}))
	expectStatus(t, continued.StatusCode(), 200, continued.Body)
	if next := resumed.next(t); next.ID != second.JSON202.ID {
		t.Fatal("resume replayed dispatched work instead of the queued comment")
	}
	stale := must(s.client.ResumeAgentQueueWithResponse(ctx, peer.session.ID, &api.ResumeAgentQueueParams{IfMatch: &etag}))
	expectStatus(t, stale.StatusCode(), 412, stale.Body)
	finishQuestion(t, s, resumed, *second.JSON202)
	resumed.ready(t, s)
	finishQuestion(t, s, resumed, *first.JSON202)
	thread := must(s.client.GetDiscussionWithResponse(ctx, *first.JSON202.DiscussionID))
	if len(thread.JSON200.Messages) != 2 {
		t.Fatal("matching late answer did not retain its original thread")
	}
	resumed.stream.stop()
	third := queueQuestion(t, s, rv, revision, peer, "Offline pending", "queue-restart-third")
	s.stop()
	s = startTestServer(t, s.root, nil)
	reconnected := s.connectPi(t, peer.external, peer.session.Cwd)
	if next := reconnected.next(t); next.ID != third.JSON202.ID {
		t.Fatal("never-dispatched offline work did not survive restart")
	}
}

func TestDeletingQueuedReviewPreservesAnotherReviewsQueue(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, revision, _ := s.seed(t)
	peer := s.connectPi(t, "queue-delete", rv.Repositories[0].RootPath)
	s.waitPiBinding(t, rv.ID, peer.session.ID)
	busy := must(s.client.UpdateAgentSessionStateWithResponse(ctx, peer.session.ID, agents.StateRequest{ConnectionID: peer.connection, Ready: false}, piActor(peer.external)))
	expectStatus(t, busy.StatusCode(), 200, busy.Body)
	other := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{Path: rv.Repositories[0].RootPath, Title: "Surviving queue"}))
	expectStatus(t, other.StatusCode(), 201, other.Body)
	otherRevision := must(s.client.CaptureRevisionWithResponse(ctx, other.JSON201.ID, nil, review.CaptureRequest{}))
	expectStatus(t, otherRevision.StatusCode(), 201, otherRevision.Body)
	removed := queueQuestion(t, s, rv, revision, peer, "Removed queued comment", "deleted-queue-head")
	survivor := queueQuestion(t, s, *other.JSON201, *otherRevision.JSON201, peer, "Surviving queued comment", "surviving-queue")
	deleted := must(s.client.DeleteReviewWithResponse(ctx, rv.ID))
	expectStatus(t, deleted.StatusCode(), 204, deleted.Body)
	missing := must(s.client.GetAgentInvocationWithResponse(ctx, removed.JSON202.ID))
	expectStatus(t, missing.StatusCode(), 404, missing.Body)
	peer.ready(t, s)
	if sent := peer.next(t); sent.ID != survivor.JSON202.ID {
		t.Fatal("deleted queue head blocked or retargeted another review")
	}
	finishQuestion(t, s, peer, *survivor.JSON202)
	late := must(s.client.CompleteAgentInvocationWithResponse(ctx, removed.JSON202.ID, nil, review.CompleteAgentInvocationRequest{DiscussionID: removed.JSON202.DiscussionID, Body: "Too late"}, piActor(peer.external)))
	expectStatus(t, late.StatusCode(), 404, late.Body)
}
