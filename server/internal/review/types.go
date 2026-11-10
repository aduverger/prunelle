package review

import "time"

// These types are the API wire format; api/openapi.yaml maps its schemas to them.

type Repository struct {
	ID               string `json:"id"`
	ReviewID         string `json:"review_id"`
	Label            string `json:"label"`
	RootPath         string `json:"root_path"`
	IncludeUntracked bool   `json:"include_untracked"`
}

type CreateRepositoryRequest struct {
	Label            string `json:"label"`
	Path             string `json:"path"`
	IncludeUntracked *bool  `json:"include_untracked,omitempty"`
}

type Review struct {
	ID                string       `json:"id"`
	ContextPath       *string      `json:"context_path"`
	Repositories      []Repository `json:"repositories"`
	Title             string       `json:"title"`
	Description       string       `json:"description"`
	Status            string       `json:"status"`
	CurrentRevisionID *string      `json:"current_revision_id"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

type CreateReviewRequest struct {
	Path         string                    `json:"path,omitempty"`
	InferContext bool                      `json:"infer_context,omitempty"`
	Repositories []CreateRepositoryRequest `json:"repositories,omitempty"`
	Title        string                    `json:"title,omitempty"`
	Description  string                    `json:"description"`
}

type CaptureRequest struct {
	BaseRef            string              `json:"base_ref,omitempty"`
	RepositoryBaseRefs []RepositoryBaseRef `json:"repository_base_refs,omitempty"`
}

type RepositoryBaseRef struct {
	RepositoryID string `json:"repository_id"`
	BaseRef      string `json:"base_ref"`
}

type Revision struct {
	ID          string         `json:"id"`
	ReviewID    string         `json:"review_id"`
	Ordinal     int            `json:"ordinal"`
	CapturedAt  time.Time      `json:"captured_at"`
	Fingerprint string         `json:"fingerprint"`
	Status      string         `json:"status"`
	Snapshots   []RepoSnapshot `json:"snapshots"`
}

type RepoSnapshot struct {
	RepositoryID    string   `json:"repository_id"`
	RepositoryLabel string   `json:"repository_label"`
	BaseRef         string   `json:"base_ref"`
	BaseSHA         string   `json:"base_sha"`
	HeadSHA         string   `json:"head_sha"`
	Branch          string   `json:"branch"`
	StagedCount     int      `json:"staged_count"`
	UnstagedCount   int      `json:"unstaged_count"`
	UntrackedCount  int      `json:"untracked_count"`
	FileCount       int      `json:"file_count"`
	Status          string   `json:"status"`
	Warnings        []string `json:"warnings"`
}

type SourceChanges struct {
	RevisionID           string             `json:"revision_id"`
	Repositories         []RepositoryChange `json:"repositories"`
	UnassignedCount      int                `json:"unassigned_count"`
	AssignmentsAvailable bool               `json:"assignments_available"`
}

type RepositoryChange struct {
	RepositoryID    string       `json:"repository_id"`
	RepositoryLabel string       `json:"repository_label"`
	Files           []SourceFile `json:"files"`
}

type SourceFile struct {
	Path              string   `json:"path"`
	OldPath           *string  `json:"old_path"`
	Status            string   `json:"status"`
	Binary            bool     `json:"binary"`
	ContentOmitted    *string  `json:"content_omitted"`
	TopicIDs          []string `json:"topic_ids"`
	Unassigned        bool     `json:"unassigned"`
	AssignmentMode    string   `json:"assignment_mode"`
	AssignmentVersion int      `json:"assignment_version"`
}

type FileContent struct {
	RevisionID     string  `json:"revision_id"`
	RepositoryID   string  `json:"repository_id"`
	Path           string  `json:"path"`
	OldPath        *string `json:"old_path"`
	Status         string  `json:"status"`
	BaseMode       *string `json:"base_mode"`
	HeadMode       *string `json:"head_mode"`
	BaseOID        *string `json:"base_oid"`
	HeadOID        *string `json:"head_oid"`
	Binary         bool    `json:"binary"`
	ContentOmitted *string `json:"content_omitted"`
	BaseText       *string `json:"base_text"`
	HeadText       *string `json:"head_text"`
}

type Import struct {
	ID         string    `json:"id"`
	ReviewID   string    `json:"review_id"`
	RevisionID string    `json:"revision_id"`
	Author     Author    `json:"author"`
	ImportedAt time.Time `json:"imported_at"`
}

type Author struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Actor makes a change: its author (the authenticated human, or an agent the request names) and the
// login that authenticated the request.
type Actor struct {
	Author
	Login string
}

type Topic struct {
	ID          string    `json:"id"`
	ReviewID    string    `json:"review_id"`
	ParentID    *string   `json:"parent_id"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Origin      string    `json:"origin"`
	State       string    `json:"state"`
	Position    int       `json:"position"`
	Author      Author    `json:"author"`
	ImportID    *string   `json:"import_id"`
	IntentIDs   []string  `json:"intent_ids"`
	ChangeIDs   []string  `json:"change_ids"`
	FindingIDs  []string  `json:"finding_ids"`
	EvidenceIDs []string  `json:"evidence_ids"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateTopicRequest struct {
	ParentID    *string  `json:"parent_id,omitempty"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	IntentIDs   []string `json:"intent_ids,omitempty"`
	ChangeIDs   []string `json:"change_ids,omitempty"`
	FindingIDs  []string `json:"finding_ids,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// UpdateTopicRequest changes the fields it sets; a link list replaces the topic's links of that kind.
type UpdateTopicRequest struct {
	Title       *string   `json:"title,omitempty"`
	Summary     *string   `json:"summary,omitempty"`
	State       *string   `json:"state,omitempty"`
	IntentIDs   *[]string `json:"intent_ids,omitempty"`
	ChangeIDs   *[]string `json:"change_ids,omitempty"`
	FindingIDs  *[]string `json:"finding_ids,omitempty"`
	EvidenceIDs *[]string `json:"evidence_ids,omitempty"`
}

// MoveTopicRequest places a topic under ParentID (nil for the root) at Position among its siblings.
type MoveTopicRequest struct {
	ParentID *string `json:"parent_id"`
	Position int     `json:"position"`
}

type Intent struct {
	ID                 string   `json:"id"`
	ReviewID           string   `json:"review_id"`
	Kind               string   `json:"kind"`
	Statement          string   `json:"statement"`
	Source             string   `json:"source"`
	AcceptanceCriteria *string  `json:"acceptance_criteria"`
	Provenance         string   `json:"provenance"`
	Author             Author   `json:"author"`
	ImportID           *string  `json:"import_id"`
	EvidenceIDs        []string `json:"evidence_ids"`
}

type ObservedChange struct {
	ID                 string   `json:"id"`
	RevisionID         string   `json:"revision_id"`
	Kind               string   `json:"kind"`
	Summary            string   `json:"summary"`
	Before             string   `json:"before"`
	After              string   `json:"after"`
	Components         []string `json:"components"`
	VerificationStatus string   `json:"verification_status"`
	Provenance         string   `json:"provenance"`
	Author             Author   `json:"author"`
	ImportID           *string  `json:"import_id"`
	EvidenceIDs        []string `json:"evidence_ids"`
}

type Finding struct {
	Version             int      `json:"version"`
	ID                  string   `json:"id"`
	ReviewID            string   `json:"review_id"`
	FirstSeenRevisionID string   `json:"first_seen_revision_id"`
	Kind                string   `json:"kind"`
	Severity            string   `json:"severity"`
	Status              string   `json:"status"`
	Title               string   `json:"title"`
	Rationale           string   `json:"rationale"`
	Provenance          string   `json:"provenance"`
	Author              Author   `json:"author"`
	ImportID            *string  `json:"import_id"`
	EvidenceIDs         []string `json:"evidence_ids"`
}

type Evidence struct {
	ID         string     `json:"id"`
	RevisionID string     `json:"revision_id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Content    *string    `json:"content"`
	Source     string     `json:"source"`
	Provenance string     `json:"provenance"`
	Author     Author     `json:"author"`
	ImportID   *string    `json:"import_id"`
	CodeRange  *CodeRange `json:"code_range"`
}

type CodeRange struct {
	RepositoryID string `json:"repository_id"`
	Path         string `json:"path"`
	Side         string `json:"side"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
	BlobHash     string `json:"blob_hash"`
	SelectedText string `json:"selected_text"`
}

// CodeRangeInput names lines of a changed file; the server resolves the blob and selected text.
type CodeRangeInput struct {
	RepositoryID string `json:"repository_id"`
	Path         string `json:"path"`
	Side         string `json:"side"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
}

type AttentionSignal struct {
	ID          string          `json:"id"`
	RevisionID  string          `json:"revision_id"`
	Target      AttentionTarget `json:"target"`
	Priority    string          `json:"priority"`
	Rationale   string          `json:"rationale"`
	Uncertainty string          `json:"uncertainty"`
	Provenance  string          `json:"provenance"`
	Author      Author          `json:"author"`
	ImportID    *string         `json:"import_id"`
	EvidenceIDs []string        `json:"evidence_ids"`
}

type AttentionTarget struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Representation is one way to present a topic. ID, Author, and Version are nil for representations
// derived from topic links (code diff, evidence list) or reported as unavailable.
type Representation struct {
	ID                *string `json:"id"`
	Version           *int    `json:"version"`
	TopicID           string  `json:"topic_id"`
	Type              string  `json:"type"`
	Title             string  `json:"title"`
	Origin            string  `json:"origin"`
	Available         bool    `json:"available"`
	UnavailableReason *string `json:"unavailable_reason"`
	RevisionID        *string `json:"revision_id"`
	Author            *Author `json:"author"`
	RepresentationConfig
	SourceFiles *[]SourceFileRef `json:"source_files,omitempty"`
}

type SourceFileRef struct {
	RevisionID   string `json:"revision_id"`
	RepositoryID string `json:"repository_id"`
	Path         string `json:"path"`
}

// RepresentationConfig holds the configuration of one representation: only the field matching its
// type is set. References are record IDs.
type RepresentationConfig struct {
	Text         *TextConfig         `json:"text,omitempty"`
	Diagram      *DiagramConfig      `json:"diagram,omitempty"`
	CodeDiff     *EvidenceListConfig `json:"code_diff,omitempty"`
	EvidenceList *EvidenceListConfig `json:"evidence_list,omitempty"`
}

type TextConfig struct {
	Sections []TextSection `json:"sections"`
}

type TextSection struct {
	Heading     string   `json:"heading"`
	Body        string   `json:"body"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type DiagramConfig struct {
	Nodes []DiagramNode `json:"nodes"`
	Edges []DiagramEdge `json:"edges"`
}

type DiagramNode struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Kind        string     `json:"kind"`
	Ref         *EntityRef `json:"ref"`
	EvidenceIDs []string   `json:"evidence_ids"`
}

type DiagramEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

type EntityRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type EvidenceListConfig struct {
	EvidenceIDs []string `json:"evidence_ids"`
}

type CreateRepresentationRequest struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	RepresentationConfig
}

// UpdateRepresentationRequest changes the title and replaces the config when set; the type is fixed.
type UpdateRepresentationRequest struct {
	Title *string `json:"title,omitempty"`
	RepresentationConfig
}

// Discussion is a thread about one anchor, made against one revision. TopicIDs are the topics
// the anchor relates to, so a topic shows every discussion about it.
type Discussion struct {
	ID         string    `json:"id"`
	ReviewID   string    `json:"review_id"`
	RevisionID string    `json:"revision_id"`
	Anchor     Anchor    `json:"anchor"`
	TopicIDs   []string  `json:"topic_ids"`
	Status     string    `json:"status"`
	Author     Author    `json:"author"`
	Version    int       `json:"version"`
	Messages   []Message `json:"messages"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Anchor is what a discussion is about. ID names the target record (the diagram representation for
// DIAGRAM_ELEMENT); it is nil for REVIEW and CODE_RANGE anchors.
type Anchor struct {
	Type      string     `json:"type"`
	ID        *string    `json:"id"`
	ElementID *string    `json:"element_id"`
	CodeRange *CodeRange `json:"code_range"`
}

type AnchorInput struct {
	Type      string          `json:"type"`
	ID        *string         `json:"id,omitempty"`
	ElementID *string         `json:"element_id,omitempty"`
	CodeRange *CodeRangeInput `json:"code_range,omitempty"`
}

type Message struct {
	ID           string    `json:"id"`
	DiscussionID string    `json:"discussion_id"`
	Author       Author    `json:"author"`
	Body         string    `json:"body"`
	CreatedAt    time.Time `json:"created_at"`
}

type CreateDiscussionRequest struct {
	RevisionID *string     `json:"revision_id,omitempty"`
	Anchor     AnchorInput `json:"anchor"`
	Body       string      `json:"body"`
}

type UpdateDiscussionRequest struct {
	Status string `json:"status"`
}

type CreateMessageRequest struct {
	Body string `json:"body"`
}

// Decision is a reviewer's verdict on one revision. REQUEST_CHANGES lists the actionable findings
// and discussions. Decisions are never modified.
type Decision struct {
	ID            string    `json:"id"`
	ReviewID      string    `json:"review_id"`
	RevisionID    string    `json:"revision_id"`
	Decision      string    `json:"decision"`
	Body          string    `json:"body"`
	FindingIDs    []string  `json:"finding_ids"`
	DiscussionIDs []string  `json:"discussion_ids"`
	Author        Author    `json:"author"`
	CreatedAt     time.Time `json:"created_at"`
}

type CreateDecisionRequest struct {
	RevisionID    *string  `json:"revision_id,omitempty"`
	Decision      string   `json:"decision"`
	Body          string   `json:"body"`
	FindingIDs    []string `json:"finding_ids,omitempty"`
	DiscussionIDs []string `json:"discussion_ids,omitempty"`
}

type ImportRequest struct {
	RevisionID *string  `json:"revision_id,omitempty"`
	Manifest   Manifest `json:"manifest"`
}

type ImportResult struct {
	ImportID   string                       `json:"import_id"`
	RevisionID string                       `json:"revision_id"`
	IDs        map[string]map[string]string `json:"ids"`
}

type RevisionView struct {
	RevisionID        *string      `json:"revision_id"`
	Editable          bool         `json:"editable"`
	Graph             *ReviewGraph `json:"graph"`
	UnavailableReason *string      `json:"unavailable_reason"`
}

type ReviewGraph struct {
	storedRepresentations []Representation
	Topics                []Topic           `json:"topics"`
	Findings              []Finding         `json:"findings"`
	Intents               []Intent          `json:"intents"`
	Changes               []ObservedChange  `json:"changes"`
	Evidence              []Evidence        `json:"evidence"`
	Attention             []AttentionSignal `json:"attention"`
	Representations       []Representation  `json:"representations"`
	SourceChanges         *SourceChanges    `json:"source_changes"`
}

type UpdateFindingRequest struct {
	RevisionID  string    `json:"revision_id"`
	Status      *string   `json:"status,omitempty"`
	EvidenceIDs *[]string `json:"evidence_ids,omitempty"`
}

type UpdateSourceTopicsRequest struct {
	RepositoryID string   `json:"repository_id"`
	Path         string   `json:"path"`
	Mode         string   `json:"mode"`
	TopicIDs     []string `json:"topic_ids"`
}

type RevisionComparison struct {
	FromRevisionID    string             `json:"from_revision_id"`
	ToRevisionID      string             `json:"to_revision_id"`
	UnavailableReason *string            `json:"unavailable_reason"`
	Topics            []EntityComparison `json:"topics"`
	Findings          []EntityComparison `json:"findings"`
}

type EntityComparison struct {
	ID            string   `json:"id"`
	Change        string   `json:"change"`
	ChangedFields []string `json:"changed_fields"`
	SourceStatus  string   `json:"source_status"`
}

type DiscussionAnchorStatus struct {
	DiscussionID string `json:"discussion_id"`
	RevisionID   string `json:"revision_id"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
}
