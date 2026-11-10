package agents

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

type heldDispatchResult struct {
	*review.Service
	afterResult func()
}

func (r *heldDispatchResult) ClaimQueuedDiscussion(ctx context.Context, target review.AgentInvocationTarget, actor review.Actor) (*review.AgentInvocation, error) {
	invocation, err := r.Service.ClaimQueuedDiscussion(ctx, target, actor)
	if invocation == nil && err == nil {
		r.afterResult()
	}
	return invocation, err
}

func (r *heldDispatchResult) CreateAgentInvocation(ctx context.Context, id string, req review.CreateAgentInvocationRequest, target review.AgentInvocationTarget, actor review.Actor) (review.AgentInvocation, error) {
	invocation, err := r.Service.CreateAgentInvocation(ctx, id, req, target, actor)
	if req.Intent != "DISCUSS" {
		r.afterResult()
	}
	return invocation, err
}

func TestAcceptedCommentDispatchesAfterOverlappingReservationEnds(t *testing.T) {
	for _, intent := range []string{"empty claim", "PREPARE_REVIEW", "IMPLEMENT"} {
		t.Run(intent, func(t *testing.T) {
			ctx := t.Context()
			db, err := store.Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			repos, err := fixture.BuildRepos(filepath.Join(t.TempDir(), "repos"))
			if err != nil {
				t.Fatal(err)
			}
			human := review.Actor{Author: review.Author{Kind: "HUMAN", ID: "owner@example.com"}, Login: "owner@example.com"}
			reviews := review.NewService(db, "main")
			rv, err := reviews.CreateReview(ctx, fixture.ReviewRequest(repos), human)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := reviews.CaptureRevision(ctx, rv.ID, review.CaptureRequest{}, human)
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(db, reviews)
			agent := review.Actor{Author: review.Author{Kind: "AGENT", ID: "pi:dispatch-fixture"}, Login: human.Login}
			session, err := service.Register(ctx, RegisterRequest{ExternalID: "dispatch-fixture", Cwd: repos.API, Version: "0.4.1"}, true, agent)
			if err != nil {
				t.Fatal(err)
			}
			connection, err := service.Connect(ctx, session.ID, agent)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { service.Disconnect(connection) })
			ready := StateRequest{ConnectionID: connection.ID, Ready: true, Model: "fixture/reviewer", ContextID: "coding-context"}
			if _, err := service.UpdateState(ctx, session.ID, ready, agent); err != nil {
				t.Fatal(err)
			}
			if err := service.ResolveReview(ctx, rv.ID, human); err != nil {
				t.Fatal(err)
			}
			question, err := reviews.CreateDiscussion(ctx, rv.ID, review.CreateDiscussionRequest{Body: "Deliver without another desktop action", Anchor: review.AnchorInput{Type: "REVIEW"}}, human)
			if err != nil {
				t.Fatal(err)
			}

			reached, released := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(released) })
			service.reviews = &heldDispatchResult{Service: reviews, afterResult: sync.OnceFunc(func() {
				close(reached)
				<-released
			})}
			finished := make(chan error, 1)
			t.Cleanup(func() {
				release()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("reservation owner did not finish")
				}
			})
			go func() {
				defer close(finished)
				if intent == "empty claim" {
					_, err := service.UpdateState(ctx, session.ID, ready, agent)
					finished <- err
					return
				}
				_, err := service.CreateInvocation(ctx, rv.ID, review.CreateAgentInvocationRequest{
					AgentSessionID: session.ID, RevisionID: revision.ID, Intent: intent, DiscussionID: &question.ID,
				}, human)
				finished <- err
			}()
			select {
			case <-reached:
			case err := <-finished:
				t.Fatalf("reservation ended before its controlled boundary: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("reservation did not reach its controlled boundary")
			}
			queued, err := service.CreateInvocation(ctx, rv.ID, review.CreateAgentInvocationRequest{
				AgentSessionID: session.ID, RevisionID: revision.ID, Intent: "DISCUSS", DiscussionID: &question.ID, MessageID: &question.Messages[0].ID,
			}, human)
			if err != nil || queued.Status != "QUEUED" {
				t.Fatalf("comment was not accepted during the reservation: %+v %v", queued, err)
			}
			release()
			select {
			case err := <-finished:
				if intent == "empty claim" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var invalid *review.Error
					if !errors.As(err, &invalid) || invalid.Code != review.CodeInvalidArgument {
						t.Fatalf("invalid immediate request was not rejected: %v", err)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("reservation did not end")
			}
			select {
			case sent := <-connection.Requests:
				if sent.ID != queued.ID || sent.Status != "PENDING" || *sent.DiscussionID != question.ID {
					t.Fatalf("wrong dispatched comment: %+v", sent)
				}
			default:
				t.Fatal("accepted comment was stranded after the reservation ended")
			}
			select {
			case extra := <-connection.Requests:
				t.Fatalf("unexpected extra dispatch: %s", extra.ID)
			default:
			}
		})
	}
}
