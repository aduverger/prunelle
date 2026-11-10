package review

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/aduverger/prunelle/server/internal/store"
)

func (s *Service) publishGeneratedReview(ctx context.Context, tx *sql.Tx, invocation AgentInvocation, generated GeneratedReview, actor Actor) (map[string]string, error) {
	if err := requireCurrentRevision(ctx, tx, invocation.ReviewID, &invocation.RevisionID); err != nil {
		return nil, err
	}
	view, err := s.revisionView(ctx, invocation.ReviewID, &invocation.RevisionID)
	if err != nil {
		return nil, err
	}
	if invocationFingerprint(view, invocation) != invocation.InputFingerprint {
		return nil, &Error{Code: CodePreconditionFailed, Message: "review content or grouping changed during generation; inspect it and explicitly request generation again"}
	}
	plan := &importPlan{
		importID: store.NewID("imp"), reviewID: invocation.ReviewID, revisionID: invocation.RevisionID, author: actor.Author,
		ids: map[string]map[string]string{"topics": {}},
	}
	topics := []ManifestTopic{}
	for i, topic := range generated.Topics {
		path := fmt.Sprintf("topics[%d]", i)
		plan.assign("topics", "top", topic.Key, path)
		plan.required(path+".title", topic.Title)
		plan.required(path+".summary", topic.Summary)
		plan.required(path+".explanation", topic.Explanation)
		topics = append(topics, ManifestTopic{Key: topic.Key, Title: topic.Title, Summary: topic.Summary,
			Representations: []ManifestRepresentation{{Type: "TEXT", Text: &ManifestText{Sections: []ManifestSection{{Heading: "Explanation", Body: topic.Explanation}}}}}})
	}
	eligibleVersions := map[fileKey]int{}
	for _, repo := range view.Graph.SourceChanges.Repositories {
		for _, file := range repo.Files {
			if eligibleForGeneration(file) {
				eligibleVersions[fileKey{repo.RepositoryID, file.Path}] = file.AssignmentVersion
			}
		}
	}
	active := map[string]bool{}
	for _, topic := range view.Graph.Topics {
		active[topic.ID] = topic.State == "ACTIVE"
	}
	assignments := []UpdateSourceTopicsRequest{}
	seen := map[fileKey]bool{}
	referenced := map[string]bool{}
	for i, file := range generated.Files {
		path := fmt.Sprintf("files[%d]", i)
		key := fileKey{file.RepositoryID, file.Path}
		if _, eligible := eligibleVersions[key]; !eligible || seen[key] {
			plan.add(path, "must name a distinct eligible unassigned file of the captured revision")
		}
		seen[key] = true
		ids := plan.refs("topics", path+".topic_keys", file.TopicKeys)
		for _, key := range file.TopicKeys {
			referenced[key] = true
		}
		for _, id := range file.TopicIDs {
			if !active[id] {
				plan.add(path+".topic_ids", "%s is not an active canonical topic of this review", id)
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			plan.add(path, "must associate at least one topic; omit unexamined files instead")
		}
		assignments = append(assignments, UpdateSourceTopicsRequest{RepositoryID: file.RepositoryID, Path: file.Path, Mode: "EXPLICIT", TopicIDs: ids})
	}
	if len(assignments) == 0 {
		plan.add("files", "at least one eligible file association is required")
	}
	for _, topic := range generated.Topics {
		if !referenced[topic.Key] {
			plan.add("topics", "new topic %s has no eligible source association", topic.Key)
		}
	}
	if err := plan.problems.err("generated review is invalid"); err != nil {
		return nil, err
	}
	writer := &importWriter{ctx: ctx, tx: tx, plan: plan, now: store.FormatTime(s.now()), origin: "GENERATED"}
	writer.exec(`INSERT INTO imports(id,review_id,revision_id,author_kind,author_id,imported_at) VALUES(?,?,?,?,?,?)`, plan.importID, plan.reviewID, plan.revisionID, actor.Kind, actor.ID, writer.now)
	if err := writer.topics(topics); err != nil {
		return nil, err
	}
	if writer.err != nil {
		return nil, writer.err
	}
	if err := plan.problems.err("generated review is invalid"); err != nil {
		return nil, err
	}
	for _, assignment := range assignments {
		if err := s.writeSourceTopics(ctx, tx, invocation.ReviewID, invocation.RevisionID, eligibleVersions[fileKey{assignment.RepositoryID, assignment.Path}], assignment, actor); err != nil {
			return nil, err
		}
	}
	if err := recordEvent(ctx, tx, "import.created", plan.importID, invocation.ReviewID, actor, s.now()); err != nil {
		return nil, err
	}
	return plan.ids["topics"], nil
}
