package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/httpapi"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/sessions"
	"github.com/aduverger/prunelle/server/internal/store"
)

const ownerLogin = "owner@example.com"

func ownerLookup(context.Context) (string, error) { return ownerLogin, nil }

type testServer struct {
	client   *api.ClientWithResponses
	url      string
	reposDir string
	sessions *sessions.Service
}

// newTestServer serves the API and returns a client whose responses are validated against openapi.yaml.
func newTestServer(t *testing.T, allowedLogins []string, backends ...sessions.Registration) *testServer {
	t.Helper()
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reposDir := filepath.Join(root, "repos")
	reviews := review.NewService(db, []string{reposDir})
	sessionSvc, err := sessions.NewService(ctx, db, reviews, backends)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewHandler(reviews, sessionSvc, httpapi.NewAuthorizer(allowedLogins, ownerLookup), "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	spec.Servers = nil
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: &validatingTransport{t: t, router: router}}
	client, err := api.NewClientWithResponses(srv.URL, api.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	return &testServer{client: client, url: srv.URL, reposDir: reposDir, sessions: sessionSvc}
}

type validatingTransport struct {
	t      *testing.T
	router routers.Router
}

func (v *validatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	route, params, err := v.router.FindRoute(req)
	if err != nil {
		v.t.Errorf("%s %s: no route in spec: %v", req.Method, req.URL.Path, err)
		return resp, nil
	}
	req.Body = io.NopCloser(bytes.NewReader(reqBody))
	input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	err = openapi3filter.ValidateResponse(req.Context(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input, Status: resp.StatusCode, Header: resp.Header,
		Body: io.NopCloser(bytes.NewReader(body)), Options: &openapi3filter.Options{IncludeResponseStatus: true},
	})
	if err != nil {
		v.t.Errorf("%s %s: response does not match spec: %v\n%s", req.Method, req.URL.Path, err, body)
	}
	return resp, nil
}

// must panics on transport errors; tests assert on status codes separately.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func expectStatus(t *testing.T, got, want int, body []byte) {
	t.Helper()
	if got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, body)
	}
}

// seed builds the fixture repositories and imports the fixture manifest through the API.
func (s *testServer) seed(t *testing.T) (review.Review, review.Revision, review.ImportResult) {
	t.Helper()
	ctx := context.Background()
	repos := must(fixture.BuildRepos(s.reposDir))
	ws := must(s.client.CreateWorkspaceWithResponse(ctx, fixture.WorkspaceRequest(repos)))
	expectStatus(t, ws.StatusCode(), http.StatusCreated, ws.Body)
	rv := must(s.client.CreateReviewWithResponse(ctx, review.CreateReviewRequest{WorkspaceID: ws.JSON201.ID, Title: fixture.ReviewTitle}))
	expectStatus(t, rv.StatusCode(), http.StatusCreated, rv.Body)
	revision := must(s.client.CaptureRevisionWithResponse(ctx, rv.JSON201.ID))
	expectStatus(t, revision.StatusCode(), http.StatusCreated, revision.Body)
	imported := must(s.client.ImportManifestWithResponse(ctx, rv.JSON201.ID, review.ImportRequest{Manifest: fixture.Manifest()}))
	expectStatus(t, imported.StatusCode(), http.StatusCreated, imported.Body)
	return *rv.JSON201, *revision.JSON201, *imported.JSON201
}

func TestFixtureReviewOverHTTP(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, revision, imported := s.seed(t)

	health := must(s.client.GetHealthWithResponse(ctx))
	expectStatus(t, health.StatusCode(), http.StatusOK, health.Body)

	workspaces := must(s.client.ListWorkspacesWithResponse(ctx))
	if len(*workspaces.JSON200) != 1 || len((*workspaces.JSON200)[0].Repositories) != 2 {
		t.Fatalf("workspaces = %s", workspaces.Body)
	}
	reviews := must(s.client.ListReviewsWithResponse(ctx, &api.ListReviewsParams{WorkspaceId: &rv.WorkspaceID}))
	if len(*reviews.JSON200) != 1 || *(*reviews.JSON200)[0].CurrentRevisionID != revision.ID {
		t.Fatalf("reviews = %s", reviews.Body)
	}
	must(s.client.ListRevisionsWithResponse(ctx, rv.ID))
	must(s.client.GetRevisionWithResponse(ctx, revision.ID))

	topics := must(s.client.ListTopicsWithResponse(ctx, rv.ID))
	if len(*topics.JSON200) != 4 {
		t.Fatalf("topics = %s", topics.Body)
	}
	for _, topic := range *topics.JSON200 {
		must(s.client.GetTopicWithResponse(ctx, topic.ID))
		reps := must(s.client.ListTopicRepresentationsWithResponse(ctx, topic.ID))
		if len(*reps.JSON200) != 4 {
			t.Errorf("%s representations = %s", topic.Title, reps.Body)
		}
	}

	changes := must(s.client.GetSourceChangesWithResponse(ctx, revision.ID))
	if changes.JSON200.UnassignedCount != 2 {
		t.Errorf("unassigned = %d", changes.JSON200.UnassignedCount)
	}
	apiRepo := changes.JSON200.Repositories[0]
	for _, f := range apiRepo.Files {
		file := must(s.client.GetFileWithResponse(ctx, revision.ID, &api.GetFileParams{RepositoryId: apiRepo.RepositoryID, Path: f.Path}))
		expectStatus(t, file.StatusCode(), http.StatusOK, file.Body)
	}

	must(s.client.ListIntentsWithResponse(ctx, rv.ID))
	must(s.client.ListChangesWithResponse(ctx, rv.ID, &api.ListChangesParams{RevisionId: &revision.ID}))
	must(s.client.ListFindingsWithResponse(ctx, rv.ID))
	must(s.client.ListAttentionWithResponse(ctx, rv.ID))
	must(s.client.ListImportsWithResponse(ctx, rv.ID))
	evidence := must(s.client.ListEvidenceWithResponse(ctx, rv.ID))
	for _, e := range *evidence.JSON200 {
		must(s.client.GetEvidenceWithResponse(ctx, e.ID))
	}
	if imported.IDs["topics"]["T-async"] == "" {
		t.Errorf("import ids = %v", imported.IDs)
	}
}

func TestErrorsUseEnvelope(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	resp := must(s.client.GetReviewWithResponse(ctx, "rvw_missing"))
	expectStatus(t, resp.StatusCode(), http.StatusNotFound, resp.Body)
	if resp.JSONDefault == nil || resp.JSONDefault.Error.Code != "NOT_FOUND" {
		t.Fatalf("body = %s", resp.Body)
	}
}

func TestRequestValidationRejectsUnknownManifestFields(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, _, _ := s.seed(t)

	body := `{"manifest": {"author": {"kind": "AGENT", "id": "pi:x"}, "findings": [
		{"key": "F", "kind": "DESIGN", "severity": "LOW", "title": "t", "rationale": "r", "evidense": ["E"]}]}}`
	resp := must(s.client.ImportManifestWithBodyWithResponse(ctx, rv.ID, "application/json", strings.NewReader(body)))
	expectStatus(t, resp.StatusCode(), http.StatusBadRequest, resp.Body)
	if resp.JSONDefault.Error.Code != "INVALID_ARGUMENT" || !strings.Contains(resp.JSONDefault.Error.Message, "evidense") {
		t.Fatalf("body = %s", resp.Body)
	}
}

func TestInvalidManifestReportsEveryProblem(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, nil)
	rv, _, _ := s.seed(t)
	m := fixture.Manifest()
	m.Topics[0].Changes = []string{"C-missing"}
	m.Topics[0].Findings = []string{"F-missing"}
	resp := must(s.client.ImportManifestWithResponse(ctx, rv.ID, review.ImportRequest{Manifest: m}))
	expectStatus(t, resp.StatusCode(), http.StatusBadRequest, resp.Body)
	if len(resp.JSONDefault.Error.Details) != 2 {
		t.Fatalf("details = %v", resp.JSONDefault.Error.Details)
	}
}

func TestAccessPolicy(t *testing.T) {
	s := newTestServer(t, []string{"friend@example.com"})
	cases := []struct {
		name       string
		path       string
		headers    map[string]string
		remoteAddr string
		want       int
	}{
		{name: "local process", path: "/v1/workspaces", want: http.StatusOK},
		{name: "rebound foreign host", path: "/v1/workspaces", headers: map[string]string{"Host": "evil.example:7390"}, want: http.StatusForbidden},
		{name: "rebound foreign host with spoofed proxy headers", path: "/v1/workspaces", headers: map[string]string{"Host": "evil.example:7390", "X-Forwarded-For": "100.101.1.2", "Tailscale-User-Login": ownerLogin}, want: http.StatusForbidden},
		{name: "owner through serve by MagicDNS name", path: "/v1/workspaces", headers: map[string]string{"Host": "devbox.tail1234.ts.net", "X-Forwarded-For": "100.101.1.2", "Tailscale-User-Login": ownerLogin}, want: http.StatusOK},
		{name: "owner through serve", path: "/v1/workspaces", headers: map[string]string{"X-Forwarded-For": "100.101.1.2", "Tailscale-User-Login": ownerLogin}, want: http.StatusOK},
		{name: "allowed login", path: "/v1/workspaces", headers: map[string]string{"X-Forwarded-For": "100.101.1.2", "Tailscale-User-Login": "friend@example.com"}, want: http.StatusOK},
		{name: "other login", path: "/v1/workspaces", headers: map[string]string{"X-Forwarded-For": "100.101.1.2", "Tailscale-User-Login": "stranger@example.com"}, want: http.StatusForbidden},
		{name: "tagged device through serve", path: "/v1/workspaces", headers: map[string]string{"X-Forwarded-For": "100.101.1.2", "X-Forwarded-Proto": "https"}, want: http.StatusUnauthorized},
		{name: "health is public", path: "/v1/health", headers: map[string]string{"X-Forwarded-For": "100.101.1.2"}, want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, s.url+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if host := tc.headers["Host"]; host != "" {
				req.Host = host
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			expectStatus(t, resp.StatusCode, tc.want, body)
			if tc.want >= 400 {
				var envelope api.Error
				if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Message == "" {
					t.Errorf("error body = %s", body)
				}
			}
		})
	}
}

func TestNonLoopbackConnectionsAreRejected(t *testing.T) {
	auth := httpapi.NewAuthorizer(nil, func(context.Context) (string, error) { return "", errors.New("no tailscale") })
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces", nil)
	req.RemoteAddr = "100.64.1.2:4567"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

// Without a known owner (Tailscale unavailable or a tagged machine), only allowed logins get in.
func TestUnknownOwnerAdmitsOnlyAllowedLogins(t *testing.T) {
	auth := httpapi.NewAuthorizer([]string{"friend@example.com"}, func(context.Context) (string, error) { return "", errors.New("no tailscale") })
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for login, want := range map[string]int{ownerLogin: http.StatusForbidden, "friend@example.com": http.StatusOK} {
		req := httptest.NewRequest(http.MethodGet, "/v1/workspaces", nil)
		req.RemoteAddr = "127.0.0.1:4567"
		req.Host = "devbox.tail1234.ts.net"
		req.Header.Set("X-Forwarded-For", "100.101.1.2")
		req.Header.Set("Tailscale-User-Login", login)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: status = %d, want %d", login, rec.Code, want)
		}
	}
}
