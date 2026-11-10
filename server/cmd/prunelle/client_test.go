package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/httpapi"
	"github.com/aduverger/prunelle/server/internal/review"
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
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := review.NewService(db, "origin/main")
	handler, err := httpapi.NewHandler(ctx, db, svc, httpapi.NewAuthorizer(nil, func(context.Context) (string, error) { return "fixture@example.com", nil }), "test")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	outside := t.TempDir()
	config := filepath.Join(outside, "config.toml")
	if err := os.WriteFile(config, []byte("[snapshot]\ndefault_base_ref = \"origin/main\"\n"), 0o600); err != nil {
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
	run(outside, false, "workspace", "ls")
	reviews, err := svc.ListReviews(ctx)
	if err != nil || len(reviews) != 0 {
		t.Fatalf("reviews created without a command: %+v %v", reviews, err)
	}
	noReviews := run(repos.API, false, "review", "capture")
	if !strings.Contains(string(noReviews), "no reviews") {
		t.Fatalf("missing-context error = %s", noReviews)
	}
	var created review.Review
	if err := json.Unmarshal(run(filepath.Dir(repos.API), true, "review", "create"), &created); err != nil {
		t.Fatal(err)
	}
	if created.Title != "feature" || created.ContextPath == nil || len(created.Repositories) != 2 {
		t.Fatalf("inferred creation = %+v", created)
	}
	var captured review.Revision
	if err := json.Unmarshal(run(repos.Worker, true, "review", "capture"), &captured); err != nil {
		t.Fatal(err)
	}
	if captured.ReviewID != created.ID || len(captured.Snapshots) != 2 || captured.Snapshots[0].BaseRef != "origin/main" {
		t.Fatalf("inferred capture = %+v", captured)
	}
	if output := run(outside, false, "review", "capture"); !strings.Contains(string(output), "no reviews contain") {
		t.Fatalf("outside cwd selected global review: %s", output)
	}
	run(outside, true, "review", "create", "--path", filepath.Dir(repos.API), "Explicit review")
	if output := run(repos.API, false, "review", "capture"); !strings.Contains(string(output), "multiple reviews") || !strings.Contains(string(output), created.ID) {
		t.Fatalf("ambiguity error = %s", output)
	}
	run(outside, true, "review", "capture", created.ID)
	run(outside, false, "review", "create", "--base-ref", "main", "Creation is not capture")
	var manual review.Review
	if err := json.Unmarshal(run(root, true, "review", "create", "--repo", "api=feature/api", "--repo", "worker=feature/worker", "Selected repositories"), &manual); err != nil {
		t.Fatal(err)
	}
	if manual.ContextPath != nil || len(manual.Repositories) != 2 || manual.Repositories[0].RootPath != repos.API || manual.Repositories[1].RootPath != repos.Worker {
		t.Fatalf("inline sources changed: %+v", manual)
	}
	var mixed review.Revision
	if err := json.Unmarshal(run(outside, true, "review", "capture", "--repo-base-ref", manual.Repositories[0].ID+"=main", manual.ID), &mixed); err != nil {
		t.Fatal(err)
	}
	if mixed.Snapshots[0].BaseRef != "main" || mixed.Snapshots[1].BaseRef != "origin/main" {
		t.Fatalf("CLI overrides lost: %+v", mixed.Snapshots)
	}
	run(outside, false, "review", "delete", created.ID)
	if _, err := svc.GetReview(ctx, created.ID); err != nil {
		t.Fatalf("unconfirmed deletion changed data: %v", err)
	}
	run(outside, true, "review", "delete", "--yes", created.ID)
	if _, err := svc.GetReview(ctx, created.ID); err == nil {
		t.Fatal("confirmed deletion retained review")
	}
	var fresh review.Review
	if err := json.Unmarshal(run(filepath.Dir(repos.API), true, "review", "create", "Fresh review"), &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.ID == created.ID {
		t.Fatal("recreation reused deleted review")
	}
	run(outside, true, "review", "delete", "--yes", created.ID)
	run(repos.API, true, "review", "capture", "--base-ref", "main", fresh.ID)
}
