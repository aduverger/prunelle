package review

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aduverger/prunelle/server/internal/git"
)

func (s *Service) CreateReview(ctx context.Context, req CreateReviewRequest) (Review, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return Review{}, invalid("review title is required")
	}
	if _, err := s.GetWorkspace(ctx, req.WorkspaceID); err != nil {
		return Review{}, err
	}
	now := s.now()
	r := Review{ID: newID("rvw"), WorkspaceID: req.WorkspaceID, Title: title, Description: req.Description,
		Status: "OPEN", CreatedAt: now, UpdatedAt: now}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO reviews (id, workspace_id, title, description, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, r.ID, r.WorkspaceID, r.Title, r.Description, r.Status, formatTime(now), formatTime(now))
		return err
	})
	return r, err
}

const reviewColumns = `id, workspace_id, title, description, status, current_revision_id, created_at, updated_at`

func scanReview(scan func(...any) error) (Review, error) {
	var r Review
	var current sql.NullString
	var created, updated string
	if err := scan(&r.ID, &r.WorkspaceID, &r.Title, &r.Description, &r.Status, &current, &created, &updated); err != nil {
		return Review{}, err
	}
	r.CurrentRevisionID = stringPtr(current)
	r.CreatedAt, r.UpdatedAt = parseTime(created), parseTime(updated)
	return r, nil
}

func (s *Service) ListReviews(ctx context.Context, workspaceID *string) ([]Review, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT `+reviewColumns+` FROM reviews
		WHERE ?1 IS NULL OR workspace_id = ?1 ORDER BY created_at, id`, nullString(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reviews := []Review{}
	for rows.Next() {
		r, err := scanReview(rows.Scan)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, rows.Err()
}

func (s *Service) GetReview(ctx context.Context, id string) (Review, error) {
	row := s.db.Reader().QueryRowContext(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE id = ?`, id)
	r, err := scanReview(row.Scan)
	if err == sql.ErrNoRows {
		return Review{}, notFound("review", id)
	}
	return r, err
}

// CaptureRevision snapshots every repository of the review's workspace into a new revision.
// Git runs outside the write transaction; the transaction only inserts the captured rows.
func (s *Service) CaptureRevision(ctx context.Context, reviewID string) (Revision, error) {
	rv, err := s.GetReview(ctx, reviewID)
	if err != nil {
		return Revision{}, err
	}
	repos, err := s.repositories(ctx, rv.WorkspaceID)
	if err != nil {
		return Revision{}, err
	}
	snapshots := make([]git.Snapshot, len(repos))
	var total int64
	for i, repo := range repos {
		snapshots[i], err = git.Capture(ctx, repo.RootPath, git.Options{BaseRef: repo.BaseRef, IncludeUntracked: repo.IncludeUntracked})
		if err != nil {
			return Revision{}, &Error{Code: CodeCaptureFailed, Message: fmt.Sprintf("repository %s: %v", repo.Label, err)}
		}
		total += snapshots[i].Size()
	}
	if total > MaxRevisionContentBytes {
		return Revision{}, &Error{Code: CodeCaptureFailed,
			Message: fmt.Sprintf("revision content is %d bytes, above the %d byte limit", total, MaxRevisionContentBytes)}
	}

	now := s.now()
	revision := Revision{ID: newID("rev"), ReviewID: reviewID, CapturedAt: now, Status: "CONSISTENT"}
	fingerprint := sha256.New()
	for i, snap := range snapshots {
		fmt.Fprintf(fingerprint, "%s\x00%s\x00", repos[i].ID, snap.Fingerprint)
		if !snap.Consistent {
			revision.Status = "INCONSISTENT"
		}
	}
	revision.Fingerprint = hex.EncodeToString(fingerprint.Sum(nil))

	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal), 0) + 1 FROM revisions WHERE review_id = ?`,
			reviewID).Scan(&revision.Ordinal); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO revisions (id, review_id, ordinal, captured_at, fingerprint, status)
			VALUES (?, ?, ?, ?, ?, ?)`, revision.ID, reviewID, revision.Ordinal, formatTime(now), revision.Fingerprint, revision.Status); err != nil {
			return err
		}
		for i, snap := range snapshots {
			if err := insertSnapshot(ctx, tx, revision.ID, repos[i].ID, snap); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE reviews SET current_revision_id = ?, updated_at = ? WHERE id = ?`,
			revision.ID, formatTime(now), reviewID)
		return err
	})
	if err != nil {
		return Revision{}, err
	}
	return s.GetRevision(ctx, revision.ID)
}

func insertSnapshot(ctx context.Context, tx *sql.Tx, revisionID, repositoryID string, snap git.Snapshot) error {
	status := "CONSISTENT"
	if !snap.Consistent {
		status = "INCONSISTENT"
	}
	warnings, err := json.Marshal(append([]string{}, snap.Warnings...))
	if err != nil {
		return err
	}
	snapshotID := newID("snap")
	if _, err := tx.ExecContext(ctx, `INSERT INTO repo_snapshots
		(id, revision_id, repository_id, base_ref, base_sha, head_sha, branch, staged_count, unstaged_count, untracked_count, fingerprint, status, warnings)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshotID, revisionID, repositoryID, snap.BaseRef, snap.BaseSHA, snap.HeadSHA, snap.Branch,
		snap.StagedCount, snap.UnstagedCount, snap.UntrackedCount, snap.Fingerprint, status, string(warnings)); err != nil {
		return err
	}
	for _, f := range snap.Files {
		baseBlob, err := insertBlob(ctx, tx, f.Base)
		if err != nil {
			return err
		}
		headBlob, err := insertBlob(ctx, tx, f.Head)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO snapshot_files
			(snapshot_id, path, old_path, status, base_mode, head_mode, base_oid, head_oid, base_blob, head_blob, binary, content_omitted)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshotID, f.Path, emptyNull(f.OldPath), string(f.Status), emptyNull(f.BaseMode), emptyNull(f.HeadMode),
			emptyNull(f.BaseOID), emptyNull(f.HeadOID), baseBlob, headBlob, f.Binary, emptyNull(f.Omitted)); err != nil {
			return err
		}
	}
	return nil
}

func insertBlob(ctx context.Context, tx *sql.Tx, c *git.Content) (sql.NullString, error) {
	if c == nil {
		return sql.NullString{}, nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO blobs (hash, size, content) VALUES (?, ?, ?) ON CONFLICT (hash) DO NOTHING`,
		c.Hash, len(c.Data), c.Data)
	return sql.NullString{String: c.Hash, Valid: true}, err
}

func emptyNull(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func (s *Service) ListRevisions(ctx context.Context, reviewID string) ([]Revision, error) {
	if _, err := s.GetReview(ctx, reviewID); err != nil {
		return nil, err
	}
	ids, err := queryStrings(ctx, s.db.Reader(), `SELECT id FROM revisions WHERE review_id = ? ORDER BY ordinal`, reviewID)
	if err != nil {
		return nil, err
	}
	revisions := []Revision{}
	for _, id := range ids {
		r, err := s.GetRevision(ctx, id)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, r)
	}
	return revisions, nil
}

func (s *Service) GetRevision(ctx context.Context, id string) (Revision, error) {
	r := Revision{ID: id}
	var captured string
	err := scanOne(s.db.Reader().QueryRowContext(ctx, `SELECT review_id, ordinal, captured_at, fingerprint, status FROM revisions WHERE id = ?`, id),
		"revision", id, &r.ReviewID, &r.Ordinal, &captured, &r.Fingerprint, &r.Status)
	if err != nil {
		return Revision{}, err
	}
	r.CapturedAt = parseTime(captured)
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT s.repository_id, p.label, s.base_ref, s.base_sha, s.head_sha, s.branch,
			s.staged_count, s.unstaged_count, s.untracked_count, s.status, s.warnings,
			(SELECT COUNT(*) FROM snapshot_files f WHERE f.snapshot_id = s.id)
		FROM repo_snapshots s JOIN repositories p ON p.id = s.repository_id
		WHERE s.revision_id = ? ORDER BY p.label`, id)
	if err != nil {
		return Revision{}, err
	}
	defer rows.Close()
	r.Snapshots = []RepoSnapshot{}
	for rows.Next() {
		var snap RepoSnapshot
		var warnings string
		if err := rows.Scan(&snap.RepositoryID, &snap.RepositoryLabel, &snap.BaseRef, &snap.BaseSHA, &snap.HeadSHA, &snap.Branch,
			&snap.StagedCount, &snap.UnstagedCount, &snap.UntrackedCount, &snap.Status, &warnings, &snap.FileCount); err != nil {
			return Revision{}, err
		}
		if err := json.Unmarshal([]byte(warnings), &snap.Warnings); err != nil {
			return Revision{}, err
		}
		r.Snapshots = append(r.Snapshots, snap)
	}
	return r, rows.Err()
}

// SourceChanges lists every changed file of a revision with the topics it belongs to.
func (s *Service) SourceChanges(ctx context.Context, revisionID string) (SourceChanges, error) {
	revision, err := s.GetRevision(ctx, revisionID)
	if err != nil {
		return SourceChanges{}, err
	}
	topicsByFile, err := s.topicsByFile(ctx, revisionID)
	if err != nil {
		return SourceChanges{}, err
	}
	result := SourceChanges{RevisionID: revisionID, Repositories: []RepositoryChange{}}
	for _, snap := range revision.Snapshots {
		rows, err := s.db.Reader().QueryContext(ctx, `SELECT f.path, f.old_path, f.status, f.binary, f.content_omitted
			FROM snapshot_files f JOIN repo_snapshots s ON s.id = f.snapshot_id
			WHERE s.revision_id = ? AND s.repository_id = ? ORDER BY f.path`, revisionID, snap.RepositoryID)
		if err != nil {
			return SourceChanges{}, err
		}
		repo := RepositoryChange{RepositoryID: snap.RepositoryID, RepositoryLabel: snap.RepositoryLabel, Files: []SourceFile{}}
		for rows.Next() {
			var f SourceFile
			var oldPath, omitted sql.NullString
			if err := rows.Scan(&f.Path, &oldPath, &f.Status, &f.Binary, &omitted); err != nil {
				rows.Close()
				return SourceChanges{}, err
			}
			f.OldPath, f.ContentOmitted = stringPtr(oldPath), stringPtr(omitted)
			f.TopicIDs = topicsByFile[fileKey{snap.RepositoryID, f.Path}]
			if f.TopicIDs == nil {
				f.TopicIDs = []string{}
				f.Unassigned = true
				result.UnassignedCount++
			}
			repo.Files = append(repo.Files, f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return SourceChanges{}, err
		}
		result.Repositories = append(result.Repositories, repo)
	}
	return result, nil
}

type fileKey struct{ repositoryID, path string }

// topicsByFile applies the canonical topic→file rule: a file belongs to a topic when a code-range
// evidence reachable from the topic points at it.
func (s *Service) topicsByFile(ctx context.Context, revisionID string) (map[fileKey][]string, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `WITH `+topicEvidenceCTE+`
		SELECT DISTINCT r.repository_id, r.path, te.topic_id
		FROM topic_reachable_evidence te
		JOIN evidence e ON e.id = te.evidence_id
		JOIN evidence_code_ranges r ON r.evidence_id = e.id
		WHERE e.revision_id = ?
		ORDER BY te.topic_id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	topics := map[fileKey][]string{}
	for rows.Next() {
		var key fileKey
		var topicID string
		if err := rows.Scan(&key.repositoryID, &key.path, &topicID); err != nil {
			return nil, err
		}
		topics[key] = append(topics[key], topicID)
	}
	return topics, rows.Err()
}

// topicEvidenceCTE defines topic_reachable_evidence(topic_id, evidence_id): evidence linked to a
// topic directly or through its linked intents, changes, and findings.
const topicEvidenceCTE = `topic_reachable_evidence(topic_id, evidence_id) AS (
	SELECT topic_id, evidence_id FROM topic_evidence
	UNION SELECT ti.topic_id, ie.evidence_id FROM topic_intents ti JOIN intent_evidence ie ON ie.intent_id = ti.intent_id
	UNION SELECT tc.topic_id, ce.evidence_id FROM topic_changes tc JOIN change_evidence ce ON ce.change_id = tc.change_id
	UNION SELECT tf.topic_id, fe.evidence_id FROM topic_findings tf JOIN finding_evidence fe ON fe.finding_id = tf.finding_id
)`

func (s *Service) GetFile(ctx context.Context, revisionID, repositoryID, path string) (FileContent, error) {
	f := FileContent{RevisionID: revisionID, RepositoryID: repositoryID, Path: path}
	var oldPath, baseMode, headMode, baseOID, headOID, omitted, baseBlob, headBlob sql.NullString
	var baseText, headText []byte
	err := scanOne(s.db.Reader().QueryRowContext(ctx, `SELECT f.old_path, f.status, f.base_mode, f.head_mode, f.base_oid, f.head_oid,
			f.binary, f.content_omitted, f.base_blob, f.head_blob, b.content, h.content
		FROM snapshot_files f
		JOIN repo_snapshots s ON s.id = f.snapshot_id
		LEFT JOIN blobs b ON b.hash = f.base_blob
		LEFT JOIN blobs h ON h.hash = f.head_blob
		WHERE s.revision_id = ? AND s.repository_id = ? AND f.path = ?`, revisionID, repositoryID, path),
		"file", path, &oldPath, &f.Status, &baseMode, &headMode, &baseOID, &headOID, &f.Binary, &omitted, &baseBlob, &headBlob, &baseText, &headText)
	if err != nil {
		return FileContent{}, err
	}
	f.OldPath, f.BaseMode, f.HeadMode = stringPtr(oldPath), stringPtr(baseMode), stringPtr(headMode)
	f.BaseOID, f.HeadOID, f.ContentOmitted = stringPtr(baseOID), stringPtr(headOID), stringPtr(omitted)
	f.BaseText, f.HeadText = blobText(baseBlob, baseText), blobText(headBlob, headText)
	return f, nil
}

// blobText returns content when the blob reference is set; an empty file has a blob with no bytes.
func blobText(ref sql.NullString, content []byte) *string {
	if !ref.Valid {
		return nil
	}
	s := string(content)
	return &s
}
