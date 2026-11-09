package review

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

func (s *Service) GetRevisionView(ctx context.Context, reviewID string, requested *string) (RevisionView, error) {
	var result RevisionView
	err := s.readSnapshot(ctx, func(reader *Service) error {
		var err error
		result, err = reader.revisionView(ctx, reviewID, requested)
		return err
	})
	return result, err
}

func (s *Service) revisionView(ctx context.Context, reviewID string, requested *string) (RevisionView, error) {
	review, err := s.GetReview(ctx, reviewID)
	if err != nil {
		return RevisionView{}, err
	}
	id := requested
	if id == nil {
		id = review.CurrentRevisionID
	}
	view := RevisionView{RevisionID: id, Editable: id == nil || (review.CurrentRevisionID != nil && *id == *review.CurrentRevisionID)}
	var revision Revision
	if id != nil {
		revision, err = s.GetRevision(ctx, *id)
		if err != nil {
			return view, err
		}
		if revision.ReviewID != reviewID {
			return view, invalid("revision %s does not belong to review %s", *id, reviewID)
		}
	}
	if !view.Editable {
		var exists bool
		if err := s.reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM revision_review_states WHERE revision_id = ?)`, *id).Scan(&exists); err != nil {
			return view, err
		}
		if !exists {
			reason := "Semantic history was not recorded for this revision. Stored source, discussions and decisions remain available."
			view.UnavailableReason = &reason
			return view, nil
		}
	}
	graph, err := s.readGraph(ctx, reviewID, id, !view.Editable)
	if err != nil {
		return view, err
	}
	if id != nil {
		source, err := s.readSourceChanges(ctx, revision)
		if err != nil {
			return view, err
		}
		if err := s.assignSourceTopics(ctx, &source, graph); err != nil {
			return view, err
		}
		graph.SourceChanges = &source
	}
	if err := s.graphRepresentations(ctx, graph, reviewID, id, !view.Editable); err != nil {
		return view, err
	}
	view.Graph = graph
	return view, nil
}

func (s *Service) readGraph(ctx context.Context, reviewID string, revisionID *string, frozen bool) (*ReviewGraph, error) {
	graph := &ReviewGraph{Representations: []Representation{}}
	var err error
	if graph.Intents, err = s.ListIntents(ctx, reviewID); err != nil {
		return nil, err
	}
	if graph.Changes, err = s.ListChanges(ctx, reviewID, nil); err != nil {
		return nil, err
	}
	if graph.Evidence, err = s.ListEvidence(ctx, reviewID); err != nil {
		return nil, err
	}
	if graph.Attention, err = s.ListAttention(ctx, reviewID); err != nil {
		return nil, err
	}
	if !frozen {
		if graph.Topics, err = s.ListTopics(ctx, reviewID); err != nil {
			return nil, err
		}
		if graph.Findings, err = s.ListFindings(ctx, reviewID); err != nil {
			return nil, err
		}
	} else {
		if graph.Topics, err = s.frozenTopics(ctx, *revisionID); err != nil {
			return nil, err
		}
		if graph.Findings, err = s.frozenFindings(ctx, *revisionID); err != nil {
			return nil, err
		}
		if graph.Intents, err = visibleRecords(ctx, s.reader(), "revision_intents", *revisionID, graph.Intents, func(v Intent) string { return v.ID }); err != nil {
			return nil, err
		}
		if graph.Changes, err = visibleRecords(ctx, s.reader(), "revision_changes", *revisionID, graph.Changes, func(v ObservedChange) string { return v.ID }); err != nil {
			return nil, err
		}
		if graph.Evidence, err = visibleRecords(ctx, s.reader(), "revision_evidence", *revisionID, graph.Evidence, func(v Evidence) string { return v.ID }); err != nil {
			return nil, err
		}
		if graph.Attention, err = visibleRecords(ctx, s.reader(), "revision_attention", *revisionID, graph.Attention, func(v AttentionSignal) string { return v.ID }); err != nil {
			return nil, err
		}
	}
	graph.Attention = slices.DeleteFunc(graph.Attention, func(v AttentionSignal) bool { return revisionID == nil || v.RevisionID != *revisionID })
	return graph, nil
}

func visibleRecords[T any](ctx context.Context, reader queryer, table, revisionID string, records []T, id func(T) string) ([]T, error) {
	ids, err := queryStrings(ctx, reader, `SELECT id FROM `+table+` WHERE view_revision_id = ?`, revisionID)
	if err != nil {
		return nil, err
	}
	visible := make(map[string]bool, len(ids))
	for _, id := range ids {
		visible[id] = true
	}
	return slices.DeleteFunc(records, func(record T) bool { return !visible[id(record)] }), nil
}

func (s *Service) frozenTopics(ctx context.Context, revisionID string) ([]Topic, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT `+topicColumns+` FROM revision_topics WHERE view_revision_id = ? ORDER BY parent_id IS NOT NULL, parent_id, position, id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	topics := []Topic{}
	for rows.Next() {
		topic, err := scanTopic(rows.Scan)
		if err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range topics {
		topic := &topics[i]
		for _, link := range []struct {
			table, column string
			ids           *[]string
		}{
			{"revision_topic_intents", "intent_id", &topic.IntentIDs},
			{"revision_topic_changes", "change_id", &topic.ChangeIDs},
			{"revision_topic_findings", "finding_id", &topic.FindingIDs},
			{"revision_topic_evidence", "evidence_id", &topic.EvidenceIDs},
		} {
			*link.ids, err = queryStrings(ctx, s.reader(), `SELECT `+link.column+` FROM `+link.table+` WHERE view_revision_id = ? AND topic_id = ? ORDER BY `+link.column, revisionID, topic.ID)
			if err != nil {
				return nil, err
			}
		}
	}
	return topics, nil
}

func (s *Service) frozenFindings(ctx context.Context, revisionID string) ([]Finding, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT `+findingColumns+` FROM revision_findings WHERE view_revision_id = ? ORDER BY CASE severity WHEN 'HIGH' THEN 0 WHEN 'MEDIUM' THEN 1 WHEN 'LOW' THEN 2 ELSE 3 END, created_at, id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	findings := []Finding{}
	for rows.Next() {
		finding, err := scanFinding(rows.Scan)
		if err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range findings {
		findings[i].EvidenceIDs, err = queryStrings(ctx, s.reader(), `SELECT evidence_id FROM revision_finding_evidence WHERE view_revision_id = ? AND finding_id = ? ORDER BY evidence_id`, revisionID, findings[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return findings, nil
}

func sealReviewState(ctx context.Context, tx *sql.Tx, reviewID, revisionID string) error {
	for _, query := range []string{
		`INSERT INTO revision_review_states SELECT ?1 FROM reviews WHERE id = ?2`,
		`INSERT INTO revision_topics SELECT ?1, ` + topicColumns + ` FROM topics WHERE review_id = ?2`,
		`INSERT INTO revision_findings SELECT ?1, ` + findingColumns + `, created_at FROM findings WHERE review_id = ?2`,
		`INSERT INTO revision_representations SELECT ?1, r.id, r.topic_id, r.revision_id, r.type, r.title, r.config, r.origin, r.position, r.author_kind, r.author_id, r.import_id, r.version FROM representations r JOIN topics t ON t.id = r.topic_id WHERE t.review_id = ?2`,
		`INSERT INTO revision_finding_evidence SELECT ?1, e.finding_id, e.evidence_id FROM finding_evidence e JOIN findings f ON f.id = e.finding_id WHERE f.review_id = ?2`,
		`INSERT INTO revision_intents SELECT ?1, id FROM intents WHERE review_id = ?2`,
		`INSERT INTO revision_changes SELECT ?1, c.id FROM observed_changes c JOIN revisions r ON r.id = c.revision_id WHERE r.review_id = ?2`,
		`INSERT INTO revision_evidence SELECT ?1, e.id FROM evidence e JOIN revisions r ON r.id = e.revision_id WHERE r.review_id = ?2`,
		`INSERT INTO revision_attention SELECT ?1, a.id FROM attention_signals a JOIN revisions r ON r.id = a.revision_id WHERE r.review_id = ?2`,
	} {
		if _, err := tx.ExecContext(ctx, query, revisionID, reviewID); err != nil {
			return err
		}
	}
	for _, link := range topicLinkKinds {
		if _, err := tx.ExecContext(ctx, `INSERT INTO revision_`+link.table+` SELECT ?, l.topic_id, l.`+link.column+` FROM `+link.table+` l JOIN topics t ON t.id = l.topic_id WHERE t.review_id = ?`, revisionID, reviewID); err != nil {
			return err
		}
	}
	return nil
}

func requireCurrentRevision(ctx context.Context, tx *sql.Tx, reviewID string, revisionID *string) error {
	var current sql.NullString
	if err := scanOne(tx.QueryRowContext(ctx, `SELECT current_revision_id FROM reviews WHERE id = ?`, reviewID), "review", reviewID, &current); err != nil {
		return err
	}
	if current != nullString(revisionID) {
		return &Error{Code: CodeConflict, Message: "The current revision changed. Reload the review before applying this semantic edit."}
	}
	return nil
}

func (g *ReviewGraph) reachableEvidence(topic Topic) (code, all []string) {
	ids := append([]string{}, topic.EvidenceIDs...)
	for _, intent := range g.Intents {
		if slices.Contains(topic.IntentIDs, intent.ID) {
			ids = append(ids, intent.EvidenceIDs...)
		}
	}
	for _, change := range g.Changes {
		if slices.Contains(topic.ChangeIDs, change.ID) {
			ids = append(ids, change.EvidenceIDs...)
		}
	}
	for _, finding := range g.Findings {
		if slices.Contains(topic.FindingIDs, finding.ID) {
			ids = append(ids, finding.EvidenceIDs...)
		}
	}
	all, code = []string{}, []string{}
	for _, evidence := range g.Evidence {
		if slices.Contains(ids, evidence.ID) {
			all = append(all, evidence.ID)
			if evidence.Kind == "CODE_RANGE" {
				code = append(code, evidence.ID)
			}
		}
	}
	slices.Sort(all)
	slices.Sort(code)
	return code, all
}

func (s *Service) graphRepresentations(ctx context.Context, graph *ReviewGraph, reviewID string, revisionID *string, frozen bool) error {
	query := `SELECT r.id, r.topic_id, r.type, r.title, r.origin, r.revision_id, r.author_kind, r.author_id, r.config, r.version FROM representations r JOIN topics t ON t.id = r.topic_id WHERE t.review_id = ? ORDER BY r.position, r.id`
	arg := reviewID
	if frozen {
		query = `SELECT id, topic_id, type, title, origin, revision_id, author_kind, author_id, config, version FROM revision_representations WHERE view_revision_id = ? ORDER BY position, id`
		arg = *revisionID
	}
	rows, err := s.reader().QueryContext(ctx, query, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	type storedRepresentation struct {
		rep    Representation
		config []byte
	}
	stored := []storedRepresentation{}
	for rows.Next() {
		var id, authoredRevision, config string
		var version int
		var author Author
		var rep Representation
		if err := rows.Scan(&id, &rep.TopicID, &rep.Type, &rep.Title, &rep.Origin, &authoredRevision, &author.Kind, &author.ID, &config, &version); err != nil {
			return err
		}
		rep.ID, rep.RevisionID, rep.Author, rep.Version = &id, &authoredRevision, &author, &version
		stored = append(stored, storedRepresentation{rep, []byte(config)})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, topic := range graph.Topics {
		code, all := graph.reachableEvidence(topic)
		provided := map[string]bool{}
		reps := []Representation{}
		for _, row := range stored {
			if row.rep.TopicID != topic.ID {
				continue
			}
			if err := decodeRepresentationConfig(&row.rep, row.config, code, all); err != nil {
				return err
			}
			provided[row.rep.Type] = true
			graph.storedRepresentations = append(graph.storedRepresentations, row.rep)
			reps = append(reps, row.rep)
		}
		for _, typ := range representationTypes {
			if provided[typ] {
				continue
			}
			rep := Representation{TopicID: topic.ID, Type: typ, Title: defaultRepresentationTitles[typ], Origin: "DERIVED"}
			if typ == "CODE_DIFF" || typ == "EVIDENCE_LIST" {
				if err := decodeRepresentationConfig(&rep, nil, code, all); err != nil {
					return err
				}
			} else {
				rep.Origin = "NONE"
				setAvailability(&rep, false, fmt.Sprintf("No %s representation was provided for this topic.", strings.ToLower(typ)))
			}
			reps = append(reps, rep)
		}
		for _, rep := range reps {
			if rep.Type == "CODE_DIFF" {
				files := []SourceFileRef{}
				if graph.SourceChanges != nil {
					for _, repo := range graph.SourceChanges.Repositories {
						for _, file := range repo.Files {
							if slices.Contains(file.TopicIDs, topic.ID) {
								files = append(files, SourceFileRef{RevisionID: *revisionID, RepositoryID: repo.RepositoryID, Path: file.Path})
							}
						}
					}
				}
				rep.SourceFiles = &files
				selectedEvidence := []string{}
				for _, id := range rep.CodeDiff.EvidenceIDs {
					for _, evidence := range graph.Evidence {
						if evidence.ID != id || evidence.CodeRange == nil {
							continue
						}
						if revisionID != nil && (evidence.RevisionID != *revisionID || slices.ContainsFunc(files, func(file SourceFileRef) bool {
							return file.RepositoryID == evidence.CodeRange.RepositoryID && file.Path == evidence.CodeRange.Path
						})) {
							selectedEvidence = append(selectedEvidence, id)
						}
					}
				}
				rep.CodeDiff = &EvidenceListConfig{EvidenceIDs: selectedEvidence}
				rep.Available = len(files) > 0 || len(rep.CodeDiff.EvidenceIDs) > 0
				rep.UnavailableReason = nil
				if !rep.Available {
					setAvailability(&rep, false, "No source files are assigned to this topic.")
				}
			}
			graph.Representations = append(graph.Representations, rep)
		}
	}
	return nil
}
