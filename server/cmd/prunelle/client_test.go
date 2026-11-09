package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/httpapi"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/sessions"
	"github.com/aduverger/prunelle/server/internal/store"
)

func TestCLIInfersActualInvocationDirectoryAndPreservesExplicitOverrides(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repos, err := fixture.BuildRepos(filepath.Join(root, "feature"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{repos.API, repos.Worker} {
		cmd := exec.Command("git", "-C", path, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture tracking ref: %v: %s", err, output)
		}
	}
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := review.NewService(db, []string{root})
	sessionSvc, err := sessions.NewService(ctx, db, svc, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewHandler(ctx, db, svc, sessionSvc, httpapi.NewAuthorizer(nil, func(context.Context) (string, error) { return "fixture@example.com", nil }), "test")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	outside := t.TempDir()
	config := filepath.Join(outside, "config.toml")
	if err := os.WriteFile(config, fmt.Appendf(nil, "workspace_root_paths = [%q]\n", root), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "prunelle")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, output)
	}
	run := func(cwd string, wantSuccess bool, args ...string) []byte {
		t.Helper()
		commandArgs := append([]string{"--config", config, args[0], args[1], "--server", server.URL}, args[2:]...)
		cmd := exec.Command(binary, commandArgs...)
		cmd.Dir = cwd
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + outside, "PRUNELLE_AGENT=pi:m2b-fixture"}
		output, err := cmd.CombinedOutput()
		if (err == nil) != wantSuccess {
			t.Fatalf("CLI %v from %s: %v: %s", args, cwd, err, output)
		}
		return output
	}
	run(outside, true, "workspace", "ls", "--discover")
	workspaces, err := svc.ListWorkspaces(ctx)
	if err != nil || len(workspaces) != 0 {
		t.Fatalf("discovery registered metadata: %+v, %v", workspaces, err)
	}
	noReviews := run(repos.API, false, "review", "capture")
	if !strings.Contains(string(noReviews), "has no reviews") {
		t.Fatalf("missing-context error = %s", noReviews)
	}
	var created review.Review
	if err := json.Unmarshal(run(filepath.Join(repos.API, "src"), true, "review", "create", "--base-ref", "origin/main"), &created); err != nil {
		t.Fatal(err)
	}
	if created.Title != "feature" || created.WorkspaceID == "" {
		t.Fatalf("inferred creation = %+v", created)
	}
	var captured review.Revision
	if err := json.Unmarshal(run(repos.Worker, true, "review", "capture"), &captured); err != nil {
		t.Fatal(err)
	}
	if captured.ReviewID != created.ID || len(captured.Snapshots) != 2 || captured.Snapshots[0].BaseRef != "origin/main" {
		t.Fatalf("inferred capture = %+v", captured)
	}
	if output := run(outside, false, "review", "capture"); !strings.Contains(string(output), "no workspace contains") {
		t.Fatalf("outside cwd selected global review: %s", output)
	}
	run(outside, true, "review", "create", "--workspace", created.WorkspaceID, "Explicit review")
	if output := run(repos.API, false, "review", "capture"); !strings.Contains(string(output), "multiple reviews") || !strings.Contains(string(output), created.ID) {
		t.Fatalf("ambiguity error = %s", output)
	}
	run(outside, true, "review", "capture", created.ID)
	run(outside, true, "review", "create", created.WorkspaceID, "Legacy form")
	run(outside, false, "workspace", "delete", created.WorkspaceID)
	if _, err := svc.GetReview(ctx, created.ID); err != nil {
		t.Fatalf("unconfirmed deletion changed data: %v", err)
	}
	run(outside, true, "workspace", "delete", "--yes", created.WorkspaceID)
	if _, err := svc.GetReview(ctx, created.ID); err == nil {
		t.Fatal("confirmed deletion retained review")
	}
	var fresh review.Review
	if err := json.Unmarshal(run(filepath.Dir(repos.API), true, "review", "create", "Fresh review"), &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.WorkspaceID == created.WorkspaceID {
		t.Fatal("recreation reused deleted workspace")
	}
	run(outside, true, "workspace", "delete", "--yes", created.WorkspaceID)
	run(repos.API, true, "review", "capture")
}
