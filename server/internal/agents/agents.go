package agents

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aduverger/prunelle/server/internal/events"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

type Session struct {
	ID         string    `json:"id"`
	ExternalID string    `json:"external_id"`
	Cwd        string    `json:"cwd"`
	Version    string    `json:"version"`
	Local      bool      `json:"local"`
	State      string    `json:"state"`
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type RegisterRequest struct {
	ExternalID string `json:"external_id"`
	Cwd        string `json:"cwd"`
	Version    string `json:"version"`
}

type StateRequest struct {
	ConnectionID string `json:"connection_id"`
	Ready        bool   `json:"ready"`
	Model        string `json:"model"`
	ContextID    string `json:"context_id"`
}

type Binding struct {
	ReviewID       string   `json:"review_id"`
	AgentSessionID *string  `json:"agent_session_id"`
	Mode           string   `json:"mode"`
	Version        int      `json:"version"`
	Candidates     []string `json:"candidates"`
}

type BindingRequest struct {
	Mode           string  `json:"mode"`
	AgentSessionID *string `json:"agent_session_id,omitempty"`
}

type Connection struct {
	ID        string
	SessionID string
	Requests  <-chan review.AgentInvocation
	Done      <-chan struct{}
	requests  chan review.AgentInvocation
	done      chan struct{}
	actor     review.Actor
	ready     bool
	model     string
	contextID string
	activeID  string
	reserved  bool
}

type Service struct {
	db          *store.DB
	reviews     *review.Service
	mu          sync.Mutex
	connections map[string]*Connection
}

func NewService(db *store.DB, reviews *review.Service) *Service {
	return &Service{db: db, reviews: reviews, connections: map[string]*Connection{}}
}

func failure(code, message string) error { return &review.Error{Code: code, Message: message} }

func (s *Service) Register(ctx context.Context, req RegisterRequest, local bool, actor review.Actor) (Session, error) {
	if actor.Kind != "AGENT" || actor.ID != "pi:"+req.ExternalID || strings.TrimSpace(req.ExternalID) == "" {
		return Session{}, failure(review.CodePermissionDenied, "register the actual Pi session with its Prunelle-Agent identity")
	}
	if strings.TrimSpace(req.Cwd) == "" || strings.TrimSpace(req.Version) == "" {
		return Session{}, failure(review.CodeInvalidArgument, "cwd and extension version are required")
	}
	if local {
		if !filepath.IsAbs(req.Cwd) {
			return Session{}, failure(review.CodeInvalidArgument, "local cwd must be absolute")
		}
		canonical, err := filepath.EvalSymlinks(req.Cwd)
		if err != nil {
			return Session{}, failure(review.CodeInvalidArgument, "local cwd is not an accessible directory")
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return Session{}, failure(review.CodeInvalidArgument, "local cwd is not a directory")
		}
		req.Cwd = canonical
	}
	id := store.NewID("ags")
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		now := store.FormatTime(time.Now().UTC())
		if err := tx.QueryRowContext(ctx, `INSERT INTO agent_sessions(id,external_id,cwd,version,local,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(external_id) DO UPDATE SET cwd=excluded.cwd,version=excluded.version,local=excluded.local,updated_at=excluded.updated_at RETURNING id`, id, req.ExternalID, req.Cwd, req.Version, local, now, now).Scan(&id); err != nil {
			return err
		}
		return events.Append(ctx, tx, "agent.session.updated", id, nil, events.Actor(actor.Author), actor.Login, time.Now().UTC())
	})
	if err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, id)
}

const sessionColumns = `id,external_id,cwd,version,local,created_at,updated_at`

func scanSession(scan func(...any) error) (Session, error) {
	var session Session
	var created, updated string
	err := scan(&session.ID, &session.ExternalID, &session.Cwd, &session.Version, &session.Local, &created, &updated)
	if err != nil {
		return session, err
	}
	session.CreatedAt, session.UpdatedAt = store.ParseTime(created), store.ParseTime(updated)
	return session, err
}
func (s *Service) state(session Session) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	session.State = "UNAVAILABLE"
	if connection := s.connections[session.ID]; connection != nil {
		session.State = "BUSY"
		session.Model = connection.model
		if connection.ready && !connection.reserved && connection.activeID == "" {
			session.State = "READY"
		}
	}
	return session
}
func (s *Service) GetSession(ctx context.Context, id string) (Session, error) {
	session, err := scanSession(s.db.Reader().QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM agent_sessions WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return session, failure(review.CodeNotFound, "Pi session not found")
	}
	return s.state(session), err
}
func (s *Service) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT `+sessionColumns+` FROM agent_sessions ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := []Session{}
	for rows.Next() {
		session, err := scanSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s.state(session))
	}
	return sessions, rows.Err()
}
func (s *Service) AuthorizeSession(ctx context.Context, id string, actor review.Actor) error {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if actor.Kind != "AGENT" || actor.ID != "pi:"+session.ExternalID {
		return failure(review.CodePermissionDenied, "this operation belongs to the registered Pi conversation")
	}
	return nil
}

func (s *Service) Connect(ctx context.Context, id string, actor review.Actor) (*Connection, error) {
	if err := s.AuthorizeSession(ctx, id, actor); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connections[id] != nil {
		return nil, failure(review.CodeConflict, "this Pi conversation already has a live connection")
	}
	requests := make(chan review.AgentInvocation, 1)
	done := make(chan struct{})
	connection := &Connection{ID: store.NewID("acn"), SessionID: id, Requests: requests, Done: done, requests: requests, done: done, actor: actor}
	s.connections[id] = connection
	return connection, nil
}
func (s *Service) Disconnect(connection *Connection) {
	s.mu.Lock()
	if s.connections[connection.SessionID] != connection {
		s.mu.Unlock()
		return
	}
	delete(s.connections, connection.SessionID)
	close(connection.done)
	activeID := connection.activeID
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if activeID != "" {
		_, _ = s.reviews.UpdateAgentInvocation(ctx, activeID, connection.SessionID, review.UpdateAgentInvocationRequest{Status: "UNKNOWN", Error: "Pi connection ended; delivery/completion may be unknown. No work will be replayed."}, connection.actor)
	}
	if err := s.recordSessionEvent(ctx, connection.SessionID, connection.actor); err != nil {
		slog.Warn("Pi disconnect notification unavailable", "err", err)
	}
	if err := s.ResolveReviews(ctx, connection.actor); err != nil {
		slog.Warn("automatic Pi selection unavailable after disconnect", "err", err)
	}
}
func (s *Service) recordSessionEvent(ctx context.Context, id string, actor review.Actor) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		return events.Append(ctx, tx, "agent.session.updated", id, nil, events.Actor(actor.Author), actor.Login, time.Now().UTC())
	})
}
func (s *Service) UpdateState(ctx context.Context, id string, req StateRequest, actor review.Actor) (Session, error) {
	if err := s.AuthorizeSession(ctx, id, actor); err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	connection := s.connections[id]
	if connection == nil || connection.ID != req.ConnectionID {
		s.mu.Unlock()
		return Session{}, failure(review.CodeConflict, "Pi connection was replaced or disconnected")
	}
	activeID := connection.activeID
	s.mu.Unlock()
	settled := activeID == ""
	if req.Ready && activeID != "" {
		invocation, err := s.reviews.GetAgentInvocation(ctx, activeID)
		var missing *review.Error
		if err != nil && !(errors.As(err, &missing) && missing.Code == review.CodeNotFound) {
			return Session{}, err
		}
		settled = err != nil || (invocation.Status != "PENDING" && invocation.Status != "RUNNING")
	}
	s.mu.Lock()
	if s.connections[id] != connection {
		s.mu.Unlock()
		return Session{}, failure(review.CodeConflict, "Pi connection ended while reporting readiness")
	}
	connection.ready = req.Ready && req.Model != "" && settled && connection.activeID == activeID
	connection.model, connection.contextID = req.Model, req.ContextID
	if connection.ready {
		connection.activeID = ""
	}
	s.mu.Unlock()
	if err := s.recordSessionEvent(ctx, id, actor); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, id)
}

func (s *Service) candidates(ctx context.Context, rv review.Review) ([]string, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, session := range sessions {
		if session.Local && session.State != "UNAVAILABLE" && rv.ContainsPath(session.Cwd) {
			ids = append(ids, session.ID)
		}
	}
	return ids, nil
}
func (s *Service) GetBinding(ctx context.Context, reviewID string) (Binding, error) {
	rv, err := s.reviews.GetReview(ctx, reviewID)
	if err != nil {
		return Binding{}, err
	}
	binding := Binding{ReviewID: reviewID, Mode: "AUTO", Version: 1, Candidates: []string{}}
	var target sql.NullString
	err = s.db.Reader().QueryRowContext(ctx, `SELECT agent_session_id,mode,version FROM review_agent_bindings WHERE review_id=?`, reviewID).Scan(&target, &binding.Mode, &binding.Version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return binding, err
	}
	if target.Valid {
		binding.AgentSessionID = &target.String
	}
	binding.Candidates, err = s.candidates(ctx, rv)
	return binding, err
}
func (s *Service) SetBinding(ctx context.Context, reviewID string, version int, req BindingRequest, actor review.Actor) (Binding, error) {
	if req.Mode != "AUTO" && req.Mode != "MANUAL" && req.Mode != "DISCONNECTED" {
		return Binding{}, failure(review.CodeInvalidArgument, "mode must be AUTO, MANUAL or DISCONNECTED")
	}
	if (req.Mode == "MANUAL") != (req.AgentSessionID != nil) {
		return Binding{}, failure(review.CodeInvalidArgument, "only MANUAL selection requires an agent_session_id")
	}
	if req.AgentSessionID != nil {
		if _, err := s.GetSession(ctx, *req.AgentSessionID); err != nil {
			return Binding{}, err
		}
	}
	current, err := s.GetBinding(ctx, reviewID)
	if err != nil {
		return Binding{}, err
	}
	target := req.AgentSessionID
	if req.Mode == "AUTO" && len(current.Candidates) == 1 {
		target = &current.Candidates[0]
	}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var stored int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT version FROM review_agent_bindings WHERE review_id=?),1)`, reviewID).Scan(&stored); err != nil {
			return err
		}
		if stored != version {
			return failure(review.CodePreconditionFailed, "Pi selection changed; reload it before editing")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO review_agent_bindings(review_id,agent_session_id,mode,version,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(review_id) DO UPDATE SET agent_session_id=excluded.agent_session_id,mode=excluded.mode,version=excluded.version,updated_at=excluded.updated_at`, reviewID, target, req.Mode, stored+1, store.FormatTime(time.Now().UTC()))
		if err != nil {
			return err
		}
		return events.Append(ctx, tx, "agent.binding.updated", reviewID, &reviewID, events.Actor(actor.Author), actor.Login, time.Now().UTC())
	})
	if err != nil {
		return Binding{}, err
	}
	return s.GetBinding(ctx, reviewID)
}
func (s *Service) ResolveReview(ctx context.Context, reviewID string, actor review.Actor) error {
	binding, err := s.GetBinding(ctx, reviewID)
	if err != nil {
		return err
	}
	if binding.Mode != "AUTO" || binding.AgentSessionID != nil || len(binding.Candidates) != 1 {
		return nil
	}
	_, err = s.SetBinding(ctx, reviewID, binding.Version, BindingRequest{Mode: "AUTO"}, actor)
	var conflict *review.Error
	if errors.As(err, &conflict) && conflict.Code == review.CodePreconditionFailed {
		return nil
	}
	return err
}
func (s *Service) ResolveReviews(ctx context.Context, actor review.Actor) error {
	reviews, err := s.reviews.ListReviews(ctx)
	if err != nil {
		return err
	}
	for _, rv := range reviews {
		if err := s.ResolveReview(ctx, rv.ID, actor); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) CreateInvocation(ctx context.Context, reviewID string, req review.CreateAgentInvocationRequest, actor review.Actor) (review.AgentInvocation, error) {
	if err := s.ResolveReview(ctx, reviewID, actor); err != nil {
		return review.AgentInvocation{}, err
	}
	binding, err := s.GetBinding(ctx, reviewID)
	if err != nil {
		return review.AgentInvocation{}, err
	}
	if binding.AgentSessionID == nil {
		return review.AgentInvocation{}, failure(review.CodeConflict, "select a Pi conversation before requesting work")
	}
	if req.AgentSessionID != *binding.AgentSessionID {
		return review.AgentInvocation{}, failure(review.CodePreconditionFailed, "the attached Pi changed; reload and review the target before sending")
	}
	s.mu.Lock()
	connection := s.connections[*binding.AgentSessionID]
	if connection == nil || !connection.ready || connection.activeID != "" || connection.reserved {
		s.mu.Unlock()
		return review.AgentInvocation{}, failure(review.CodeConflict, "Pi is unavailable or busy; no work was queued")
	}
	connection.reserved = true
	target := review.AgentInvocationTarget{SessionID: connection.SessionID, ConnectionID: connection.ID, Model: connection.model, ContextID: connection.contextID}
	s.mu.Unlock()
	invocation, err := s.reviews.CreateAgentInvocation(ctx, reviewID, req, target, actor)
	s.mu.Lock()
	connection.reserved = false
	if err != nil {
		s.mu.Unlock()
		return review.AgentInvocation{}, err
	}
	if s.connections[connection.SessionID] != connection {
		s.mu.Unlock()
		return s.reviews.UpdateAgentInvocation(ctx, invocation.ID, connection.SessionID, review.UpdateAgentInvocationRequest{Status: "FAILED", Error: "Pi disconnected before dispatch; no work was sent"}, connection.actor)
	}
	connection.activeID = invocation.ID
	connection.ready = false
	connection.requests <- invocation
	s.mu.Unlock()
	return invocation, nil
}
func (s *Service) invocationState(invocation review.AgentInvocation) review.AgentInvocation {
	if invocation.Status != "PENDING" && invocation.Status != "RUNNING" {
		return invocation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	connection := s.connections[invocation.AgentSessionID]
	if connection == nil || connection.ID != invocation.ConnectionID {
		invocation.Status = "UNKNOWN"
		reason := "The original Pi connection is unavailable; delivery/completion may be unknown. No work was replayed."
		invocation.Error = &reason
	}
	return invocation
}
func (s *Service) GetInvocation(ctx context.Context, id string) (review.AgentInvocation, error) {
	invocation, err := s.reviews.GetAgentInvocation(ctx, id)
	return s.invocationState(invocation), err
}
func (s *Service) ListInvocations(ctx context.Context, reviewID string) ([]review.AgentInvocation, error) {
	invocations, err := s.reviews.ListAgentInvocations(ctx, reviewID)
	for i := range invocations {
		invocations[i] = s.invocationState(invocations[i])
	}
	return invocations, err
}
func (s *Service) UpdateInvocation(ctx context.Context, id string, req review.UpdateAgentInvocationRequest, actor review.Actor) (review.AgentInvocation, error) {
	invocation, err := s.reviews.GetAgentInvocation(ctx, id)
	if err != nil {
		return invocation, err
	}
	if err := s.AuthorizeSession(ctx, invocation.AgentSessionID, actor); err != nil {
		return invocation, err
	}
	return s.reviews.UpdateAgentInvocation(ctx, id, invocation.AgentSessionID, req, actor)
}
func (s *Service) CompleteInvocation(ctx context.Context, id string, req review.CompleteAgentInvocationRequest, actor review.Actor) (review.AgentInvocation, error) {
	invocation, err := s.reviews.GetAgentInvocation(ctx, id)
	if err != nil {
		return invocation, err
	}
	if err := s.AuthorizeSession(ctx, invocation.AgentSessionID, actor); err != nil {
		return invocation, err
	}
	return s.reviews.CompleteAgentInvocation(ctx, id, invocation.AgentSessionID, req, actor)
}
