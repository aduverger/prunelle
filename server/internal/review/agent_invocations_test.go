package review_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/aduverger/prunelle/server/internal/agents"
	"github.com/aduverger/prunelle/server/internal/review"
)

func generationAgent(t *testing.T, e *env) (*agents.Service, agents.Session, *agents.Connection, review.Actor) {
	t.Helper()
	service := agents.NewService(e.db, e.svc)
	actor := review.Actor{Author: review.Author{Kind: "AGENT", ID: "pi:generation-fixture"}, Login: reviewer.Login}
	session, err := service.Register(e.ctx, agents.RegisterRequest{ExternalID: "generation-fixture", Cwd: e.repos.API, Version: "0.4.0"}, true, actor)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.Connect(e.ctx, session.ID, actor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Disconnect(connection) })
	if _, err := service.UpdateState(e.ctx, session.ID, agents.StateRequest{ConnectionID: connection.ID, Ready: true, Model: "fixture/reviewer", ContextID: "original-coding-context"}, actor); err != nil {
		t.Fatal(err)
	}
	return service, session, connection, actor
}

func TestQueueRecoveryDoesNotRedispatchAcceptedWorkAfterProcessLoss(t *testing.T) {
	e := newEnv(t)
	service, session, connection, agent := generationAgent(t, e)
	question := must[review.Discussion](t)(e.svc.CreateDiscussion(e.ctx, e.review.ID, review.CreateDiscussionRequest{Body: "Question before process loss", Anchor: review.AnchorInput{Type: "REVIEW"}}, reviewer))
	request := review.CreateAgentInvocationRequest{AgentSessionID: session.ID, RevisionID: e.revision.ID, Intent: "DISCUSS", DiscussionID: &question.ID, MessageID: &question.Messages[0].ID}
	first := must[review.AgentInvocation](t)(service.CreateInvocation(e.ctx, e.review.ID, request, reviewer))
	if sent := <-connection.Requests; sent.ID != first.ID {
		t.Fatal("wrong initial dispatch")
	}
	second := must[review.AgentInvocation](t)(service.CreateInvocation(e.ctx, e.review.ID, request, reviewer))
	// Discard the live-service state without a graceful disconnect, as in a process crash.
	if err := e.svc.RecoverAgentInvocations(e.ctx); err != nil {
		t.Fatal(err)
	}
	fresh := agents.NewService(e.db, e.svc)
	resumed := must[*agents.Connection](t)(fresh.Connect(e.ctx, session.ID, agent))
	t.Cleanup(func() { fresh.Disconnect(resumed) })
	state := must[agents.Session](t)(fresh.UpdateState(e.ctx, session.ID, agents.StateRequest{ConnectionID: resumed.ID, Ready: true, Model: "fixture/reviewer", ContextID: "after-restart"}, agent))
	if !state.QueuePaused {
		t.Fatal("restart failed to persist queue uncertainty")
	}
	select {
	case <-resumed.Requests:
		t.Fatal("reconnect replayed or dispatched work before human acknowledgement")
	default:
	}
	unknown := must[review.AgentInvocation](t)(fresh.GetInvocation(e.ctx, first.ID))
	waiting := must[review.AgentInvocation](t)(fresh.GetInvocation(e.ctx, second.ID))
	if unknown.Status != "UNKNOWN" || waiting.Status != "QUEUED" {
		t.Fatal("recovery confused dispatched and never-dispatched work")
	}
	must[agents.Session](t)(fresh.ResumeQueue(e.ctx, session.ID, state.QueueVersion, reviewer))
	if sent := <-resumed.Requests; sent.ID != second.ID || sent.ContextID != "after-restart" {
		t.Fatal("resume did not use the new context for the queued request")
	}
	must[review.AgentInvocation](t)(fresh.CompleteInvocation(e.ctx, second.ID, review.CompleteAgentInvocationRequest{DiscussionID: &question.ID, Body: "Answer after resume"}, agent))
	if err := e.svc.RecoverAgentInvocations(e.ctx); err != nil {
		t.Fatal(err)
	}
	if current := must[agents.Session](t)(fresh.GetSession(e.ctx, session.ID)); current.QueuePaused {
		t.Fatal("restart forgot the human acknowledgement of older UNKNOWN history")
	}
}

func TestGeneratedReviewPublicationIsAtomicCanonicalAndReplayable(t *testing.T) {
	e := newEnv(t)
	imported := e.importFixture(t)
	title := "Human-controlled title"
	original, err := e.svc.GetTopic(e.ctx, imported.IDs["topics"]["T-tests"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateTopic(e.ctx, original.ID, original.Version, review.UpdateTopicRequest{Title: &title}, reviewer); err != nil {
		t.Fatal(err)
	}
	service, session, _, agent := generationAgent(t, e)
	invocation, err := service.CreateInvocation(e.ctx, e.review.ID, review.CreateAgentInvocationRequest{AgentSessionID: session.ID, RevisionID: e.revision.ID, Intent: "PREPARE_REVIEW"}, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	input, err := e.svc.AgentInvocationContext(e.ctx, invocation.ID)
	if err != nil || len(input.Eligible) == 0 {
		t.Fatalf("eligible generation input: %+v %v", input, err)
	}
	file := input.Eligible[0]
	generated := review.GeneratedReview{Topics: []review.GeneratedTopic{{Key: "feature", Title: "Feature-level explanation", Summary: "Short navigation summary", Explanation: "The captured changes add an asynchronous path; source links are not test verification."}},
		Files: []review.GeneratedFileTopics{{RepositoryID: file.RepositoryID, Path: file.Path, TopicKeys: []string{"feature"}, TopicIDs: []string{}}}}
	before, err := e.svc.GetRevisionView(e.ctx, e.review.ID, &e.revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.Write(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, `CREATE TRIGGER fail_generated_assignment BEFORE INSERT ON source_topic_assignments BEGIN SELECT RAISE(ABORT,'fixture generation assignment failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	request := review.CompleteAgentInvocationRequest{Generation: &generated}
	if _, err := service.CompleteInvocation(e.ctx, invocation.ID, request, agent); err == nil {
		t.Fatal("injected assignment failure committed generated content")
	}
	after, err := e.svc.GetRevisionView(e.ctx, e.review.ID, &e.revision.ID)
	if err != nil || !reflect.DeepEqual(before.Graph, after.Graph) {
		t.Fatalf("partial generation changed the graph: %v", err)
	}
	if err := e.db.Write(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, `DROP TRIGGER fail_generated_assignment`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	completed, err := service.CompleteInvocation(e.ctx, invocation.ID, request, agent)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "SUCCEEDED" || completed.Result == nil || completed.Result.TopicIDs["feature"] == "" {
		t.Fatalf("publication: %+v", completed)
	}
	topicID := completed.Result.TopicIDs["feature"]
	topic, err := e.svc.GetTopic(e.ctx, topicID)
	if err != nil || topic.Origin != "GENERATED" || topic.Author != agent.Author {
		t.Fatalf("generated identity/provenance: %+v %v", topic, err)
	}
	representations, err := e.svc.TopicRepresentations(e.ctx, topicID)
	if err != nil {
		t.Fatal(err)
	}
	textFound, codeFound := false, false
	for _, rep := range representations {
		if rep.Type == "TEXT" && rep.Available && rep.Text.Sections[0].Body == generated.Topics[0].Explanation && rep.Origin == "GENERATED" {
			textFound = true
		}
		if rep.Type == "CODE_DIFF" && rep.SourceFiles != nil {
			for _, selected := range *rep.SourceFiles {
				if selected.RepositoryID == file.RepositoryID && selected.Path == file.Path {
					codeFound = true
				}
			}
		}
	}
	if !textFound || !codeFound {
		t.Fatal("generated explanation or canonical source view is missing")
	}
	preserved, err := e.svc.GetTopic(e.ctx, original.ID)
	if err != nil || preserved.Title != title {
		t.Fatal("generation overwrote human metadata", err)
	}
	published, err := e.svc.GetRevisionView(e.ctx, e.review.ID, &e.revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.CompleteInvocation(e.ctx, invocation.ID, request, agent)
	if err != nil || !reflect.DeepEqual(completed, replayed) {
		t.Fatalf("committed publication changed after freeze: %v", err)
	}
	frozen, err := e.svc.GetRevisionView(e.ctx, e.review.ID, &e.revision.ID)
	if err != nil || !reflect.DeepEqual(published.Graph, frozen.Graph) {
		t.Fatalf("generated history changed at capture: %v", err)
	}
	generated.Topics[0].Title = "A conflicting second result"
	if _, err := service.CompleteInvocation(e.ctx, invocation.ID, request, agent); err == nil {
		t.Fatal("a second result replaced committed generation")
	}
}

func TestGenerationProtectsManualEmptyAssignmentsAndChangedSemanticInputs(t *testing.T) {
	e := newEnv(t)
	imported := e.importFixture(t)
	service, session, connection, agent := generationAgent(t, e)
	view, err := e.svc.GetRevisionView(e.ctx, e.review.ID, &e.revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	var protected review.SourceFileRef
	for _, repo := range view.Graph.SourceChanges.Repositories {
		for _, file := range repo.Files {
			if file.Unassigned && !file.Binary && file.ContentOmitted == nil {
				protected = review.SourceFileRef{RevisionID: e.revision.ID, RepositoryID: repo.RepositoryID, Path: file.Path}
				break
			}
		}
		if protected.Path != "" {
			break
		}
	}
	if protected.Path == "" {
		t.Fatal("fixture lacks unassigned source")
	}
	if _, err := e.svc.UpdateSourceTopics(e.ctx, e.revision.ID, 1, review.UpdateSourceTopicsRequest{RepositoryID: protected.RepositoryID, Path: protected.Path, Mode: "EXPLICIT", TopicIDs: []string{}}, reviewer); err != nil {
		t.Fatal(err)
	}
	invoke := func() review.AgentInvocation {
		t.Helper()
		inv, err := service.CreateInvocation(e.ctx, e.review.ID, review.CreateAgentInvocationRequest{AgentSessionID: session.ID, RevisionID: e.revision.ID, Intent: "PREPARE_REVIEW"}, reviewer)
		if err != nil {
			t.Fatal(err)
		}
		if dispatched := <-connection.Requests; dispatched.ID != inv.ID {
			t.Fatal("wrong invocation dispatched")
		}
		return inv
	}
	inv := invoke()
	input, err := e.svc.AgentInvocationContext(e.ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range input.Eligible {
		if file.RepositoryID == protected.RepositoryID && file.Path == protected.Path {
			t.Fatal("manual unassignment became a generation target")
		}
	}
	bad := review.GeneratedReview{Topics: []review.GeneratedTopic{{Key: "bad", Title: "Wrong", Summary: "Wrong", Explanation: "Must not replace the manual choice"}}, Files: []review.GeneratedFileTopics{{RepositoryID: protected.RepositoryID, Path: protected.Path, TopicKeys: []string{"bad"}}}}
	if _, err := service.CompleteInvocation(e.ctx, inv.ID, review.CompleteAgentInvocationRequest{Generation: &bad}, agent); err == nil {
		t.Fatal("manual empty assignment was overwritten")
	}
	if len(input.Eligible) == 0 {
		t.Fatal("fixture should retain another eligible file")
	}
	file := input.Eligible[0]
	topic, err := e.svc.GetTopic(e.ctx, imported.IDs["topics"]["T-tests"])
	if err != nil {
		t.Fatal(err)
	}
	changed := "A human changed the generation context"
	if _, err := e.svc.UpdateTopic(e.ctx, topic.ID, topic.Version, review.UpdateTopicRequest{Title: &changed}, reviewer); err != nil {
		t.Fatal(err)
	}
	valid := review.GeneratedReview{Topics: []review.GeneratedTopic{{Key: "new", Title: "New", Summary: "Summary", Explanation: "Explanation"}}, Files: []review.GeneratedFileTopics{{RepositoryID: file.RepositoryID, Path: file.Path, TopicKeys: []string{"new"}}}}
	_, err = service.CompleteInvocation(e.ctx, inv.ID, review.CompleteAgentInvocationRequest{Generation: &valid}, agent)
	var stale *review.Error
	if !errors.As(err, &stale) || stale.Code != review.CodePreconditionFailed {
		t.Fatalf("changed generation context was not rejected: %v", err)
	}
	if _, err := service.UpdateInvocation(e.ctx, inv.ID, review.UpdateAgentInvocationRequest{Status: "FAILED", Error: "stale generation"}, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateState(e.ctx, session.ID, agents.StateRequest{ConnectionID: connection.ID, Ready: true, Model: "fixture/reviewer", ContextID: "new-context"}, agent); err != nil {
		t.Fatal(err)
	}
	second := invoke()
	if _, err := e.svc.CaptureRevision(e.ctx, e.review.ID, review.CaptureRequest{}, reviewer); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteInvocation(e.ctx, second.ID, review.CompleteAgentInvocationRequest{Generation: &valid}, agent); err == nil {
		t.Fatal("generation retargeted after capture")
	}
	binding, err := service.GetBinding(context.Background(), e.review.ID)
	if err != nil || binding.AgentSessionID == nil || *binding.AgentSessionID != session.ID {
		t.Fatal("capture discarded the Pi binding", err)
	}
}
