package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/aduverger/prunelle/server/internal/store"
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
		im.ImportedAt = store.ParseTime(at)
		imports = append(imports, im)
	}
	return imports, rows.Err()
}

const topicColumns = `id, review_id, parent_id, title, summary, origin, state, position, author_kind, author_id, import_id, version, created_at, updated_at`

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

func scanTopic(scan func(...any) error) (Topic, error) {
	var t Topic
	var parent, importID sql.NullString
	var created, updated string
	if err := scan(&t.ID, &t.ReviewID, &parent, &t.Title, &t.Summary, &t.Origin, &t.State, &t.Position, &t.Author.Kind, &t.Author.ID, &importID, &t.Version, &created, &updated); err != nil {
		return Topic{}, err
	}
	t.ParentID, t.ImportID = stringPtr(parent), stringPtr(importID)
	t.CreatedAt, t.UpdatedAt = store.ParseTime(created), store.ParseTime(updated)
	return t, nil
}

func (s *Service) GetTopic(ctx context.Context, id string) (Topic, error) {
	t, err := scanTopic(s.reader().QueryRowContext(ctx, `SELECT `+topicColumns+` FROM topics WHERE id = ?`, id).Scan)
	if err == sql.ErrNoRows {
		return Topic{}, notFound("topic", id)
	}
	if err != nil {
		return Topic{}, err
	}
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

func (s *Service) TopicRepresentations(ctx context.Context, topicID string) ([]Representation, error) {
	topic, err := s.GetTopic(ctx, topicID)
	if err != nil {
		return nil, err
	}
	view, err := s.GetRevisionView(ctx, topic.ReviewID, nil)
	if err != nil {
		return nil, err
	}
	reps := []Representation{}
	for _, rep := range view.Graph.Representations {
		if rep.TopicID == topicID {
			reps = append(reps, rep)
		}
	}
	return reps, nil
}

func setAvailability(rep *Representation, available bool, reason string) {
	rep.Available = available
	if !available {
		rep.UnavailableReason = &reason
	}
}

// decodeRepresentationConfig fills the typed config and availability of a representation. Code and
// evidence lists without a stored selection (or derived ones, with a nil config) show everything
// reachable from the topic.
func decodeRepresentationConfig(rep *Representation, config []byte, codeEvidence, allEvidence []string) error {
	var err error
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
	case "CODE_DIFF":
		if rep.CodeDiff, err = selectEvidence(config, codeEvidence); err != nil {
			return err
		}
		setAvailability(rep, len(rep.CodeDiff.EvidenceIDs) > 0, "No code ranges are linked to this topic.")
	case "EVIDENCE_LIST":
		if rep.EvidenceList, err = selectEvidence(config, allEvidence); err != nil {
			return err
		}
		setAvailability(rep, len(rep.EvidenceList.EvidenceIDs) > 0, "No evidence is linked to this topic.")
	default:
		return fmt.Errorf("unknown representation type %q", rep.Type)
	}
	return nil
}

func selectEvidence(config []byte, reachable []string) (*EvidenceListConfig, error) {
	list := EvidenceListConfig{}
	if config != nil {
		if err := json.Unmarshal(config, &list); err != nil {
			return nil, err
		}
	}
	if len(list.EvidenceIDs) == 0 {
		list.EvidenceIDs = reachable
	}
	return &list, nil
}

func (s *Service) ListIntents(ctx context.Context, reviewID string) ([]Intent, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id, kind, statement, source, acceptance_criteria, provenance, author_kind, author_id, import_id
		FROM intents WHERE review_id = ? ORDER BY created_at, id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := []Intent{}
	for rows.Next() {
		in := Intent{ReviewID: reviewID}
		var criteria, importID sql.NullString
		if err := rows.Scan(&in.ID, &in.Kind, &in.Statement, &in.Source, &criteria, &in.Provenance, &in.Author.Kind, &in.Author.ID, &importID); err != nil {
			return nil, err
		}
		in.AcceptanceCriteria, in.ImportID = stringPtr(criteria), stringPtr(importID)
		intents = append(intents, in)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
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
			c.components, c.verification_status, c.provenance, c.author_kind, c.author_id, c.import_id
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
			&c.VerificationStatus, &c.Provenance, &c.Author.Kind, &c.Author.ID, &importID); err != nil {
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
	rows.Close()
	for i := range changes {
		if changes[i].EvidenceIDs, err = queryStrings(ctx, s.reader(),
			`SELECT evidence_id FROM change_evidence WHERE change_id = ? ORDER BY evidence_id`, changes[i].ID); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

const findingColumns = `id, review_id, first_seen_revision_id, kind, severity, status, title, rationale, provenance, author_kind, author_id, import_id, version`

func scanFinding(scan func(...any) error) (Finding, error) {
	var f Finding
	var importID sql.NullString
	if err := scan(&f.ID, &f.ReviewID, &f.FirstSeenRevisionID, &f.Kind, &f.Severity, &f.Status, &f.Title, &f.Rationale, &f.Provenance, &f.Author.Kind, &f.Author.ID, &importID, &f.Version); err != nil {
		return Finding{}, err
	}
	f.ImportID = stringPtr(importID)
	return f, nil
}

func (s *Service) ListFindings(ctx context.Context, reviewID string) ([]Finding, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT `+findingColumns+`
		FROM findings WHERE review_id = ?
		ORDER BY CASE severity WHEN 'HIGH' THEN 0 WHEN 'MEDIUM' THEN 1 WHEN 'LOW' THEN 2 ELSE 3 END, created_at, id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	findings := []Finding{}
	for rows.Next() {
		f, err := scanFinding(rows.Scan)
		if err != nil {
			return nil, err
		}
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range findings {
		if findings[i].EvidenceIDs, err = queryStrings(ctx, s.reader(),
			`SELECT evidence_id FROM finding_evidence WHERE finding_id = ? ORDER BY evidence_id`, findings[i].ID); err != nil {
			return nil, err
		}
	}
	return findings, nil
}

const evidenceSelect = `SELECT e.id, e.revision_id, e.kind, e.title, e.content, e.source, e.provenance, e.author_kind, e.author_id, e.import_id,
		r.repository_id, r.path, r.side, r.start_line, r.end_line, r.blob_hash, r.selected_text
	FROM evidence e LEFT JOIN evidence_code_ranges r ON r.evidence_id = e.id`

func scanEvidence(scan func(...any) error) (Evidence, error) {
	var e Evidence
	var content, importID, repoID, path, side, blobHash, selected sql.NullString
	var start, end sql.NullInt64
	if err := scan(&e.ID, &e.RevisionID, &e.Kind, &e.Title, &content, &e.Source, &e.Provenance, &e.Author.Kind, &e.Author.ID, &importID,
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
			a.priority, a.rationale, a.uncertainty, a.provenance, a.author_kind, a.author_id, a.import_id
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
			&a.Uncertainty, &a.Provenance, &a.Author.Kind, &a.Author.ID, &importID); err != nil {
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
	rows.Close()
	for i := range signals {
		if signals[i].EvidenceIDs, err = queryStrings(ctx, s.reader(),
			`SELECT evidence_id FROM attention_evidence WHERE attention_id = ? ORDER BY evidence_id`, signals[i].ID); err != nil {
			return nil, err
		}
	}
	return signals, nil
}
