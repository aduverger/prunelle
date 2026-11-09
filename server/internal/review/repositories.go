package review

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/aduverger/prunelle/server/internal/git"
	"github.com/aduverger/prunelle/server/internal/store"
)

func (s *Service) prepareReview(ctx context.Context, req CreateReviewRequest) (Review, error) {
	if (req.Path == "") == (len(req.Repositories) == 0) {
		return Review{}, invalid("provide exactly one of path or repositories")
	}
	if req.InferContext && req.Path == "" {
		return Review{}, invalid("infer_context requires path")
	}
	title := strings.TrimSpace(req.Title)
	if req.Title != "" && title == "" {
		return Review{}, invalid("review title must not be blank")
	}
	now := s.now()
	r := Review{ID: store.NewID("rvw"), Title: title, Description: req.Description, Status: "OPEN", CreatedAt: now, UpdatedAt: now}
	repositories := req.Repositories
	if req.Path != "" {
		path, inputs, err := s.sourceRepositories(ctx, req.Path, req.InferContext)
		if err != nil {
			return Review{}, err
		}
		r.ContextPath, repositories = &path, inputs
		if r.Title == "" {
			r.Title = filepath.Base(path)
		}
	}
	labels, paths := map[string]bool{}, map[string]bool{}
	for _, input := range repositories {
		label := strings.TrimSpace(input.Label)
		if label == "" {
			return Review{}, invalid("repository label is required")
		}
		if !filepath.IsAbs(input.Path) {
			return Review{}, invalid("repository %s needs an absolute server-side path", label)
		}
		root, err := git.ResolveRoot(ctx, input.Path)
		if err != nil {
			return Review{}, invalid("repository %s: %v", label, err)
		}
		if labels[label] || paths[root] {
			return Review{}, invalid("repository %s is listed twice", label)
		}
		labels[label], paths[root] = true, true
		include := input.IncludeUntracked == nil || *input.IncludeUntracked
		r.Repositories = append(r.Repositories, Repository{ID: store.NewID("repo"), ReviewID: r.ID, Label: label, RootPath: root, IncludeUntracked: include})
	}
	if r.Title == "" {
		names := make([]string, len(r.Repositories))
		for i, repo := range r.Repositories {
			names[i] = repo.Label
		}
		r.Title = strings.Join(names, ", ")
	}
	return r, nil
}

func insertReviewRepositories(ctx context.Context, tx *sql.Tx, r Review) error {
	for _, repo := range r.Repositories {
		if _, err := tx.ExecContext(ctx, `INSERT INTO repositories (id, root_path, created_at) VALUES (?, ?, ?)`, repo.ID, repo.RootPath, store.FormatTime(r.CreatedAt)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO review_repositories (review_id, repository_id, label, include_untracked) VALUES (?, ?, ?, ?)`, r.ID, repo.ID, repo.Label, repo.IncludeUntracked); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) repositories(ctx context.Context, reviewID string) ([]Repository, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT p.id, r.label, p.root_path, r.include_untracked
		FROM review_repositories r JOIN repositories p ON p.id = r.repository_id WHERE r.review_id = ? ORDER BY r.label`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	repositories := []Repository{}
	for rows.Next() {
		r := Repository{ReviewID: reviewID}
		if err := rows.Scan(&r.ID, &r.Label, &r.RootPath, &r.IncludeUntracked); err != nil {
			return nil, err
		}
		repositories = append(repositories, r)
	}
	return repositories, rows.Err()
}
