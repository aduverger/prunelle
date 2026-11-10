import type { API, Model } from './api/client';
import { result } from './api/client';
import { useSubmit } from './hooks';
import { Notice } from './Views';

export function ChangeBadge({ change }: { change?: Model['EntityComparison'] }) {
  if (!change) return null;
  const label = change.change.toLowerCase().replaceAll('_', ' ');
  return (
    <small className="revision-change" title={change.changed_fields.join(', ')}>
      {label}
      {change.changed_fields.length > 0 && ' · ' + change.changed_fields.join(', ')}
      {change.source_status === 'CHANGED' && ' · linked source changed'}
      {change.source_status === 'UNAVAILABLE' && ' · source comparison unavailable'}
    </small>
  );
}

const findingActionLabels = {
  OPEN: 'Reopen finding',
  RESOLVED: 'Resolve finding',
  DISMISSED: 'Dismiss finding',
};

export function FindingActions({
  api,
  finding,
  revisionID,
  editable,
}: {
  api: API;
  finding: Model['Finding'];
  revisionID: string;
  editable: boolean;
}) {
  const action = useSubmit();
  return (
    <div aria-label="Finding status">
      {!editable && <small>Historical finding state is read-only.</small>}
      <div className="row">
        {(['RESOLVED', 'DISMISSED', 'OPEN'] as const)
          .filter((status) => status !== finding.status)
          .map((status) => (
            <button
              key={status}
              disabled={!editable || action.busy}
              onClick={() => {
                void action.submit([revisionID, finding.id, finding.version, status], () =>
                  result(
                    api.PATCH('/v1/findings/{findingId}', {
                      params: {
                        path: { findingId: finding.id },
                        header: { 'If-Match': `"${finding.version}"` },
                      },
                      body: { revision_id: revisionID, status },
                    }),
                  ),
                );
              }}
            >
              {findingActionLabels[status]}
            </button>
          ))}
      </div>
      {action.error && (
        <p className="error" role="alert">
          {action.error}
        </p>
      )}
    </div>
  );
}

export type SourceTopicDraft = {
  reviewID: string;
  revisionID: string;
  repositoryID: string;
  file: Model['SourceFile'];
  topics: Model['Topic'][];
  topicIDs: string[];
  dirty: boolean;
};

export function SourceTopicForm({
  api,
  draft,
  currentRevisionID,
  change,
  saved,
  discard,
}: {
  api: API;
  draft: SourceTopicDraft;
  currentRevisionID: string | null;
  change: (draft: SourceTopicDraft) => void;
  saved: () => void;
  discard: () => void;
}) {
  const action = useSubmit();
  const editable = draft.revisionID === currentRevisionID;
  async function apply(mode: 'EXPLICIT' | 'DERIVED') {
    change({ ...draft, dirty: true });
    const request = {
      repository_id: draft.repositoryID,
      path: draft.file.path,
      mode,
      topic_ids: mode === 'EXPLICIT' ? draft.topicIDs : [],
    };
    if (
      await action.submit([draft.revisionID, draft.file.assignment_version, request], () =>
        result(
          api.PATCH('/v1/revisions/{revisionId}/source-topics', {
            params: {
              path: { revisionId: draft.revisionID },
              header: { 'If-Match': `"${draft.file.assignment_version}"` },
            },
            body: request,
          }),
        ),
      )
    )
      saved();
  }
  function reload() {
    if (
      draft.dirty &&
      !window.confirm('Reload this assignment? Unsaved topic choices will be discarded.')
    )
      return;
    void action.submit(
      ['reload-assignment', draft.revisionID, draft.repositoryID, draft.file.path],
      async () => {
        const view = await result(
          api.GET('/v1/reviews/{reviewId}/revision-view', {
            params: {
              path: { reviewId: draft.reviewID },
              query: { revision_id: draft.revisionID },
            },
          }),
        );
        const file = view.graph?.source_changes?.repositories
          .find((repo) => repo.repository_id === draft.repositoryID)
          ?.files.find((file) => file.path === draft.file.path);
        if (!file || !view.graph)
          throw new Error('Original assignment is unavailable. Your choices are kept.');
        change({
          ...draft,
          file,
          topicIDs: file.topic_ids,
          topics: view.graph.topics.filter((topic) => topic.state === 'ACTIVE'),
          dirty: false,
        });
      },
    );
  }
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void apply('EXPLICIT');
      }}
      aria-label="File topic correction"
    >
      <h3>{draft.file.path}</h3>
      <small>
        Against {draft.revisionID} · {draft.file.assignment_mode.toLowerCase()}
      </small>
      {!editable && (
        <Notice>
          This revision is no longer current. Your original choices are kept; they will not be
          applied to another revision.
        </Notice>
      )}
      <fieldset disabled={action.busy}>
        <legend>Assign to topics (none means deliberately unassigned)</legend>
        {draft.topics.map((topic) => (
          <label className="row" key={topic.id}>
            <input
              type="checkbox"
              checked={draft.topicIDs.includes(topic.id)}
              onChange={(event) =>
                change({
                  ...draft,
                  dirty: true,
                  topicIDs: event.target.checked
                    ? [...draft.topicIDs, topic.id]
                    : draft.topicIDs.filter((id) => id !== topic.id),
                })
              }
            />
            {topic.title}
          </label>
        ))}
      </fieldset>
      {action.error && (
        <p className="error" role="alert">
          {action.error} Your original choices are kept.
        </p>
      )}
      <div className="row">
        <button disabled={!editable || action.busy}>Apply topics</button>
        <button
          type="button"
          disabled={!editable || action.busy}
          onClick={() => void apply('DERIVED')}
        >
          Reset to derived
        </button>
        <button type="button" disabled={action.busy} onClick={reload}>
          Reload assignment
        </button>
        <button type="button" disabled={action.busy} onClick={discard}>
          Discard correction
        </button>
      </div>
    </form>
  );
}
