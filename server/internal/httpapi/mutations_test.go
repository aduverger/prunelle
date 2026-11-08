package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/events"
	"github.com/aduverger/prunelle/server/internal/review"
)

func expectErrorCode(t *testing.T, status, wantStatus int, body []byte, wantCode string) {
	t.Helper()
	expectStatus(t, status, wantStatus, body)
	var envelope api.Error
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code != api.ErrorErrorCode(wantCode) {
		t.Fatalf("error = %s, want code %s", body, wantCode)
	}
}

func TestUpdatesRequireTheETagOfTheEditedVersion(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	_, _, imported := s.seed(t)
	topicID := imported.IDs["topics"]["T-tests"]
	got := must(s.client.GetTopicWithResponse(ctx, topicID))
	etag := got.HTTPResponse.Header.Get("ETag")
	if etag != `"1"` {
		t.Fatalf("ETag = %q", etag)
	}
	title := "Tests and configuration"
	edit := review.UpdateTopicRequest{Title: &title}

	missing := must(s.client.UpdateTopicWithResponse(ctx, topicID, &api.UpdateTopicParams{}, edit))
	expectErrorCode(t, missing.StatusCode(), http.StatusPreconditionRequired, missing.Body, "PRECONDITION_REQUIRED")
	updated := must(s.client.UpdateTopicWithResponse(ctx, topicID, &api.UpdateTopicParams{IfMatch: &etag}, edit))
	expectStatus(t, updated.StatusCode(), http.StatusOK, updated.Body)
	if updated.HTTPResponse.Header.Get("ETag") != `"2"` || updated.JSON200.Title != title {
		t.Errorf("updated = %s, ETag %s", updated.Body, updated.HTTPResponse.Header.Get("ETag"))
	}
	stale := must(s.client.UpdateTopicWithResponse(ctx, topicID, &api.UpdateTopicParams{IfMatch: &etag}, edit))
	expectErrorCode(t, stale.StatusCode(), http.StatusPreconditionFailed, stale.Body, "PRECONDITION_FAILED")
	unquoted := "2"
	malformed := must(s.client.UpdateTopicWithResponse(ctx, topicID, &api.UpdateTopicParams{IfMatch: &unquoted}, edit))
	expectErrorCode(t, malformed.StatusCode(), http.StatusPreconditionFailed, malformed.Body, "PRECONDITION_FAILED")
}

// throughServe makes a request arrive through Tailscale Serve from friend@example.com.
func throughServe(_ context.Context, req *http.Request) error {
	req.Host = "devbox.tail1234.ts.net"
	req.Header.Set("X-Forwarded-For", "100.101.1.2")
	req.Header.Set("Tailscale-User-Login", "friend@example.com")
	return nil
}

func TestIdempotencyKeyMakesRetriesSafe(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, []string{"friend@example.com"})
	rv, _, _ := s.seed(t)
	key := "comment-1"
	params := &api.CreateDiscussionParams{IdempotencyKey: &key}

	// A rejected request changes nothing and leaves the key usable.
	rejected := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, params, review.CreateDiscussionRequest{
		Anchor: review.AnchorInput{Type: "TOPIC"}, Body: "Why?"}))
	expectErrorCode(t, rejected.StatusCode(), http.StatusBadRequest, rejected.Body, "INVALID_ARGUMENT")

	comment := review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Is a retry safe?"}
	first := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, params, comment))
	expectStatus(t, first.StatusCode(), http.StatusCreated, first.Body)
	retry := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, params, comment))
	expectStatus(t, retry.StatusCode(), http.StatusCreated, retry.Body)
	if retry.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" || string(retry.Body) != string(first.Body) {
		t.Errorf("retry = %s (replayed %q)", retry.Body, retry.HTTPResponse.Header.Get("Idempotent-Replayed"))
	}
	// Keys belong to their caller: another reviewer's identical request is not answered with this one's response.
	otherCaller := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, params, comment, throughServe))
	expectStatus(t, otherCaller.StatusCode(), http.StatusCreated, otherCaller.Body)
	if otherCaller.HTTPResponse.Header.Get("Idempotent-Replayed") != "" || otherCaller.JSON201.ID == first.JSON201.ID {
		t.Errorf("another caller got the stored response: %s", otherCaller.Body)
	}
	comment.Body = "Something else"
	reused := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, params, comment))
	expectErrorCode(t, reused.StatusCode(), http.StatusConflict, reused.Body, "CONFLICT")

	discussions := must(s.client.ListDiscussionsWithResponse(ctx, rv.ID))
	if len(*discussions.JSON200) != 2 {
		t.Errorf("discussions = %s", discussions.Body)
	}
}

func TestChangesAreAttributedToTheCaller(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, []string{"friend@example.com"})
	rv, _, _ := s.seed(t)
	comment := review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: "Looks good."}
	remote := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, nil, comment, throughServe))
	expectStatus(t, remote.StatusCode(), http.StatusCreated, remote.Body)
	local := must(s.client.CreateDecisionWithResponse(ctx, rv.ID, nil, review.CreateDecisionRequest{Decision: "APPROVE"}))
	expectStatus(t, local.StatusCode(), http.StatusCreated, local.Body)

	if remote.JSON201.Author != (review.Author{Kind: "HUMAN", ID: "friend@example.com"}) ||
		local.JSON201.Author != (review.Author{Kind: "HUMAN", ID: ownerLogin}) {
		t.Errorf("authors: remote %+v, local %+v", remote.JSON201.Author, local.JSON201.Author)
	}

	// An agent names itself; its changes are its own, tied to the login it acts through, and it cannot decide.
	asAgent := func(ctx context.Context, req *http.Request) error {
		req.Header.Set("Prunelle-Agent", "pi:session-42")
		return throughServe(ctx, req)
	}
	stream := s.openEvents(t, "")
	stream.next(t) // ready
	reply := must(s.client.CreateMessageWithResponse(ctx, remote.JSON201.ID, nil, review.CreateMessageRequest{Body: "Fixed in the next revision."}, asAgent))
	expectStatus(t, reply.StatusCode(), http.StatusCreated, reply.Body)
	if reply.JSON201.Author != (review.Author{Kind: "AGENT", ID: "pi:session-42"}) {
		t.Errorf("agent reply author = %+v", reply.JSON201.Author)
	}
	if e := stream.next(t).decode(t); e.Actor != (events.Actor{Kind: "AGENT", ID: "pi:session-42"}) || e.Login != "friend@example.com" {
		t.Errorf("agent event = %+v", e)
	}
	decision := must(s.client.CreateDecisionWithResponse(ctx, rv.ID, nil, review.CreateDecisionRequest{Decision: "APPROVE"}, asAgent))
	expectErrorCode(t, decision.StatusCode(), http.StatusForbidden, decision.Body, "PERMISSION_DENIED")
}

type sseEvent struct {
	id    int64
	event string
	data  string
}

type sseStream struct {
	events chan sseEvent
	stop   func()
}

// openEvents connects to the event stream, optionally resuming after lastEventID.
func (s *testServer) openEvents(t *testing.T, lastEventID string) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	stream := &sseStream{events: make(chan sseEvent, 100), stop: func() { cancel(); resp.Body.Close() }}
	go func() {
		defer close(stream.events)
		lines := bufio.NewScanner(resp.Body)
		var e sseEvent
		for lines.Scan() {
			line := lines.Text()
			switch {
			case line == "":
				if e.event != "" {
					stream.events <- e
				}
				e = sseEvent{}
			case strings.HasPrefix(line, "id: "):
				e.id, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			case strings.HasPrefix(line, "event: "):
				e.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	t.Cleanup(stream.stop)
	return stream
}

func (s *sseStream) next(t *testing.T) sseEvent {
	t.Helper()
	select {
	case e, ok := <-s.events:
		if !ok {
			t.Fatal("event stream closed")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s")
	}
	return sseEvent{}
}

func (e sseEvent) decode(t *testing.T) events.Event {
	t.Helper()
	var decoded events.Event
	if err := json.Unmarshal([]byte(e.data), &decoded); err != nil {
		t.Fatalf("event data %q: %v", e.data, err)
	}
	return decoded
}

func TestEventStreamResumesWhereTheClientLeft(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, _, _ := s.seed(t)
	comment := func(body string) review.Discussion {
		resp := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, nil, review.CreateDiscussionRequest{Anchor: review.AnchorInput{Type: "REVIEW"}, Body: body}))
		expectStatus(t, resp.StatusCode(), http.StatusCreated, resp.Body)
		return *resp.JSON201
	}

	stream := s.openEvents(t, "")
	ready := stream.next(t)
	if ready.event != "ready" || ready.data != `{"sequence":`+strconv.FormatInt(ready.id, 10)+`}` || ready.id < 4 {
		t.Fatalf("ready = %+v", ready)
	}
	first := comment("First")
	created := stream.next(t)
	if e := created.decode(t); created.event != "discussion.created" || e.EntityID != first.ID || e.Sequence != created.id || *e.ReviewID != rv.ID {
		t.Errorf("event = %+v", created)
	}
	stream.stop()

	missed := comment("Posted while disconnected")
	resumed := s.openEvents(t, strconv.FormatInt(created.id, 10))
	if ready := resumed.next(t); ready.event != "ready" || ready.id != created.id {
		t.Errorf("resumed ready = %+v", ready)
	}
	if e := resumed.next(t).decode(t); e.EntityID != missed.ID {
		t.Errorf("missed event = %+v", e)
	}
}

func TestReviewStateSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, _, imported := s.seed(t)
	key := "decision-1"
	decision := review.CreateDecisionRequest{Decision: "REQUEST_CHANGES", Body: "Guard retries.", FindingIDs: []string{imported.IDs["findings"]["F-1"]}}
	submitted := must(s.client.CreateDecisionWithResponse(ctx, rv.ID, &api.CreateDecisionParams{IdempotencyKey: &key}, decision))
	expectStatus(t, submitted.StatusCode(), http.StatusCreated, submitted.Body)
	discussion := must(s.client.CreateDiscussionWithResponse(ctx, rv.ID, nil, review.CreateDiscussionRequest{
		Anchor: review.AnchorInput{Type: "FINDING", ID: &decision.FindingIDs[0]}, Body: "Use the document ID as job ID."}))
	expectStatus(t, discussion.StatusCode(), http.StatusCreated, discussion.Body)
	s.stop()

	restarted := startTestServer(t, s.root, nil)
	discussions := must(restarted.client.ListDiscussionsWithResponse(ctx, rv.ID))
	if len(*discussions.JSON200) != 1 || (*discussions.JSON200)[0].Messages[0].Body != "Use the document ID as job ID." {
		t.Errorf("discussions after restart = %s", discussions.Body)
	}
	retry := must(restarted.client.CreateDecisionWithResponse(ctx, rv.ID, &api.CreateDecisionParams{IdempotencyKey: &key}, decision))
	expectStatus(t, retry.StatusCode(), http.StatusCreated, retry.Body)
	if retry.JSON201.ID != submitted.JSON201.ID || retry.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("retried decision = %s", retry.Body)
	}
	decisions := must(restarted.client.ListDecisionsWithResponse(ctx, rv.ID))
	if len(*decisions.JSON200) != 1 {
		t.Errorf("decisions after restart = %s", decisions.Body)
	}

	stream := restarted.openEvents(t, "0")
	stream.next(t) // ready
	var types []string
	for len(types) < 6 {
		types = append(types, stream.next(t).event)
	}
	want := []string{"workspace.created", "review.created", "revision.created", "import.created", "decision.created", "discussion.created"}
	if strings.Join(types, " ") != strings.Join(want, " ") {
		t.Errorf("events after restart = %v", types)
	}
}
