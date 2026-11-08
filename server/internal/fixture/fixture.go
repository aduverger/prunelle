// Package fixture builds a two-repository feature workspace and its review manifest.
// The same data seeds demos (prunelle fixture) and tests.
package fixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aduverger/prunelle/server/internal/review"
)

const (
	BaseBranch    = "main"
	FeatureBranch = "feature/async-processing"
	ReviewTitle   = "Process documents asynchronously"
)

// Repos locates the fixture repositories.
type Repos struct {
	API    string
	Worker string
}

// UnassignedFiles lists changed files that no topic links to, per repository label.
var UnassignedFiles = map[string][]string{
	"api": {"docs/async-upload.md", "package.json"},
}

// BuildRepos creates the api and worker repositories under dir, which must not contain them yet.
// Each ends on the feature branch with committed, staged, unstaged, and (api only) untracked changes.
func BuildRepos(dir string) (Repos, error) {
	repos := Repos{API: filepath.Join(dir, "api"), Worker: filepath.Join(dir, "worker")}
	steps := []func() error{
		func() error {
			return buildRepo(repos.API, []step{
				write("package.json", apiPackageJSONBase),
				write("src/processing.ts", apiProcessingBase),
				write("src/routes/upload.ts", apiUploadBase),
				commit("Process uploads synchronously"),
				branch(FeatureBranch),
				write("package.json", apiPackageJSONFeature),
				remove("src/processing.ts"),
				write("src/queue.ts", apiQueue),
				write("src/routes/upload.ts", apiUploadCommitted),
				commit("Enqueue document processing"),
				write("src/routes/status.ts", apiStatus),
				stage("src/routes/status.ts"),
				write("src/routes/upload.ts", apiUploadWorktree),
				write("docs/async-upload.md", apiDocsUntracked),
			})
		},
		func() error {
			return buildRepo(repos.Worker, []step{
				write("README.md", workerReadme),
				write("worker/config.py", workerConfigBase),
				commit("Initial worker"),
				branch(FeatureBranch),
				write("worker/consumer.py", workerConsumer),
				write("worker/processing.py", workerProcessing),
				commit("Consume document processing jobs"),
				write("tests/test_consumer.py", workerConsumerTest),
				stage("tests/test_consumer.py"),
				write("worker/config.py", workerConfigWorktree),
			})
		},
	}
	for _, build := range steps {
		if err := build(); err != nil {
			return Repos{}, err
		}
	}
	return repos, nil
}

// WorkspaceRequest registers both repositories against the base branch.
func WorkspaceRequest(repos Repos) review.CreateWorkspaceRequest {
	return review.CreateWorkspaceRequest{
		Name: "Document platform",
		Repositories: []review.CreateRepositoryRequest{
			{Label: "api", Path: repos.API, BaseRef: BaseBranch},
			{Label: "worker", Path: repos.Worker, BaseRef: BaseBranch},
		},
	}
}

type step func(root string) error

func write(path, content string) step {
	return func(root string) error {
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		return os.WriteFile(abs, []byte(content), 0o644)
	}
}

func remove(path string) step {
	return func(root string) error { return gitCmd(root, "rm", "-q", path) }
}

func stage(path string) step {
	return func(root string) error { return gitCmd(root, "add", path) }
}

func commit(message string) step {
	return func(root string) error {
		if err := gitCmd(root, "add", "-A"); err != nil {
			return err
		}
		return gitCmd(root, "commit", "-q", "-m", message)
	}
}

func branch(name string) step {
	return func(root string) error { return gitCmd(root, "switch", "-q", "-c", name) }
}

func buildRepo(root string, steps []step) error {
	if _, err := os.Stat(root); err == nil {
		return fmt.Errorf("%s already exists", root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if err := gitCmd(root, "init", "-q", "-b", BaseBranch); err != nil {
		return err
	}
	for _, s := range steps {
		if err := s(root); err != nil {
			return err
		}
	}
	return nil
}

// gitCmd runs git with a fixed identity and no user configuration, so fixtures are reproducible.
func gitCmd(root string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Prunelle Fixture", "GIT_AUTHOR_EMAIL=fixture@prunelle.invalid", "GIT_AUTHOR_DATE=2026-11-01T10:00:00Z",
		"GIT_COMMITTER_NAME=Prunelle Fixture", "GIT_COMMITTER_EMAIL=fixture@prunelle.invalid", "GIT_COMMITTER_DATE=2026-11-01T10:00:00Z",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}
