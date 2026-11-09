package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aduverger/prunelle/server/internal/git"
)

func (s *Service) workspaceRoots() ([]string, error) {
	roots := []string{}
	for _, path := range s.allowedRoots {
		root, err := filepath.EvalSymlinks(path)
		if err == nil {
			root, err = filepath.Abs(root)
		}
		if err != nil {
			return nil, invalid("workspace root %s: %v", path, err)
		}
		if !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	slices.Sort(roots)
	return roots, nil
}

func (s *Service) DiscoverWorkspaces(ctx context.Context) ([]WorkspaceCandidate, error) {
	roots, err := s.workspaceRoots()
	if err != nil {
		return nil, err
	}
	candidates := []WorkspaceCandidate{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, invalid("read workspace root %s: %v", root, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == ".git" {
				continue
			}
			candidate, err := s.discoverWorkspace(ctx, filepath.Join(root, entry.Name()), roots)
			if err != nil {
				return nil, err
			}
			if candidate != nil {
				candidates = append(candidates, *candidate)
			}
		}
	}
	slices.SortFunc(candidates, func(a, b WorkspaceCandidate) int { return strings.Compare(a.Path, b.Path) })
	return candidates, nil
}

func (s *Service) resolveWorkspace(ctx context.Context, path string) (WorkspaceCandidate, error) {
	if !filepath.IsAbs(path) {
		return WorkspaceCandidate{}, invalid("workspace path must be an absolute server-side path")
	}
	cwd, err := filepath.EvalSymlinks(path)
	if err != nil {
		return WorkspaceCandidate{}, invalid("workspace path %s: %v", path, err)
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return WorkspaceCandidate{}, invalid("workspace path must be a directory")
	}
	roots, err := s.workspaceRoots()
	if err != nil {
		return WorkspaceCandidate{}, err
	}
	var selected *WorkspaceCandidate
	for _, root := range roots {
		rel, err := filepath.Rel(root, cwd)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		first, _, _ := strings.Cut(rel, string(filepath.Separator))
		candidate, err := s.discoverWorkspace(ctx, filepath.Join(root, first), roots)
		if err != nil {
			return WorkspaceCandidate{}, err
		}
		if candidate != nil && (selected == nil || len(candidate.Path) > len(selected.Path)) {
			selected = candidate
		}
	}
	if selected == nil {
		return WorkspaceCandidate{}, invalid("no workspace contains %s; configure workspace_root_paths with its parent directory, or use an explicit workspace/review ID", path)
	}
	return *selected, nil
}

func (s *Service) discoverWorkspace(ctx context.Context, path string, roots []string) (*WorkspaceCandidate, error) {
	if slices.Contains(roots, path) {
		return nil, nil
	}
	candidate := WorkspaceCandidate{Path: path, Name: filepath.Base(path), Repositories: []CreateRepositoryRequest{}}
	repo, err := s.discoverRepository(ctx, path)
	if err != nil {
		return nil, err
	}
	if repo != nil {
		candidate.Repositories = append(candidate.Repositories, *repo)
	} else {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, invalid("read workspace %s: %v", path, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == ".git" {
				continue
			}
			repo, err := s.discoverRepository(ctx, filepath.Join(path, entry.Name()))
			if err != nil {
				return nil, err
			}
			if repo != nil {
				candidate.Repositories = append(candidate.Repositories, *repo)
			}
		}
	}
	if len(candidate.Repositories) == 0 {
		return nil, nil
	}
	id, err := workspaceForCandidate(ctx, s.db.Reader(), candidate)
	if err != nil {
		return nil, err
	}
	if id != "" {
		candidate.WorkspaceID = &id
	}
	return &candidate, nil
}

func (s *Service) discoverRepository(ctx context.Context, path string) (*CreateRepositoryRequest, error) {
	if _, err := os.Lstat(filepath.Join(path, ".git")); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, invalid("inspect repository %s: %v", path, err)
	}
	root, err := git.ResolveRoot(ctx, path, s.allowedRoots)
	if err != nil {
		return nil, invalid("repository %s: %v", path, err)
	}
	if root != path {
		return nil, invalid("repository %s resolves to a different root %s", path, root)
	}
	return &CreateRepositoryRequest{Label: filepath.Base(root), Path: root, BaseRef: "main"}, nil
}

func (s *Service) ResolveReview(ctx context.Context, path string) (Review, error) {
	candidate, err := s.resolveWorkspace(ctx, path)
	if err != nil {
		return Review{}, err
	}
	var reviews []Review
	if candidate.WorkspaceID != nil {
		reviews, err = s.ListReviews(ctx, candidate.WorkspaceID)
		if err != nil {
			return Review{}, err
		}
	}
	switch len(reviews) {
	case 0:
		return Review{}, invalid("workspace %s has no reviews; run prunelle review create first", candidate.Path)
	case 1:
		return reviews[0], nil
	}
	details := make([]string, len(reviews))
	for i, review := range reviews {
		details[i] = review.ID + ": " + review.Title
	}
	return Review{}, &Error{Code: CodeConflict, Message: "workspace has multiple reviews; run prunelle review capture REVIEW_ID", Details: details}
}

// workspaceQuery allows the registration match to be rechecked inside the creation transaction.
type workspaceQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func workspaceForCandidate(ctx context.Context, db workspaceQuery, candidate WorkspaceCandidate) (string, error) {
	paths := make([]string, len(candidate.Repositories))
	for i, repo := range candidate.Repositories {
		paths[i] = repo.Path
	}
	encoded, err := json.Marshal(paths)
	if err != nil {
		return "", err
	}
	var claimed string
	err = db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE root_path = ?`, candidate.Path).Scan(&claimed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	rows, err := db.QueryContext(ctx, `SELECT w.id FROM workspaces w
		WHERE (w.root_path = ? OR w.root_path IS NULL)
		AND (SELECT COUNT(*) FROM repositories r WHERE r.workspace_id = w.id) = ?
		AND NOT EXISTS (SELECT 1 FROM repositories r WHERE r.workspace_id = w.id
			AND r.root_path NOT IN (SELECT value FROM json_each(?))) ORDER BY w.id`, candidate.Path, len(paths), string(encoded))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if claimed != "" {
		if slices.Contains(ids, claimed) {
			return claimed, nil
		}
		return "", &Error{Code: CodeConflict, Message: fmt.Sprintf("repositories changed for workspace %s; use its explicit ID to retain the registered membership", claimed)}
	}
	if len(ids) > 1 {
		return "", &Error{Code: CodeConflict, Message: "multiple registered workspaces match this folder; select an explicit workspace ID", Details: ids}
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", nil
}
