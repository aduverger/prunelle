package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aduverger/prunelle/server/internal/store"
)

// Manifest is the import format for review content. Entities reference each other by
// manifest-local keys, unique per entity kind; the import assigns Prunelle IDs. Every imported
// record is attributed to the manifest author; provenance is stored as declared.
type Manifest struct {
	Author    Author              `json:"author"`
	Intents   []ManifestIntent    `json:"intents,omitempty"`
	Evidence  []ManifestEvidence  `json:"evidence,omitempty"`
	Changes   []ManifestChange    `json:"changes,omitempty"`
	Findings  []ManifestFinding   `json:"findings,omitempty"`
	Topics    []ManifestTopic     `json:"topics,omitempty"`
	Attention []ManifestAttention `json:"attention,omitempty"`
}

type ManifestIntent struct {
	Key                string   `json:"key"`
	Kind               string   `json:"kind"`
	Statement          string   `json:"statement"`
	Source             string   `json:"source"`
	AcceptanceCriteria *string  `json:"acceptance_criteria,omitempty"`
	Provenance         string   `json:"provenance,omitempty"`
	Evidence           []string `json:"evidence,omitempty"`
}

type ManifestEvidence struct {
	Key        string             `json:"key"`
	Kind       string             `json:"kind"`
	Title      string             `json:"title"`
	Content    *string            `json:"content,omitempty"`
	Source     string             `json:"source,omitempty"`
	Provenance string             `json:"provenance,omitempty"`
	CodeRange  *ManifestCodeRange `json:"code_range,omitempty"`
}

type ManifestCodeRange struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Side       string `json:"side"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
}

type ManifestChange struct {
	Key                string   `json:"key"`
	Kind               string   `json:"kind"`
	Summary            string   `json:"summary"`
	Before             string   `json:"before"`
	After              string   `json:"after"`
	Components         []string `json:"components,omitempty"`
	VerificationStatus string   `json:"verification_status,omitempty"`
	Provenance         string   `json:"provenance,omitempty"`
	Evidence           []string `json:"evidence,omitempty"`
}

type ManifestFinding struct {
	Key        string   `json:"key"`
	Kind       string   `json:"kind"`
	Severity   string   `json:"severity"`
	Title      string   `json:"title"`
	Rationale  string   `json:"rationale"`
	Provenance string   `json:"provenance,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

type ManifestTopic struct {
	Key             string                   `json:"key"`
	Parent          string                   `json:"parent,omitempty"`
	Title           string                   `json:"title"`
	Summary         string                   `json:"summary"`
	Intents         []string                 `json:"intents,omitempty"`
	Changes         []string                 `json:"changes,omitempty"`
	Findings        []string                 `json:"findings,omitempty"`
	Evidence        []string                 `json:"evidence,omitempty"`
	Representations []ManifestRepresentation `json:"representations,omitempty"`
}

type ManifestRepresentation struct {
	Type         string                `json:"type"`
	Title        string                `json:"title,omitempty"`
	Text         *ManifestText         `json:"text,omitempty"`
	Diagram      *ManifestDiagram      `json:"diagram,omitempty"`
	CodeDiff     *ManifestEvidenceList `json:"code_diff,omitempty"`
	EvidenceList *ManifestEvidenceList `json:"evidence_list,omitempty"`
}

type ManifestText struct {
	Sections []ManifestSection `json:"sections"`
}

type ManifestSection struct {
	Heading  string   `json:"heading"`
	Body     string   `json:"body"`
	Evidence []string `json:"evidence,omitempty"`
}

type ManifestDiagram struct {
	Nodes []ManifestNode `json:"nodes"`
	Edges []DiagramEdge  `json:"edges,omitempty"`
}

type ManifestNode struct {
	ID       string       `json:"id"`
	Label    string       `json:"label"`
	Kind     string       `json:"kind"`
	Ref      *ManifestRef `json:"ref,omitempty"`
	Evidence []string     `json:"evidence,omitempty"`
}

type ManifestRef struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

type ManifestEvidenceList struct {
	Evidence []string `json:"evidence,omitempty"`
}

type ManifestAttention struct {
	Key         string      `json:"key,omitempty"`
	Target      ManifestRef `json:"target"`
	Priority    string      `json:"priority"`
	Rationale   string      `json:"rationale"`
	Uncertainty string      `json:"uncertainty"`
	Provenance  string      `json:"provenance,omitempty"`
	Evidence    []string    `json:"evidence,omitempty"`
}

var (
	authorKinds         = []string{"HUMAN", "AGENT"}
	provenances         = []string{"MEASURED", "OBSERVED", "INFERRED", "UNKNOWN"}
	intentKinds         = []string{"BEHAVIOR", "CONSTRAINT"}
	evidenceKinds       = []string{"CODE_RANGE", "TEST_RESULT", "SPECIFICATION", "ANALYSIS_OUTPUT", "ARTIFACT"}
	changeKinds         = []string{"BEHAVIOR", "INTERFACE", "DATA", "DEPENDENCY", "CONFIGURATION", "UI", "OTHER"}
	verificationStates  = []string{"UNVERIFIED", "SUPPORTED", "CONTRADICTED"}
	findingKinds        = []string{"CORRECTNESS", "DESIGN", "SECURITY", "PERFORMANCE", "TEST_GAP", "OTHER"}
	severities          = []string{"HIGH", "MEDIUM", "LOW", "INFO"}
	priorities          = []string{"HIGH", "MEDIUM", "LOW", "UNKNOWN"}
	representationTypes = []string{"TEXT", "DIAGRAM", "CODE_DIFF", "EVIDENCE_LIST"}
	diagramNodeKinds    = []string{"COMPONENT", "SERVICE", "DATASTORE", "QUEUE", "ACTOR", "STEP", "STATE", "OTHER"}
	entityTypes         = []string{"TOPIC", "INTENT", "CHANGE", "FINDING", "EVIDENCE"}
	attentionTargets    = []string{"TOPIC", "CHANGE", "FINDING"}
	sides               = []string{"BASE", "HEAD"}
)

var defaultRepresentationTitles = map[string]string{
	"TEXT": "Explanation", "DIAGRAM": "Diagram", "CODE_DIFF": "Code", "EVIDENCE_LIST": "Evidence",
}

// importPlan is a validated manifest with Prunelle IDs assigned and references resolved.
type importPlan struct {
	importID, reviewID, revisionID string
	author                         Author
	ids                            map[string]map[string]string // entity kind → key → ID
	codeRanges                     map[string]CodeRange         // evidence key → resolved range
	evidenceKinds                  map[string]string            // evidence key → kind
	problems
}

// assign registers a new key of kind, returning its ID.
func (p *importPlan) assign(kind, prefix, key, path string) string {
	if key == "" {
		p.add(path+".key", "is required")
		return ""
	}
	if _, dup := p.ids[kind][key]; dup {
		p.add(path+".key", "duplicate %s key %q", kind, key)
		return ""
	}
	id := store.NewID(prefix)
	p.ids[kind][key] = id
	return id
}

// refs resolves keys of kind, reporting unknown ones. Duplicates are dropped.
func (p *importPlan) refs(kind, path string, keys []string) []string {
	ids := []string{}
	for i, key := range keys {
		id, ok := p.ids[kind][key]
		if !ok {
			p.add(fmt.Sprintf("%s[%d]", path, i), "unknown %s key %q", kind, key)
			continue
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// provenance validates a declared provenance, using fallback when none is declared.
func (p *importPlan) provenance(path, declared, fallback string) string {
	if declared == "" {
		return fallback
	}
	p.oneOf(path+".provenance", declared, provenances)
	return declared
}

// Import validates manifest against a revision of the review and stores its records in one transaction.
// Records are attributed to the manifest author; the import event, to the actor who submitted it.
func (s *Service) Import(ctx context.Context, reviewID string, req ImportRequest, actor Actor) (ImportResult, error) {
	revision, err := s.reviewRevision(ctx, reviewID, req.RevisionID)
	if err != nil {
		return ImportResult{}, err
	}

	m := req.Manifest
	plan := &importPlan{
		importID: store.NewID("imp"), reviewID: reviewID, revisionID: revision.ID, author: m.Author,
		ids:           map[string]map[string]string{"intents": {}, "evidence": {}, "changes": {}, "findings": {}, "topics": {}, "attention": {}},
		codeRanges:    map[string]CodeRange{},
		evidenceKinds: map[string]string{},
	}
	plan.oneOf("author.kind", m.Author.Kind, authorKinds)
	plan.required("author.id", m.Author.ID)
	if err := s.planEvidence(ctx, plan, revision, m.Evidence); err != nil {
		return ImportResult{}, err
	}
	for i, in := range m.Intents {
		plan.assign("intents", "int", in.Key, fmt.Sprintf("intents[%d]", i))
	}
	for i, c := range m.Changes {
		plan.assign("changes", "chg", c.Key, fmt.Sprintf("changes[%d]", i))
	}
	for i, f := range m.Findings {
		plan.assign("findings", "fnd", f.Key, fmt.Sprintf("findings[%d]", i))
	}
	for i, t := range m.Topics {
		path := fmt.Sprintf("topics[%d]", i)
		if t.Parent != "" {
			if _, ok := plan.ids["topics"][t.Parent]; !ok {
				plan.add(path+".parent", "parent %q must be a topic listed earlier", t.Parent)
			}
		}
		plan.assign("topics", "top", t.Key, path)
	}
	for i, a := range m.Attention {
		key := a.Key
		if key == "" {
			key = strconv.Itoa(i)
		}
		plan.assign("attention", "att", key, fmt.Sprintf("attention[%d]", i))
	}

	var result ImportResult
	now := s.now()
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		w := &importWriter{ctx: ctx, tx: tx, plan: plan, now: store.FormatTime(now)}
		w.exec(`INSERT INTO imports (id, review_id, revision_id, author_kind, author_id, imported_at) VALUES (?, ?, ?, ?, ?, ?)`,
			plan.importID, reviewID, revision.ID, m.Author.Kind, m.Author.ID, w.now)
		w.evidence(m.Evidence)
		w.intents(m.Intents)
		w.changes(m.Changes)
		w.findings(m.Findings)
		if err := w.topics(m.Topics); err != nil {
			return err
		}
		w.attention(m.Attention)
		if w.err != nil {
			return w.err
		}
		if err := plan.problems.err("manifest is invalid"); err != nil {
			return err
		}
		result = ImportResult{ImportID: plan.importID, RevisionID: revision.ID, IDs: plan.ids}
		return recordEvent(ctx, tx, "import.created", plan.importID, reviewID, actor, now)
	})
	return result, err
}

// planEvidence assigns evidence IDs and resolves code ranges against the revision's stored contents.
func (s *Service) planEvidence(ctx context.Context, plan *importPlan, revision Revision, evidence []ManifestEvidence) error {
	repoIDs := map[string]string{}
	for _, snap := range revision.Snapshots {
		repoIDs[snap.RepositoryLabel] = snap.RepositoryID
	}
	for i, e := range evidence {
		path := fmt.Sprintf("evidence[%d]", i)
		plan.assign("evidence", "evd", e.Key, path)
		plan.oneOf(path+".kind", e.Kind, evidenceKinds)
		plan.required(path+".title", e.Title)
		plan.evidenceKinds[e.Key] = e.Kind
		if e.Kind != "CODE_RANGE" {
			if e.CodeRange != nil {
				plan.add(path+".code_range", "only CODE_RANGE evidence has a code range")
			}
			continue
		}
		if e.CodeRange == nil {
			plan.add(path+".code_range", "is required for CODE_RANGE evidence")
			continue
		}
		r := e.CodeRange
		repoID, ok := repoIDs[r.Repository]
		if !ok {
			plan.add(path+".code_range.repository", "unknown repository %q", r.Repository)
			continue
		}
		plan.oneOf(path+".code_range.side", r.Side, sides)
		resolved, err := s.resolveCodeRange(ctx, revision.ID, CodeRangeInput{RepositoryID: repoID, Path: r.Path, Side: r.Side, StartLine: r.StartLine, EndLine: r.EndLine})
		var rangeErr *Error
		if errors.As(err, &rangeErr) {
			plan.add(path+".code_range", "%s: %s", r.Repository, rangeErr.Message)
			continue
		}
		if err != nil {
			return err
		}
		plan.codeRanges[e.Key] = resolved
	}
	return nil
}

// resolveCodeRange checks a line range against the stored content of a changed file of the revision
// and returns it with its blob hash and selected text.
func (s *Service) resolveCodeRange(ctx context.Context, revisionID string, r CodeRangeInput) (CodeRange, error) {
	content, blobHash, err := s.snapshotContent(ctx, revisionID, r.RepositoryID, r.Path, r.Side)
	if err != nil {
		return CodeRange{}, err
	}
	lines := splitLines(content)
	if r.StartLine < 1 || r.EndLine < r.StartLine || r.EndLine > len(lines) {
		return CodeRange{}, invalid("lines %d-%d are outside %s (%d lines on %s side)", r.StartLine, r.EndLine, r.Path, len(lines), r.Side)
	}
	return CodeRange{
		RepositoryID: r.RepositoryID, Path: r.Path, Side: r.Side, StartLine: r.StartLine, EndLine: r.EndLine,
		BlobHash: blobHash, SelectedText: strings.Join(lines[r.StartLine-1:r.EndLine], "\n"),
	}, nil
}

// snapshotContent returns the stored text of one side of a changed file.
func (s *Service) snapshotContent(ctx context.Context, revisionID, repositoryID, path, side string) (string, string, error) {
	column := "f.head_blob"
	if side == "BASE" {
		column = "f.base_blob"
	}
	var blobHash sql.NullString
	err := scanOne(s.db.Reader().QueryRowContext(ctx, `SELECT `+column+` FROM snapshot_files f
		JOIN repo_snapshots s ON s.id = f.snapshot_id
		WHERE s.revision_id = ? AND s.repository_id = ? AND f.path = ?`, revisionID, repositoryID, path),
		"changed file", path, &blobHash)
	if err != nil {
		return "", "", err
	}
	if !blobHash.Valid {
		return "", "", notFound("stored content for side "+side+" of", path)
	}
	var content []byte
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT content FROM blobs WHERE hash = ?`, blobHash.String).Scan(&content); err != nil {
		return "", "", err
	}
	return string(content), blobHash.String, nil
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

// importWriter inserts a planned manifest, accumulating validation problems in the plan and
// the first database error in err.
type importWriter struct {
	ctx  context.Context
	tx   *sql.Tx
	plan *importPlan
	now  string
	err  error
}

func (w *importWriter) exec(query string, args ...any) {
	if w.err != nil || len(w.plan.problems) > 0 {
		return
	}
	if _, err := w.tx.ExecContext(w.ctx, query, args...); err != nil {
		w.err = err
	}
}

func (w *importWriter) link(table, leftColumn, leftID, rightColumn string, rightIDs []string) {
	for _, id := range rightIDs {
		w.exec(`INSERT INTO `+table+` (`+leftColumn+`, `+rightColumn+`) VALUES (?, ?)`, leftID, id)
	}
}

func (w *importWriter) evidence(evidence []ManifestEvidence) {
	p := w.plan
	for i, e := range evidence {
		path := fmt.Sprintf("evidence[%d]", i)
		id := p.ids["evidence"][e.Key]
		fallback := "UNKNOWN"
		if e.Kind == "CODE_RANGE" {
			fallback = "OBSERVED" // a code range points at captured code
		}
		w.exec(`INSERT INTO evidence (id, revision_id, kind, title, content, source, provenance, author_kind, author_id, import_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, p.revisionID, e.Kind, e.Title, nullString(e.Content), e.Source,
			p.provenance(path, e.Provenance, fallback), p.author.Kind, p.author.ID, p.importID, w.now)
		if r, ok := p.codeRanges[e.Key]; ok {
			w.exec(`INSERT INTO evidence_code_ranges (evidence_id, repository_id, path, side, start_line, end_line, blob_hash, selected_text)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, r.RepositoryID, r.Path, r.Side, r.StartLine, r.EndLine, r.BlobHash, r.SelectedText)
		}
	}
}

func (w *importWriter) intents(intents []ManifestIntent) {
	p := w.plan
	for i, in := range intents {
		path := fmt.Sprintf("intents[%d]", i)
		p.oneOf(path+".kind", in.Kind, intentKinds)
		p.required(path+".statement", in.Statement)
		id := p.ids["intents"][in.Key]
		w.exec(`INSERT INTO intents (id, review_id, kind, statement, source, acceptance_criteria, provenance, author_kind, author_id, import_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, p.reviewID, in.Kind, in.Statement, in.Source, nullString(in.AcceptanceCriteria),
			p.provenance(path, in.Provenance, "UNKNOWN"), p.author.Kind, p.author.ID, p.importID, w.now, w.now)
		w.link("intent_evidence", "intent_id", id, "evidence_id", p.refs("evidence", path+".evidence", in.Evidence))
	}
}

func (w *importWriter) changes(changes []ManifestChange) {
	p := w.plan
	for i, c := range changes {
		path := fmt.Sprintf("changes[%d]", i)
		p.oneOf(path+".kind", c.Kind, changeKinds)
		p.required(path+".summary", c.Summary)
		verification := c.VerificationStatus
		if verification == "" {
			verification = "UNVERIFIED"
		}
		p.oneOf(path+".verification_status", verification, verificationStates)
		components, err := json.Marshal(append([]string{}, c.Components...))
		if err != nil {
			w.err = err
			return
		}
		id := p.ids["changes"][c.Key]
		w.exec(`INSERT INTO observed_changes (id, revision_id, kind, summary, before_claim, after_claim, components, verification_status, provenance, author_kind, author_id, import_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, p.revisionID, c.Kind, c.Summary, c.Before, c.After, string(components),
			verification, p.provenance(path, c.Provenance, "UNKNOWN"), p.author.Kind, p.author.ID, p.importID, w.now, w.now)
		w.link("change_evidence", "change_id", id, "evidence_id", p.refs("evidence", path+".evidence", c.Evidence))
	}
}

func (w *importWriter) findings(findings []ManifestFinding) {
	p := w.plan
	for i, f := range findings {
		path := fmt.Sprintf("findings[%d]", i)
		p.oneOf(path+".kind", f.Kind, findingKinds)
		p.oneOf(path+".severity", f.Severity, severities)
		p.required(path+".title", f.Title)
		id := p.ids["findings"][f.Key]
		w.exec(`INSERT INTO findings (id, review_id, first_seen_revision_id, kind, severity, status, title, rationale, provenance, author_kind, author_id, import_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'OPEN', ?, ?, ?, ?, ?, ?, ?, ?)`, id, p.reviewID, p.revisionID, f.Kind, f.Severity, f.Title, f.Rationale,
			p.provenance(path, f.Provenance, "UNKNOWN"), p.author.Kind, p.author.ID, p.importID, w.now, w.now)
		w.link("finding_evidence", "finding_id", id, "evidence_id", p.refs("evidence", path+".evidence", f.Evidence))
	}
}

func (w *importWriter) topics(topics []ManifestTopic) error {
	p := w.plan
	var nextRootPosition int
	if err := w.tx.QueryRowContext(w.ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM topics WHERE review_id = ? AND parent_id IS NULL`,
		p.reviewID).Scan(&nextRootPosition); err != nil {
		return err
	}
	childPositions := map[string]int{}
	for i, t := range topics {
		path := fmt.Sprintf("topics[%d]", i)
		p.required(path+".title", t.Title)
		id := p.ids["topics"][t.Key]
		var parentID sql.NullString
		position := nextRootPosition
		if t.Parent != "" {
			parentID = sql.NullString{String: p.ids["topics"][t.Parent], Valid: true}
			position = childPositions[t.Parent]
			childPositions[t.Parent]++
		} else {
			nextRootPosition++
		}
		w.exec(`INSERT INTO topics (id, review_id, parent_id, title, summary, origin, state, position, author_kind, author_id, import_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'IMPORTED', 'ACTIVE', ?, ?, ?, ?, ?, ?)`, id, p.reviewID, parentID, t.Title, t.Summary, position,
			p.author.Kind, p.author.ID, p.importID, w.now, w.now)
		w.link("topic_intents", "topic_id", id, "intent_id", p.refs("intents", path+".intents", t.Intents))
		w.link("topic_changes", "topic_id", id, "change_id", p.refs("changes", path+".changes", t.Changes))
		w.link("topic_findings", "topic_id", id, "finding_id", p.refs("findings", path+".findings", t.Findings))
		w.link("topic_evidence", "topic_id", id, "evidence_id", p.refs("evidence", path+".evidence", t.Evidence))
		w.representations(id, path, t.Representations)
	}
	return nil
}

func (w *importWriter) representations(topicID, topicPath string, reps []ManifestRepresentation) {
	p := w.plan
	seen := map[string]bool{}
	for i, rep := range reps {
		path := fmt.Sprintf("%s.representations[%d]", topicPath, i)
		p.oneOf(path+".type", rep.Type, representationTypes)
		if seen[rep.Type] {
			p.add(path+".type", "topic already has a %s representation", rep.Type)
		}
		seen[rep.Type] = true
		config, ok := checkRepresentationConfig(&p.problems, path, rep.Type, w.representationConfig(path, rep))
		if !ok {
			continue
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			w.err = err
			return
		}
		title := rep.Title
		if title == "" {
			title = defaultRepresentationTitles[rep.Type]
		}
		w.exec(`INSERT INTO representations (id, topic_id, revision_id, type, title, config, origin, position, author_kind, author_id, import_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'IMPORTED', ?, ?, ?, ?, ?, ?)`, store.NewID("rep"), topicID, p.revisionID, rep.Type, title, string(encoded), i,
			p.author.Kind, p.author.ID, p.importID, w.now, w.now)
	}
}

// representationConfig resolves the manifest keys of rep into record IDs.
func (w *importWriter) representationConfig(path string, rep ManifestRepresentation) RepresentationConfig {
	p := w.plan
	var config RepresentationConfig
	if rep.Text != nil {
		config.Text = &TextConfig{Sections: []TextSection{}}
		for i, s := range rep.Text.Sections {
			config.Text.Sections = append(config.Text.Sections, TextSection{Heading: s.Heading, Body: s.Body,
				EvidenceIDs: p.refs("evidence", fmt.Sprintf("%s.text.sections[%d].evidence", path, i), s.Evidence)})
		}
	}
	if rep.Diagram != nil {
		config.Diagram = &DiagramConfig{Edges: rep.Diagram.Edges}
		for i, n := range rep.Diagram.Nodes {
			nodePath := fmt.Sprintf("%s.diagram.nodes[%d]", path, i)
			node := DiagramNode{ID: n.ID, Label: n.Label, Kind: n.Kind, EvidenceIDs: p.refs("evidence", nodePath+".evidence", n.Evidence)}
			if n.Ref != nil {
				node.Ref = p.entityRef(nodePath+".ref", *n.Ref, entityTypes)
			}
			config.Diagram.Nodes = append(config.Diagram.Nodes, node)
		}
	}
	if rep.CodeDiff != nil {
		for i, key := range rep.CodeDiff.Evidence {
			if p.evidenceKinds[key] != "CODE_RANGE" {
				p.add(fmt.Sprintf("%s.code_diff.evidence[%d]", path, i), "%q is not CODE_RANGE evidence", key)
			}
		}
		config.CodeDiff = &EvidenceListConfig{EvidenceIDs: p.refs("evidence", path+".code_diff.evidence", rep.CodeDiff.Evidence)}
	}
	if rep.EvidenceList != nil {
		config.EvidenceList = &EvidenceListConfig{EvidenceIDs: p.refs("evidence", path+".evidence_list.evidence", rep.EvidenceList.Evidence)}
	}
	return config
}

var entityKindByType = map[string]string{
	"TOPIC": "topics", "INTENT": "intents", "CHANGE": "changes", "FINDING": "findings", "EVIDENCE": "evidence",
}

func (p *importPlan) entityRef(path string, ref ManifestRef, allowed []string) *EntityRef {
	if !slices.Contains(allowed, ref.Type) {
		p.add(path+".type", "%q is not one of %s", ref.Type, strings.Join(allowed, ", "))
		return nil
	}
	id, ok := p.ids[entityKindByType[ref.Type]][ref.Key]
	if !ok {
		p.add(path+".key", "unknown %s key %q", strings.ToLower(ref.Type), ref.Key)
		return nil
	}
	return &EntityRef{Type: ref.Type, ID: id}
}

func (w *importWriter) attention(signals []ManifestAttention) {
	p := w.plan
	for i, a := range signals {
		path := fmt.Sprintf("attention[%d]", i)
		p.oneOf(path+".priority", a.Priority, priorities)
		p.required(path+".rationale", a.Rationale)
		key := a.Key
		if key == "" {
			key = strconv.Itoa(i)
		}
		id := p.ids["attention"][key]
		target := p.entityRef(path+".target", a.Target, attentionTargets)
		var topicID, findingID, changeID sql.NullString
		if target != nil {
			ref := sql.NullString{String: target.ID, Valid: true}
			switch target.Type {
			case "TOPIC":
				topicID = ref
			case "FINDING":
				findingID = ref
			case "CHANGE":
				changeID = ref
			}
		}
		w.exec(`INSERT INTO attention_signals (id, revision_id, topic_id, finding_id, change_id, priority, rationale, uncertainty, provenance, author_kind, author_id, import_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, p.revisionID, topicID, findingID, changeID, a.Priority, a.Rationale, a.Uncertainty,
			p.provenance(path, a.Provenance, "UNKNOWN"), p.author.Kind, p.author.ID, p.importID, w.now)
		w.link("attention_evidence", "attention_id", id, "evidence_id", p.refs("evidence", path+".evidence", a.Evidence))
	}
}
