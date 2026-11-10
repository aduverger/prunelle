package review

import (
	"context"
	"database/sql"

	"github.com/aduverger/prunelle/server/internal/store"
)

func (s *Service) pauseAgentQueue(ctx context.Context, tx *sql.Tx, sessionID string, actor Actor) error {
	if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET queue_paused=1,queue_version=queue_version+1 WHERE id=?`, sessionID); err != nil {
		return err
	}
	return recordEvent(ctx, tx, "agent.queue.updated", sessionID, "", actor, s.now())
}

// Startup cannot prove delivery or completion of a previously dispatched request.
func (s *Service) RecoverAgentInvocations(ctx context.Context) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,review_id,agent_session_id,author_kind,author_id,login FROM agent_invocations WHERE status IN ('PENDING','RUNNING')`)
		if err != nil {
			return err
		}
		type interrupted struct {
			id, reviewID, sessionID string
			actor                   Actor
		}
		var interruptedRequests []interrupted
		for rows.Next() {
			var request interrupted
			if err := rows.Scan(&request.id, &request.reviewID, &request.sessionID, &request.actor.Kind, &request.actor.ID, &request.actor.Login); err != nil {
				rows.Close()
				return err
			}
			interruptedRequests = append(interruptedRequests, request)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, request := range interruptedRequests {
			if _, err := tx.ExecContext(ctx, `UPDATE agent_invocations SET status='UNKNOWN',error='Server restarted after dispatch; inspect Pi before resuming the queue. This request will not be replayed.',updated_at=? WHERE id=?`, store.FormatTime(s.now()), request.id); err != nil {
				return err
			}
			if err := s.pauseAgentQueue(ctx, tx, request.sessionID, request.actor); err != nil {
				return err
			}
			if err := recordEvent(ctx, tx, "agent.invocation.updated", request.id, request.reviewID, request.actor, s.now()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) ListAgentQueue(ctx context.Context, sessionID string) ([]AgentInvocation, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT `+invocationColumns+` FROM agent_invocations WHERE agent_session_id=? AND status IN ('QUEUED','PENDING','RUNNING','UNKNOWN') ORDER BY rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	invocations := []AgentInvocation{}
	for rows.Next() {
		invocation, err := scanInvocation(rows.Scan)
		if err != nil {
			return nil, err
		}
		invocations = append(invocations, invocation)
	}
	return invocations, rows.Err()
}

func (s *Service) ClaimQueuedDiscussion(ctx context.Context, target AgentInvocationTarget, actor Actor) (*AgentInvocation, error) {
	var claimed *AgentInvocation
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		reader := *s
		reader.readConn = tx
		invocation, err := scanInvocation(tx.QueryRowContext(ctx, `SELECT `+invocationColumns+` FROM agent_invocations WHERE agent_session_id=? AND status='QUEUED' AND EXISTS (SELECT 1 FROM agent_sessions WHERE id=? AND queue_paused=0) AND NOT EXISTS (SELECT 1 FROM agent_invocations WHERE agent_session_id=? AND status IN ('PENDING','RUNNING')) ORDER BY rowid LIMIT 1`, target.SessionID, target.SessionID, target.SessionID).Scan)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		var selected sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT agent_session_id FROM review_agent_bindings WHERE review_id=?`, invocation.ReviewID).Scan(&selected); err != nil && err != sql.ErrNoRows {
			return err
		}
		if !selected.Valid || selected.String != target.SessionID {
			invocation.Status = "CANCELLED"
			reason := "Pi selection changed before dispatch; no work was sent"
			invocation.Error = &reason
		} else {
			discussion, err := reader.GetDiscussion(ctx, *invocation.DiscussionID)
			if err != nil {
				return err
			}
			invocation.Prompt, err = discussionPrompt(discussion, *invocation.MessageID)
			if err != nil {
				return err
			}
			invocation.Status = "PENDING"
			invocation.ConnectionID, invocation.Model, invocation.ContextID = target.ConnectionID, target.Model, target.ContextID
		}
		invocation.UpdatedAt = s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE agent_invocations SET status=?,connection_id=?,model=?,context_id=?,prompt=?,error=?,updated_at=? WHERE id=?`, invocation.Status, invocation.ConnectionID, invocation.Model, invocation.ContextID, invocation.Prompt, invocation.Error, store.FormatTime(invocation.UpdatedAt), invocation.ID); err != nil {
			return err
		}
		claimed = &invocation
		return recordEvent(ctx, tx, "agent.invocation.updated", invocation.ID, invocation.ReviewID, actor, s.now())
	})
	return claimed, err
}

func (s *Service) CancelQueuedInvocation(ctx context.Context, id string, actor Actor) (AgentInvocation, error) {
	if actor.Kind != "HUMAN" {
		return AgentInvocation{}, &Error{Code: CodePermissionDenied, Message: "only a human cancels queued work"}
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		reader := *s
		reader.readConn = tx
		invocation, err := reader.GetAgentInvocation(ctx, id)
		if err != nil {
			return err
		}
		if invocation.Status == "CANCELLED" {
			return nil
		}
		if invocation.Status != "QUEUED" {
			return &Error{Code: CodeConflict, Message: "only queued work can be cancelled; dispatched work may already be running"}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_invocations SET status='CANCELLED',error='Cancelled before dispatch',updated_at=? WHERE id=?`, store.FormatTime(s.now()), id); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "agent.invocation.updated", id, invocation.ReviewID, actor, s.now())
	})
	if err != nil {
		return AgentInvocation{}, err
	}
	return s.GetAgentInvocation(ctx, id)
}
