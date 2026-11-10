package review

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
)

func (s *Service) CompareRevisions(ctx context.Context, reviewID, fromID, toID string) (RevisionComparison, error) {
	result := RevisionComparison{FromRevisionID: fromID, ToRevisionID: toID, Topics: []EntityComparison{}, Findings: []EntityComparison{}}
	err := s.readSnapshot(ctx, func(reader *Service) error {
		from, err := reader.GetRevision(ctx, fromID)
		if err != nil {
			return err
		}
		to, err := reader.GetRevision(ctx, toID)
		if err != nil {
			return err
		}
		if from.ReviewID != reviewID || to.ReviewID != reviewID || from.Ordinal >= to.Ordinal {
			return invalid("comparison requires an earlier baseline and a later target from the same review")
		}
		before, err := reader.revisionView(ctx, reviewID, &fromID)
		if err != nil {
			return err
		}
		after, err := reader.revisionView(ctx, reviewID, &toID)
		if err != nil {
			return err
		}
		if before.Graph == nil {
			reason := fmt.Sprintf("Semantic history for baseline revision %d is unavailable; no semantic changes were inferred.", from.Ordinal)
			result.UnavailableReason = &reason
			return nil
		}
		if after.Graph == nil {
			reason := fmt.Sprintf("Semantic history for target revision %d is unavailable; no semantic changes were inferred.", to.Ordinal)
			result.UnavailableReason = &reason
			return nil
		}
		result.Topics, err = reader.compareTopics(ctx, fromID, toID, before.Graph, after.Graph)
		if err != nil {
			return err
		}
		result.Findings, err = reader.compareFindings(ctx, fromID, toID, before.Graph, after.Graph)
		return err
	})
	return result, err
}

func (s *Service) compareTopics(ctx context.Context, fromID, toID string, before, after *ReviewGraph) ([]EntityComparison, error) {
	result := []EntityComparison{}
	for _, topic := range after.Topics {
		previous := slices.IndexFunc(before.Topics, func(v Topic) bool { return v.ID == topic.ID })
		item := EntityComparison{ID: topic.ID, Change: "ADDED", ChangedFields: []string{}, SourceStatus: "NONE"}
		keys := after.topicSourceFiles(topic)
		if previous >= 0 {
			old := before.Topics[previous]
			keys = append(keys, before.topicSourceFiles(old)...)
			item.ChangedFields = changedFields(topicFields(old, before), topicFields(topic, after))
			item.Change = "UNCHANGED"
			if len(item.ChangedFields) > 0 {
				item.Change = "CHANGED"
			}
			if old.State != topic.State {
				if topic.State == "OBSOLETE" {
					item.Change = "OBSOLETE"
				} else {
					item.Change = "REACTIVATED"
				}
			}
		}
		var err error
		item.SourceStatus, err = s.compareSourceFiles(ctx, fromID, toID, keys)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) compareFindings(ctx context.Context, fromID, toID string, before, after *ReviewGraph) ([]EntityComparison, error) {
	result := []EntityComparison{}
	for _, finding := range after.Findings {
		previous := slices.IndexFunc(before.Findings, func(v Finding) bool { return v.ID == finding.ID })
		item := EntityComparison{ID: finding.ID, Change: "ADDED", ChangedFields: []string{}, SourceStatus: "NONE"}
		keys := after.evidenceSourceFiles(finding.EvidenceIDs)
		if previous >= 0 {
			old := before.Findings[previous]
			keys = append(keys, before.evidenceSourceFiles(old.EvidenceIDs)...)
			item.ChangedFields = changedFields(findingFields(old), findingFields(finding))
			item.Change = "UNCHANGED"
			if len(item.ChangedFields) > 0 {
				item.Change = "CHANGED"
			}
			if old.Status != finding.Status {
				item.Change = finding.Status
				if finding.Status == "OPEN" {
					item.Change = "REOPENED"
				}
			}
		}
		var err error
		item.SourceStatus, err = s.compareSourceFiles(ctx, fromID, toID, keys)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func topicFields(topic Topic, graph *ReviewGraph) map[string]any {
	reps := []any{}
	for _, rep := range graph.storedRepresentations {
		if rep.TopicID != topic.ID || rep.ID == nil {
			continue
		}
		reps = append(reps, struct {
			ID                  string
			Type, Title, Origin string
			Config              RepresentationConfig
		}{*rep.ID, rep.Type, rep.Title, rep.Origin, comparableConfig(rep.RepresentationConfig)})
	}
	return map[string]any{"title": topic.Title, "summary": topic.Summary, "state": topic.State, "parent_id": topic.ParentID, "position": topic.Position, "intent_ids": sortedIDs(topic.IntentIDs), "change_ids": sortedIDs(topic.ChangeIDs), "finding_ids": sortedIDs(topic.FindingIDs), "evidence_ids": sortedIDs(topic.EvidenceIDs), "representations": reps, "source_topics": graph.topicSourceCorrections(topic)}
}

func findingFields(finding Finding) map[string]any {
	return map[string]any{"kind": finding.Kind, "severity": finding.Severity, "status": finding.Status, "title": finding.Title, "rationale": finding.Rationale, "provenance": finding.Provenance, "evidence_ids": sortedIDs(finding.EvidenceIDs)}
}

func sortedIDs(ids []string) []string {
	result := append([]string{}, ids...)
	slices.Sort(result)
	return slices.Compact(result)
}

func changedFields(before, after map[string]any) []string {
	fields := []string{}
	for field, value := range before {
		if !reflect.DeepEqual(value, after[field]) {
			fields = append(fields, field)
		}
	}
	slices.Sort(fields)
	return fields
}

func comparableConfig(config RepresentationConfig) RepresentationConfig {
	if config.Text != nil {
		text := *config.Text
		text.Sections = slices.Clone(text.Sections)
		for i := range text.Sections {
			text.Sections[i].EvidenceIDs = sortedIDs(text.Sections[i].EvidenceIDs)
		}
		config.Text = &text
	}
	if config.Diagram != nil {
		diagram := *config.Diagram
		diagram.Nodes = slices.Clone(diagram.Nodes)
		for i := range diagram.Nodes {
			diagram.Nodes[i].EvidenceIDs = sortedIDs(diagram.Nodes[i].EvidenceIDs)
		}
		config.Diagram = &diagram
	}
	if config.CodeDiff != nil {
		code := *config.CodeDiff
		code.EvidenceIDs = sortedIDs(code.EvidenceIDs)
		config.CodeDiff = &code
	}
	if config.EvidenceList != nil {
		evidence := *config.EvidenceList
		evidence.EvidenceIDs = sortedIDs(evidence.EvidenceIDs)
		config.EvidenceList = &evidence
	}
	return config
}

func (g *ReviewGraph) evidenceSourceFiles(ids []string) []fileKey {
	keys := []fileKey{}
	for _, e := range g.Evidence {
		if e.CodeRange != nil && slices.Contains(ids, e.ID) {
			keys = append(keys, fileKey{e.CodeRange.RepositoryID, e.CodeRange.Path})
		}
	}
	return keys
}

func (g *ReviewGraph) topicSourceCorrections(topic Topic) []any {
	corrections := []any{}
	if g.SourceChanges == nil {
		return corrections
	}
	_, evidenceIDs := g.reachableEvidence(topic)
	referenced := g.evidenceSourceFiles(evidenceIDs)
	for _, repo := range g.SourceChanges.Repositories {
		for _, file := range repo.Files {
			if file.AssignmentMode != "EXPLICIT" {
				continue
			}
			included := slices.Contains(file.TopicIDs, topic.ID)
			if included || slices.Contains(referenced, fileKey{repo.RepositoryID, file.Path}) {
				corrections = append(corrections, struct {
					RepositoryID, Path string
					Included           bool
				}{repo.RepositoryID, file.Path, included})
			}
		}
	}
	return corrections
}

func (g *ReviewGraph) topicSourceFiles(topic Topic) []fileKey {
	_, ids := g.reachableEvidence(topic)
	keys := g.evidenceSourceFiles(ids)
	if g.SourceChanges != nil {
		for _, repo := range g.SourceChanges.Repositories {
			for _, file := range repo.Files {
				if slices.Contains(file.TopicIDs, topic.ID) {
					keys = append(keys, fileKey{repo.RepositoryID, file.Path})
				}
			}
		}
	}
	return keys
}

type storedFileBlobs struct {
	base, head sql.NullString
	binary     bool
	omitted    sql.NullString
}

func (s *Service) fileBlobs(ctx context.Context, revisionID string, file fileKey) (storedFileBlobs, error) {
	var blobs storedFileBlobs
	err := s.reader().QueryRowContext(ctx, `SELECT f.base_blob,f.head_blob,f.binary,f.content_omitted FROM snapshot_files f JOIN repo_snapshots s ON s.id=f.snapshot_id WHERE s.revision_id=? AND s.repository_id=? AND f.path=?`, revisionID, file.repositoryID, file.path).Scan(&blobs.base, &blobs.head, &blobs.binary, &blobs.omitted)
	return blobs, err
}

func (s *Service) compareSourceFiles(ctx context.Context, fromID, toID string, files []fileKey) (string, error) {
	if len(files) == 0 {
		return "NONE", nil
	}
	seen := map[fileKey]bool{}
	status := "UNCHANGED"
	for _, file := range files {
		if seen[file] {
			continue
		}
		seen[file] = true
		before, err := s.fileBlobs(ctx, fromID, file)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
		after, nextErr := s.fileBlobs(ctx, toID, file)
		if nextErr != nil && nextErr != sql.ErrNoRows {
			return "", nextErr
		}
		if err != nil || nextErr != nil || before.binary || after.binary || before.omitted.Valid || after.omitted.Valid {
			status = "UNAVAILABLE"
			continue
		}
		if before.base != after.base || before.head != after.head {
			return "CHANGED", nil
		}
	}
	return status, nil
}

func (s *Service) DiscussionAnchorStatuses(ctx context.Context, reviewID, targetID string) ([]DiscussionAnchorStatus, error) {
	result := []DiscussionAnchorStatus{}
	err := s.readSnapshot(ctx, func(reader *Service) error {
		target, err := reader.revisionView(ctx, reviewID, &targetID)
		if err != nil {
			return err
		}
		discussions, err := reader.ListDiscussions(ctx, reviewID)
		if err != nil {
			return err
		}
		originals := map[string]RevisionView{}
		for _, discussion := range discussions {
			status := DiscussionAnchorStatus{DiscussionID: discussion.ID, RevisionID: targetID, Status: "ORIGINAL", Reason: "Open the original revision to inspect this discussion."}
			switch {
			case discussion.RevisionID == targetID:
				if discussion.Anchor.Type == "REVIEW" || discussion.Anchor.CodeRange != nil {
					break
				}
				if target.Graph == nil {
					status.Status = "UNAVAILABLE"
					status.Reason = "Historical semantic context is unavailable."
				} else if _, exists := anchorValue(target.Graph, discussion.Anchor); !exists {
					status.Status = "UNAVAILABLE"
					status.Reason = "The original semantic target is absent from this revision's recorded state. Its ID is retained; per-edit snapshots were not recorded."
				}
			case discussion.Anchor.CodeRange != nil:
				code := discussion.Anchor.CodeRange
				blobs, err := reader.fileBlobs(ctx, targetID, fileKey{code.RepositoryID, code.Path})
				if err != nil && err != sql.ErrNoRows {
					return err
				}
				hash := blobs.head
				if code.Side == "BASE" {
					hash = blobs.base
				}
				switch {
				case err != nil || !hash.Valid || blobs.binary || blobs.omitted.Valid:
					status.Status = "UNAVAILABLE"
					status.Reason = "This side is not available in the target's stored changed files; that does not prove deletion or resolution."
				case hash.String == code.BlobHash:
					status.Status = "UNCHANGED"
					status.Reason = "Stored content is identical. The original range is retained; the concern is not automatically resolved."
				default:
					status.Status = "OUTDATED"
					status.Reason = "Stored content changed. No line relocation was attempted."
				}
			case discussion.Anchor.Type != "REVIEW":
				original, ok := originals[discussion.RevisionID]
				if !ok {
					original, err = reader.revisionView(ctx, reviewID, &discussion.RevisionID)
					if err != nil {
						return err
					}
					originals[discussion.RevisionID] = original
				}
				if original.Graph == nil || target.Graph == nil {
					status.Status = "UNAVAILABLE"
					status.Reason = "Historical semantic context is unavailable."
					break
				}
				before, existsBefore := anchorValue(original.Graph, discussion.Anchor)
				if !existsBefore {
					status.Status = "UNAVAILABLE"
					status.Reason = "The original semantic target is absent from this revision's recorded state. Its ID is retained; per-edit snapshots were not recorded."
					break
				}
				after, existsAfter := anchorValue(target.Graph, discussion.Anchor)
				if existsAfter && reflect.DeepEqual(before, after) {
					status.Status = "UNCHANGED"
					status.Reason = "The anchored semantic content is unchanged; open its original revision for context."
				} else {
					status.Status = "OUTDATED"
					status.Reason = "The anchored semantic content changed or is absent. The original anchor is retained."
				}
			}
			result = append(result, status)
		}
		return nil
	})
	return result, err
}

func anchorValue(graph *ReviewGraph, anchor Anchor) (any, bool) {
	if anchor.ID == nil {
		return nil, false
	}
	switch anchor.Type {
	case "TOPIC":
		for _, v := range graph.Topics {
			if v.ID == *anchor.ID {
				return topicFields(v, graph), true
			}
		}
	case "FINDING":
		for _, v := range graph.Findings {
			if v.ID == *anchor.ID {
				return findingFields(v), true
			}
		}
	case "INTENT":
		for _, v := range graph.Intents {
			if v.ID == *anchor.ID {
				return v, true
			}
		}
	case "CHANGE":
		for _, v := range graph.Changes {
			if v.ID == *anchor.ID {
				return v, true
			}
		}
	case "EVIDENCE":
		for _, v := range graph.Evidence {
			if v.ID == *anchor.ID {
				return v, true
			}
		}
	case "DIAGRAM_ELEMENT":
		for _, rep := range graph.Representations {
			if rep.ID != nil && *rep.ID == *anchor.ID && rep.Diagram != nil && anchor.ElementID != nil {
				for _, node := range rep.Diagram.Nodes {
					if node.ID == *anchor.ElementID {
						node.EvidenceIDs = sortedIDs(node.EvidenceIDs)
						return node, true
					}
				}
			}
		}
	}
	return nil, false
}
