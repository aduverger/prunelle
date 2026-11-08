package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

// Registration configures one backend from the server config file.
type Registration struct {
	Name    string
	Config  json.RawMessage
	Backend Backend
}

// WorkspaceDirectory reads workspaces; review.Service implements it.
type WorkspaceDirectory interface {
	ListWorkspaces(ctx context.Context) ([]review.Workspace, error)
	GetWorkspace(ctx context.Context, id string) (review.Workspace, error)
}

type Service struct {
	db         *store.DB
	workspaces WorkspaceDirectory
	now        func() time.Time

	mu       sync.Mutex
	backends []*backendState // enabled backends, in configuration order
}

type backendState struct {
	id      string
	backend Backend
	health  BackendHealth
}

// NewService records registrations as session backends (disabling stored backends no longer
// configured) and marks every runtime not yet re-observed as UNKNOWN.
func NewService(ctx context.Context, db *store.DB, workspaces WorkspaceDirectory, registrations []Registration) (*Service, error) {
	s := &Service{db: db, workspaces: workspaces, now: func() time.Time { return time.Now().UTC() }}
	now := store.FormatTime(s.now())
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE session_backends SET enabled = 0, updated_at = ?`, now); err != nil {
			return err
		}
		for _, reg := range registrations {
			id := store.NewID("sbk")
			if err := tx.QueryRowContext(ctx, `INSERT INTO session_backends (id, name, kind, config, enabled, created_at, updated_at)
				VALUES (?, ?, ?, ?, 1, ?, ?)
				ON CONFLICT (name) DO UPDATE SET kind = excluded.kind, config = excluded.config, enabled = 1, updated_at = excluded.updated_at
				RETURNING id`, id, reg.Name, reg.Backend.Kind(), string(reg.Config), now, now).Scan(&id); err != nil {
				return fmt.Errorf("register session backend %s: %w", reg.Name, err)
			}
			s.backends = append(s.backends, &backendState{id: id, backend: reg.Backend, health: BackendHealth{State: HealthConnecting, Info: map[string]string{}}})
		}
		_, err := tx.ExecContext(ctx, `UPDATE runtime_sessions SET state = 'UNKNOWN', state_changed_at = ?
			WHERE state IN ('ONLINE', 'OFFLINE')`, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Run observes every enabled backend until ctx is done.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, b := range s.backends {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.observe(ctx, b)
		}()
	}
	wg.Wait()
}

const (
	minBackoff       = time.Second
	maxBackoff       = 30 * time.Second
	healthyStreamFor = 30 * time.Second
)

// observe keeps one backend's runtimes current: full discovery, then events. Any failure or
// end of stream restarts from a fresh discovery after a bounded backoff.
func (s *Service) observe(ctx context.Context, b *backendState) {
	backoff := minBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := s.syncAndFollow(ctx, b)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.markUnreachable(ctx, b, err)
		}
		if time.Since(started) > healthyStreamFor {
			backoff = minBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (s *Service) syncAndFollow(ctx context.Context, b *backendState) error {
	if err := s.Sync(ctx, b.id); err != nil {
		return err
	}
	events, err := b.backend.Subscribe(ctx)
	if err != nil {
		return err
	}
	for event := range events {
		if event.ExternalID == "" {
			err = s.Sync(ctx, b.id)
		} else {
			err = s.refresh(ctx, b, event.ExternalID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) backend(id string) (*backendState, error) {
	for _, b := range s.backends {
		if b.id == id {
			return b, nil
		}
	}
	return nil, &review.Error{Code: review.CodeNotFound, Message: fmt.Sprintf("session backend %s is not enabled", id)}
}

// Sync runs a full discovery of one backend and reconciles stored runtimes with it.
func (s *Service) Sync(ctx context.Context, backendID string) error {
	b, err := s.backend(backendID)
	if err != nil {
		return err
	}
	discovery, err := b.backend.Discover(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	seen := map[string]bool{}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		for _, observed := range discovery.Runtimes {
			seen[observed.ExternalID] = true
			if err := upsertRuntime(ctx, tx, b, observed, now); err != nil {
				return err
			}
		}
		known, err := queryPairs(ctx, tx, `SELECT external_id, state FROM runtime_sessions WHERE backend_id = ?`, b.id)
		if err != nil {
			return err
		}
		for externalID, state := range known {
			if !seen[externalID] && state != StateStopped {
				if err := setState(ctx, tx, b.id, externalID, StateStopped, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.setHealth(b, BackendHealth{State: HealthReachable, Info: discovery.Info}, now)
	return nil
}

// refresh re-reads one runtime after an event.
func (s *Service) refresh(ctx context.Context, b *backendState, externalID string) error {
	observed, err := b.backend.Inspect(ctx, externalID)
	now := s.now()
	if errors.Is(err, ErrNotFound) {
		return s.db.Write(ctx, func(tx *sql.Tx) error { return setState(ctx, tx, b.id, externalID, StateStopped, now) })
	}
	if err != nil {
		return err
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error { return upsertRuntime(ctx, tx, b, observed, now) })
}

func (s *Service) markUnreachable(ctx context.Context, b *backendState, cause error) {
	now := s.now()
	message := cause.Error()
	slog.Warn("session backend unavailable", "backend", b.id, "err", message)
	s.setHealth(b, BackendHealth{State: HealthUnreachable, Error: &message}, now)
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE runtime_sessions SET state = 'UNKNOWN', state_changed_at = ?
			WHERE backend_id = ? AND state IN ('ONLINE', 'OFFLINE')`, store.FormatTime(now), b.id)
		return err
	})
	if err != nil && ctx.Err() == nil {
		slog.Error("mark runtime sessions unknown", "backend", b.id, "err", err)
	}
}

func (s *Service) setHealth(b *backendState, health BackendHealth, at time.Time) {
	if health.Info == nil {
		health.Info = map[string]string{}
	}
	health.CheckedAt = &at
	s.mu.Lock()
	defer s.mu.Unlock()
	if health.State == HealthUnreachable && len(b.health.Info) > 0 {
		health.Info = b.health.Info // keep the last known endpoint details
	}
	b.health = health
}

func upsertRuntime(ctx context.Context, tx *sql.Tx, b *backendState, o Observed, now time.Time) error {
	if !slices.Contains([]string{StateOnline, StateOffline, StateStopped, StateUnknown}, o.State) {
		return fmt.Errorf("backend %s reported invalid state %q for %s", b.id, o.State, o.ExternalID)
	}
	metadata := o.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}
	at := store.FormatTime(now)
	_, err := tx.ExecContext(ctx, `INSERT INTO runtime_sessions
		(id, backend_id, external_id, state, label, cwd, process, local, harness_hint, metadata, first_seen_at, last_seen_at, state_changed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (backend_id, external_id) DO UPDATE SET
			state_changed_at = CASE WHEN runtime_sessions.state = excluded.state THEN runtime_sessions.state_changed_at ELSE excluded.state_changed_at END,
			state = excluded.state, label = excluded.label, cwd = excluded.cwd, process = excluded.process, local = excluded.local,
			harness_hint = excluded.harness_hint, metadata = excluded.metadata,
			last_seen_at = excluded.last_seen_at`,
		store.NewID("rts"), b.id, o.ExternalID, o.State, o.Label, emptyNull(o.Cwd), emptyNull(o.Process), o.Local, emptyNull(o.HarnessHint),
		string(metadata), at, at, at)
	return err
}

func setState(ctx context.Context, tx *sql.Tx, backendID, externalID, state string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE runtime_sessions SET state = ?, state_changed_at = ?
		WHERE backend_id = ? AND external_id = ? AND state != ?`, state, store.FormatTime(now), backendID, externalID, state)
	return err
}

func queryPairs(ctx context.Context, tx *sql.Tx, query string, args ...any) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pairs := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		pairs[k] = v
	}
	return pairs, rows.Err()
}

// ListBackends returns every stored backend, enabled or not, with its current health.
func (s *Service) ListBackends(ctx context.Context) ([]SessionBackend, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id, name, kind, enabled, config FROM session_backends ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	backends := []SessionBackend{}
	for rows.Next() {
		var b SessionBackend
		var config string
		if err := rows.Scan(&b.ID, &b.Name, &b.Kind, &b.Enabled, &config); err != nil {
			return nil, err
		}
		b.Config = json.RawMessage(config)
		b.Capabilities = []string{}
		b.Health = BackendHealth{State: HealthDisabled, Info: map[string]string{}}
		if state, err := s.backend(b.ID); err == nil && b.Enabled {
			s.mu.Lock()
			b.Health = state.health
			s.mu.Unlock()
			b.Capabilities = state.backend.Capabilities()
		}
		backends = append(backends, b)
	}
	return backends, rows.Err()
}

const runtimeColumns = `r.id, r.backend_id, b.kind, r.external_id, r.state, r.label, r.cwd, r.process, r.local, r.harness_hint,
	r.metadata, r.first_seen_at, r.last_seen_at, r.state_changed_at`

// ListRuntimeSessions returns runtimes, only those associated with workspaceID when set.
func (s *Service) ListRuntimeSessions(ctx context.Context, workspaceID *string) ([]RuntimeSession, error) {
	query := `SELECT ` + runtimeColumns + ` FROM runtime_sessions r JOIN session_backends b ON b.id = r.backend_id`
	var args []any
	if workspaceID != nil {
		if _, err := s.workspaces.GetWorkspace(ctx, *workspaceID); err != nil {
			return nil, err
		}
		query += ` JOIN workspace_runtime_sessions w ON w.runtime_session_id = r.id WHERE w.workspace_id = ?`
		args = append(args, *workspaceID)
	}
	query += ` ORDER BY r.state = 'STOPPED', r.label, r.id`
	runtimes, err := s.queryRuntimes(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return runtimes, s.addAssociations(ctx, runtimes)
}

func (s *Service) GetRuntimeSession(ctx context.Context, id string) (RuntimeSession, error) {
	runtimes, err := s.queryRuntimes(ctx, `SELECT `+runtimeColumns+` FROM runtime_sessions r
		JOIN session_backends b ON b.id = r.backend_id WHERE r.id = ?`, id)
	if err != nil {
		return RuntimeSession{}, err
	}
	if len(runtimes) == 0 {
		return RuntimeSession{}, &review.Error{Code: review.CodeNotFound, Message: fmt.Sprintf("runtime session %s not found", id)}
	}
	return runtimes[0], s.addAssociations(ctx, runtimes)
}

func (s *Service) queryRuntimes(ctx context.Context, query string, args ...any) ([]RuntimeSession, error) {
	rows, err := s.db.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runtimes := []RuntimeSession{}
	for rows.Next() {
		var r RuntimeSession
		var cwd, process, hint sql.NullString
		var metadata, first, last, changed string
		if err := rows.Scan(&r.ID, &r.BackendID, &r.BackendKind, &r.ExternalID, &r.State, &r.Label, &cwd, &process, &r.Local, &hint,
			&metadata, &first, &last, &changed); err != nil {
			return nil, err
		}
		r.Cwd, r.Process, r.HarnessHint = nullablePtr(cwd), nullablePtr(process), nullablePtr(hint)
		r.Metadata = json.RawMessage(metadata)
		// Capabilities belong to the backend; a runtime of a disabled backend offers none.
		r.Capabilities = []string{}
		if b, err := s.backend(r.BackendID); err == nil {
			r.Capabilities = b.backend.Capabilities()
		}
		r.FirstSeenAt, r.LastSeenAt, r.StateChangedAt = store.ParseTime(first), store.ParseTime(last), store.ParseTime(changed)
		runtimes = append(runtimes, r)
	}
	return runtimes, rows.Err()
}

// addAssociations fills explicit workspace associations and suggestions: a local runtime whose
// working directory lies inside a workspace repository suggests that workspace.
func (s *Service) addAssociations(ctx context.Context, runtimes []RuntimeSession) error {
	if len(runtimes) == 0 {
		return nil
	}
	workspaces, err := s.workspaces.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for i := range runtimes {
		r := &runtimes[i]
		r.WorkspaceIDs, err = queryIDs(ctx, s.db.Reader(), `SELECT workspace_id FROM workspace_runtime_sessions
			WHERE runtime_session_id = ? ORDER BY workspace_id`, r.ID)
		if err != nil {
			return err
		}
		r.SuggestedWorkspaceIDs = []string{}
		if !r.Local || r.Cwd == nil {
			continue
		}
		for _, ws := range workspaces {
			if slices.Contains(r.WorkspaceIDs, ws.ID) {
				continue
			}
			if slices.ContainsFunc(ws.Repositories, func(repo review.Repository) bool { return within(*r.Cwd, repo.RootPath) }) {
				r.SuggestedWorkspaceIDs = append(r.SuggestedWorkspaceIDs, ws.ID)
			}
		}
	}
	return nil
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Associate links a runtime session to a workspace. Repeating it is harmless.
func (s *Service) Associate(ctx context.Context, workspaceID, runtimeID string) (RuntimeSession, error) {
	if _, err := s.workspaces.GetWorkspace(ctx, workspaceID); err != nil {
		return RuntimeSession{}, err
	}
	if _, err := s.GetRuntimeSession(ctx, runtimeID); err != nil {
		return RuntimeSession{}, err
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO workspace_runtime_sessions (workspace_id, runtime_session_id, created_at)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, workspaceID, runtimeID, store.FormatTime(s.now()))
		return err
	})
	if err != nil {
		return RuntimeSession{}, err
	}
	return s.GetRuntimeSession(ctx, runtimeID)
}

// Dissociate removes a workspace association; removing a missing one is harmless.
func (s *Service) Dissociate(ctx context.Context, workspaceID, runtimeID string) error {
	if _, err := s.workspaces.GetWorkspace(ctx, workspaceID); err != nil {
		return err
	}
	if _, err := s.GetRuntimeSession(ctx, runtimeID); err != nil {
		return err
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM workspace_runtime_sessions WHERE workspace_id = ? AND runtime_session_id = ?`,
			workspaceID, runtimeID)
		return err
	})
}

func queryIDs(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func emptyNull(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullablePtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}
