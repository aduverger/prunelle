package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aduverger/prunelle/server/internal/store"
)

// checkRepresentationConfig applies the structural rules shared by imports and edits, and returns
// the config to store for typ. References are checked by the caller.
func checkRepresentationConfig(p *problems, path, typ string, c RepresentationConfig) (any, bool) {
	configs := 0
	for _, present := range []bool{c.Text != nil, c.Diagram != nil, c.CodeDiff != nil, c.EvidenceList != nil} {
		if present {
			configs++
		}
	}
	if configs > 1 {
		p.add(subpath(path, "config"), "has more than one config")
		return nil, false
	}
	switch typ {
	case "TEXT":
		if c.Text == nil || len(c.Text.Sections) == 0 {
			p.add(subpath(path, "text"), "needs at least one section")
			return nil, false
		}
		for i := range c.Text.Sections {
			c.Text.Sections[i].EvidenceIDs = nonNil(c.Text.Sections[i].EvidenceIDs)
		}
		return *c.Text, true
	case "DIAGRAM":
		if c.Diagram == nil || len(c.Diagram.Nodes) == 0 {
			p.add(subpath(path, "diagram"), "needs at least one node")
			return nil, false
		}
		return checkDiagram(p, subpath(path, "diagram"), *c.Diagram), true
	case "CODE_DIFF":
		return evidenceListOrEmpty(c.CodeDiff), true
	case "EVIDENCE_LIST":
		return evidenceListOrEmpty(c.EvidenceList), true
	}
	return nil, false
}

func checkDiagram(p *problems, path string, d DiagramConfig) DiagramConfig {
	nodeIDs := map[string]bool{}
	for i, n := range d.Nodes {
		nodePath := fmt.Sprintf("%s.nodes[%d]", path, i)
		p.required(nodePath+".id", n.ID)
		p.required(nodePath+".label", n.Label)
		p.oneOf(nodePath+".kind", n.Kind, diagramNodeKinds)
		if nodeIDs[n.ID] {
			p.add(nodePath+".id", "duplicate node id %q", n.ID)
		}
		nodeIDs[n.ID] = true
		d.Nodes[i].EvidenceIDs = nonNil(n.EvidenceIDs)
	}
	for i, e := range d.Edges {
		if !nodeIDs[e.From] || !nodeIDs[e.To] {
			p.add(fmt.Sprintf("%s.edges[%d]", path, i), "edge %q → %q references an unknown node", e.From, e.To)
		}
	}
	d.Edges = nonNil(d.Edges)
	return d
}

// checkRepresentationRefs reports references to records outside the review, and code diffs that
// list evidence other than code ranges.
func checkRepresentationRefs(p *problems, records reviewRecords, c RepresentationConfig) {
	if c.Text != nil {
		for i, s := range c.Text.Sections {
			records.check(p, fmt.Sprintf("text.sections[%d].evidence_ids", i), "EVIDENCE", s.EvidenceIDs)
		}
	}
	if c.Diagram != nil {
		for i, n := range c.Diagram.Nodes {
			nodePath := fmt.Sprintf("diagram.nodes[%d]", i)
			records.check(p, nodePath+".evidence_ids", "EVIDENCE", n.EvidenceIDs)
			if n.Ref != nil {
				records.checkOne(p, nodePath+".ref.id", n.Ref.Type, n.Ref.ID)
			}
		}
	}
	if c.CodeDiff != nil {
		for i, id := range c.CodeDiff.EvidenceIDs {
			path := fmt.Sprintf("code_diff.evidence_ids[%d]", i)
			if records.checkOne(p, path, "EVIDENCE", id) && records[id].kind != "CODE_RANGE" {
				p.add(path, "%s is not CODE_RANGE evidence", id)
			}
		}
	}
	if c.EvidenceList != nil {
		records.check(p, "evidence_list.evidence_ids", "EVIDENCE", c.EvidenceList.EvidenceIDs)
	}
}

func (c RepresentationConfig) empty() bool {
	return c.Text == nil && c.Diagram == nil && c.CodeDiff == nil && c.EvidenceList == nil
}

// CreateRepresentation adds a manual representation to a topic, authored against the review's current revision.
func (s *Service) CreateRepresentation(ctx context.Context, topicID string, req CreateRepresentationRequest, actor Author) (Representation, error) {
	topic, err := s.GetTopic(ctx, topicID)
	if err != nil {
		return Representation{}, err
	}
	revision, err := s.reviewRevision(ctx, topic.ReviewID, nil)
	if err != nil {
		return Representation{}, err
	}
	var p problems
	p.oneOf("type", req.Type, representationTypes)
	config, _ := checkRepresentationConfig(&p, "", req.Type, req.RepresentationConfig)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = defaultRepresentationTitles[req.Type]
	}
	id := store.NewID("rep")
	now := s.now()
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		records, err := loadReviewRecords(ctx, tx, topic.ReviewID)
		if err != nil {
			return err
		}
		checkRepresentationRefs(&p, records, req.RepresentationConfig)
		if err := p.err("representation is invalid"); err != nil {
			return err
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			return err
		}
		var position int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM representations WHERE topic_id = ?`, topicID).Scan(&position); err != nil {
			return err
		}
		at := store.FormatTime(now)
		if _, err := tx.ExecContext(ctx, `INSERT INTO representations (id, topic_id, revision_id, type, title, config, origin, position, author_kind, author_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'MANUAL', ?, ?, ?, ?, ?)`, id, topicID, revision.ID, req.Type, title, string(encoded), position,
			actor.Kind, actor.ID, at, at); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "representation.created", id, topic.ReviewID, actor, now)
	})
	if err != nil {
		return Representation{}, err
	}
	return s.getRepresentation(ctx, id)
}

// UpdateRepresentation edits a representation last read at version. A config replaces the stored one.
func (s *Service) UpdateRepresentation(ctx context.Context, id string, version int, req UpdateRepresentationRequest, actor Author) (Representation, error) {
	var p problems
	if req.Title != nil {
		p.required("title", *req.Title)
	}
	now := s.now()
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var reviewID, typ, title, config string
		var stored int
		err := scanOne(tx.QueryRowContext(ctx, `SELECT t.review_id, r.type, r.title, r.config, r.version
			FROM representations r JOIN topics t ON t.id = r.topic_id WHERE r.id = ?`, id),
			"representation", id, &reviewID, &typ, &title, &config, &stored)
		if err != nil {
			return err
		}
		if err := checkVersion("representation", id, stored, version); err != nil {
			return err
		}
		if !req.RepresentationConfig.empty() {
			checked, ok := checkRepresentationConfig(&p, "", typ, req.RepresentationConfig)
			records, err := loadReviewRecords(ctx, tx, reviewID)
			if err != nil {
				return err
			}
			checkRepresentationRefs(&p, records, req.RepresentationConfig)
			if ok {
				encoded, err := json.Marshal(checked)
				if err != nil {
					return err
				}
				config = string(encoded)
			}
		}
		if err := p.err("representation update is invalid"); err != nil {
			return err
		}
		if req.Title != nil {
			title = strings.TrimSpace(*req.Title)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE representations SET title = ?, config = ?, version = version + 1, updated_at = ? WHERE id = ?`,
			title, config, store.FormatTime(now), id); err != nil {
			return err
		}
		return recordEvent(ctx, tx, "representation.updated", id, reviewID, actor, now)
	})
	if err != nil {
		return Representation{}, err
	}
	return s.getRepresentation(ctx, id)
}

func (s *Service) getRepresentation(ctx context.Context, id string) (Representation, error) {
	var topicID string
	if err := scanOne(s.db.Reader().QueryRowContext(ctx, `SELECT topic_id FROM representations WHERE id = ?`, id),
		"representation", id, &topicID); err != nil {
		return Representation{}, err
	}
	reps, err := s.TopicRepresentations(ctx, topicID)
	if err != nil {
		return Representation{}, err
	}
	for _, rep := range reps {
		if rep.ID != nil && *rep.ID == id {
			return rep, nil
		}
	}
	return Representation{}, notFound("representation", id)
}

func evidenceListOrEmpty(list *EvidenceListConfig) EvidenceListConfig {
	if list == nil {
		return EvidenceListConfig{EvidenceIDs: []string{}}
	}
	return EvidenceListConfig{EvidenceIDs: nonNil(list.EvidenceIDs)}
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// subpath joins a field name to a validation path that may be empty.
func subpath(path, field string) string {
	if path == "" {
		return field
	}
	return path + "." + field
}
