package agents

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/aduverger/prunelle/server/internal/events"
	"github.com/aduverger/prunelle/server/internal/review"
)

func (s *Service) dispatchNext(ctx context.Context, sessionID string) error {
	// A committed queue entry must not depend on its desktop request staying connected.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for {
		s.mu.Lock()
		connection := s.connections[sessionID]
		if connection != nil && connection.reserved {
			connection.dispatchPending = true
			s.mu.Unlock()
			return nil
		}
		if !connection.readyForDispatch() {
			s.mu.Unlock()
			return nil
		}
		connection.reserved = true
		connection.dispatchPending = false
		target := review.AgentInvocationTarget{SessionID: sessionID, ConnectionID: connection.ID, Model: connection.model, ContextID: connection.contextID}
		s.mu.Unlock()
		invocation, err := s.reviews.ClaimQueuedDiscussion(ctx, target, connection.actor)
		s.mu.Lock()
		pending := connection.dispatchPending
		connection.reserved, connection.dispatchPending = false, false
		if err != nil || invocation == nil {
			s.mu.Unlock()
			if err == nil && pending {
				continue
			}
			return err
		}
		if invocation.Status == "CANCELLED" {
			s.mu.Unlock()
			continue
		}
		if s.connections[sessionID] != connection {
			s.mu.Unlock()
			_, err := s.reviews.UpdateAgentInvocation(ctx, invocation.ID, sessionID, review.UpdateAgentInvocationRequest{Status: "FAILED", Error: "Pi disconnected before dispatch; no work was sent"}, connection.actor)
			return err
		}
		connection.activeID, connection.ready = invocation.ID, false
		connection.requests <- *invocation
		s.mu.Unlock()
		return nil
	}
}

func (s *Service) DispatchReady(ctx context.Context) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.connections))
	for id := range s.connections {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		if err := s.dispatchNext(ctx, id); err != nil {
			slog.Warn("Pi queue dispatch unavailable", "agent_session_id", id, "err", err)
		}
	}
}

func (s *Service) ListQueue(ctx context.Context, id string) ([]review.AgentInvocation, error) {
	if _, err := s.GetSession(ctx, id); err != nil {
		return nil, err
	}
	return s.reviews.ListAgentQueue(ctx, id)
}

func (s *Service) ResumeQueue(ctx context.Context, id string, version int, actor review.Actor) (Session, error) {
	if actor.Kind != "HUMAN" {
		return Session{}, failure(review.CodePermissionDenied, "only a human resumes an uncertain Pi queue")
	}
	if _, err := s.GetSession(ctx, id); err != nil {
		return Session{}, err
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET queue_paused=0,queue_version=queue_version+1 WHERE id=? AND queue_version=?`, id, version)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			return failure(review.CodePreconditionFailed, "Pi queue changed; inspect the latest uncertainty before resuming")
		}
		return events.Append(ctx, tx, "agent.queue.updated", id, nil, events.Actor(actor.Author), actor.Login, time.Now().UTC())
	})
	if err != nil {
		return Session{}, err
	}
	if err := s.dispatchNext(ctx, id); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, id)
}

func (s *Service) CancelInvocation(ctx context.Context, id string, actor review.Actor) (review.AgentInvocation, error) {
	invocation, err := s.reviews.CancelQueuedInvocation(ctx, id, actor)
	if err == nil {
		if dispatchErr := s.dispatchNext(ctx, invocation.AgentSessionID); dispatchErr != nil {
			slog.Warn("Pi queue dispatch unavailable after cancellation", "err", dispatchErr)
		}
	}
	return invocation, err
}
