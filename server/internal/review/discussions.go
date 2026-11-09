package review

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/aduverger/prunelle/server/internal/store"
)

var (
	anchorTypes        = []string{"REVIEW", "TOPIC", "INTENT", "CHANGE", "FINDING", "EVIDENCE", "CODE_RANGE", "DIAGRAM_ELEMENT"}
	discussionStatuses = []string{"OPEN", "RESOLVED"}
)

// anchorTargets lists the anchor types that point at a record, with the record's entity type and
// the discussions column holding its ID. Column order matches the INSERT in CreateDiscussion.
type anchorTarget struct{ anchorType, entityType, column string }

var anchorTargets = []anchorTarget{
	{"TOPIC", "TOPIC", "topic_id"},
	{"INTENT", "INTENT", "intent_id"},
	{"CHANGE", "CHANGE", "change_id"},
	{"FINDING", "FINDING", "finding_id"},
	{"EVIDENCE", "EVIDENCE", "evidence_id"},
	{"DIAGRAM_ELEMENT", "REPRESENTATION", "representation_id"},
}

// CreateDiscussion opens a thread on an anchor with its first message. The anchor refers to the
// requested revision, by default the review's current one.
func (s *Service) CreateDiscussion(ctx context.Context, reviewID string, req CreateDiscussionRequest, actor Actor) (Discussion, error) {
	revision, err := s.reviewRevision(ctx, reviewID, req.RevisionID)
	if err != nil {
		return Discussion{}, err
	}
	var p problems
	p.required("body", req.Body)
	a := req.Anchor
	p.oneOf("anchor.type", a.Type, anchorTypes)
	targetIndex := slices.IndexFunc(anchorTargets, func(t anchorTarget) bool { return t.anchorType == a.Type })
	expectField(&p, "anchor.id", a.Type, a.ID != nil, targetIndex >= 0)
	expectField(&p, "anchor.element_id", a.Type, a.ElementID != nil, a.Type == "DIAGRAM_ELEMENT")
	expectField(&p, "anchor.code_range", a.Type, a.CodeRange != nil, a.Type == "CODE_RANGE")
	var codeRange *CodeRange
	if a.Type == "CODE_RANGE" && a.CodeRange != nil {
		resolved, err := s.resolveCodeRange(ctx, revision.ID, *a.CodeRange)
		var rangeErr *Error
		switch {
		case errors.As(err, &rangeErr):
			p.add("anchor.code_range", "%s", rangeErr.Message)
		case err != nil:
			return Discussion{}, err
		default:
			codeRange = &resolved
		}
	}

	id := store.NewID("dsc")
	now := s.now()
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		targets := make([]any, len(anchorTargets))
		if targetIndex >= 0 && a.ID != nil {
			reader := *s
			reader.readConn = tx
			view, err := reader.revisionView(ctx, reviewID, &revision.ID)
			if err != nil {
				return err
			}
			if view.Graph == nil {
				return invalid("semantic history is unavailable for this revision; keep the original text or use a stored-code/review anchor")
			}
			records := view.Graph.records()
			target := anchorTargets[targetIndex]
			if records.checkOne(&p, "anchor.id", target.entityType, *a.ID) && a.Type == "DIAGRAM_ELEMENT" && a.ElementID != nil {
				checkDiagramElement(&p, view.Graph, *a.ID, *a.ElementID)
			}
			targets[targetIndex] = *a.ID
		}
		if err := p.err("discussion is invalid"); err != nil {
			return err
		}
		at := store.FormatTime(now)
		args := append([]any{id, reviewID, revision.ID, a.Type}, targets...)
		args = append(args, nullString(a.ElementID), actor.Kind, actor.ID, at, at)
		if _, err := tx.ExecContext(ctx, `INSERT INTO discussions (id, review_id, revision_id, anchor_type,
				topic_id, intent_id, change_id, finding_id, evidence_id, representation_id, element_id,
				status, author_kind, author_id, version, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'OPEN', ?, ?, 1, ?, ?)`, args...); err != nil {
			return err
		}
		if codeRange != nil {
			r := codeRange
			if _, err := tx.ExecContext(ctx, `INSERT INTO discussion_code_ranges (discussion_id, repository_id, path, side, start_line, end_line, blob_hash, selected_text)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, r.RepositoryID, r.Path, r.Side, r.StartLine, r.EndLine, r.BlobHash, r.SelectedText); err != nil {
				return err
			}
		}
		if err := insertMessage(ctx, tx, store.NewID("msg"), id, req.Body, actor.Author, at); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "discussion.created", id, reviewID, actor, now)
	})
	if err != nil {
		return Discussion{}, err
	}
	return s.GetDiscussion(ctx, id)
}

// expectField reports a field that the anchor type requires but is missing, or does not use but is set.
func expectField(p *problems, path, anchorType string, present, wanted bool) {
	switch {
	case wanted && !present:
		p.add(path, "is required for %s anchors", anchorType)
	case present && !wanted:
		p.add(path, "is not used by %s anchors", anchorType)
	}
}

func checkDiagramElement(p *problems, graph *ReviewGraph, representationID, elementID string) {
	for _, rep := range graph.Representations {
		if rep.ID == nil || *rep.ID != representationID {
			continue
		}
		if rep.Diagram == nil {
			p.add("anchor.id", "%s is not a diagram", representationID)
			return
		}
		if !slices.ContainsFunc(rep.Diagram.Nodes, func(n DiagramNode) bool { return n.ID == elementID }) {
			p.add("anchor.element_id", "diagram %s has no node %q", representationID, elementID)
		}
		return
	}
}

func insertMessage(ctx context.Context, tx *sql.Tx, id, discussionID, body string, author Author, at string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO discussion_messages (id, discussion_id, author_kind, author_id, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, id, discussionID, author.Kind, author.ID, body, at)
	return err
}

// Reply adds a message to a discussion. It changes the discussion's version, so a reviewer
// resolving a thread they read before the reply is asked to reload it.
func (s *Service) Reply(ctx context.Context, discussionID string, req CreateMessageRequest, actor Actor) (Message, error) {
	if strings.TrimSpace(req.Body) == "" {
		return Message{}, invalid("body is required")
	}
	message := Message{ID: store.NewID("msg"), DiscussionID: discussionID, Author: actor.Author, Body: req.Body, CreatedAt: s.now()}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var reviewID string
		if err := scanOne(tx.QueryRowContext(ctx, `SELECT review_id FROM discussions WHERE id = ?`, discussionID),
			"discussion", discussionID, &reviewID); err != nil {
			return err
		}
		at := store.FormatTime(message.CreatedAt)
		if err := insertMessage(ctx, tx, message.ID, discussionID, req.Body, actor.Author, at); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE discussions SET version = version + 1, updated_at = ? WHERE id = ?`, at, discussionID); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "discussion.updated", discussionID, reviewID, actor, message.CreatedAt)
	})
	if err != nil {
		return Message{}, err
	}
	return message, nil
}

// UpdateDiscussion resolves or reopens a discussion last read at version.
func (s *Service) UpdateDiscussion(ctx context.Context, id string, version int, req UpdateDiscussionRequest, actor Actor) (Discussion, error) {
	var p problems
	p.oneOf("status", req.Status, discussionStatuses)
	if err := p.err("discussion update is invalid"); err != nil {
		return Discussion{}, err
	}
	now := s.now()
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var reviewID string
		var stored int
		if err := scanOne(tx.QueryRowContext(ctx, `SELECT review_id, version FROM discussions WHERE id = ?`, id),
			"discussion", id, &reviewID, &stored); err != nil {
			return err
		}
		if err := checkVersion("discussion", id, stored, version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE discussions SET status = ?, version = version + 1, updated_at = ? WHERE id = ?`,
			req.Status, store.FormatTime(now), id); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "discussion.updated", id, reviewID, actor, now)
	})
	if err != nil {
		return Discussion{}, err
	}
	return s.GetDiscussion(ctx, id)
}

const discussionSelect = `SELECT d.id, d.review_id, d.revision_id, d.anchor_type,
		COALESCE(d.topic_id, d.intent_id, d.change_id, d.finding_id, d.evidence_id, d.representation_id), d.element_id,
		d.status, d.author_kind, d.author_id, d.version, d.created_at, d.updated_at,
		r.repository_id, r.path, r.side, r.start_line, r.end_line, r.blob_hash, r.selected_text
	FROM discussions d LEFT JOIN discussion_code_ranges r ON r.discussion_id = d.id`

// ListDiscussions returns every discussion of the review with its messages, oldest first.
func (s *Service) ListDiscussions(ctx context.Context, reviewID string) ([]Discussion, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	return s.queryDiscussions(ctx, discussionSelect+` WHERE d.review_id = ? ORDER BY d.created_at, d.id`, reviewID)
}

func (s *Service) GetDiscussion(ctx context.Context, id string) (Discussion, error) {
	discussions, err := s.queryDiscussions(ctx, discussionSelect+` WHERE d.id = ?`, id)
	if err != nil {
		return Discussion{}, err
	}
	if len(discussions) == 0 {
		return Discussion{}, notFound("discussion", id)
	}
	return discussions[0], nil
}

func (s *Service) queryDiscussions(ctx context.Context, query string, args ...any) ([]Discussion, error) {
	if s.readConn == nil {
		var discussions []Discussion
		err := s.readSnapshot(ctx, func(reader *Service) error {
			var err error
			discussions, err = reader.queryDiscussions(ctx, query, args...)
			return err
		})
		return discussions, err
	}
	rows, err := s.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	discussions := []Discussion{}
	for rows.Next() {
		var d Discussion
		var target, element, repoID, path, side, blobHash, selected sql.NullString
		var start, end sql.NullInt64
		var created, updated string
		if err := rows.Scan(&d.ID, &d.ReviewID, &d.RevisionID, &d.Anchor.Type, &target, &element,
			&d.Status, &d.Author.Kind, &d.Author.ID, &d.Version, &created, &updated,
			&repoID, &path, &side, &start, &end, &blobHash, &selected); err != nil {
			return nil, err
		}
		d.Anchor.ID, d.Anchor.ElementID = stringPtr(target), stringPtr(element)
		if repoID.Valid {
			d.Anchor.CodeRange = &CodeRange{RepositoryID: repoID.String, Path: path.String, Side: side.String,
				StartLine: int(start.Int64), EndLine: int(end.Int64), BlobHash: blobHash.String, SelectedText: selected.String}
		}
		d.CreatedAt, d.UpdatedAt = store.ParseTime(created), store.ParseTime(updated)
		discussions = append(discussions, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	graphs := map[string]*ReviewGraph{}
	for i := range discussions {
		d := &discussions[i]
		if d.Messages, err = s.messages(ctx, d.ID); err != nil {
			return nil, err
		}
		if d.TopicIDs, err = s.anchorTopics(ctx, *d, graphs); err != nil {
			return nil, err
		}
	}
	return discussions, nil
}

func (s *Service) messages(ctx context.Context, discussionID string) ([]Message, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT id, author_kind, author_id, body, created_at
		FROM discussion_messages WHERE discussion_id = ? ORDER BY created_at, id`, discussionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []Message{}
	for rows.Next() {
		m := Message{DiscussionID: discussionID}
		var created string
		if err := rows.Scan(&m.ID, &m.Author.Kind, &m.Author.ID, &m.Body, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = store.ParseTime(created)
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (s *Service) anchorTopics(ctx context.Context, d Discussion, graphs map[string]*ReviewGraph) ([]string, error) {
	if d.Anchor.Type == "REVIEW" {
		return []string{}, nil
	}
	if d.Anchor.Type == "TOPIC" {
		return []string{*d.Anchor.ID}, nil
	}
	graph, ok := graphs[d.RevisionID]
	if !ok {
		view, err := s.revisionView(ctx, d.ReviewID, &d.RevisionID)
		if err != nil {
			return nil, err
		}
		graph = view.Graph
		graphs[d.RevisionID] = graph
	}
	if graph == nil {
		return []string{}, nil
	}
	return graph.anchorTopics(d.Anchor), nil
}

func (g *ReviewGraph) anchorTopics(anchor Anchor) []string {
	ids := []string{}
	if anchor.CodeRange != nil {
		if g.SourceChanges == nil {
			return ids
		}
		for _, repo := range g.SourceChanges.Repositories {
			if repo.RepositoryID == anchor.CodeRange.RepositoryID {
				for _, file := range repo.Files {
					if file.Path == anchor.CodeRange.Path {
						return file.TopicIDs
					}
				}
			}
		}
		return ids
	}
	for _, topic := range g.Topics {
		matches := false
		switch anchor.Type {
		case "TOPIC":
			matches = topic.ID == *anchor.ID
		case "INTENT":
			matches = slices.Contains(topic.IntentIDs, *anchor.ID)
		case "CHANGE":
			matches = slices.Contains(topic.ChangeIDs, *anchor.ID)
		case "FINDING":
			matches = slices.Contains(topic.FindingIDs, *anchor.ID)
		case "EVIDENCE":
			_, evidenceIDs := g.reachableEvidence(topic)
			matches = slices.Contains(evidenceIDs, *anchor.ID)
		case "DIAGRAM_ELEMENT":
			matches = slices.ContainsFunc(g.Representations, func(rep Representation) bool {
				return rep.TopicID == topic.ID && rep.ID != nil && *rep.ID == *anchor.ID
			})
		}
		if matches {
			ids = append(ids, topic.ID)
		}
	}
	slices.Sort(ids)
	return ids
}
