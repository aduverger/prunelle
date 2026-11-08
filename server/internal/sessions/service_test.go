package sessions_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/sessions"
	"github.com/aduverger/prunelle/server/internal/sessions/sessionstest"
	"github.com/aduverger/prunelle/server/internal/store"
)

type env struct {
	ctx       context.Context
	db        *store.DB
	reviews   *review.Service
	workspace review.Workspace
	repos     fixture.Repos
	fake      *sessionstest.Fake
	svc       *sessions.Service
}

func newEnv(t *testing.T, runtimes ...sessions.Observed) *env {
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
	repos, err := fixture.BuildRepos(reposDir)
	if err != nil {
		t.Fatal(err)
	}
	reviews := review.NewService(db, []string{reposDir})
	ws, err := reviews.CreateWorkspace(ctx, fixture.WorkspaceRequest(repos))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{ctx: ctx, db: db, reviews: reviews, workspace: ws, repos: repos, fake: sessionstest.NewFake(runtimes...)}
	e.svc = e.newService(t, e.fake)
	return e
}

func (e *env) newService(t *testing.T, backends ...sessions.Backend) *sessions.Service {
	t.Helper()
	var regs []sessions.Registration
	for i, b := range backends {
		regs = append(regs, sessions.Registration{Name: []string{"rex", "rex-2"}[i], Config: json.RawMessage(`{}`), Backend: b})
	}
	svc, err := sessions.NewService(e.ctx, e.db, e.reviews, regs)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func (e *env) backendID(t *testing.T) string {
	t.Helper()
	backends, err := e.svc.ListBackends(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return backends[0].ID
}

func (e *env) runtimes(t *testing.T) map[string]sessions.RuntimeSession {
	t.Helper()
	list, err := e.svc.ListRuntimeSessions(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]sessions.RuntimeSession{}
	for _, r := range list {
		m[r.ExternalID] = r
	}
	return m
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func piRuntime(cwd string) sessions.Observed {
	return sessions.Observed{ExternalID: "s/pi", State: sessions.StateOnline, Label: "prunelle / shell", Cwd: cwd,
		Process: "pi", Local: true, HarnessHint: "pi", Metadata: json.RawMessage(`{"rex_block_id":"block:pi"}`)}
}

func TestSyncAssignsStableIDsAndStopsVanishedRuntimes(t *testing.T) {
	e := newEnv(t, piRuntime("/elsewhere"), sessions.Observed{ExternalID: "s/shell", State: sessions.StateOnline, Label: "notes / shell"})
	backendID := e.backendID(t)
	if err := e.svc.Sync(e.ctx, backendID); err != nil {
		t.Fatal(err)
	}
	first := e.runtimes(t)
	if len(first) != 2 || first["s/pi"].State != sessions.StateOnline || *first["s/pi"].HarnessHint != "pi" {
		t.Fatalf("runtimes = %+v", first)
	}
	if !slices.Equal(first["s/pi"].Capabilities, e.fake.Capabilities()) {
		t.Errorf("capabilities = %v, want the backend's", first["s/pi"].Capabilities)
	}

	e.fake.Remove("s/shell")
	if err := e.svc.Sync(e.ctx, backendID); err != nil {
		t.Fatal(err)
	}
	second := e.runtimes(t)
	if second["s/pi"].ID != first["s/pi"].ID {
		t.Errorf("ID changed across syncs")
	}
	if second["s/shell"].State != sessions.StateStopped || second["s/shell"].ID != first["s/shell"].ID {
		t.Errorf("vanished runtime = %+v", second["s/shell"])
	}
}

func TestObserverFollowsEventsAndOutages(t *testing.T) {
	e := newEnv(t, piRuntime("/elsewhere"))
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	eventually(t, "initial discovery", func() bool { return e.runtimes(t)["s/pi"].State == sessions.StateOnline })
	backends, err := e.svc.ListBackends(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if backends[0].Health.State != sessions.HealthReachable || backends[0].Health.Info["backend"] != "fake" {
		t.Errorf("health = %+v", backends[0].Health)
	}

	e.fake.Set(sessions.Observed{ExternalID: "s/new", State: sessions.StateOnline, Label: "new / shell"})
	eventually(t, "created runtime", func() bool { return e.runtimes(t)["s/new"].State == sessions.StateOnline })
	e.fake.Remove("s/new")
	eventually(t, "closed runtime", func() bool { return e.runtimes(t)["s/new"].State == sessions.StateStopped })

	e.fake.SetUnreachable(sessionstest.ErrUnreachable)
	eventually(t, "unknown state during outage", func() bool { return e.runtimes(t)["s/pi"].State == sessions.StateUnknown })
	backends, _ = e.svc.ListBackends(e.ctx)
	if backends[0].Health.State != sessions.HealthUnreachable || backends[0].Health.Error == nil {
		t.Errorf("health during outage = %+v", backends[0].Health)
	}
	if e.runtimes(t)["s/new"].State != sessions.StateStopped {
		t.Error("an outage must not revive or alter stopped runtimes")
	}

	e.fake.SetUnreachable(nil)
	eventually(t, "recovery", func() bool { return e.runtimes(t)["s/pi"].State == sessions.StateOnline })
}

func TestObserverClosesEventStreamWhenHandlingAnEventFails(t *testing.T) {
	e := newEnv(t, piRuntime("/elsewhere"))
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	eventually(t, "subscription", func() bool { return e.fake.Subscriptions() == 1 })

	e.fake.FailNextInspect(errors.New("rex timed out"))
	e.fake.Set(piRuntime("/elsewhere/again"))
	eventually(t, "stream closed before retrying", func() bool { return e.fake.Subscriptions() == 0 })
	eventually(t, "resubscribed", func() bool { return e.fake.Subscriptions() == 1 })
}

func TestRestartMarksRuntimesUnknownAndDisablesRemovedBackends(t *testing.T) {
	e := newEnv(t, piRuntime("/elsewhere"))
	if err := e.svc.Sync(e.ctx, e.backendID(t)); err != nil {
		t.Fatal(err)
	}
	e.svc = e.newService(t) // restart with no backend configured
	if got := e.runtimes(t)["s/pi"]; got.State != sessions.StateUnknown || len(got.Capabilities) != 0 {
		t.Errorf("after restart without its backend: state %s, capabilities %v", got.State, got.Capabilities)
	}
	backends, err := e.svc.ListBackends(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(backends) != 1 || backends[0].Enabled || backends[0].Health.State != sessions.HealthDisabled {
		t.Errorf("backends = %+v", backends)
	}
}

func TestAssociationIsExplicitAndSuggestionsComeFromLocalCwd(t *testing.T) {
	e := newEnv(t, piRuntime(""), sessions.Observed{ExternalID: "s/remote", State: sessions.StateOnline, Label: "remote", Local: false})
	e.fake.Set(piRuntime(filepath.Join(e.repos.Worker, "worker")))
	e.fake.Set(sessions.Observed{ExternalID: "s/remote", State: sessions.StateOnline, Label: "remote", Cwd: e.repos.API, Local: false})
	if err := e.svc.Sync(e.ctx, e.backendID(t)); err != nil {
		t.Fatal(err)
	}
	pi := e.runtimes(t)["s/pi"]
	if len(pi.WorkspaceIDs) != 0 || !slices.Equal(pi.SuggestedWorkspaceIDs, []string{e.workspace.ID}) {
		t.Fatalf("pi associations = %v suggested = %v", pi.WorkspaceIDs, pi.SuggestedWorkspaceIDs)
	}
	if remote := e.runtimes(t)["s/remote"]; len(remote.SuggestedWorkspaceIDs) != 0 {
		t.Errorf("remote runtime suggested %v", remote.SuggestedWorkspaceIDs)
	}
	associated, err := e.svc.ListRuntimeSessions(e.ctx, &e.workspace.ID)
	if err != nil || len(associated) != 0 {
		t.Fatalf("associated before link = %v, %v", associated, err)
	}

	linked, err := e.svc.Associate(e.ctx, e.workspace.ID, pi.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(linked.WorkspaceIDs, []string{e.workspace.ID}) || len(linked.SuggestedWorkspaceIDs) != 0 {
		t.Errorf("after link = %v / %v", linked.WorkspaceIDs, linked.SuggestedWorkspaceIDs)
	}
	if _, err := e.svc.Associate(e.ctx, e.workspace.ID, pi.ID); err != nil {
		t.Errorf("repeat link: %v", err)
	}
	associated, err = e.svc.ListRuntimeSessions(e.ctx, &e.workspace.ID)
	if err != nil || len(associated) != 1 || associated[0].ID != pi.ID {
		t.Fatalf("associated = %+v, %v", associated, err)
	}

	if err := e.svc.Dissociate(e.ctx, e.workspace.ID, pi.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.runtimes(t)["s/pi"]; len(got.WorkspaceIDs) != 0 {
		t.Errorf("after unlink = %v", got.WorkspaceIDs)
	}

	var reviewErr *review.Error
	if _, err := e.svc.Associate(e.ctx, "ws_missing", pi.ID); !errors.As(err, &reviewErr) || reviewErr.Code != review.CodeNotFound {
		t.Errorf("unknown workspace: %v", err)
	}
	if _, err := e.svc.Associate(e.ctx, e.workspace.ID, "rts_missing"); !errors.As(err, &reviewErr) || reviewErr.Code != review.CodeNotFound {
		t.Errorf("unknown runtime: %v", err)
	}
}

func TestFakeSatisfiesContract(t *testing.T) {
	sessionstest.RunContract(t, func(t *testing.T) sessionstest.Subject {
		fake := sessionstest.NewFake(piRuntime("/work"))
		return sessionstest.Subject{Backend: fake, MissingID: "s/missing", Change: func() { fake.Set(piRuntime("/work/other")) }}
	})
}
