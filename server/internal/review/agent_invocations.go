package review

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aduverger/prunelle/server/internal/store"
)

type AgentInvocation struct {
	ID               string                 `json:"id"`
	ReviewID         string                 `json:"review_id"`
	RevisionID       string                 `json:"revision_id"`
	AgentSessionID   string                 `json:"agent_session_id"`
	ConnectionID     string                 `json:"connection_id"`
	Intent           string                 `json:"intent"`
	Status           string                 `json:"status"`
	DiscussionID     *string                `json:"discussion_id"`
	MessageID        *string                `json:"message_id"`
	DecisionID       *string                `json:"decision_id"`
	Prompt           string                 `json:"prompt"`
	Author           Author                 `json:"author"`
	Model            string                 `json:"model"`
	ContextID        string                 `json:"context_id"`
	InputFingerprint string                 `json:"input_fingerprint"`
	Result           *AgentInvocationResult `json:"result"`
	Error            *string                `json:"error"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

type CreateAgentInvocationRequest struct {
	AgentSessionID string  `json:"agent_session_id"`
	RevisionID     string  `json:"revision_id"`
	Intent         string  `json:"intent"`
	DiscussionID   *string `json:"discussion_id,omitempty"`
	MessageID      *string `json:"message_id,omitempty"`
	DecisionID     *string `json:"decision_id,omitempty"`
}

type AgentInvocationTarget struct {
	SessionID    string
	ConnectionID string
	Model        string
	ContextID    string
}

type AgentInvocationContext struct {
	InvocationID string          `json:"invocation_id"`
	View         RevisionView    `json:"view"`
	Eligible     []SourceFileRef `json:"eligible_files"`
}

type GeneratedTopic struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Explanation string `json:"explanation"`
}

type GeneratedFileTopics struct {
	RepositoryID string   `json:"repository_id"`
	Path         string   `json:"path"`
	TopicKeys    []string `json:"topic_keys"`
	TopicIDs     []string `json:"topic_ids"`
}

type GeneratedReview struct {
	Topics []GeneratedTopic      `json:"topics"`
	Files  []GeneratedFileTopics `json:"files"`
}

type CompleteAgentInvocationRequest struct {
	InvocationID string           `json:"invocation_id,omitempty"`
	DiscussionID *string          `json:"discussion_id,omitempty"`
	Body         string           `json:"body,omitempty"`
	Generation   *GeneratedReview `json:"generation,omitempty"`
}

type AgentInvocationResult struct {
	MessageID *string           `json:"message_id"`
	TopicIDs  map[string]string `json:"topic_ids"`
	Summary   string            `json:"summary"`
}

type UpdateAgentInvocationRequest struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

const invocationColumns = `id, review_id, revision_id, agent_session_id, connection_id, intent, status, discussion_id, message_id, decision_id, prompt, author_kind, author_id, model, context_id, input_fingerprint, result, error, created_at, updated_at`

func scanInvocation(scan func(...any) error) (AgentInvocation, error) {
	var invocation AgentInvocation
	var discussion, message, decision, result, failure sql.NullString
	var created, updated string
	err := scan(&invocation.ID, &invocation.ReviewID, &invocation.RevisionID, &invocation.AgentSessionID, &invocation.ConnectionID,
		&invocation.Intent, &invocation.Status, &discussion, &message, &decision, &invocation.Prompt,
		&invocation.Author.Kind, &invocation.Author.ID, &invocation.Model, &invocation.ContextID,
		&invocation.InputFingerprint, &result, &failure, &created, &updated)
	if err != nil {
		return invocation, err
	}
	invocation.DiscussionID, invocation.MessageID, invocation.DecisionID, invocation.Error = stringPtr(discussion), stringPtr(message), stringPtr(decision), stringPtr(failure)
	invocation.CreatedAt, invocation.UpdatedAt = store.ParseTime(created), store.ParseTime(updated)
	if result.Valid {
		err = json.Unmarshal([]byte(result.String), &invocation.Result)
	}
	return invocation, err
}

func (s *Service) GetAgentInvocation(ctx context.Context, id string) (AgentInvocation, error) {
	invocation, err := scanInvocation(s.reader().QueryRowContext(ctx, `SELECT `+invocationColumns+` FROM agent_invocations WHERE id=?`, id).Scan)
	if err == sql.ErrNoRows {
		return invocation, notFound("agent invocation", id)
	}
	return invocation, err
}

func (s *Service) ListAgentInvocations(ctx context.Context, reviewID string) ([]AgentInvocation, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT `+invocationColumns+` FROM agent_invocations WHERE review_id=? ORDER BY created_at,id`, reviewID)
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

func invocationFingerprint(view RevisionView, invocation AgentInvocation) string {
	encoded, _ := json.Marshal(struct {
		View                    RevisionView
		Model, Context, Version string
	}{view, invocation.Model, invocation.ContextID, "prunelle-pi/0.4.0"})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func eligibleForGeneration(file SourceFile) bool {
	return file.Unassigned && file.AssignmentMode == "DERIVED" && !file.Binary && file.ContentOmitted == nil
}

func eligibleGenerationFiles(view RevisionView) []SourceFileRef {
	files := []SourceFileRef{}
	if view.Graph == nil || view.Graph.SourceChanges == nil {
		return files
	}
	for _, repo := range view.Graph.SourceChanges.Repositories {
		for _, file := range repo.Files {
			if eligibleForGeneration(file) {
				files = append(files, SourceFileRef{RevisionID: *view.RevisionID, RepositoryID: repo.RepositoryID, Path: file.Path})
			}
		}
	}
	return files
}

func (s *Service) CreateAgentInvocation(ctx context.Context, reviewID string, req CreateAgentInvocationRequest, target AgentInvocationTarget, actor Actor) (AgentInvocation, error) {
	var p problems
	p.oneOf("intent", req.Intent, []string{"DISCUSS", "PREPARE_REVIEW", "IMPLEMENT"})
	p.required("revision_id", req.RevisionID)
	p.required("agent_session_id", req.AgentSessionID)
	if actor.Kind != "HUMAN" {
		return AgentInvocation{}, &Error{Code: CodePermissionDenied, Message: "only a human explicitly requests agent work"}
	}
	if err := p.err("agent request is invalid"); err != nil {
		return AgentInvocation{}, err
	}
	id := store.NewID("ain")
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		reader := *s
		reader.readConn = tx
		if _, err := reader.reviewRevision(ctx, reviewID, &req.RevisionID); err != nil {
			return err
		}
		var selected sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT agent_session_id FROM review_agent_bindings WHERE review_id=?`, reviewID).Scan(&selected); err != nil && err != sql.ErrNoRows {
			return err
		}
		if !selected.Valid || selected.String != target.SessionID || req.AgentSessionID != target.SessionID {
			return &Error{Code: CodePreconditionFailed, Message: "the attached Pi changed; reload and review the target before sending"}
		}
		if req.Intent != "DISCUSS" {
			var blocked bool
			if err := tx.QueryRowContext(ctx, `SELECT queue_paused OR EXISTS (SELECT 1 FROM agent_invocations WHERE agent_session_id=? AND status='QUEUED') FROM agent_sessions WHERE id=?`, target.SessionID, target.SessionID).Scan(&blocked); err != nil {
				return err
			}
			if blocked {
				return &Error{Code: CodeConflict, Message: "Pi queue is paused or has pending comments"}
			}
		}
		invocation := AgentInvocation{ID: id, ReviewID: reviewID, RevisionID: req.RevisionID, AgentSessionID: target.SessionID,
			ConnectionID: target.ConnectionID, Intent: req.Intent, Status: "PENDING", DiscussionID: req.DiscussionID,
			MessageID: req.MessageID, DecisionID: req.DecisionID, Author: actor.Author, Model: target.Model, ContextID: target.ContextID}
		if req.Intent == "DISCUSS" && target.ConnectionID == "" {
			invocation.Status = "QUEUED"
		}
		switch req.Intent {
		case "DISCUSS":
			if req.DiscussionID == nil || req.MessageID == nil || req.DecisionID != nil {
				return invalid("DISCUSS requires discussion_id and message_id only")
			}
			discussion, err := reader.GetDiscussion(ctx, *req.DiscussionID)
			if err != nil {
				return err
			}
			if discussion.ReviewID != reviewID {
				return invalid("discussion belongs to another review")
			}
			invocation.Prompt, err = discussionPrompt(discussion, *req.MessageID)
			if err != nil {
				return err
			}
		case "PREPARE_REVIEW":
			if req.DiscussionID != nil || req.MessageID != nil || req.DecisionID != nil {
				return invalid("PREPARE_REVIEW does not target a discussion or decision")
			}
			if err := requireCurrentRevision(ctx, tx, reviewID, &req.RevisionID); err != nil {
				return err
			}
			view, err := reader.revisionView(ctx, reviewID, &req.RevisionID)
			if err != nil {
				return err
			}
			if len(eligibleGenerationFiles(view)) == 0 {
				return invalid("no eligible unassigned captured text files; existing content and explicit assignments are preserved")
			}
			invocation.InputFingerprint = invocationFingerprint(view, invocation)
			invocation.Prompt = "Prepare a feature-level review of the eligible captured changes: topics, clear explanations and canonical source associations. Read prunelle_context before publishing. Existing titles, explanations and explicit assignments must remain unchanged."
		case "IMPLEMENT":
			if req.DecisionID == nil || req.DiscussionID != nil || req.MessageID != nil {
				return invalid("IMPLEMENT requires decision_id only")
			}
			decisions, err := reader.queryDecisions(ctx, `WHERE id=? AND review_id=?`, *req.DecisionID, reviewID)
			if err != nil {
				return err
			}
			if len(decisions) != 1 || decisions[0].Decision != "REQUEST_CHANGES" || decisions[0].RevisionID != req.RevisionID {
				return invalid("implementation must target the saved REQUEST_CHANGES decision and its exact revision")
			}
			type originalDiscussionAnchor struct {
				DiscussionID string `json:"discussion_id"`
				RevisionID   string `json:"revision_id"`
				Anchor       Anchor `json:"anchor"`
			}
			anchors := []originalDiscussionAnchor{}
			for _, discussionID := range decisions[0].DiscussionIDs {
				discussion, err := reader.GetDiscussion(ctx, discussionID)
				if err != nil {
					return err
				}
				anchors = append(anchors, originalDiscussionAnchor{discussion.ID, discussion.RevisionID, discussion.Anchor})
			}
			encoded, err := json.Marshal(struct {
				Decision                  Decision                   `json:"decision"`
				OriginalDiscussionAnchors []originalDiscussionAnchor `json:"original_discussion_anchors"`
			}{decisions[0], anchors})
			if err != nil {
				return err
			}
			invocation.Prompt = "Implement this explicit human request against your live working environment, which may differ from the captured revision. Keep original actionable IDs/anchors. Do not assume authorization to commit, publish, deploy, resolve concerns or capture a new revision.\n\n" + string(encoded)
		}
		now := store.FormatTime(s.now())
		_, err := tx.ExecContext(ctx, `INSERT INTO agent_invocations(id,review_id,revision_id,agent_session_id,connection_id,intent,status,discussion_id,message_id,decision_id,prompt,author_kind,author_id,login,model,context_id,input_fingerprint,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, reviewID, req.RevisionID, target.SessionID, target.ConnectionID, req.Intent, invocation.Status, nullString(req.DiscussionID), nullString(req.MessageID), nullString(req.DecisionID), invocation.Prompt, actor.Kind, actor.ID, actor.Login, target.Model, target.ContextID, invocation.InputFingerprint, now, now)
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, "agent.invocation.created", id, reviewID, actor, s.now())
	})
	if err != nil {
		return AgentInvocation{}, err
	}
	return s.GetAgentInvocation(ctx, id)
}

func discussionPrompt(discussion Discussion, messageID string) (string, error) {
	for index, message := range discussion.Messages {
		if message.ID != messageID {
			continue
		}
		prompt := fmt.Sprintf("Question:\n%s\n\nOriginal captured revision: %s", message.Body, discussion.RevisionID)
		if code := discussion.Anchor.CodeRange; code != nil {
			fence := "```"
			for strings.Contains(code.SelectedText, fence) {
				fence += "`"
			}
			prompt += fmt.Sprintf("\nRepository: %s\nFile: %s · %s lines %d–%d\nBlob: %s\n\nSelected code only:\n%s\n%s\n%s", code.RepositoryID, code.Path, code.Side, code.StartLine, code.EndLine, code.BlobHash, fence, code.SelectedText, fence)
		} else {
			anchor, err := json.Marshal(discussion.Anchor)
			if err != nil {
				return "", err
			}
			prompt += "\nAnchor: " + string(anchor)
		}
		if index > 0 {
			prompt += "\n\nEarlier discussion messages:"
			for _, earlier := range discussion.Messages[:index] {
				prompt += fmt.Sprintf("\n\n%s (%s):\n%s", earlier.Author.Kind, earlier.Author.ID, earlier.Body)
			}
		}
		return prompt, nil
	}
	return "", invalid("message is not a saved message of this discussion")
}

func (s *Service) AgentInvocationContext(ctx context.Context, id string) (AgentInvocationContext, error) {
	var result AgentInvocationContext
	err := s.readSnapshot(ctx, func(reader *Service) error {
		invocation, err := reader.GetAgentInvocation(ctx, id)
		if err != nil {
			return err
		}
		view, err := reader.revisionView(ctx, invocation.ReviewID, &invocation.RevisionID)
		if err != nil {
			return err
		}
		if invocation.Intent == "PREPARE_REVIEW" && invocation.Status != "SUCCEEDED" && invocationFingerprint(view, invocation) != invocation.InputFingerprint {
			return &Error{Code: CodeConflict, Message: "generation context changed; inspect current content and explicitly request generation again"}
		}
		result = AgentInvocationContext{InvocationID: id, View: view, Eligible: eligibleGenerationFiles(view)}
		return nil
	})
	return result, err
}

func (s *Service) UpdateAgentInvocation(ctx context.Context, id, sessionID string, req UpdateAgentInvocationRequest, actor Actor) (AgentInvocation, error) {
	var p problems
	p.oneOf("status", req.Status, []string{"RUNNING", "FAILED", "UNKNOWN"})
	if err := p.err("invocation status is invalid"); err != nil {
		return AgentInvocation{}, err
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		reader := *s
		reader.readConn = tx
		invocation, err := reader.GetAgentInvocation(ctx, id)
		if err != nil {
			return err
		}
		if invocation.AgentSessionID != sessionID {
			return &Error{Code: CodePermissionDenied, Message: "invocation belongs to another Pi conversation"}
		}
		if invocation.Status == "QUEUED" || invocation.Status == "CANCELLED" {
			return &Error{Code: CodeConflict, Message: "this invocation was not dispatched"}
		}
		if invocation.Status == "SUCCEEDED" || invocation.Status == "FAILED" || (invocation.Status == "UNKNOWN" && req.Status == "UNKNOWN") {
			return nil
		}
		if invocation.Status == "UNKNOWN" && req.Status == "RUNNING" {
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE agent_invocations SET status=?,error=NULLIF(?,''),updated_at=? WHERE id=?`, req.Status, req.Error, store.FormatTime(s.now()), id)
		if err != nil {
			return err
		}
		if req.Status == "UNKNOWN" {
			if err := s.pauseAgentQueue(ctx, tx, sessionID, actor); err != nil {
				return err
			}
		}
		return recordEvent(ctx, tx, "agent.invocation.updated", id, invocation.ReviewID, actor, s.now())
	})
	if err != nil {
		return AgentInvocation{}, err
	}
	return s.GetAgentInvocation(ctx, id)
}

func (s *Service) CompleteAgentInvocation(ctx context.Context, id, sessionID string, req CompleteAgentInvocationRequest, actor Actor) (AgentInvocation, error) {
	if req.InvocationID != "" && req.InvocationID != id {
		return AgentInvocation{}, invalid("invocation_id does not match the request")
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return AgentInvocation{}, err
	}
	hash := sha256.Sum256(encoded)
	resultHash := hex.EncodeToString(hash[:])
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		reader := *s
		reader.readConn = tx
		invocation, err := reader.GetAgentInvocation(ctx, id)
		if err != nil {
			return err
		}
		if invocation.AgentSessionID != sessionID {
			return &Error{Code: CodePermissionDenied, Message: "invocation belongs to another Pi conversation"}
		}
		var storedHash sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT result_hash FROM agent_invocations WHERE id=?`, id).Scan(&storedHash); err != nil {
			return err
		}
		if storedHash.Valid {
			if storedHash.String != resultHash {
				return &Error{Code: CodeConflict, Message: "this invocation already published a different result"}
			}
			return nil
		}
		if invocation.Status == "FAILED" || invocation.Status == "CANCELLED" || invocation.Status == "QUEUED" {
			return &Error{Code: CodeConflict, Message: "this invocation cannot publish a result"}
		}
		result := AgentInvocationResult{TopicIDs: map[string]string{}, Summary: req.Body}
		if invocation.Intent == "PREPARE_REVIEW" {
			if req.Generation == nil || req.DiscussionID != nil || req.Body != "" {
				return invalid("generation requires structured topics/files only")
			}
			result.TopicIDs, err = reader.publishGeneratedReview(ctx, tx, invocation, *req.Generation, actor)
			if err != nil {
				return err
			}
			result.Summary = fmt.Sprintf("Published %d generated topics and %d source associations", len(req.Generation.Topics), len(req.Generation.Files))
		} else {
			if req.Generation != nil || strings.TrimSpace(req.Body) == "" {
				return invalid("a nonempty answer is required")
			}
			if invocation.Intent == "DISCUSS" {
				if req.DiscussionID == nil || invocation.DiscussionID == nil || *req.DiscussionID != *invocation.DiscussionID {
					return invalid("discussion_id must be the invocation's original discussion")
				}
				messageID := store.NewID("msg")
				if err := insertMessage(ctx, tx, messageID, *invocation.DiscussionID, req.Body, actor.Author, store.FormatTime(s.now())); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE discussions SET version=version+1,updated_at=? WHERE id=?`, store.FormatTime(s.now()), *invocation.DiscussionID); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO agent_invocation_replies VALUES(?,?)`, id, messageID); err != nil {
					return err
				}
				result.MessageID = &messageID
				if err := recordEvent(ctx, tx, "discussion.updated", *invocation.DiscussionID, invocation.ReviewID, actor, s.now()); err != nil {
					return err
				}
			} else if req.DiscussionID != nil {
				return invalid("implementation summary does not append to a discussion")
			}
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_invocations SET status='SUCCEEDED',result=?,result_hash=?,error=NULL,updated_at=? WHERE id=?`, string(encoded), resultHash, store.FormatTime(s.now()), id); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "agent.invocation.updated", id, invocation.ReviewID, actor, s.now())
	})
	if err != nil {
		return AgentInvocation{}, err
	}
	return s.GetAgentInvocation(ctx, id)
}
