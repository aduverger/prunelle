package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func (s *Service) ListImports(ctx context.Context, reviewID string) ([]Import, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id, revision_id, author_kind, author_id, imported_at
		FROM imports WHERE review_id = ? ORDER BY imported_at, id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	imports := []Import{}
	for rows.Next() {
		im := Import{ReviewID: reviewID}
		var at string
		if err := rows.Scan(&im.ID, &im.RevisionID, &im.Author.Kind, &im.Author.ID, &at); err != nil {
			return nil, err
		}
		im.ImportedAt = parseTime(at)
		imports = append(imports, im)
	}
	return imports, rows.Err()
}

const topicColumns = `id, review_id, parent_id, title, summary, origin, state, position, import_id, created_at, updated_at`

// ListTopics returns the review's topics ordered parent-first by position; clients build the tree from parent_id.
func (s *Service) ListTopics(ctx context.Context, reviewID string) ([]Topic, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	ids, err := queryStrings(ctx, s.reader(), `SELECT id FROM topics WHERE review_id = ?
		ORDER BY parent_id IS NOT NULL, parent_id, position, id`, reviewID)
	if err != nil {
		return nil, err
	}
	topics := []Topic{}
	for _, id := range ids {
		t, err := s.GetTopic(ctx, id)
		if err != nil {
			return nil, err
		}
		topics = append(topics, t)
	}
	return topics, nil
}

func (s *Service) GetTopic(ctx context.Context, id string) (Topic, error) {
	var t Topic
	var parent, importID sql.NullString
	var created, updated string
	err := scanOne(s.reader().QueryRowContext(ctx, `SELECT `+topicColumns+` FROM topics WHERE id = ?`, id), "topic", id,
		&t.ID, &t.ReviewID, &parent, &t.Title, &t.Summary, &t.Origin, &t.State, &t.Position, &importID, &created, &updated)
	if err != nil {
		return Topic{}, err
	}
	t.ParentID, t.ImportID = stringPtr(parent), stringPtr(importID)
	t.CreatedAt, t.UpdatedAt = parseTime(created), parseTime(updated)
	links := []struct {
		dest  *[]string
		query string
	}{
		{&t.IntentIDs, `SELECT intent_id FROM topic_intents WHERE topic_id = ? ORDER BY intent_id`},
		{&t.ChangeIDs, `SELECT change_id FROM topic_changes WHERE topic_id = ? ORDER BY change_id`},
		{&t.FindingIDs, `SELECT finding_id FROM topic_findings WHERE topic_id = ? ORDER BY finding_id`},
		{&t.EvidenceIDs, `SELECT evidence_id FROM topic_evidence WHERE topic_id = ? ORDER BY evidence_id`},
	}
	for _, l := range links {
		if *l.dest, err = queryStrings(ctx, s.reader(), l.query, id); err != nil {
			return Topic{}, err
		}
	}
	return t, nil
}

// reachableEvidence returns evidence IDs reachable from the topic, split into code ranges and all.
func (s *Service) reachableEvidence(ctx context.Context, topicID string) (code, all []string, err error) {
	rows, err := s.reader().QueryContext(ctx, `WITH `+topicEvidenceCTE+`
		SELECT te.evidence_id, e.kind = 'CODE_RANGE'
		FROM topic_reachable_evidence te JOIN evidence e ON e.id = te.evidence_id
		WHERE te.topic_id = ? ORDER BY te.evidence_id`, topicID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	code, all = []string{}, []string{}
	for rows.Next() {
		var id string
		var isCode bool
		if err := rows.Scan(&id, &isCode); err != nil {
			return nil, nil, err
		}
		all = append(all, id)
		if isCode {
			code = append(code, id)
		}
	}
	return code, all, rows.Err()
}

// TopicRepresentations returns the stored representations of a topic plus one entry for every
// MVP type it lacks. Code and evidence views without a stored row derive from topic links;
// text and diagram views are never synthesized.
func (s *Service) TopicRepresentations(ctx context.Context, topicID string) ([]Representation, error) {
	if _, err := s.GetTopic(ctx, topicID); err != nil {
		return nil, err
	}
	codeEvidence, allEvidence, err := s.reachableEvidence(ctx, topicID)
	if err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id, type, title, origin, revision_id, config
		FROM representations WHERE topic_id = ? ORDER BY position, id`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reps := []Representation{}
	stored := map[string]bool{}
	for rows.Next() {
		var id, revisionID, config string
		rep := Representation{TopicID: topicID}
		if err := rows.Scan(&id, &rep.Type, &rep.Title, &rep.Origin, &revisionID, &config); err != nil {
			return nil, err
		}
		rep.ID, rep.RevisionID = &id, &revisionID
		if err := decodeRepresentationConfig(&rep, []byte(config), codeEvidence, allEvidence); err != nil {
			return nil, err
		}
		stored[rep.Type] = true
		reps = append(reps, rep)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, typ := range representationTypes {
		if stored[typ] {
			continue
		}
		rep := Representation{TopicID: topicID, Type: typ, Title: defaultRepresentationTitles[typ], Origin: "DERIVED"}
		switch typ {
		case "CODE_DIFF":
			rep.CodeDiff = &EvidenceListConfig{EvidenceIDs: codeEvidence}
			setAvailability(&rep, len(codeEvidence) > 0, "No code ranges are linked to this topic.")
		case "EVIDENCE_LIST":
			rep.EvidenceList = &EvidenceListConfig{EvidenceIDs: allEvidence}
			setAvailability(&rep, len(allEvidence) > 0, "No evidence is linked to this topic.")
		default:
			rep.Origin = "NONE"
			setAvailability(&rep, false, fmt.Sprintf("No %s representation was provided for this topic.", typeNoun[typ]))
		}
		reps = append(reps, rep)
	}
	return reps, nil
}

var typeNoun = map[string]string{"TEXT": "text", "DIAGRAM": "diagram"}

func setAvailability(rep *Representation, available bool, reason string) {
	rep.Available = available
	if !available {
		rep.UnavailableReason = &reason
	}
}

// decodeRepresentationConfig fills the typed config of a stored representation. Code and evidence
// lists with no explicit selection show everything reachable from the topic.
func decodeRepresentationConfig(rep *Representation, config []byte, codeEvidence, allEvidence []string) error {
	switch rep.Type {
	case "TEXT":
		rep.Text = &TextConfig{}
		if err := json.Unmarshal(config, rep.Text); err != nil {
			return err
		}
		setAvailability(rep, len(rep.Text.Sections) > 0, "The text has no sections.")
	case "DIAGRAM":
		rep.Diagram = &DiagramConfig{}
		if err := json.Unmarshal(config, rep.Diagram); err != nil {
			return err
		}
		setAvailability(rep, len(rep.Diagram.Nodes) > 0, "The diagram has no nodes.")
	case "CODE_DIFF", "EVIDENCE_LIST":
		var list EvidenceListConfig
		if err := json.Unmarshal(config, &list); err != nil {
			return err
		}
		reachable := allEvidence
		if rep.Type == "CODE_DIFF" {
			reachable = codeEvidence
		}
		if len(list.EvidenceIDs) == 0 {
			list.EvidenceIDs = reachable
		}
		if rep.Type == "CODE_DIFF" {
			rep.CodeDiff = &list
			setAvailability(rep, len(list.EvidenceIDs) > 0, "No code ranges are linked to this topic.")
		} else {
			rep.EvidenceList = &list
			setAvailability(rep, len(list.EvidenceIDs) > 0, "No evidence is linked to this topic.")
		}
	default:
		return fmt.Errorf("unknown representation type %q", rep.Type)
	}
	return nil
}

func (s *Service) ListIntents(ctx context.Context, reviewID string) ([]Intent, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id, kind, statement, source, acceptance_criteria, provenance, import_id
		FROM intents WHERE review_id = ? ORDER BY created_at, id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := []Intent{}
	for rows.Next() {
		in := Intent{ReviewID: reviewID}
		var criteria, importID sql.NullString
		if err := rows.Scan(&in.ID, &in.Kind, &in.Statement, &in.Source, &criteria, &in.Provenance, &importID); err != nil {
			return nil, err
		}
		in.AcceptanceCriteria, in.ImportID = stringPtr(criteria), stringPtr(importID)
		intents = append(intents, in)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range intents {
		if intents[i].EvidenceIDs, err = queryStrings(ctx, s.reader(),
			`SELECT evidence_id FROM intent_evidence WHERE intent_id = ? ORDER BY evidence_id`, intents[i].ID); err != nil {
			return nil, err
		}
	}
	return intents, nil
}

// ListChanges returns observed changes of every revision of the review, optionally one revision.
func (s *Service) ListChanges(ctx context.Context, reviewID string, revisionID *string) ([]ObservedChange, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT c.id, c.revision_id, c.kind, c.summary, c.before_claim, c.after_claim,
			c.components, c.verification_status, c.provenance, c.import_id
		FROM observed_changes c JOIN revisions r ON r.id = c.revision_id
		WHERE r.review_id = ?1 AND (?2 IS NULL OR c.revision_id = ?2)
		ORDER BY r.ordinal, c.created_at, c.id`, reviewID, nullString(revisionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := []ObservedChange{}
	for rows.Next() {
		var c ObservedChange
		var components string
		var importID sql.NullString
		if err := rows.Scan(&c.ID, &c.RevisionID, &c.Kind, &c.Summary, &c.Before, &c.After, &components,
			&c.VerificationStatus, &c.Provenance, &importID); err != nil {
			return nil, err
		}
		c.ImportID = stringPtr(importID)
		if err := json.Unmarshal([]byte(components), &c.Components); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range changes {
		if changes[i].EvidenceIDs, err = queryStrings(ctx, s.reader(),
			`SELECT evidence_id FROM change_evidence WHERE change_id = ? ORDER BY evidence_id`, changes[i].ID); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

func (s *Service) ListFindings(ctx context.Context, reviewID string) ([]Finding, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id, first_seen_revision_id, kind, severity, status, title, rationale, provenance, import_id
		FROM findings WHERE review_id = ?
		ORDER BY CASE severity WHEN 'HIGH' THEN 0 WHEN 'MEDIUM' THEN 1 WHEN 'LOW' THEN 2 ELSE 3 END, created_at, id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	findings := []Finding{}
	for rows.Next() {
		f := Finding{ReviewID: reviewID}
		var importID sql.NullString
		if err := rows.Scan(&f.ID, &f.FirstSeenRevisionID, &f.Kind, &f.Severity, &f.Status, &f.Title, &f.Rationale, &f.Provenance, &importID); err != nil {
			return nil, err
		}
		f.ImportID = stringPtr(importID)
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range findings {
		if findings[i].EvidenceIDs, err = queryStrings(ctx, s.reader(),
			`SELECT evidence_id FROM finding_evidence WHERE finding_id = ? ORDER BY evidence_id`, findings[i].ID); err != nil {
			return nil, err
		}
	}
	return findings, nil
}

const evidenceSelect = `SELECT e.id, e.revision_id, e.kind, e.title, e.content, e.source, e.collection_method, e.provenance, e.import_id,
		r.repository_id, r.path, r.side, r.start_line, r.end_line, r.blob_hash, r.selected_text
	FROM evidence e LEFT JOIN evidence_code_ranges r ON r.evidence_id = e.id`

func scanEvidence(scan func(...any) error) (Evidence, error) {
	var e Evidence
	var content, importID, repoID, path, side, blobHash, selected sql.NullString
	var start, end sql.NullInt64
	if err := scan(&e.ID, &e.RevisionID, &e.Kind, &e.Title, &content, &e.Source, &e.CollectionMethod, &e.Provenance, &importID,
		&repoID, &path, &side, &start, &end, &blobHash, &selected); err != nil {
		return Evidence{}, err
	}
	e.Content, e.ImportID = stringPtr(content), stringPtr(importID)
	if repoID.Valid {
		e.CodeRange = &CodeRange{RepositoryID: repoID.String, Path: path.String, Side: side.String,
			StartLine: int(start.Int64), EndLine: int(end.Int64), BlobHash: blobHash.String, SelectedText: selected.String}
	}
	return e, nil
}

func (s *Service) ListEvidence(ctx context.Context, reviewID string) ([]Evidence, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, evidenceSelect+` JOIN revisions rv ON rv.id = e.revision_id
		WHERE rv.review_id = ? ORDER BY rv.ordinal, e.created_at, e.id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evidence := []Evidence{}
	for rows.Next() {
		e, err := scanEvidence(rows.Scan)
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, e)
	}
	return evidence, rows.Err()
}

func (s *Service) GetEvidence(ctx context.Context, id string) (Evidence, error) {
	e, err := scanEvidence(s.reader().QueryRowContext(ctx, evidenceSelect+` WHERE e.id = ?`, id).Scan)
	if err == sql.ErrNoRows {
		return Evidence{}, notFound("evidence", id)
	}
	return e, err
}

func (s *Service) ListAttention(ctx context.Context, reviewID string) ([]AttentionSignal, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT a.id, a.revision_id, a.topic_id, a.finding_id, a.change_id,
			a.priority, a.rationale, a.uncertainty, a.source, a.provenance, a.import_id
		FROM attention_signals a JOIN revisions r ON r.id = a.revision_id
		WHERE r.review_id = ?
		ORDER BY r.ordinal, CASE a.priority WHEN 'HIGH' THEN 0 WHEN 'MEDIUM' THEN 1 WHEN 'LOW' THEN 2 ELSE 3 END, a.id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	signals := []AttentionSignal{}
	for rows.Next() {
		var a AttentionSignal
		var topicID, findingID, changeID, importID sql.NullString
		if err := rows.Scan(&a.ID, &a.RevisionID, &topicID, &findingID, &changeID, &a.Priority, &a.Rationale,
			&a.Uncertainty, &a.Source, &a.Provenance, &importID); err != nil {
			return nil, err
		}
		a.ImportID = stringPtr(importID)
		switch {
		case topicID.Valid:
			a.Target = AttentionTarget{Type: "TOPIC", ID: topicID.String}
		case findingID.Valid:
			a.Target = AttentionTarget{Type: "FINDING", ID: findingID.String}
		default:
			a.Target = AttentionTarget{Type: "CHANGE", ID: changeID.String}
		}
		signals = append(signals, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range signals {
		if signals[i].EvidenceIDs, err = queryStrings(ctx, s.reader(),
			`SELECT evidence_id FROM attention_evidence WHERE attention_id = ? ORDER BY evidence_id`, signals[i].ID); err != nil {
			return nil, err
		}
	}
	return signals, nil
}
