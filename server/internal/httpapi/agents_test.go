package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/agents"
	"github.com/aduverger/prunelle/server/internal/review"
)

func piActor(external string) api.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("Prunelle-Agent", "pi:"+external)
		return nil
	}
}

type piPeer struct {
	session    agents.Session
	stream     *sseStream
	external   string
	connection string
}

func (s *testServer) connectPi(t *testing.T, external, cwd string) piPeer {
	t.Helper()
	ctx := context.Background()
	registered := must(s.client.RegisterAgentSessionWithResponse(ctx, nil, agents.RegisterRequest{ExternalID: external, Cwd: cwd, Version: "0.4.0"}, piActor(external)))
	expectStatus(t, registered.StatusCode(), 200, registered.Body)
	stream := s.openSSE(t, "/v1/agent-sessions/"+registered.JSON200.ID+"/connect", map[string]string{"Prunelle-Agent": "pi:" + external})
	frame := stream.next(t)
	var ready struct {
		ConnectionID string `json:"connection_id"`
	}
	if frame.event != "ready" || json.Unmarshal([]byte(frame.data), &ready) != nil || ready.ConnectionID == "" {
		t.Fatalf("Pi handshake: %+v", frame)
	}
	peer := piPeer{*registered.JSON200, stream, external, ready.ConnectionID}
	peer.ready(t, s)
	return peer
}

func (p piPeer) ready(t *testing.T, s *testServer) agents.Session {
	t.Helper()
	state := must(s.client.UpdateAgentSessionStateWithResponse(context.Background(), p.session.ID, agents.StateRequest{ConnectionID: p.connection, Ready: true, Model: "fixture/captured-reviewer", ContextID: "original-conversation-context"}, piActor(p.external)))
	expectStatus(t, state.StatusCode(), 200, state.Body)
	return *state.JSON200
}
func (p piPeer) next(t *testing.T) review.AgentInvocation {
	t.Helper()
	frame := p.stream.next(t)
	var invocation review.AgentInvocation
	if frame.event != "request" || json.Unmarshal([]byte(frame.data), &invocation) != nil {
		t.Fatalf("Pi request: %+v", frame)
	}
	return invocation
}
func (s *testServer) waitPiBinding(t *testing.T, reviewID, agentID string) agents.Binding {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		binding := must(s.client.GetAgentBindingWithResponse(context.Background(), reviewID))
		expectStatus(t, binding.StatusCode(), 200, binding.Body)
		if binding.JSON200.AgentSessionID != nil && *binding.JSON200.AgentSessionID == agentID {
			return *binding.JSON200
		}
		if time.Now().After(deadline) {
			t.Fatalf("Pi was not automatically attached: %s", binding.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPiAutomaticAttachmentTracksUniqueConnectionsAndPreservesOverrides(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	original, _, _ := s.seed(t)
	canonical := filepath.Join(original.Repositories[0].RootPath, "src")
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(canonical, alias); err != nil {
		t.Fatal(err)
	}
	first := s.connectPi(t, "automatic-first", alias)
	if first.session.Cwd != canonical {
		t.Fatal("Pi cwd was not canonicalized")
	}
	s.waitPiBinding(t, original.ID, first.session.ID)
	second := s.connectPi(t, "automatic-second", canonical)
	auto := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{Path: original.Repositories[0].RootPath, Title: "Ambiguous automatic selection"}))
	expectStatus(t, auto.StatusCode(), 201, auto.Body)
	ambiguous := must(s.client.GetAgentBindingWithResponse(ctx, auto.JSON201.ID))
	if ambiguous.JSON200.AgentSessionID != nil || len(ambiguous.JSON200.Candidates) != 2 {
		t.Fatal("ambiguous Pi candidates were guessed")
	}
	manual := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{Path: original.Repositories[0].RootPath, Title: "Deliberately disconnected"}))
	expectStatus(t, manual.StatusCode(), 201, manual.Body)
	binding := must(s.client.GetAgentBindingWithResponse(ctx, manual.JSON201.ID))
	etag := binding.HTTPResponse.Header.Get("ETag")
	noVersion := must(s.client.SetAgentBindingWithResponse(ctx, manual.JSON201.ID, nil, agents.BindingRequest{Mode: "DISCONNECTED"}))
	expectStatus(t, noVersion.StatusCode(), 428, noVersion.Body)
	disconnected := must(s.client.SetAgentBindingWithResponse(ctx, manual.JSON201.ID, &api.SetAgentBindingParams{IfMatch: &etag}, agents.BindingRequest{Mode: "DISCONNECTED"}))
	expectStatus(t, disconnected.StatusCode(), 200, disconnected.Body)
	stale := must(s.client.SetAgentBindingWithResponse(ctx, manual.JSON201.ID, &api.SetAgentBindingParams{IfMatch: &etag}, agents.BindingRequest{Mode: "MANUAL", AgentSessionID: &second.session.ID}))
	expectStatus(t, stale.StatusCode(), 412, stale.Body)
	first.stream.stop()
	s.waitPiBinding(t, auto.JSON201.ID, second.session.ID)
	preserved := must(s.client.GetAgentBindingWithResponse(ctx, manual.JSON201.ID))
	if preserved.JSON200.Mode != "DISCONNECTED" || preserved.JSON200.AgentSessionID != nil {
		t.Fatal("discovery undid manual disconnection")
	}
	oldTarget := must(s.client.GetAgentBindingWithResponse(ctx, original.ID))
	if oldTarget.JSON200.AgentSessionID == nil || *oldTarget.JSON200.AgentSessionID != first.session.ID {
		t.Fatal("disconnect silently replaced an existing target")
	}
}

func TestPiHTTPExplicitDispatchTargetSafetyAndStructuredResults(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, revision, _ := s.seed(t)
	peer := s.connectPi(t, "http-original", filepath.Join(rv.Repositories[0].RootPath, "src"))
	binding := s.waitPiBinding(t, rv.ID, peer.session.ID)
	question := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, nil, review.CreateDiscussionRequest{Body: "Explain this exact stored code.", Anchor: review.AnchorInput{Type: "CODE_RANGE", CodeRange: &review.CodeRangeInput{RepositoryID: revision.Snapshots[0].RepositoryID, Path: "src/routes/upload.ts", Side: "HEAD", StartLine: 8, EndLine: 9}}}))
	expectStatus(t, question.StatusCode(), 201, question.Body)
	decision := must(s.client.CreateDecisionWithResponse(ctx, rv.ID, nil, review.CreateDecisionRequest{Decision: "REQUEST_CHANGES", Body: "Guard the captured retry path.", DiscussionIDs: []string{question.JSON201.ID}}))
	expectStatus(t, decision.StatusCode(), 201, decision.Body)
	passive := must(s.client.ListAgentInvocationsWithResponse(ctx, rv.ID))
	if len(*passive.JSON200) != 0 {
		t.Fatal("attachment, comments or a verdict dispatched work")
	}
	request := review.CreateAgentInvocationRequest{AgentSessionID: peer.session.ID, RevisionID: revision.ID, Intent: "DISCUSS", DiscussionID: &question.JSON201.ID, MessageID: &question.JSON201.Messages[0].ID}
	wrongTarget := request
	wrongTarget.AgentSessionID = "different-Pi-target"
	wrong := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, nil, wrongTarget))
	expectStatus(t, wrong.StatusCode(), 412, wrong.Body)
	asAgent := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, nil, request, piActor(peer.external)))
	expectStatus(t, asAgent.StatusCode(), 403, asAgent.Body)
	key := "original-question-dispatch"
	first := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, &api.CreateAgentInvocationParams{IdempotencyKey: &key}, request))
	expectStatus(t, first.StatusCode(), 202, first.Body)
	if dispatched := peer.next(t); dispatched.ID != first.JSON202.ID || dispatched.DiscussionID == nil || *dispatched.DiscussionID != question.JSON201.ID || dispatched.Prompt != question.JSON201.Messages[0].Body {
		t.Fatalf("wrong dispatch: %+v", dispatched)
	}
	if ready := peer.ready(t, s); ready.State != "BUSY" {
		t.Fatal("a premature readiness report freed an in-flight request")
	}
	busy := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, nil, request))
	expectStatus(t, busy.StatusCode(), 409, busy.Body)
	result := review.CompleteAgentInvocationRequest{InvocationID: first.JSON202.ID, DiscussionID: &question.JSON201.ID, Body: "An inferred explanation of the captured retry path."}
	foreign := must(s.client.CompleteAgentInvocationWithResponse(ctx, first.JSON202.ID, nil, result, piActor("another-conversation")))
	expectStatus(t, foreign.StatusCode(), 403, foreign.Body)
	wrongThread := result
	wrongThread.DiscussionID = ptr("different-discussion")
	rejected := must(s.client.CompleteAgentInvocationWithResponse(ctx, first.JSON202.ID, nil, wrongThread, piActor(peer.external)))
	expectStatus(t, rejected.StatusCode(), 400, rejected.Body)
	completed := must(s.client.CompleteAgentInvocationWithResponse(ctx, first.JSON202.ID, nil, result, piActor(peer.external)))
	expectStatus(t, completed.StatusCode(), 200, completed.Body)
	duplicate := must(s.client.CompleteAgentInvocationWithResponse(ctx, first.JSON202.ID, nil, result, piActor(peer.external)))
	if !bytes.Equal(completed.Body, duplicate.Body) {
		t.Fatal("identical completion did not replay its result")
	}
	conflicting := result
	conflicting.Body = "A conflicting replacement answer"
	conflict := must(s.client.CompleteAgentInvocationWithResponse(ctx, first.JSON202.ID, nil, conflicting, piActor(peer.external)))
	expectStatus(t, conflict.StatusCode(), 409, conflict.Body)
	thread := must(s.client.GetDiscussionWithResponse(ctx, question.JSON201.ID))
	if len(thread.JSON200.Messages) != 2 || thread.JSON200.Messages[1].Author != (review.Author{Kind: "AGENT", ID: "pi:" + peer.external}) || !reflect.DeepEqual(thread.JSON200.Anchor, question.JSON201.Anchor) {
		t.Fatal("reply attribution, exact-once insertion or original anchor changed")
	}
	second := must(s.client.CaptureRevisionWithResponse(ctx, rv.ID, nil, review.CaptureRequest{}))
	expectStatus(t, second.StatusCode(), 201, second.Body)
	replay := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, &api.CreateAgentInvocationParams{IdempotencyKey: &key}, request))
	if !bytes.Equal(first.Body, replay.Body) || replay.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("capture changed committed dispatch replay")
	}
	peer.ready(t, s)
	handoff := review.CreateAgentInvocationRequest{AgentSessionID: peer.session.ID, RevisionID: revision.ID, Intent: "IMPLEMENT", DecisionID: &decision.JSON201.ID}
	implementation := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, nil, handoff))
	expectStatus(t, implementation.StatusCode(), 202, implementation.Body)
	dispatched := peer.next(t)
	if dispatched.RevisionID != revision.ID || !strings.Contains(dispatched.Prompt, decision.JSON201.ID) || !strings.Contains(dispatched.Prompt, question.JSON201.ID) || !strings.Contains(dispatched.Prompt, question.JSON201.Anchor.CodeRange.BlobHash) {
		t.Fatal("implementation lost the exact decision/revision/actionable anchor")
	}
	etag := fmt.Sprintf(`"%d"`, binding.Version)
	detached := must(s.client.SetAgentBindingWithResponse(ctx, rv.ID, &api.SetAgentBindingParams{IfMatch: &etag}, agents.BindingRequest{Mode: "DISCONNECTED"}))
	expectStatus(t, detached.StatusCode(), 200, detached.Body)
	finished := must(s.client.CompleteAgentInvocationWithResponse(ctx, implementation.JSON202.ID, nil, review.CompleteAgentInvocationRequest{Body: "Implementation summary; capture and verdict remain manual."}, piActor(peer.external)))
	expectStatus(t, finished.StatusCode(), 200, finished.Body)
	if finished.JSON200.AgentSessionID != peer.session.ID || finished.JSON200.RevisionID != revision.ID {
		t.Fatal("binding changes retargeted in-flight implementation")
	}
	after := must(s.client.ListDecisionsWithResponse(ctx, rv.ID))
	current := must(s.client.GetReviewWithResponse(ctx, rv.ID))
	unchanged := must(s.client.GetDiscussionWithResponse(ctx, question.JSON201.ID))
	if len(*after.JSON200) != 1 || *current.JSON200.CurrentRevisionID != second.JSON201.ID || unchanged.JSON200.Status != "OPEN" {
		t.Fatal("agent completion changed the verdict, captured source or thread status")
	}
}

func TestPiHTTPRestartDoesNotReplayAndDeletionRejectsLateResults(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, revision, _ := s.seed(t)
	peer := s.connectPi(t, "restart-original", rv.Repositories[0].RootPath)
	s.waitPiBinding(t, rv.ID, peer.session.ID)
	question := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, nil, review.CreateDiscussionRequest{Body: "Keep this original thread.", Anchor: review.AnchorInput{Type: "REVIEW"}}))
	expectStatus(t, question.StatusCode(), 201, question.Body)
	request := review.CreateAgentInvocationRequest{AgentSessionID: peer.session.ID, RevisionID: revision.ID, Intent: "DISCUSS", DiscussionID: &question.JSON201.ID, MessageID: &question.JSON201.Messages[0].ID}
	key := "restart-question"
	first := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, &api.CreateAgentInvocationParams{IdempotencyKey: &key}, request))
	expectStatus(t, first.StatusCode(), 202, first.Body)
	peer.next(t)
	peer.stream.stop()
	s.stop()
	s = startTestServer(t, s.root, nil)
	unknown := must(s.client.GetAgentInvocationWithResponse(ctx, first.JSON202.ID))
	expectStatus(t, unknown.StatusCode(), 200, unknown.Body)
	if unknown.JSON200.Status != "UNKNOWN" {
		t.Fatalf("restart invented completion certainty: %s", unknown.Body)
	}
	replay := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, &api.CreateAgentInvocationParams{IdempotencyKey: &key}, request))
	if !bytes.Equal(first.Body, replay.Body) {
		t.Fatal("restart lost committed dispatch replay")
	}
	resumed := s.connectPi(t, peer.external, peer.session.Cwd)
	if resumed.session.ID != peer.session.ID {
		t.Fatal("reconnect replaced the logical conversation identity")
	}
	invocations := must(s.client.ListAgentInvocationsWithResponse(ctx, rv.ID))
	if len(*invocations.JSON200) != 1 || (*invocations.JSON200)[0].Status != "UNKNOWN" {
		t.Fatal("reconnect resumed or recreated an old execution")
	}
	late := review.CompleteAgentInvocationRequest{InvocationID: first.JSON202.ID, DiscussionID: &question.JSON201.ID, Body: "A matching late answer to the original request."}
	accepted := must(s.client.CompleteAgentInvocationWithResponse(ctx, first.JSON202.ID, nil, late, piActor(peer.external)))
	expectStatus(t, accepted.StatusCode(), 200, accepted.Body)
	resumed.ready(t, s)
	pending := must(s.client.CreateAgentInvocationWithResponse(ctx, rv.ID, nil, request))
	expectStatus(t, pending.StatusCode(), 202, pending.Body)
	resumed.next(t)
	other := must(s.client.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{Path: rv.Repositories[0].RootPath, Title: "Surviving shared source"}))
	expectStatus(t, other.StatusCode(), 201, other.Body)
	deleted := must(s.client.DeleteReviewWithResponse(ctx, rv.ID))
	expectStatus(t, deleted.StatusCode(), 204, deleted.Body)
	late.InvocationID = pending.JSON202.ID
	refused := must(s.client.CompleteAgentInvocationWithResponse(ctx, pending.JSON202.ID, nil, late, piActor(peer.external)))
	expectStatus(t, refused.StatusCode(), 404, refused.Body)
	missing := must(s.client.GetAgentInvocationWithResponse(ctx, pending.JSON202.ID))
	expectStatus(t, missing.StatusCode(), 404, missing.Body)
	absentThread := must(s.client.GetDiscussionWithResponse(ctx, question.JSON201.ID))
	expectStatus(t, absentThread.StatusCode(), 404, absentThread.Body)
	survived := must(s.client.GetReviewWithResponse(ctx, other.JSON201.ID))
	expectStatus(t, survived.StatusCode(), 200, survived.Body)
	binding := must(s.client.GetAgentBindingWithResponse(ctx, other.JSON201.ID))
	if binding.JSON200.AgentSessionID == nil || *binding.JSON200.AgentSessionID != peer.session.ID {
		t.Fatal("deletion damaged another review's shared Pi/source identity")
	}
}
