package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aduverger/prunelle/server/internal/git"
)

func canonicalDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", invalid("path must be an absolute server-side directory")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", invalid("source path %s: %v", path, err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", invalid("source path must be a directory")
	}
	return canonical, nil
}

func containsPath(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (r Review) ContainsPath(path string) bool {
	return r.ContextPath != nil && containsPath(*r.ContextPath, path) || r.ContainsRepositoryPath(path)
}

func (r Review) ContainsRepositoryPath(path string) bool {
	for _, repository := range r.Repositories {
		if containsPath(repository.RootPath, path) {
			return true
		}
	}
	return false
}

func (s *Service) sourceRepositories(ctx context.Context, requested string, inferContext bool) (string, []CreateRepositoryRequest, error) {
	path, err := canonicalDirectory(requested)
	if err != nil {
		return "", nil, err
	}
	if inferContext {
		reviews, err := s.ListReviews(ctx)
		if err != nil {
			return "", nil, err
		}
		contexts := []string{}
		for _, r := range reviews {
			if r.ContextPath != nil && containsPath(*r.ContextPath, path) && !slices.Contains(contexts, *r.ContextPath) {
				contexts = append(contexts, *r.ContextPath)
			}
		}
		if len(contexts) > 1 {
			return "", nil, &Error{Code: CodeConflict, Message: "multiple source contexts contain cwd; choose review create --path explicitly", Details: contexts}
		}
		if len(contexts) == 1 {
			path = contexts[0]
		}
	}
	root, rootErr := git.ResolveRoot(ctx, path)
	if rootErr == nil {
		return root, []CreateRepositoryRequest{{Label: filepath.Base(root), Path: root}}, nil
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if _, err := os.Lstat(filepath.Join(path, ".git")); !errors.Is(err, os.ErrNotExist) {
		return "", nil, invalid("repository %s: %v", path, rootErr)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", nil, invalid("read source %s: %v", path, err)
	}
	repositories := []CreateRepositoryRequest{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".git" {
			continue
		}
		child := filepath.Join(path, entry.Name())
		if _, err := os.Lstat(filepath.Join(child, ".git")); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", nil, invalid("inspect repository %s: %v", child, err)
		}
		root, err := git.ResolveRoot(ctx, child)
		if err != nil {
			return "", nil, invalid("repository %s: %v", child, err)
		}
		if root != child {
			return "", nil, invalid("repository %s changed its canonical root", child)
		}
		repositories = append(repositories, CreateRepositoryRequest{Label: entry.Name(), Path: root})
	}
	if len(repositories) == 0 {
		return "", nil, invalid("%s is neither a Git worktree nor a folder of immediate Git worktrees; choose --path explicitly", path)
	}
	return path, repositories, nil
}

func (s *Service) ResolveReview(ctx context.Context, path string) (Review, error) {
	canonical, err := canonicalDirectory(path)
	if err != nil {
		return Review{}, err
	}
	reviews, err := s.ListReviews(ctx)
	if err != nil {
		return Review{}, err
	}
	matches := []Review{}
	for _, r := range reviews {
		if r.ContainsPath(canonical) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return Review{}, invalid("no reviews contain %s; run review create from the intended source folder or select a review ID", canonical)
	case 1:
		return matches[0], nil
	}
	details := make([]string, len(matches))
	for i, r := range matches {
		details[i] = r.ID + ": " + r.Title
	}
	return Review{}, &Error{Code: CodeConflict, Message: "multiple reviews contain cwd; select an explicit review ID", Details: details}
}
