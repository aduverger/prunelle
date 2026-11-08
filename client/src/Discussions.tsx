import { Markdown } from './Markdown';
import { useState } from 'react';
import { result, type API, type Model } from './api/client';
import { useSubmit } from './hooks';
import type { DiscussionTarget } from './model';
import { Attribution, Notice } from './Views';

export function anchorLabel(anchor: Model['Anchor']) {
  const range = anchor.code_range;
  return range
    ? `${range.path}:${range.start_line}–${range.end_line} (${range.side})`
    : `${anchor.type.replaceAll('_', ' ')} ${anchor.element_id || ''}`;
}
export function DiscussionPanel({
  api,
  reviewID,
  revisionID,
  topicID,
  discussions,
  target,
  setTarget,
  openAnchor,
}: {
  api: API;
  reviewID: string;
  revisionID: string;
  topicID: string | null;
  discussions: Model['Discussion'][];
  target: DiscussionTarget | null;
  setTarget: (target: DiscussionTarget | null) => void;
  openAnchor: (d: Model['Discussion']) => void;
}) {
  const [body, setBody] = useState('');
  const [draftTarget, setDraftTarget] = useState<DiscussionTarget | null>(null);
  const [scope, setScope] = useState<'topic' | 'all'>('topic');
  const action = useSubmit();
  const currentTarget = draftTarget || target;
  const inScope = (d: Model['Discussion']) =>
    scope === 'all' || !topicID || d.topic_ids.includes(topicID);
  const visible = discussions.filter(inScope);
  async function post() {
    if (!currentTarget || !body.trim()) return;
    const request = { revision_id: currentTarget.revisionID, anchor: currentTarget.anchor, body };
    if (
      await action.submit([reviewID, request], (key) =>
        result(
          api.POST('/v1/reviews/{reviewId}/discussions', {
            params: { path: { reviewId: reviewID }, header: { 'Idempotency-Key': key } },
            body: request,
          }),
        ),
      )
    ) {
      setBody('');
      setDraftTarget(null);
      setTarget(null);
    }
  }
  return (
    <section className="discussion-panel" aria-label="Discussions">
      <header className="section-heading">
        <h2>
          Discussions <span className="count">{visible.length}</span>
        </h2>
        <select
          aria-label="Discussion scope"
          value={scope}
          onChange={(e) => setScope(e.target.value as 'topic' | 'all')}
        >
          <option value="topic">Selected topic</option>
          <option value="all">Entire review</option>
        </select>
      </header>
      <p className="muted">Comments never change code or start an agent.</p>
      <button
        className="link"
        onClick={() =>
          setTarget({ revisionID, anchor: { type: 'REVIEW' }, label: 'Entire review' })
        }
      >
        Discuss review
      </button>
      {currentTarget && (
        <form
          className="composer"
          onSubmit={(e) => {
            e.preventDefault();
            void post();
          }}
        >
          <strong>New discussion · {currentTarget.label}</strong>
          <small>Against {currentTarget.revisionID}</small>
          {draftTarget && target && JSON.stringify(draftTarget) !== JSON.stringify(target) && (
            <Notice>
              Your draft stays on its original anchor. Post or discard it before starting another.
            </Notice>
          )}
          <textarea
            aria-label="New discussion"
            disabled={action.busy}
            placeholder="What needs a closer look?"
            value={body}
            required
            onChange={(e) => {
              if (!draftTarget) setDraftTarget(currentTarget);
              setBody(e.target.value);
            }}
          />
          {action.error && (
            <p role="alert" className="error">
              {action.error} Your draft is kept; retrying unchanged is safe.
            </p>
          )}
          <div className="row">
            <button type="submit" className="primary" disabled={action.busy || !body.trim()}>
              {action.busy ? 'Posting…' : 'Post discussion'}
            </button>
            <button
              type="button"
              disabled={action.busy}
              onClick={() => {
                setBody('');
                setDraftTarget(null);
                setTarget(null);
              }}
            >
              Discard
            </button>
          </div>
        </form>
      )}
      {!visible.length && <Notice>No discussions yet.</Notice>}
      {discussions.map((d) => (
        <div key={d.id} hidden={!inScope(d)}>
          <Thread api={api} discussion={d} open={() => openAnchor(d)} />
        </div>
      ))}
    </section>
  );
}

function Thread({
  api,
  discussion: d,
  open,
}: {
  api: API;
  discussion: Model['Discussion'];
  open: () => void;
}) {
  const [reply, setReply] = useState('');
  const action = useSubmit();
  async function post() {
    if (!reply.trim()) return;
    if (
      await action.submit([d.id, reply], (key) =>
        result(
          api.POST('/v1/discussions/{discussionId}/messages', {
            params: { path: { discussionId: d.id }, header: { 'Idempotency-Key': key } },
            body: { body: reply },
          }),
        ),
      )
    )
      setReply('');
  }
  async function toggle() {
    await action.submit([d.id, d.version, d.status], () =>
      result(
        api.PATCH('/v1/discussions/{discussionId}', {
          params: { path: { discussionId: d.id }, header: { 'If-Match': `"${d.version}"` } },
          body: { status: d.status === 'OPEN' ? 'RESOLVED' : 'OPEN' },
        }),
      ),
    );
  }
  return (
    <article className="thread">
      <div className="section-heading">
        <button className="link" onClick={open}>
          {anchorLabel(d.anchor)}
        </button>
        <span className="badge">{d.status}</span>
      </div>
      <small className="muted">{d.revision_id}</small>
      {d.messages.map((m) => (
        <div className="message" key={m.id}>
          <Attribution author={m.author} />
          <time>{new Date(m.created_at).toLocaleString()}</time>
          <Markdown>{m.body}</Markdown>
        </div>
      ))}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void post();
        }}
      >
        <textarea
          aria-label={'Reply to ' + anchorLabel(d.anchor)}
          disabled={action.busy}
          placeholder="Reply…"
          value={reply}
          onChange={(e) => setReply(e.target.value)}
        />
        <div className="row">
          <button disabled={action.busy || !reply.trim()} type="submit">
            Reply
          </button>
          <button
            type="button"
            disabled={action.busy}
            onClick={() => {
              void toggle();
            }}
          >
            {d.status === 'OPEN' ? 'Resolve' : 'Reopen'}
          </button>
        </div>
      </form>
      {action.error && (
        <p role="alert" className="error">
          {action.error}
        </p>
      )}
    </article>
  );
}

export function Decisions({
  api,
  reviewID,
  revision,
  findings,
  discussions,
  decisions,
}: {
  api: API;
  reviewID: string;
  revision: Model['Revision'];
  findings: Model['Finding'][];
  discussions: Model['Discussion'][];
  decisions: Model['Decision'][];
}) {
  const [draftRevision, setDraftRevision] = useState(revision);
  const [verdict, setVerdict] = useState<Model['CreateDecisionRequest']['decision']>('COMMENT');
  const [body, setBody] = useState('');
  const [findingIDs, setFindingIDs] = useState<string[]>([]);
  const [discussionIDs, setDiscussionIDs] = useState<string[]>([]);
  const action = useSubmit();
  function toggle(id: string, ids: string[], set: (ids: string[]) => void) {
    set(ids.includes(id) ? ids.filter((v) => v !== id) : [...ids, id]);
  }
  async function submit() {
    const request = {
      revision_id: draftRevision.id,
      decision: verdict,
      body,
      finding_ids: verdict === 'REQUEST_CHANGES' ? findingIDs : [],
      discussion_ids: verdict === 'REQUEST_CHANGES' ? discussionIDs : [],
    };
    if (
      await action.submit([reviewID, request], (key) =>
        result(
          api.POST('/v1/reviews/{reviewId}/decisions', {
            params: { path: { reviewId: reviewID }, header: { 'Idempotency-Key': key } },
            body: request,
          }),
        ),
      )
    ) {
      setBody('');
      setFindingIDs([]);
      setDiscussionIDs([]);
    }
  }
  const invalid =
    (verdict === 'COMMENT' && !body.trim()) ||
    (verdict === 'REQUEST_CHANGES' && !findingIDs.length && !discussionIDs.length);
  return (
    <section className="decisions" aria-label="Review decision">
      <h2>Decision · revision {draftRevision.ordinal}</h2>
      <Notice>
        A decision records your verdict on this exact snapshot. It does not start implementation.
      </Notice>
      {draftRevision.id !== revision.id && (
        <Notice>
          Your decision draft still targets revision {draftRevision.ordinal}.{' '}
          <button disabled={action.busy} onClick={() => setDraftRevision(revision)}>
            Use selected revision {revision.ordinal}
          </button>
        </Notice>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <label>
          Verdict
          <select
            disabled={action.busy}
            value={verdict}
            onChange={(e) => setVerdict(e.target.value as typeof verdict)}
          >
            <option value="COMMENT">Comment</option>
            <option value="APPROVE">Approve</option>
            <option value="REQUEST_CHANGES">Request changes</option>
          </select>
        </label>
        <textarea
          aria-label="Decision notes"
          disabled={action.busy}
          placeholder="Review notes…"
          value={body}
          onChange={(e) => setBody(e.target.value)}
        />
        {verdict === 'REQUEST_CHANGES' && (
          <fieldset disabled={action.busy}>
            <legend>Actionable findings or discussions (select at least one)</legend>
            {findings.map((f) => (
              <label key={f.id}>
                <input
                  type="checkbox"
                  checked={findingIDs.includes(f.id)}
                  onChange={() => toggle(f.id, findingIDs, setFindingIDs)}
                />
                {f.title}
              </label>
            ))}
            {discussions.map((d) => (
              <label key={d.id}>
                <input
                  type="checkbox"
                  checked={discussionIDs.includes(d.id)}
                  onChange={() => toggle(d.id, discussionIDs, setDiscussionIDs)}
                />
                {d.messages[0]?.body} · {d.status}
              </label>
            ))}
          </fieldset>
        )}
        <button className="primary" disabled={action.busy || invalid}>
          Submit decision
        </button>
        {action.error && (
          <p className="error" role="alert">
            {action.error}
          </p>
        )}
      </form>
      <h3>History for revision {revision.ordinal}</h3>
      {decisions
        .filter((d) => d.revision_id === revision.id)
        .map((d) => (
          <article className="card" key={d.id}>
            <span className="badge">{d.decision.replaceAll('_', ' ')}</span>
            <Attribution author={d.author} />
            <p>{d.body}</p>
            <small>
              {new Date(d.created_at).toLocaleString()} · {d.finding_ids.length} findings ·{' '}
              {d.discussion_ids.length} discussions
            </small>
          </article>
        ))}
    </section>
  );
}
