package review

import (
	"context"
	"database/sql"
	"strings"

	"github.com/aduverger/prunelle/server/internal/git"
	"github.com/aduverger/prunelle/server/internal/store"
)

func (s *Service) CreateWorkspace(ctx context.Context, req CreateWorkspaceRequest, actor Actor) (Workspace, error) {
	ws, err := s.prepareWorkspace(ctx, req)
	if err != nil {
		return Workspace{}, err
	}
	err = s.db.Write(ctx, func(tx *sql.Tx) error { return insertWorkspace(ctx, tx, ws, actor) })
	return ws, err
}

func (s *Service) prepareWorkspace(ctx context.Context, req CreateWorkspaceRequest) (Workspace, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return Workspace{}, invalid("workspace name is required")
	}
	if len(req.Repositories) == 0 {
		return Workspace{}, invalid("a workspace needs at least one repository")
	}
	now := s.now()
	ws := Workspace{ID: store.NewID("ws"), Name: name, CreatedAt: now, UpdatedAt: now}
	labels, roots := map[string]bool{}, map[string]bool{}
	for _, r := range req.Repositories {
		label := strings.TrimSpace(r.Label)
		if label == "" {
			return Workspace{}, invalid("repository label is required")
		}
		if err := git.ValidateRef(r.BaseRef); err != nil {
			return Workspace{}, invalid("repository %s: %v", label, err)
		}
		root, err := git.ResolveRoot(ctx, r.Path, s.allowedRoots)
		if err != nil {
			return Workspace{}, invalid("repository %s: %v", label, err)
		}
		if labels[label] || roots[root] {
			return Workspace{}, invalid("repository %s is listed twice", label)
		}
		labels[label], roots[root] = true, true
		includeUntracked := r.IncludeUntracked == nil || *r.IncludeUntracked
		ws.Repositories = append(ws.Repositories, Repository{
			ID: store.NewID("repo"), WorkspaceID: ws.ID, Label: label, RootPath: root, BaseRef: r.BaseRef, IncludeUntracked: includeUntracked,
		})
	}
	return ws, nil
}

func insertWorkspace(ctx context.Context, tx *sql.Tx, ws Workspace, actor Actor) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, root_path, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		ws.ID, ws.Name, ws.RootPath, store.FormatTime(ws.CreatedAt), store.FormatTime(ws.UpdatedAt)); err != nil {
		return err
	}
	for _, r := range ws.Repositories {
		if _, err := tx.ExecContext(ctx, `INSERT INTO repositories
			(id, workspace_id, label, root_path, base_ref, include_untracked, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.WorkspaceID, r.Label, r.RootPath, r.BaseRef, r.IncludeUntracked, store.FormatTime(ws.CreatedAt), store.FormatTime(ws.UpdatedAt)); err != nil {
			return err
		}
	}
	return recordEvent(ctx, tx, "workspace.created", ws.ID, "", actor, ws.CreatedAt)
}

func (s *Service) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	ids, err := queryStrings(ctx, s.db.Reader(), `SELECT id FROM workspaces ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	workspaces := []Workspace{}
	for _, id := range ids {
		ws, err := s.GetWorkspace(ctx, id)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, ws)
	}
	return workspaces, nil
}

func (s *Service) GetWorkspace(ctx context.Context, id string) (Workspace, error) {
	ws := Workspace{ID: id}
	var created, updated string
	var root sql.NullString
	err := scanOne(s.db.Reader().QueryRowContext(ctx, `SELECT name, root_path, created_at, updated_at FROM workspaces WHERE id = ?`, id),
		"workspace", id, &ws.Name, &root, &created, &updated)
	if err != nil {
		return Workspace{}, err
	}
	ws.RootPath = stringPtr(root)
	ws.CreatedAt, ws.UpdatedAt = store.ParseTime(created), store.ParseTime(updated)
	ws.Repositories, err = s.repositories(ctx, id)
	return ws, err
}

func (s *Service) repositories(ctx context.Context, workspaceID string) ([]Repository, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id, label, root_path, base_ref, include_untracked
		FROM repositories WHERE workspace_id = ? ORDER BY label`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	repos := []Repository{}
	for rows.Next() {
		r := Repository{WorkspaceID: workspaceID}
		if err := rows.Scan(&r.ID, &r.Label, &r.RootPath, &r.BaseRef, &r.IncludeUntracked); err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}
