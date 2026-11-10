import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  apiFor,
  desktop,
  result,
  serverURL,
  type API,
  type Model,
  type DiagramNode,
} from './api/client';
import { useConnection, useReview, useSourceChanges, useSubmit, type ReviewData } from './hooks';
import {
  attentionFor,
  loadPreferences,
  orderedTopics,
  presets,
  viewNames,
  type DiscussionTarget,
  type FileSelection,
  type Navigation,
  type Perspective,
  type Preferences,
  type TopicOrder,
  type View,
} from './model';
import {
  Attribution,
  ErrorNotice,
  EvidenceCard,
  EvidenceLinks,
  Notice,
  RepresentationView,
} from './Views';
import {
  Decisions,
  CommentIndex,
  ContextDiscussions,
  DiscussionProvider,
  useDiscussionComposer,
} from './Discussions';
import { CodeReview, sourceFileID } from './CodeReview';
import {
  AgentPanel,
  AgentRequestForm,
  useAgentState,
  useCommentDelivery,
  type AgentAction,
  type AgentPreview,
} from './Agents';
import { IntentCard, ChangeCard } from './Records';
import { SourceTree } from './SourceTree';
import {
  ChangeBadge,
  FindingActions,
  SourceTopicForm,
  type SourceTopicDraft,
} from './RevisionTools';

function hasTextDrafts() {
  return Array.from(document.querySelectorAll('textarea')).some((t) => t.value.trim());
}

function leaveDrafts(hiddenDrafts: boolean) {
  return (
    !(hiddenDrafts || hasTextDrafts()) ||
    window.confirm('Leave this review? Unposted drafts will be discarded.')
  );
}

function hasComparisonDetail(change: Model['EntityComparison']) {
  return (
    change.change !== 'UNCHANGED' ||
    change.source_status === 'CHANGED' ||
    change.source_status === 'UNAVAILABLE'
  );
}

export function App() {
  const [preferences, setPreferences] = useState(loadPreferences);
  const [address, setAddress] = useState(preferences.server);
  const [addressError, setAddressError] = useState('');
  const [reviewID, setReviewID] = useState('');
  const hiddenDrafts = useRef(false);
  const reportDrafts = useCallback((dirty: boolean) => {
    hiddenDrafts.current = dirty;
  }, []);
  const cache = useQueryClient();
  const api = useMemo(() => apiFor(preferences.server), [preferences.server]);
  const status = useConnection(api);
  const reviews = useQuery({
    queryKey: ['reviews'],
    queryFn: ({ signal }) => result(api.GET('/v1/reviews', { signal })),
  });
  useEffect(() => {
    try {
      localStorage.setItem('prunelle.preferences', JSON.stringify(preferences));
    } catch {
      /* Local preferences are optional. */
    }
  }, [preferences]);
  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (hiddenDrafts.current || hasTextDrafts()) e.preventDefault();
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, []);
  function connect() {
    try {
      const server = serverURL(address.trim());
      if (!leaveDrafts(hiddenDrafts.current)) return;
      void cache.cancelQueries();
      cache.clear();
      setReviewID('');
      setPreferences((p) => ({ ...p, server }));
      setAddressError('');
    } catch (e) {
      setAddressError((e as Error).message);
    }
  }
  const selectedID = reviewID || reviews.data?.[0]?.id;
  if (!reviewID && selectedID) setReviewID(selectedID);
  const selectedMissing =
    !!selectedID && !!reviews.data && !reviews.data.some((r) => r.id === selectedID);
  return (
    <div className="app">
      <header className="app-bar">
        <div className="brand">
          <span className="brand-icon">p</span>
          <strong>prunelle</strong>
          <span className="muted">/</span>
        </div>
        <select
          aria-label="Feature review"
          value={selectedID || ''}
          onChange={(e) => {
            if (leaveDrafts(hiddenDrafts.current)) setReviewID(e.target.value);
          }}
        >
          {selectedMissing && (
            <option value={selectedID} disabled>
              Review unavailable · {selectedID}
            </option>
          )}
          {reviews.data?.map((r) => (
            <option key={r.id} value={r.id}>
              {r.title}
            </option>
          ))}
        </select>
        <div className="spacer" />
        <span className={'connection ' + (status === 'Connected' ? 'online' : '')} role="status">
          ● {status}
        </span>
        <button
          className="quiet"
          onClick={() => {
            void cache.invalidateQueries();
          }}
        >
          Refresh
        </button>
        {desktop && (
          <details className="server-settings">
            <summary>Server</summary>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                connect();
              }}
            >
              <label>
                Server URL
                <input value={address} onChange={(e) => setAddress(e.target.value)} />
              </label>
              <button>Connect</button>
              {addressError && (
                <p className="error" role="alert">
                  {addressError}
                </p>
              )}
            </form>
          </details>
        )}
      </header>
      <ErrorNotice error={reviews.error} />
      {selectedMissing && (
        <Notice>
          Selected review was removed or is unavailable. Your unposted drafts remain here until you
          leave. Copy any text you want to keep before switching reviews or closing.
        </Notice>
      )}
      {selectedID ? (
        <ReviewLoader
          key={preferences.server + selectedID}
          api={api}
          reviewID={selectedID}
          preferences={preferences}
          setPreferences={setPreferences}
          onDraftChange={reportDrafts}
        />
      ) : (
        <div className="welcome">
          <h1>Review the feature, not just the diff.</h1>
          <Notice>
            {reviews.isPending
              ? 'Connecting to the review server…'
              : 'No reviews yet. From a source folder on the server, run prunelle review create, then prunelle review capture.'}
          </Notice>
          {!desktop && (
            <p>
              Browser development mode · target configured with PRUNELLE_SERVER on the Vite process.
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function ReviewLoader(props: {
  onDraftChange: (dirty: boolean) => void;
  api: API;
  reviewID: string;
  preferences: Preferences;
  setPreferences: React.Dispatch<React.SetStateAction<Preferences>>;
}) {
  const query = useReview(props.api, props.reviewID);
  const placeholderMessage = query.error ? 'Review unavailable.' : 'Loading review…';
  return (
    <>
      <ErrorNotice error={query.error} />
      {query.data ? (
        <ReviewPage {...props} data={query.data} />
      ) : (
        <Notice>{placeholderMessage}</Notice>
      )}
    </>
  );
}

type Inspector =
  | { type: 'evidence'; id: string }
  | { type: 'finding'; id: string }
  | { type: 'INTENT' | 'CHANGE'; id: string }
  | { type: 'node'; node: DiagramNode; revisionID: string | null }
  | null;
function ReviewPage({
  api,
  data: reviewData,
  preferences: pref,
  setPreferences,
  onDraftChange,
}: {
  onDraftChange: (dirty: boolean) => void;
  api: API;
  data: ReviewData;
  preferences: Preferences;
  setPreferences: React.Dispatch<React.SetStateAction<Preferences>>;
}) {
  const [revisionID, setRevisionID] = useState<string | null>(null);
  const effectiveRevisionID = revisionID ?? reviewData.review.current_revision_id;
  const revision = reviewData.revisions.find((r) => r.id === effectiveRevisionID);
  const view = useQuery({
    queryKey: ['revision-view', reviewData.review.id, effectiveRevisionID],
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/reviews/{reviewId}/revision-view', {
          params: {
            path: { reviewId: reviewData.review.id },
            query: { revision_id: effectiveRevisionID || undefined },
          },
          signal,
        }),
      ),
  });
  const graph = view.data?.graph;
  const data = {
    ...reviewData,
    topics: graph?.topics || [],
    findings: graph?.findings || [],
    intents: graph?.intents || [],
    changes: graph?.changes || [],
    evidence: graph?.evidence || [],
    attention: graph?.attention || [],
  };
  const [topicSelection, setTopicID] = useState<string | null | undefined>(undefined);
  const initialTopicID = pref.perspective === 'Source' ? null : data.topics[0]?.id || null;
  if (topicSelection === undefined && graph) setTopicID(initialTopicID);
  const topicID = topicSelection === undefined ? initialTopicID : topicSelection;
  const [baselineChoice, setBaselineChoice] = useState<string | undefined>(undefined);
  const earlierRevisions = data.revisions.filter((r) => revision && r.ordinal < revision.ordinal);
  const baselineID =
    baselineChoice === undefined
      ? earlierRevisions.at(-1)?.id
      : earlierRevisions.find((r) => r.id === baselineChoice)?.id;
  const comparison = useQuery({
    queryKey: ['comparison', data.review.id, baselineID, revision?.id],
    enabled: !!baselineID && !!revision,
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/reviews/{reviewId}/comparison', {
          params: {
            path: { reviewId: data.review.id },
            query: { from_revision_id: baselineID!, to_revision_id: revision!.id },
          },
          signal,
        }),
      ),
  });
  const anchorStatuses = useQuery({
    queryKey: ['anchor-status', data.review.id, effectiveRevisionID],
    enabled: !!effectiveRevisionID,
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/reviews/{reviewId}/anchor-status', {
          params: {
            path: { reviewId: data.review.id },
            query: { revision_id: effectiveRevisionID! },
          },
          signal,
        }),
      ),
  });
  const [sourceDraft, setSourceDraft] = useState<SourceTopicDraft | null>(null);
  const [discussionDirty, setDiscussionDirty] = useState(false);
  const [decisionDirty, setDecisionDirty] = useState(false);
  const [agentDirty, setAgentDirty] = useState(false);
  useEffect(() => () => onDraftChange(false), [onDraftChange]);
  const [tab, setTab] = useState<Navigation>(presets[pref.perspective].tab);
  const [order, setOrder] = useState(presets[pref.perspective].order);
  const [file, setFile] = useState<FileSelection | null>(null);
  const [inspector, setInspector] = useState<Inspector>(null);
  if (inspector?.type === 'node' && inspector.revisionID !== effectiveRevisionID)
    setInspector(null);
  const [panel, setPanel] = useState<
    'decision' | 'agents' | 'agent-request' | 'source-topics' | null
  >(null);
  const [agentPreview, setAgentPreview] = useState<AgentPreview | null>(null);
  const agents = useAgentState(api, data.review.id, panel === 'agents');
  const commentDelivery = useCommentDelivery(api, data.review.id, agents);
  useEffect(() => {
    onDraftChange(
      discussionDirty ||
        decisionDirty ||
        agentDirty ||
        commentDelivery.dirty ||
        !!sourceDraft?.dirty,
    );
  }, [
    discussionDirty,
    decisionDirty,
    agentDirty,
    commentDelivery.dirty,
    sourceDraft?.dirty,
    onDraftChange,
  ]);
  const attachedPi = agents.data?.sessions.find(
    (session) => session.id === agents.data?.binding.agent_session_id,
  );
  let attachmentLabel = 'Pi · Unattached';
  if (agents.isPending) attachmentLabel = 'Pi · Loading';
  else if (attachedPi)
    attachmentLabel = `Pi ${attachedPi.external_id.slice(0, 12)} · ${attachedPi.state}${attachedPi.queue_paused ? ' · Queue paused' : ''}`;
  function requestAgent(action: AgentAction) {
    let requestedRevisionID = effectiveRevisionID;
    let savedInput: string | string[] | null = null;
    switch (action.intent) {
      case 'DISCUSS':
        requestedRevisionID = action.discussion.revision_id;
        savedInput = [action.discussion.id, action.message.id];
        break;
      case 'IMPLEMENT':
        requestedRevisionID = action.decision.revision_id;
        savedInput = action.decision.id;
        break;
      case 'PREPARE_REVIEW':
        break;
    }
    if (!requestedRevisionID) return;
    const key = JSON.stringify([action.intent, requestedRevisionID, savedInput]);
    if (agentPreview?.key !== key) {
      if (
        agentDirty &&
        !window.confirm(
          'Discard the unresolved Pi request preview? Inspect existing requests before starting another.',
        )
      )
        return;
      setAgentPreview({
        key,
        action,
        revisionID: requestedRevisionID,
        agentSessionID: agents.data?.binding.agent_session_id || null,
      });
    }
    setPanel('agent-request');
  }
  const [navigation, setNavigation] = useState(0);
  const [focusedDiscussionID, setFocusedDiscussionID] = useState<string | null>(null);
  const [discussionNavigation, setDiscussionNavigation] = useState(0);
  const composer = useDiscussionComposer(api, data.review.id, commentDelivery.post);
  const [representationIDs, setRepresentationIDs] = useState<Partial<Record<View, string>>>({});
  const [elementID, setElementID] = useState<string | null>(null);
  const topic = data.topics.find((t) => t.id === topicID);
  const reps = (graph?.representations || []).filter((rep) => rep.topic_id === topicID);
  const source = useSourceChanges(api, effectiveRevisionID || undefined);
  const sourceMode = !topic;
  const codeRevision = data.revisions.find((r) => r.id === file?.revisionID) || revision;
  const codeSource = useSourceChanges(api, sourceMode ? codeRevision?.id : undefined);
  const sourceFiles = useMemo(
    () =>
      codeSource.data?.repositories.flatMap((repository) =>
        repository.files.map((file) => ({
          revisionID: codeSource.data!.revision_id,
          repositoryID: repository.repository_id,
          path: file.path,
        })),
      ) || [],
    [codeSource.data],
  );
  const capture = useSubmit();
  function openFile(next: FileSelection) {
    setFile(next);
    setNavigation((value) => value + 1);
  }
  function selectTopic(id: string) {
    setFocusedDiscussionID(null);
    setTopicID(id);
    setFile(null);
    setInspector(null);
    setElementID(null);
  }
  function showCode() {
    setPreferences((p) => {
      if (p.views.slice(0, p.split ? 2 : 1).includes('CODE_DIFF')) return p;
      if (p.split) return { ...p, views: [p.views[0], 'CODE_DIFF'] };
      return { ...p, views: ['CODE_DIFF', p.views[1]] };
    });
  }
  function openEvidence(e: Model['Evidence']) {
    setInspector({ type: 'evidence', id: e.id });
    if (e.code_range) {
      openFile({
        revisionID: e.revision_id,
        repositoryID: e.code_range.repository_id,
        path: e.code_range.path,
        range: e.code_range,
      });
      showCode();
    }
  }
  function inspectNode(node: DiagramNode) {
    if (node.ref?.type === 'TOPIC') selectTopic(node.ref.id);
    setElementID(node.id);
    setInspector({ type: 'node', node, revisionID: effectiveRevisionID });
    const e = data.evidence.find(
      (e) =>
        node.evidence_ids.includes(e.id) || (node.ref?.type === 'EVIDENCE' && e.id === node.ref.id),
    );
    if (e) openEvidence(e);
    if (node.ref?.type === 'FINDING') setInspector({ type: 'finding', id: node.ref.id });
    if (node.ref?.type === 'INTENT' || node.ref?.type === 'CHANGE')
      setInspector({ type: node.ref.type, id: node.ref.id });
  }
  const discuss = composer.start;
  function openAnchor(d: {
    id?: string;
    anchor: DiscussionTarget['anchor'] | Model['Anchor'];
    revision_id: string;
    topic_ids?: string[];
  }) {
    setRevisionID(d.revision_id);
    if (d.anchor.code_range) {
      const r = d.anchor.code_range;
      setTopicID(null);
      setInspector(null);
      openFile({
        revisionID: d.revision_id,
        repositoryID: r.repository_id,
        path: r.path,
        range: r,
      });
      showCode();
    } else if (d.anchor.type === 'TOPIC' && d.anchor.id) selectTopic(d.anchor.id);
    else if (d.anchor.type === 'DIAGRAM_ELEMENT' && d.anchor.id && d.topic_ids?.[0]) {
      selectTopic(d.topic_ids[0]);
      setRepresentationIDs((ids) => ({ ...ids, DIAGRAM: d.anchor.id! }));
      setElementID(d.anchor.element_id || null);
      setView(0, 'DIAGRAM');
    } else if (d.anchor.type === 'FINDING' && d.anchor.id)
      setInspector({ type: 'finding', id: d.anchor.id });
    else if ((d.anchor.type === 'INTENT' || d.anchor.type === 'CHANGE') && d.anchor.id)
      setInspector({ type: d.anchor.type, id: d.anchor.id });
    else if (d.anchor.type === 'EVIDENCE' && d.anchor.id)
      setInspector({ type: 'evidence', id: d.anchor.id });
    if (d.anchor.type !== 'CODE_RANGE') {
      setFocusedDiscussionID(d.id || null);
      setDiscussionNavigation((value) => value + 1);
    }
  }
  function openDraft() {
    composer.requestFocus();
    if (composer.target)
      openAnchor({
        anchor: composer.target.anchor,
        revision_id: composer.target.revisionID,
        topic_ids: composer.target.topicID ? [composer.target.topicID] : undefined,
      });
  }
  function perspective(p: Perspective) {
    setTopicID(p === 'Source' ? null : topicID || data.topics[0]?.id || null);
    setPreferences((old) => ({ ...old, perspective: p, views: presets[p].views }));
    setTab(presets[p].tab);
    setOrder(presets[p].order);
  }
  function setView(index: number, type: View) {
    setPreferences((p) => ({ ...p, views: index === 0 ? [type, p.views[1]] : [p.views[0], type] }));
  }
  const displayedSource = sourceMode ? codeSource.data : source.data;
  const sourceFile =
    displayedSource?.revision_id === file?.revisionID
      ? displayedSource?.repositories
          .find((r) => r.repository_id === file?.repositoryID)
          ?.files.find((f) => f.path === file?.path)
      : null;
  const finding =
    inspector?.type === 'finding' ? data.findings.find((f) => f.id === inspector.id) : null;
  const evidence =
    inspector?.type === 'evidence' ? data.evidence.find((e) => e.id === inspector.id) : null;
  const intent =
    inspector?.type === 'INTENT' ? data.intents.find((i) => i.id === inspector.id) : null;
  const change =
    inspector?.type === 'CHANGE' ? data.changes.find((c) => c.id === inspector.id) : null;
  let sourceTopicSummary: React.ReactNode = null;
  if (sourceFile) {
    if (!displayedSource?.assignments_available) {
      sourceTopicSummary = <span className="badge">Topic history unavailable</span>;
    } else if (sourceFile.unassigned) {
      sourceTopicSummary = <span className="badge">Unassigned Changes</span>;
    } else {
      sourceTopicSummary = sourceFile.topic_ids.map((id) => (
        <button
          key={id}
          className="link"
          onClick={() => {
            setRevisionID(displayedSource.revision_id);
            selectTopic(id);
          }}
        >
          {data.topics.find((topic) => topic.id === id)?.title || id}
        </button>
      ));
    }
  }
  const sourceMembership = sourceFile && (
    <div className="row source-topics">
      <small>File belongs to:</small>
      {topic &&
        sourceFile &&
        displayedSource?.assignments_available &&
        !sourceFile.topic_ids.includes(topic.id) && (
          <small>Selected stored source is not currently assigned to this topic.</small>
        )}
      {sourceTopicSummary}
      {displayedSource?.assignments_available && (
        <button
          disabled={!view.data?.editable || file?.revisionID !== data.review.current_revision_id}
          onClick={() => {
            if (sourceDraft?.dirty) {
              if (
                sourceDraft.revisionID === file!.revisionID &&
                sourceDraft.repositoryID === file!.repositoryID &&
                sourceDraft.file.path === file!.path
              ) {
                setPanel('source-topics');
                return;
              }
              if (!window.confirm('Discard the unsaved file-topic correction and start another?'))
                return;
            }
            setSourceDraft({
              reviewID: data.review.id,
              revisionID: file!.revisionID,
              repositoryID: file!.repositoryID,
              file: sourceFile,
              topics: data.topics.filter((topic) => topic.state === 'ACTIVE'),
              topicIDs: sourceFile.topic_ids,
              dirty: false,
            });
            setPanel('source-topics');
          }}
        >
          Edit file topics
        </button>
      )}
    </div>
  );
  const reviewContext = (
    <>
      <ErrorNotice error={view.error} />
      <ErrorNotice error={anchorStatuses.error} />
      {view.isPending && <Notice>Loading semantic view…</Notice>}
      {view.data?.unavailable_reason && <Notice>{view.data.unavailable_reason}</Notice>}
      {graph && topicID && !topic && (
        <Notice>
          The selected topic is not present in this revision. Choose a topic or inspect all source
          changes.
        </Notice>
      )}
      {!revision && !effectiveRevisionID && (
        <Notice>No source snapshot yet. Capture one to comment or decide.</Notice>
      )}
      {revision && (
        <details className="snapshot">
          <summary>
            Revision {revision.ordinal} · {new Date(revision.captured_at).toLocaleString()} ·{' '}
            {revision.snapshots.length} repositories · {revision.status}
          </summary>
          {revision.snapshots.map((s) => (
            <div key={s.repository_id}>
              <strong>{s.repository_label}</strong> · {s.branch} · {s.base_ref} ·{' '}
              {s.base_sha.slice(0, 8)} → {s.head_sha.slice(0, 8)} · {s.file_count} files
              {s.warnings.map((w, i) => (
                <p className="error" key={i}>
                  {w}
                </p>
              ))}
            </div>
          ))}
          <p>
            {view.data?.graph &&
              (view.data.editable && effectiveRevisionID === data.review.current_revision_id
                ? 'Latest semantic state is editable; the next capture freezes it.'
                : 'Frozen semantic state for this revision. Source and original anchors are immutable.')}
          </p>
        </details>
      )}
      <ErrorNotice error={comparison.error} />
      {comparison.data?.unavailable_reason && <Notice>{comparison.data.unavailable_reason}</Notice>}
      {comparison.data && !comparison.data.unavailable_reason && (
        <details aria-label="Revision comparison">
          <summary>
            Changes since revision {earlierRevisions.find((r) => r.id === baselineID)?.ordinal}
          </summary>
          <p>
            Metadata changes and linked-source changes are separate. Neither automatically resolves
            a concern.
          </p>
          <ul>
            {comparison.data.topics.filter(hasComparisonDetail).map((item) => (
              <li key={item.id}>
                <button className="link" onClick={() => selectTopic(item.id)}>
                  {data.topics.find((topic) => topic.id === item.id)?.title || item.id}
                </button>{' '}
                <ChangeBadge change={item} />
              </li>
            ))}
            {comparison.data.findings.filter(hasComparisonDetail).map((item) => (
              <li key={item.id}>
                <button
                  className="link"
                  onClick={() => setInspector({ type: 'finding', id: item.id })}
                >
                  {data.findings.find((finding) => finding.id === item.id)?.title || item.id}
                </button>{' '}
                <ChangeBadge change={item} />
              </li>
            ))}
          </ul>
        </details>
      )}
      <div className="topic-header">
        <div className="eyebrow">{topic ? 'REVIEW TOPIC · ' + topic.state : 'SOURCE EXPLORER'}</div>
        <h2>{topic?.title || 'All source changes'}</h2>
        <p>{topic?.summary || 'Every changed file, including changes not assigned to a topic.'}</p>
        {topic && (
          <>
            <Attribution author={topic.author} />
            <ChangeBadge change={comparison.data?.topics.find((item) => item.id === topic.id)} />
            <button
              className="link"
              disabled={!revision}
              onClick={() =>
                discuss({
                  revisionID: revision!.id,
                  anchor: { type: 'TOPIC', id: topic.id },
                  label: topic.title,
                })
              }
            >
              Discuss topic
            </button>
          </>
        )}
      </div>
      {topic && (
        <details className="context" open={pref.perspective === 'Design'}>
          <summary>Intent, observed changes & attention</summary>
          {data.intents
            .filter((i) => topic.intent_ids.includes(i.id))
            .map((i) => (
              <IntentCard
                key={i.id}
                intent={i}
                revisionID={revision?.id}
                evidence={data.evidence}
                open={openEvidence}
                discuss={discuss}
              />
            ))}
          {data.changes
            .filter((c) => topic.change_ids.includes(c.id))
            .map((c) => (
              <ChangeCard
                key={c.id}
                change={c}
                evidence={data.evidence}
                open={openEvidence}
                discuss={discuss}
              />
            ))}
          {attentionFor(topic, data.attention).map((a) => (
            <article className="card attention" key={a.id}>
              <div className="eyebrow">ATTENTION · {a.priority} · NOT A DEFECT PROBABILITY</div>
              <p>{a.rationale}</p>
              <p>Unknown: {a.uncertainty || 'Not stated'}</p>
              <Attribution author={a.author} provenance={a.provenance} />
              <EvidenceLinks ids={a.evidence_ids} evidence={data.evidence} select={openEvidence} />
            </article>
          ))}
          {!topic.intent_ids.length &&
            !topic.change_ids.length &&
            !attentionFor(topic, data.attention).length && (
              <Notice>No intent, observed changes or attention signals linked.</Notice>
            )}
        </details>
      )}
      {topic && (
        <div className="topic-findings">
          {data.findings
            .filter((f) => topic.finding_ids.includes(f.id))
            .map((f) => (
              <button key={f.id} onClick={() => setInspector({ type: 'finding', id: f.id })}>
                <span className={'severity ' + f.severity.toLowerCase()}>{f.severity}</span>
                {f.title}
              </button>
            ))}
        </div>
      )}
    </>
  );
  const code = (files: FileSelection[], header?: React.ReactNode) => (
    <CodeReview
      api={api}
      files={files}
      file={file}
      choose={setFile}
      navigation={navigation}
      repositories={codeRevision?.snapshots || []}
      header={header}
      loading={sourceMode && !!codeRevision && codeSource.isPending}
      error={sourceMode ? codeSource.error || source.error : null}
      openDraft={openDraft}
    />
  );
  const decisionRevision =
    revision ||
    data.revisions.find((r) => r.id === data.review.current_revision_id) ||
    data.revisions.at(-1);
  const codeVisible =
    sourceMode || (pref.views.slice(0, pref.split ? 2 : 1).includes('CODE_DIFF') && !!file);
  return (
    <DiscussionProvider
      api={api}
      discussions={data.discussions}
      revisions={data.revisions}
      composer={composer}
      openAnchor={openAnchor}
      onDraftChange={setDiscussionDirty}
      anchorStatuses={anchorStatuses.data || []}
      requestAgent={requestAgent}
      delivery={commentDelivery}
    >
      <div className="review-bar">
        <div>
          <div className="eyebrow">FEATURE REVIEW · {data.review.status}</div>
          <h1>{data.review.title}</h1>
          <small>
            {data.review.repositories.map((repository) => repository.label).join(' · ')}
          </small>
        </div>
        <div className="spacer" />
        <label className="compact">
          Perspective
          <select
            aria-label="Perspective"
            value={pref.perspective}
            onChange={(e) => perspective(e.target.value as Perspective)}
          >
            {Object.keys(presets).map((p) => (
              <option key={p}>{p}</option>
            ))}
          </select>
        </label>
        <label className="compact">
          Snapshot
          <select
            aria-label="Revision"
            value={revision?.id || ''}
            onChange={(e) => {
              setRevisionID(e.target.value);
              setFile(null);
            }}
          >
            {data.revisions.map((r) => (
              <option key={r.id} value={r.id}>
                Revision {r.ordinal}
                {r.id === data.review.current_revision_id ? ' · latest' : ''}
              </option>
            ))}
          </select>
        </label>
        {earlierRevisions.length > 0 && (
          <label className="compact">
            Compare with
            <select
              aria-label="Comparison baseline"
              value={baselineID || ''}
              onChange={(event) => setBaselineChoice(event.target.value)}
            >
              <option value="">No comparison</option>
              {earlierRevisions.map((r) => (
                <option key={r.id} value={r.id}>
                  Revision {r.ordinal}
                </option>
              ))}
            </select>
          </label>
        )}
        <span
          className="badge"
          title={attachedPi?.external_id || 'Choose a Pi conversation in Settings'}
        >
          {attachmentLabel}
        </span>
        {agentPreview && (
          <button onClick={() => setPanel('agent-request')}>Resume Pi request</button>
        )}
        <button
          disabled={!view.data?.editable || !revision}
          onClick={() => requestAgent({ intent: 'PREPARE_REVIEW' })}
        >
          Prepare review
        </button>
        {sourceDraft && (
          <button onClick={() => setPanel('source-topics')}>Resume file-topic correction</button>
        )}
        <button
          disabled={capture.busy}
          onClick={() => {
            void capture.submit([data.review.id, 'capture'], async (key) => {
              const r = await result(
                api.POST('/v1/reviews/{reviewId}/revisions', {
                  params: {
                    path: { reviewId: data.review.id },
                    header: { 'Idempotency-Key': key },
                  },
                }),
              );
              setRevisionID(r.id);
              setFile(null);
            });
          }}
        >
          {capture.busy ? 'Capturing…' : 'Capture'}
        </button>
        <button className="primary" disabled={!revision} onClick={() => setPanel('decision')}>
          Review decision
        </button>
      </div>
      {capture.error && (
        <p role="alert" className="error">
          {capture.error}
        </p>
      )}
      <div className="workspace">
        <aside className="navigation">
          <nav className="nav-tabs" aria-label="Review navigation">
            {(['Topics', 'Findings', 'All Source Changes', 'Comments'] as Navigation[]).map((t) => (
              <button
                key={t}
                aria-pressed={tab === t}
                onClick={() => {
                  setTab(t);
                  if (t === 'All Source Changes') setTopicID(null);
                }}
              >
                {t === 'All Source Changes' ? 'Files' : t}
              </button>
            ))}
          </nav>
          <div className="navigation-content">
            {tab === 'Topics' && (
              <>
                <div className="nav-heading">
                  <span className="eyebrow">FEATURE MAP</span>
                  <select
                    aria-label="Topic order"
                    value={order}
                    onChange={(e) => setOrder(e.target.value as typeof order)}
                  >
                    <option value="manual">Manual order</option>
                    <option value="attention">Attention first</option>
                    <option value="intent">Intent first</option>
                  </select>
                </div>
                <TopicTree
                  topics={data.topics}
                  attention={data.attention}
                  selected={topicID}
                  select={selectTopic}
                  order={order}
                  changes={comparison.data?.topics || []}
                />
                {!data.topics.length && view.data?.graph && (
                  <Notice>
                    No topics yet. Prepare a review with Pi or import a manifest. All Source Changes
                    remains exhaustive.
                  </Notice>
                )}
              </>
            )}
            {tab === 'Findings' && (
              <div className="nav-list">
                <div className="eyebrow">{data.findings.length} FINDINGS</div>
                {data.findings.map((f) => (
                  <button
                    key={f.id}
                    aria-pressed={finding?.id === f.id}
                    onClick={() => {
                      setInspector({ type: 'finding', id: f.id });
                    }}
                  >
                    <span className={'severity ' + f.severity.toLowerCase()}>{f.severity}</span>
                    <strong>{f.title}</strong>
                    <small>
                      {f.kind} · {f.status}
                    </small>
                    <ChangeBadge
                      change={comparison.data?.findings.find((item) => item.id === f.id)}
                    />
                  </button>
                ))}
                {!data.findings.length && graph && <Notice>No findings recorded.</Notice>}
                {!graph && (
                  <Notice>
                    {view.isPending
                      ? 'Loading findings…'
                      : view.data?.unavailable_reason || 'Findings could not be loaded.'}
                  </Notice>
                )}
              </div>
            )}
            <div hidden={tab !== 'All Source Changes'}>
              {source.data ? (
                <SourceTree
                  key={source.data.revision_id}
                  source={source.data}
                  file={file}
                  choose={(f) => {
                    setTopicID(null);
                    setInspector(null);
                    openFile(f);
                    showCode();
                  }}
                />
              ) : (
                <Notice>
                  {revision ? 'Loading source…' : 'Capture a revision to inspect source.'}
                </Notice>
              )}
            </div>
            <div hidden={tab !== 'Comments'}>
              <CommentIndex
                topicID={topicID}
                revisionID={revision?.id}
                openAnchor={openAnchor}
                openDraft={openDraft}
              />
            </div>
          </div>
          <footer>
            <button className="quiet" onClick={() => setPanel('agents')}>
              Settings
            </button>
            <small>Review works without an agent.</small>
          </footer>
        </aside>
        <main className={'review-content ' + (sourceMode ? 'source-mode' : '')}>
          {sourceMode ? (
            code(
              file && !sourceFiles.some((source) => sourceFileID(source) === sourceFileID(file))
                ? [...sourceFiles, file]
                : sourceFiles,
              <>
                {reviewContext}
                <p className="muted">Code snapshot: revision {codeRevision?.ordinal || '—'}</p>
                {sourceMembership}
                {codeSource.error && !codeSource.data && (
                  <Notice>
                    Source listing unavailable. Showing the selected stored file only.
                  </Notice>
                )}
                <ContextDiscussions
                  topicID={null}
                  focusedID={focusedDiscussionID}
                  navigation={discussionNavigation}
                />
              </>,
            )
          ) : (
            <>
              {reviewContext}
              <div className="layout-bar">
                <span className="eyebrow">VIEWS OF THE SAME TOPIC</span>
                <div className="spacer" />
                <label>
                  <input
                    type="checkbox"
                    checked={pref.split}
                    onChange={(e) => setPreferences((p) => ({ ...p, split: e.target.checked }))}
                  />
                  Two views
                </label>
              </div>
              {sourceMembership}
              <div className={'view-grid ' + (pref.split ? 'split' : '')}>
                {pref.views.slice(0, pref.split ? 2 : 1).map((v, i) => (
                  <section className="view-block" key={i}>
                    <nav
                      className="view-tabs"
                      aria-label={i === 0 ? 'Primary view' : 'Secondary view'}
                    >
                      {(Object.keys(viewNames) as View[]).map((type) => (
                        <button
                          key={type}
                          aria-pressed={v === type}
                          onClick={() => setView(i, type)}
                        >
                          {viewNames[type]}
                        </button>
                      ))}
                    </nav>
                    <RepresentationView
                      type={v}
                      representationID={representationIDs[v]}
                      chooseRepresentation={(id) =>
                        setRepresentationIDs((ids) => ({ ...ids, [v]: id }))
                      }
                      elementID={elementID}
                      representations={reps}
                      revisionID={revision?.id}
                      openSource={(source) => {
                        openFile({
                          revisionID: source.revision_id,
                          repositoryID: source.repository_id,
                          path: source.path,
                        });
                        showCode();
                      }}
                      evidence={data.evidence}
                      code={file && v === 'CODE_DIFF' ? code([file]) : undefined}
                      openEvidence={openEvidence}
                      selectNode={inspectNode}
                      discuss={discuss}
                    />
                  </section>
                ))}
              </div>
              <ContextDiscussions
                topicID={topicID}
                includeCode={!codeVisible}
                focusedID={focusedDiscussionID}
                navigation={discussionNavigation}
              />
            </>
          )}
          {inspector && (
            <section className="inspector" aria-label="Selected evidence or finding">
              <div className="section-heading">
                <h2>Grounding</h2>
                <button onClick={() => setInspector(null)}>Close</button>
              </div>
              {evidence && (
                <EvidenceCard evidence={evidence} open={openEvidence} discuss={discuss} />
              )}
              {intent && (
                <IntentCard
                  intent={intent}
                  revisionID={revision?.id}
                  evidence={data.evidence}
                  open={openEvidence}
                  discuss={discuss}
                />
              )}
              {change && (
                <ChangeCard
                  change={change}
                  evidence={data.evidence}
                  open={openEvidence}
                  discuss={discuss}
                />
              )}
              {finding && (
                <article className="card">
                  <div className="eyebrow">
                    FINDING · {finding.kind} · {finding.status}
                  </div>
                  <h3>{finding.title}</h3>
                  <Attribution author={finding.author} provenance={finding.provenance} />
                  <p>{finding.rationale}</p>
                  <ChangeBadge
                    change={comparison.data?.findings.find((item) => item.id === finding.id)}
                  />
                  {revision && (
                    <FindingActions
                      key={finding.id + revision.id}
                      api={api}
                      finding={finding}
                      revisionID={revision.id}
                      editable={
                        !!view.data?.editable && revision.id === data.review.current_revision_id
                      }
                    />
                  )}
                  <EvidenceLinks
                    ids={finding.evidence_ids}
                    evidence={data.evidence}
                    select={openEvidence}
                  />
                  <div className="row">
                    {data.topics
                      .filter((t) => t.finding_ids.includes(finding.id))
                      .map((t) => (
                        <button key={t.id} onClick={() => selectTopic(t.id)}>
                          Topic: {t.title}
                        </button>
                      ))}
                    <button
                      disabled={!revision}
                      onClick={() =>
                        discuss({
                          revisionID: revision!.id,
                          anchor: { type: 'FINDING', id: finding.id },
                          label: finding.title,
                        })
                      }
                    >
                      Discuss finding
                    </button>
                  </div>
                </article>
              )}
              {inspector.type === 'node' && (
                <article className="card">
                  <h3>{inspector.node.label}</h3>
                  <EvidenceLinks
                    ids={inspector.node.evidence_ids}
                    evidence={data.evidence}
                    select={openEvidence}
                  />
                  {!inspector.node.evidence_ids.length && (
                    <Notice>No supporting evidence is linked to this node.</Notice>
                  )}
                </article>
              )}
            </section>
          )}
        </main>
      </div>
      <ReviewDialog
        open={panel === 'decision'}
        title="Review decision"
        close={() => setPanel(null)}
      >
        {decisionRevision && (
          <Decisions
            api={api}
            reviewID={data.review.id}
            revision={decisionRevision}
            findings={data.findings}
            onDraftChange={setDecisionDirty}
            discussions={data.discussions}
            decisions={data.decisions}
          />
        )}
      </ReviewDialog>
      <ReviewDialog
        open={panel === 'source-topics'}
        title="File topics"
        close={() => setPanel(null)}
      >
        {sourceDraft && (
          <SourceTopicForm
            key={sourceDraft.revisionID + sourceDraft.repositoryID + sourceDraft.file.path}
            api={api}
            draft={sourceDraft}
            currentRevisionID={data.review.current_revision_id}
            change={setSourceDraft}
            saved={() => {
              setSourceDraft(null);
              setPanel(null);
            }}
            discard={() => {
              if (
                !sourceDraft.dirty ||
                window.confirm('Discard the unsaved file-topic correction?')
              ) {
                setSourceDraft(null);
                setPanel(null);
              }
            }}
          />
        )}
      </ReviewDialog>
      <ReviewDialog
        open={panel === 'agents'}
        title="Settings · Integrations"
        close={() => setPanel(null)}
      >
        {panel === 'agents' && <AgentPanel api={api} reviewID={data.review.id} query={agents} />}
      </ReviewDialog>
      <ReviewDialog
        open={panel === 'agent-request'}
        title="Pi request"
        close={() => setPanel(null)}
      >
        {agentPreview && (
          <AgentRequestForm
            key={agentPreview.key}
            api={api}
            reviewID={data.review.id}
            preview={agentPreview}
            query={agents}
            open={panel === 'agent-request'}
            onDraftChange={setAgentDirty}
            settings={() => setPanel('agents')}
          />
        )}
      </ReviewDialog>
    </DiscussionProvider>
  );
}

function ReviewDialog({
  open,
  title,
  close,
  children,
}: {
  open: boolean;
  title: string;
  close: () => void;
  children: React.ReactNode;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (open && !dialog.current?.open) dialog.current?.showModal();
    else if (!open && dialog.current?.open) dialog.current.close();
  }, [open]);
  return (
    <dialog
      ref={dialog}
      aria-label={title}
      onClose={() => {
        if (open) close();
      }}
      onCancel={close}
    >
      <div className="section-heading">
        <h2>{title}</h2>
        <button aria-label={'Close ' + title} onClick={close}>
          Close
        </button>
      </div>
      {children}
    </dialog>
  );
}

function TopicTree({
  topics,
  attention,
  selected,
  select,
  order,
  changes,
  parent = null,
}: {
  topics: Model['Topic'][];
  attention: Model['AttentionSignal'][];
  selected: string | null;
  select: (id: string) => void;
  order: TopicOrder;
  changes: Model['EntityComparison'][];
  parent?: string | null;
}) {
  return (
    <ul className="topic-tree">
      {orderedTopics(topics, attention, order, parent).map((t) => (
        <li key={t.id}>
          <button aria-pressed={selected === t.id} onClick={() => select(t.id)}>
            <span className="topic-dot">◇</span>
            <span>
              {t.title}
              {t.state === 'OBSOLETE' && <small>Obsolete</small>}
              <ChangeBadge change={changes.find((item) => item.id === t.id)} />
            </span>
            {t.finding_ids.length > 0 && <span className="count">{t.finding_ids.length}</span>}
          </button>
          <TopicTree
            topics={topics}
            attention={attention}
            order={order}
            changes={changes}
            selected={selected}
            select={select}
            parent={t.id}
          />
        </li>
      ))}
    </ul>
  );
}
