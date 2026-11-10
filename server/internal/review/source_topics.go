package review

import (
	"context"
	"database/sql"
	"slices"
)

func (s *Service) readSourceChanges(ctx context.Context, revision Revision) (SourceChanges, error) {
	source := SourceChanges{RevisionID: revision.ID, Repositories: []RepositoryChange{}}
	for _, snapshot := range revision.Snapshots {
		rows, err := s.reader().QueryContext(ctx, `SELECT f.path, f.old_path, f.status, f.binary, f.content_omitted, COALESCE(a.mode, 'DERIVED'), COALESCE(a.version, 1)
   FROM snapshot_files f JOIN repo_snapshots s ON s.id = f.snapshot_id
   LEFT JOIN source_topic_assignments a ON a.snapshot_id = f.snapshot_id AND a.path = f.path
   WHERE s.revision_id = ? AND s.repository_id = ? ORDER BY f.path`, revision.ID, snapshot.RepositoryID)
		if err != nil {
			return source, err
		}
		repo := RepositoryChange{RepositoryID: snapshot.RepositoryID, RepositoryLabel: snapshot.RepositoryLabel, Files: []SourceFile{}}
		for rows.Next() {
			file := SourceFile{TopicIDs: []string{}}
			var oldPath, omitted sql.NullString
			if err := rows.Scan(&file.Path, &oldPath, &file.Status, &file.Binary, &omitted, &file.AssignmentMode, &file.AssignmentVersion); err != nil {
				rows.Close()
				return source, err
			}
			file.OldPath, file.ContentOmitted = stringPtr(oldPath), stringPtr(omitted)
			repo.Files = append(repo.Files, file)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return source, err
		}
		source.Repositories = append(source.Repositories, repo)
	}
	return source, nil
}

func (s *Service) assignSourceTopics(ctx context.Context, source *SourceChanges, graph *ReviewGraph) error {
	source.AssignmentsAvailable = true
	derived := map[fileKey][]string{}
	active := map[string]bool{}
	for _, topic := range graph.Topics {
		if topic.State != "ACTIVE" {
			continue
		}
		active[topic.ID] = true
		evidenceIDs, _ := graph.reachableEvidence(topic)
		for _, evidence := range graph.Evidence {
			if evidence.RevisionID != source.RevisionID || evidence.CodeRange == nil || !slices.Contains(evidenceIDs, evidence.ID) {
				continue
			}
			key := fileKey{evidence.CodeRange.RepositoryID, evidence.CodeRange.Path}
			if !slices.Contains(derived[key], topic.ID) {
				derived[key] = append(derived[key], topic.ID)
			}
		}
	}
	for ri := range source.Repositories {
		repo := &source.Repositories[ri]
		for fi := range repo.Files {
			file := &repo.Files[fi]
			ids := derived[fileKey{repo.RepositoryID, file.Path}]
			if file.AssignmentMode == "EXPLICIT" {
				var err error
				ids, err = queryStrings(ctx, s.reader(), `SELECT a.topic_id FROM source_topic_assignment_topics a JOIN repo_snapshots s ON s.id = a.snapshot_id WHERE s.revision_id = ? AND s.repository_id = ? AND a.path = ? ORDER BY a.topic_id`, source.RevisionID, repo.RepositoryID, file.Path)
				if err != nil {
					return err
				}
			}
			file.TopicIDs = []string{}
			for _, id := range ids {
				if active[id] {
					file.TopicIDs = append(file.TopicIDs, id)
				}
			}
			slices.Sort(file.TopicIDs)
			file.Unassigned = len(file.TopicIDs) == 0
			if file.Unassigned {
				source.UnassignedCount++
			}
		}
	}
	return nil
}

func (s *Service) UpdateSourceTopics(ctx context.Context, revisionID string, version int, req UpdateSourceTopicsRequest, actor Actor) (SourceFile, error) {
	revision, err := s.GetRevision(ctx, revisionID)
	if err != nil {
		return SourceFile{}, err
	}
	var p problems
	p.oneOf("mode", req.Mode, []string{"DERIVED", "EXPLICIT"})
	if req.Mode == "DERIVED" && len(req.TopicIDs) > 0 {
		p.add("topic_ids", "must be empty when resetting to derived associations")
	}
	if err := p.err("source topic assignment is invalid"); err != nil {
		return SourceFile{}, err
	}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		if err := requireCurrentRevision(ctx, tx, revision.ReviewID, &revisionID); err != nil {
			return err
		}
		var snapshotID string
		var currentVersion int
		if err := scanOne(tx.QueryRowContext(ctx, `SELECT f.snapshot_id, COALESCE(a.version,1) FROM snapshot_files f JOIN repo_snapshots s ON s.id = f.snapshot_id LEFT JOIN source_topic_assignments a ON a.snapshot_id=f.snapshot_id AND a.path=f.path WHERE s.revision_id=? AND s.repository_id=? AND f.path=?`, revisionID, req.RepositoryID, req.Path), "source file", req.Path, &snapshotID, &currentVersion); err != nil {
			return err
		}
		if err := checkVersion("source assignment", req.Path, currentVersion, version); err != nil {
			return err
		}
		for _, id := range uniqueIDs(req.TopicIDs) {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM topics WHERE id = ? AND review_id = ? AND state = 'ACTIVE')`, id, revision.ReviewID).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return invalid("topic %s is not an active topic of this review", id)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_topic_assignments(snapshot_id,path,mode,version) VALUES(?,?,?,?) ON CONFLICT(snapshot_id,path) DO UPDATE SET mode=excluded.mode,version=excluded.version`, snapshotID, req.Path, req.Mode, currentVersion+1); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM source_topic_assignment_topics WHERE snapshot_id=? AND path=?`, snapshotID, req.Path); err != nil {
			return err
		}
		for _, id := range uniqueIDs(req.TopicIDs) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO source_topic_assignment_topics VALUES(?,?,?)`, snapshotID, req.Path, id); err != nil {
				return err
			}
		}
		return recordEvent(ctx, tx, "source.topics.updated", revisionID, revision.ReviewID, actor, s.now())
	})
	if err != nil {
		return SourceFile{}, err
	}
	source, err := s.SourceChanges(ctx, revisionID)
	if err != nil {
		return SourceFile{}, err
	}
	for _, repo := range source.Repositories {
		if repo.RepositoryID == req.RepositoryID {
			for _, file := range repo.Files {
				if file.Path == req.Path {
					return file, nil
				}
			}
		}
	}
	return SourceFile{}, notFound("source file", req.Path)
}

func carrySourceTopics(ctx context.Context, tx *sql.Tx, previousID, revisionID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO source_topic_assignments
  SELECT next.id, a.path, a.mode, a.version + 1 FROM source_topic_assignments a
  JOIN repo_snapshots prev ON prev.id=a.snapshot_id JOIN repo_snapshots next ON next.repository_id=prev.repository_id AND next.revision_id=?
  JOIN snapshot_files f ON f.snapshot_id=next.id AND f.path=a.path WHERE prev.revision_id=?`, revisionID, previousID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO source_topic_assignment_topics
  SELECT next.id, a.path, a.topic_id FROM source_topic_assignment_topics a
  JOIN repo_snapshots prev ON prev.id=a.snapshot_id JOIN repo_snapshots next ON next.repository_id=prev.repository_id AND next.revision_id=?
  JOIN source_topic_assignments target ON target.snapshot_id=next.id AND target.path=a.path WHERE prev.revision_id=?`, revisionID, previousID)
	return err
}
