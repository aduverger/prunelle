package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/sessions"
	"github.com/aduverger/prunelle/server/internal/sessions/rex"
	"github.com/aduverger/prunelle/server/internal/sessions/sessionstest"
)

// runObservers runs the session observers for the duration of the test.
func (s *testServer) runObservers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.sessions.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestReviewWorksWhileRexIsUnavailable(t *testing.T) {
	ctx := context.Background()
	missingRex := rex.New(rex.Config{Path: filepath.Join(t.TempDir(), "rex")})
	s := newTestServer(t, nil, sessions.Registration{Name: "rex", Config: json.RawMessage(`{}`), Backend: missingRex})
	s.runObservers(t)

	rv, revision, _ := s.seed(t)
	topics := must(s.client.ListTopicsWithResponse(ctx, rv.ID))
	expectStatus(t, topics.StatusCode(), http.StatusOK, topics.Body)
	changes := must(s.client.GetSourceChangesWithResponse(ctx, revision.ID))
	expectStatus(t, changes.StatusCode(), http.StatusOK, changes.Body)

	deadline := time.Now().Add(10 * time.Second)
	for {
		backends := must(s.client.ListSessionBackendsWithResponse(ctx))
		expectStatus(t, backends.StatusCode(), http.StatusOK, backends.Body)
		health := (*backends.JSON200)[0].Health
		if health.State == sessions.HealthUnreachable {
			if health.Error == nil {
				t.Fatalf("unreachable without error: %s", backends.Body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend never reported unreachable: %s", backends.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	runtimes := must(s.client.ListRuntimeSessionsWithResponse(ctx, &api.ListRuntimeSessionsParams{}))
	expectStatus(t, runtimes.StatusCode(), http.StatusOK, runtimes.Body)
	if len(*runtimes.JSON200) != 0 {
		t.Errorf("runtimes = %s", runtimes.Body)
	}
}

func TestRuntimeSessionAssociationOverHTTP(t *testing.T) {
	ctx := context.Background()
	fake := sessionstest.NewFake()
	s := newTestServer(t, nil, sessions.Registration{Name: "rex", Config: json.RawMessage(`{"server":""}`), Backend: fake})
	rv, _, _ := s.seed(t)
	ws := must(s.client.GetWorkspaceWithResponse(ctx, rv.WorkspaceID))
	fake.Set(sessions.Observed{ExternalID: "session:a/block:a1", State: sessions.StateOnline, Label: "prunelle / shell",
		Cwd: ws.JSON200.Repositories[0].RootPath, Process: "pi", Local: true, HarnessHint: "pi", Metadata: json.RawMessage(`{"rex_block_id":"block:a1"}`)})
	backends := must(s.client.ListSessionBackendsWithResponse(ctx))
	if err := s.sessions.Sync(ctx, (*backends.JSON200)[0].ID); err != nil {
		t.Fatal(err)
	}

	listed := must(s.client.ListRuntimeSessionsWithResponse(ctx, &api.ListRuntimeSessionsParams{}))
	expectStatus(t, listed.StatusCode(), http.StatusOK, listed.Body)
	runtime := (*listed.JSON200)[0]
	if len(runtime.SuggestedWorkspaceIDs) != 1 || runtime.SuggestedWorkspaceIDs[0] != rv.WorkspaceID || len(runtime.WorkspaceIDs) != 0 {
		t.Fatalf("runtime = %s", listed.Body)
	}

	linked := must(s.client.AssociateRuntimeSessionWithResponse(ctx, rv.WorkspaceID, runtime.ID))
	expectStatus(t, linked.StatusCode(), http.StatusOK, linked.Body)
	filtered := must(s.client.ListRuntimeSessionsWithResponse(ctx, &api.ListRuntimeSessionsParams{WorkspaceId: &rv.WorkspaceID}))
	if len(*filtered.JSON200) != 1 {
		t.Fatalf("associated = %s", filtered.Body)
	}
	got := must(s.client.GetRuntimeSessionWithResponse(ctx, runtime.ID))
	expectStatus(t, got.StatusCode(), http.StatusOK, got.Body)

	unlinked := must(s.client.DissociateRuntimeSessionWithResponse(ctx, rv.WorkspaceID, runtime.ID))
	expectStatus(t, unlinked.StatusCode(), http.StatusNoContent, unlinked.Body)
	missing := must(s.client.AssociateRuntimeSessionWithResponse(ctx, rv.WorkspaceID, "rts_missing"))
	expectStatus(t, missing.StatusCode(), http.StatusNotFound, missing.Body)
	unknownWorkspace := must(s.client.ListRuntimeSessionsWithResponse(ctx, &api.ListRuntimeSessionsParams{WorkspaceId: ptr("ws_missing")}))
	expectStatus(t, unknownWorkspace.StatusCode(), http.StatusNotFound, unknownWorkspace.Body)
}

func ptr(s string) *string { return &s }
