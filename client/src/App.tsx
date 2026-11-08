import { useEffect, useMemo, useState } from 'react';
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
import { useConnection, useReview, useSubmit, type ReviewData } from './hooks';
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
import { Decisions, DiscussionPanel } from './Discussions';
import { Sessions } from './Sessions';
import { IntentCard, ChangeCard } from './Records';

function hasTextDrafts() {
  return Array.from(document.querySelectorAll('textarea')).some((t) => t.value.trim());
}

function leaveDrafts() {
  return (
    !hasTextDrafts() || window.confirm('Leave this review? Unposted drafts will be discarded.')
  );
}

export function App() {
  const [preferences, setPreferences] = useState(loadPreferences);
  const [address, setAddress] = useState(preferences.server);
  const [addressError, setAddressError] = useState('');
  const [reviewID, setReviewID] = useState('');
  const cache = useQueryClient();
  const api = useMemo(() => apiFor(preferences.server), [preferences.server]);
  const status = useConnection(api);
  const reviews = useQuery({
    queryKey: ['reviews'],
    queryFn: ({ signal }) => result(api.GET('/v1/reviews', { signal })),
  });
  const workspaces = useQuery({
    queryKey: ['workspaces'],
    queryFn: ({ signal }) => result(api.GET('/v1/workspaces', { signal })),
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
      if (hasTextDrafts()) e.preventDefault();
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, []);
  function connect() {
    try {
      const server = serverURL(address.trim());
      if (!leaveDrafts()) return;
      void cache.cancelQueries();
      cache.clear();
      setReviewID('');
      setPreferences((p) => ({ ...p, server }));
      setAddressError('');
    } catch (e) {
      setAddressError((e as Error).message);
    }
  }
  const selected = reviews.data?.find((r) => r.id === reviewID) || reviews.data?.[0];
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
          value={selected?.id || ''}
          onChange={(e) => {
            if (leaveDrafts()) setReviewID(e.target.value);
          }}
        >
          {reviews.data?.map((r) => (
            <option key={r.id} value={r.id}>
              {workspaces.data?.find((w) => w.id === r.workspace_id)?.name || r.workspace_id} /{' '}
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
      {selected ? (
        <ReviewLoader
          key={preferences.server + selected.id}
          api={api}
          reviewID={selected.id}
          preferences={preferences}
          setPreferences={setPreferences}
        />
      ) : (
        <div className="welcome">
          <h1>Review the feature, not just the diff.</h1>
          <Notice>
            {reviews.isPending
              ? 'Connecting to the review server…'
              : 'No reviews yet. Register a workspace and import review content with the prunelle CLI.'}
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
  api: API;
  reviewID: string;
  preferences: Preferences;
  setPreferences: React.Dispatch<React.SetStateAction<Preferences>>;
}) {
  const query = useReview(props.api, props.reviewID);
  return (
    <>
      <ErrorNotice error={query.error} />
      {query.data ? <ReviewPage {...props} data={query.data} /> : <Notice>Loading review…</Notice>}
    </>
  );
}

type Inspector =
  | { type: 'evidence'; id: string }
  | { type: 'finding'; id: string }
  | { type: 'INTENT' | 'CHANGE'; id: string }
  | { type: 'node'; node: DiagramNode }
  | null;
function ReviewPage({
  api,
  data,
  preferences: pref,
  setPreferences,
}: {
  api: API;
  data: ReviewData;
  preferences: Preferences;
  setPreferences: React.Dispatch<React.SetStateAction<Preferences>>;
}) {
  const [topicID, setTopicID] = useState<string | null>(data.topics[0]?.id || null);
  const [revisionID, setRevisionID] = useState<string | null>(null);
  const revision =
    data.revisions.find((r) => r.id === revisionID) ||
    data.revisions.find((r) => r.id === data.review.current_revision_id);
  const [tab, setTab] = useState<Navigation>(presets[pref.perspective].tab);
  const [order, setOrder] = useState(presets[pref.perspective].order);
  const [file, setFile] = useState<FileSelection | null>(null);
  const [inspector, setInspector] = useState<Inspector>(null);
  const [panel, setPanel] = useState<'discussions' | 'decision' | 'sessions'>('discussions');
  const [target, setTarget] = useState<DiscussionTarget | null>(null);
  const [representationIDs, setRepresentationIDs] = useState<Partial<Record<View, string>>>({});
  const [elementID, setElementID] = useState<string | null>(null);
  const topic = data.topics.find((t) => t.id === topicID);
  const reps = useQuery({
    queryKey: ['representations', topicID],
    enabled: !!topicID,
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/topics/{topicId}/representations', {
          params: { path: { topicId: topicID! } },
          signal,
        }),
      ),
  });
  const source = useQuery({
    queryKey: ['source', revision?.id],
    enabled: !!revision,
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/revisions/{revisionId}/source-changes', {
          params: { path: { revisionId: revision!.id } },
          signal,
        }),
      ),
  });
  const capture = useSubmit();
  function selectTopic(id: string) {
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
      setFile({
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
    setInspector({ type: 'node', node });
    const e = data.evidence.find(
      (e) =>
        node.evidence_ids.includes(e.id) || (node.ref?.type === 'EVIDENCE' && e.id === node.ref.id),
    );
    if (e) openEvidence(e);
    if (node.ref?.type === 'FINDING') setInspector({ type: 'finding', id: node.ref.id });
    if (node.ref?.type === 'INTENT' || node.ref?.type === 'CHANGE')
      setInspector({ type: node.ref.type, id: node.ref.id });
  }
  function discuss(t: DiscussionTarget) {
    setTarget(t);
    setPanel('discussions');
  }
  function openAnchor(d: Model['Discussion']) {
    if (d.anchor.code_range) {
      const r = d.anchor.code_range;
      setFile({ revisionID: d.revision_id, repositoryID: r.repository_id, path: r.path, range: r });
      showCode();
    } else if (d.anchor.type === 'TOPIC' && d.anchor.id) selectTopic(d.anchor.id);
    else if (d.anchor.type === 'DIAGRAM_ELEMENT' && d.anchor.id && d.topic_ids[0]) {
      selectTopic(d.topic_ids[0]);
      setRepresentationIDs((ids) => ({ ...ids, DIAGRAM: d.anchor.id! }));
      setElementID(d.anchor.element_id);
      setView(0, 'DIAGRAM');
    } else if (d.anchor.type === 'FINDING' && d.anchor.id)
      setInspector({ type: 'finding', id: d.anchor.id });
    else if ((d.anchor.type === 'INTENT' || d.anchor.type === 'CHANGE') && d.anchor.id)
      setInspector({ type: d.anchor.type, id: d.anchor.id });
    else if (d.anchor.type === 'EVIDENCE') {
      const e = data.evidence.find((e) => e.id === d.anchor.id);
      if (e) openEvidence(e);
    }
  }
  function perspective(p: Perspective) {
    setPreferences((old) => ({ ...old, perspective: p, views: presets[p].views }));
    setTab(presets[p].tab);
    setOrder(presets[p].order);
  }
  function setView(index: number, type: View) {
    setPreferences((p) => ({ ...p, views: index === 0 ? [type, p.views[1]] : [p.views[0], type] }));
  }
  const sourceFile =
    source.data?.revision_id === file?.revisionID
      ? source.data?.repositories
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
  const repositoryLabel = data.revisions
    .find((r) => r.id === file?.revisionID)
    ?.snapshots.find((s) => s.repository_id === file?.repositoryID)?.repository_label;
  return (
    <>
      <div className="review-bar">
        <div>
          <div className="eyebrow">FEATURE REVIEW · {data.review.status}</div>
          <h1>{data.review.title}</h1>
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
        <button
          className="primary"
          onClick={() => setPanel(panel === 'decision' ? 'discussions' : 'decision')}
        >
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
            {(['Topics', 'Findings', 'All Source Changes'] as Navigation[]).map((t) => (
              <button key={t} aria-pressed={tab === t} onClick={() => setTab(t)}>
                {t}
              </button>
            ))}
          </nav>
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
              />
              {!data.topics.length && (
                <Notice>No topics imported yet. All Source Changes remains exhaustive.</Notice>
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
                </button>
              ))}
              {!data.findings.length && <Notice>No findings recorded.</Notice>}
            </div>
          )}
          {tab === 'All Source Changes' && (
            <>
              <ErrorNotice error={source.error} />
              {source.data ? (
                <SourceTree
                  source={source.data}
                  file={file}
                  choose={(f) => {
                    setTopicID(null);
                    setInspector(null);
                    setFile(f);
                    showCode();
                  }}
                />
              ) : (
                <Notice>
                  {revision ? 'Loading source…' : 'Capture a revision to inspect source.'}
                </Notice>
              )}
            </>
          )}
          <footer>
            <button
              className="quiet"
              onClick={() => setPanel(panel === 'sessions' ? 'discussions' : 'sessions')}
            >
              ▤ Runtime sessions
            </button>
            <small>Review works without an agent.</small>
          </footer>
        </aside>
        <main className="review-content">
          {!revision && <Notice>No source snapshot yet. Capture one to comment or decide.</Notice>}
          {revision && (
            <details className="snapshot">
              <summary>
                Revision {revision.ordinal} · {new Date(revision.captured_at).toLocaleString()} ·{' '}
                {revision.snapshots.length} repositories · {revision.status}
              </summary>
              {revision.snapshots.map((s) => (
                <div key={s.repository_id}>
                  <strong>{s.repository_label}</strong> · {s.branch} · {s.base_sha.slice(0, 8)} →{' '}
                  {s.head_sha.slice(0, 8)} · {s.file_count} files
                  {s.warnings.map((w, i) => (
                    <p className="error" key={i}>
                      {w}
                    </p>
                  ))}
                </div>
              ))}
              <p>
                Topics and explanations are the current review graph; revision-aware comparison
                arrives in M3.
              </p>
            </details>
          )}
          <div className="topic-header">
            <div className="eyebrow">
              {topic ? 'REVIEW TOPIC · ' + topic.state : 'SOURCE EXPLORER'}
            </div>
            <h2>{topic?.title || 'All source changes'}</h2>
            <p>
              {topic?.summary || 'Every changed file, including changes not assigned to a topic.'}
            </p>
            {topic && (
              <>
                <Attribution author={topic.author} />
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
                  <EvidenceLinks
                    ids={a.evidence_ids}
                    evidence={data.evidence}
                    select={openEvidence}
                  />
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
          {sourceFile && (
            <div className="row source-topics">
              <small>File belongs to:</small>
              {sourceFile.unassigned ? (
                <span className="badge">Unassigned Changes</span>
              ) : (
                sourceFile.topic_ids.map((id) => (
                  <button key={id} className="link" onClick={() => selectTopic(id)}>
                    {data.topics.find((t) => t.id === id)?.title || id}
                  </button>
                ))
              )}
            </div>
          )}
          <ErrorNotice error={reps.error} />
          <div className={'view-grid ' + (pref.split ? 'split' : '')}>
            {pref.views.slice(0, pref.split ? 2 : 1).map((v, i) => (
              <section className="view-block" key={i}>
                <nav className="view-tabs" aria-label={i === 0 ? 'Primary view' : 'Secondary view'}>
                  {(Object.keys(viewNames) as View[]).map((type) => (
                    <button key={type} aria-pressed={v === type} onClick={() => setView(i, type)}>
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
                  representations={reps.data || []}
                  evidence={data.evidence}
                  file={file}
                  repositoryLabel={repositoryLabel}
                  api={api}
                  chooseFile={setFile}
                  openEvidence={openEvidence}
                  selectNode={inspectNode}
                  discuss={discuss}
                />
              </section>
            ))}
          </div>
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
        <aside className="review-panel">
          <div className="panel-tabs">
            <button aria-pressed={panel === 'discussions'} onClick={() => setPanel('discussions')}>
              Discussions
            </button>
            <button aria-pressed={panel === 'decision'} onClick={() => setPanel('decision')}>
              Decisions
            </button>
            <button aria-pressed={panel === 'sessions'} onClick={() => setPanel('sessions')}>
              Sessions
            </button>
          </div>
          {revision && (
            <div hidden={panel !== 'discussions'}>
              <DiscussionPanel
                api={api}
                reviewID={data.review.id}
                revisionID={revision.id}
                topicID={topicID}
                discussions={data.discussions}
                target={target}
                setTarget={setTarget}
                openAnchor={openAnchor}
              />
            </div>
          )}
          {revision && (
            <div hidden={panel !== 'decision'}>
              <Decisions
                api={api}
                reviewID={data.review.id}
                revision={revision}
                findings={data.findings}
                discussions={data.discussions}
                decisions={data.decisions}
              />
            </div>
          )}
          {panel === 'sessions' && <Sessions api={api} workspaceID={data.review.workspace_id} />}
        </aside>
      </div>
    </>
  );
}

function TopicTree({
  topics,
  attention,
  selected,
  select,
  order,
  parent = null,
}: {
  topics: Model['Topic'][];
  attention: Model['AttentionSignal'][];
  selected: string | null;
  select: (id: string) => void;
  order: TopicOrder;
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
            </span>
            {t.finding_ids.length > 0 && <span className="count">{t.finding_ids.length}</span>}
          </button>
          <TopicTree
            topics={topics}
            attention={attention}
            order={order}
            selected={selected}
            select={select}
            parent={t.id}
          />
        </li>
      ))}
    </ul>
  );
}
function SourceTree({
  source,
  file,
  choose,
}: {
  source: Model['SourceChanges'];
  file: FileSelection | null;
  choose: (f: FileSelection) => void;
}) {
  const entries = source.repositories.flatMap((r) =>
    r.files.map((f) => ({ ...f, repositoryID: r.repository_id, label: r.repository_label })),
  );
  return (
    <div className="source-tree">
      <h3>
        Unassigned Changes <span className="count">{source.unassigned_count}</span>
      </h3>
      {!source.unassigned_count && <p className="muted">Every file is linked to a topic.</p>}
      {entries
        .filter((f) => f.unassigned)
        .map((f) => (
          <button
            key={f.repositoryID + f.path}
            onClick={() =>
              choose({ revisionID: source.revision_id, repositoryID: f.repositoryID, path: f.path })
            }
          >
            {f.label}/{f.path}
          </button>
        ))}
      {source.repositories.map((r) => (
        <section key={r.repository_id}>
          <h3>
            {r.repository_label} <span className="count">{r.files.length}</span>
          </h3>
          {r.files.map((f) => (
            <button
              key={f.path}
              aria-pressed={file?.repositoryID === r.repository_id && file.path === f.path}
              onClick={() =>
                choose({
                  revisionID: source.revision_id,
                  repositoryID: r.repository_id,
                  path: f.path,
                })
              }
            >
              <span>{f.path}</span>
              <small>
                {f.status}
                {f.unassigned ? ' · Unassigned' : ''}
              </small>
            </button>
          ))}
          {!r.files.length && <p className="muted">No changed files.</p>}
        </section>
      ))}
    </div>
  );
}
