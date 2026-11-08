package review

import "time"

// These types are the API wire format; api/openapi.yaml maps its schemas to them.

type Workspace struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Repositories []Repository `json:"repositories"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type Repository struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspace_id"`
	Label            string `json:"label"`
	RootPath         string `json:"root_path"`
	BaseRef          string `json:"base_ref"`
	IncludeUntracked bool   `json:"include_untracked"`
}

type CreateWorkspaceRequest struct {
	Name         string                    `json:"name"`
	Repositories []CreateRepositoryRequest `json:"repositories"`
}

type CreateRepositoryRequest struct {
	Label            string `json:"label"`
	Path             string `json:"path"`
	BaseRef          string `json:"base_ref"`
	IncludeUntracked *bool  `json:"include_untracked,omitempty"`
}

type Review struct {
	ID                string    `json:"id"`
	WorkspaceID       string    `json:"workspace_id"`
	Title             string    `json:"title"`
	Description       string    `json:"description"`
	Status            string    `json:"status"`
	CurrentRevisionID *string   `json:"current_revision_id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type CreateReviewRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
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
	RevisionID      string             `json:"revision_id"`
	Repositories    []RepositoryChange `json:"repositories"`
	UnassignedCount int                `json:"unassigned_count"`
}

type RepositoryChange struct {
	RepositoryID    string       `json:"repository_id"`
	RepositoryLabel string       `json:"repository_label"`
	Files           []SourceFile `json:"files"`
}

type SourceFile struct {
	Path           string   `json:"path"`
	OldPath        *string  `json:"old_path"`
	Status         string   `json:"status"`
	Binary         bool     `json:"binary"`
	ContentOmitted *string  `json:"content_omitted"`
	TopicIDs       []string `json:"topic_ids"`
	Unassigned     bool     `json:"unassigned"`
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

type Topic struct {
	ID          string    `json:"id"`
	ReviewID    string    `json:"review_id"`
	ParentID    *string   `json:"parent_id"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Origin      string    `json:"origin"`
	State       string    `json:"state"`
	Position    int       `json:"position"`
	ImportID    *string   `json:"import_id"`
	IntentIDs   []string  `json:"intent_ids"`
	ChangeIDs   []string  `json:"change_ids"`
	FindingIDs  []string  `json:"finding_ids"`
	EvidenceIDs []string  `json:"evidence_ids"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Intent struct {
	ID                 string   `json:"id"`
	ReviewID           string   `json:"review_id"`
	Kind               string   `json:"kind"`
	Statement          string   `json:"statement"`
	Source             string   `json:"source"`
	AcceptanceCriteria *string  `json:"acceptance_criteria"`
	Provenance         string   `json:"provenance"`
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
	ImportID           *string  `json:"import_id"`
	EvidenceIDs        []string `json:"evidence_ids"`
}

type Finding struct {
	ID                  string   `json:"id"`
	ReviewID            string   `json:"review_id"`
	FirstSeenRevisionID string   `json:"first_seen_revision_id"`
	Kind                string   `json:"kind"`
	Severity            string   `json:"severity"`
	Status              string   `json:"status"`
	Title               string   `json:"title"`
	Rationale           string   `json:"rationale"`
	Provenance          string   `json:"provenance"`
	ImportID            *string  `json:"import_id"`
	EvidenceIDs         []string `json:"evidence_ids"`
}

type Evidence struct {
	ID               string     `json:"id"`
	RevisionID       string     `json:"revision_id"`
	Kind             string     `json:"kind"`
	Title            string     `json:"title"`
	Content          *string    `json:"content"`
	Source           string     `json:"source"`
	CollectionMethod string     `json:"collection_method"`
	Provenance       string     `json:"provenance"`
	ImportID         *string    `json:"import_id"`
	CodeRange        *CodeRange `json:"code_range"`
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

type AttentionSignal struct {
	ID          string          `json:"id"`
	RevisionID  string          `json:"revision_id"`
	Target      AttentionTarget `json:"target"`
	Priority    string          `json:"priority"`
	Rationale   string          `json:"rationale"`
	Uncertainty string          `json:"uncertainty"`
	Source      string          `json:"source"`
	Provenance  string          `json:"provenance"`
	ImportID    *string         `json:"import_id"`
	EvidenceIDs []string        `json:"evidence_ids"`
}

type AttentionTarget struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Representation is one way to present a topic. ID is nil for representations derived
// from topic links (code diff, evidence list) or reported as unavailable.
type Representation struct {
	ID                *string             `json:"id"`
	TopicID           string              `json:"topic_id"`
	Type              string              `json:"type"`
	Title             string              `json:"title"`
	Origin            string              `json:"origin"`
	Available         bool                `json:"available"`
	UnavailableReason *string             `json:"unavailable_reason"`
	RevisionID        *string             `json:"revision_id"`
	Text              *TextConfig         `json:"text,omitempty"`
	Diagram           *DiagramConfig      `json:"diagram,omitempty"`
	CodeDiff          *EvidenceListConfig `json:"code_diff,omitempty"`
	EvidenceList      *EvidenceListConfig `json:"evidence_list,omitempty"`
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

type ImportRequest struct {
	RevisionID *string  `json:"revision_id,omitempty"`
	Manifest   Manifest `json:"manifest"`
}

type ImportResult struct {
	ImportID   string                       `json:"import_id"`
	RevisionID string                       `json:"revision_id"`
	IDs        map[string]map[string]string `json:"ids"`
	Warnings   []string                     `json:"warnings"`
}
