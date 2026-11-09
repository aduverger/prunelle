import { Markdown } from './Markdown';
import { createContext, useCallback, useContext, useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useQuery } from '@tanstack/react-query';
import { result, type API, type Model } from './api/client';
import { useSubmit } from './hooks';
import type { DiscussionTarget } from './model';
import { Attribution, Notice } from './Views';

export function anchorLabel(anchor: DiscussionTarget['anchor'] | Model['Anchor']) {
  const range = anchor.code_range;
  return range
    ? `${range.path}:${range.start_line}–${range.end_line} (${range.side})`
    : `${anchor.type.replaceAll('_', ' ')} ${anchor.element_id || ''}`;
}

export function useDiscussionComposer(api: API, reviewID: string) {
  const [target, setTarget] = useState<DiscussionTarget | null>(null);
  const [draftTarget, setDraftTarget] = useState<DiscussionTarget | null>(null);
  const [body, setBody] = useState('');
  const focusRequested = useRef(false);
  const action = useSubmit();
  const currentTarget = draftTarget || target;
  function start(next: DiscussionTarget) {
    setTarget(next);
    focusRequested.current = true;
  }
  function discard() {
    setBody('');
    setDraftTarget(null);
    setTarget(null);
  }
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
    )
      discard();
  }
  return {
    target: currentTarget,
    body,
    start,
    discard,
    post,
    action,
    requestFocus() {
      focusRequested.current = true;
    },
    focus(node: HTMLTextAreaElement | null) {
      if (node && focusRequested.current) {
        node.focus({ preventScroll: currentTarget?.anchor.type === 'CODE_RANGE' });
        focusRequested.current = false;
      }
    },
    moved: !!(draftTarget && target && JSON.stringify(draftTarget) !== JSON.stringify(target)),
    edit(value: string) {
      if (!draftTarget) setDraftTarget(currentTarget);
      setBody(value);
    },
  };
}
type Composer = ReturnType<typeof useDiscussionComposer>;
type Host = { id: string; node: HTMLElement; reveal?: number };
const DiscussionContext = createContext<{
  composer: Composer;
  discussions: Model['Discussion'][];
  revisions: Model['Revision'][];
  anchorStatuses: Model['DiscussionAnchorStatus'][];
  register: (key: string, id: string, node: HTMLElement | null, reveal?: number) => void;
} | null>(null);

export function useDiscussions() {
  const context = useContext(DiscussionContext);
  if (!context) throw new Error('Discussion surface must be inside its review provider.');
  return context;
}

export function DiscussionSlot({ id, reveal }: { id: string; reveal?: number }) {
  const { register } = useDiscussions();
  const key = useId();
  const ref = useCallback(
    (node: HTMLDivElement | null) => register(key, id, node, reveal),
    [register, key, id, reveal],
  );
  return <div ref={ref} data-discussion-slot={id} />;
}

export function DiscussionProvider({
  api,
  discussions,
  revisions,
  composer,
  openAnchor,
  onDraftChange,
  anchorStatuses = [],
  children,
}: {
  api: API;
  discussions: Model['Discussion'][];
  revisions: Model['Revision'][];
  composer: Composer;
  openAnchor: (d: Model['Discussion']) => void;
  onDraftChange: (dirty: boolean) => void;
  anchorStatuses?: Model['DiscussionAnchorStatus'][];
  children: React.ReactNode;
}) {
  const [hosts, setHosts] = useState<Record<string, Host>>({});
  const [replyDrafts, setReplyDrafts] = useState<Set<string>>(() => new Set());
  const register = useCallback(
    (key: string, id: string, node: HTMLElement | null, reveal?: number) => {
      setHosts((previous) => {
        if (
          (previous[key]?.node === node && previous[key]?.reveal === reveal) ||
          (!node && !previous[key])
        )
          return previous;
        const next = { ...previous };
        if (node) next[key] = { id, node, reveal };
        else delete next[key];
        return next;
      });
    },
    [],
  );
  const reportReply = useCallback((id: string, dirty: boolean) => {
    setReplyDrafts((previous) => {
      if (previous.has(id) === dirty) return previous;
      const next = new Set(previous);
      if (dirty) next.add(id);
      else next.delete(id);
      return next;
    });
  }, []);
  useEffect(() => {
    onDraftChange(!!composer.body.trim() || replyDrafts.size > 0);
  }, [composer.body, replyDrafts, onDraftChange]);
  useEffect(() => () => onDraftChange(false), [onDraftChange]);
  return (
    <DiscussionContext.Provider
      value={{ composer, discussions, revisions, anchorStatuses, register }}
    >
      {children}
      {Object.entries(hosts)
        .filter(([, host]) => host.id === 'composer')
        .map(([key, host]) => createPortal(<ComposerForm composer={composer} />, host.node, key))}
      {discussions.map((d) => (
        <ThreadController
          key={d.id}
          api={api}
          discussion={d}
          contextLabel={discussionRevisionLabel(d, revisions)}
          anchorStatus={anchorStatuses.find((status) => status.discussion_id === d.id)}
          hosts={Object.entries(hosts).filter(([, host]) => host.id === d.id)}
          open={() => openAnchor(d)}
          reportDraft={reportReply}
        />
      ))}
    </DiscussionContext.Provider>
  );
}

function ComposerForm({ composer: c }: { composer: Composer }) {
  if (!c.target) return null;
  return (
    <form
      className="composer"
      onSubmit={(event) => {
        event.preventDefault();
        void c.post();
      }}
    >
      <strong>New discussion · {c.target.label}</strong>
      <small>Against {c.target.revisionID}</small>
      {c.moved && (
        <Notice>
          Your draft stays on its original anchor. Post or discard it before starting another.
        </Notice>
      )}
      <textarea
        aria-label="New discussion"
        disabled={c.action.busy}
        placeholder="What needs a closer look?"
        value={c.body}
        required
        ref={c.focus}
        onChange={(event) => c.edit(event.target.value)}
      />
      {c.action.error && (
        <p role="alert" className="error">
          {c.action.error} Your draft is kept; retrying unchanged is safe.
        </p>
      )}
      <div className="row">
        <button type="submit" className="primary" disabled={c.action.busy || !c.body.trim()}>
          {c.action.busy ? 'Posting…' : 'Post discussion'}
        </button>
        <button type="button" disabled={c.action.busy} onClick={c.discard}>
          Discard
        </button>
      </div>
    </form>
  );
}

function discussionRevisionLabel(discussion: Model['Discussion'], revisions: Model['Revision'][]) {
  const revision = revisions.find((revision) => revision.id === discussion.revision_id);
  const repository = revision?.snapshots.find(
    (snapshot) => snapshot.repository_id === discussion.anchor.code_range?.repository_id,
  );
  return `Revision ${revision?.ordinal || discussion.revision_id}${repository ? ' · ' + repository.repository_label : ''}`;
}

function discussionInTopic(discussion: Model['Discussion'], topicID: string | null) {
  return !topicID || discussion.topic_ids.includes(topicID) || discussion.anchor.type === 'REVIEW';
}

export function CommentIndex({
  topicID,
  revisionID,
  openAnchor,
  openDraft,
}: {
  topicID: string | null;
  revisionID?: string;
  openAnchor: (d: Model['Discussion']) => void;
  openDraft: () => void;
}) {
  const { discussions, composer, revisions, anchorStatuses } = useDiscussions();
  const [scope, setScope] = useState<'topic' | 'all'>('topic');
  const visible = discussions.filter((d) => scope === 'all' || discussionInTopic(d, topicID));
  return (
    <section className="comment-index" aria-label="Comments">
      <div className="section-heading">
        <h2>
          Comments <span className="count">{visible.length}</span>
        </h2>
        <select
          aria-label="Discussion scope"
          value={scope}
          onChange={(event) => setScope(event.target.value as 'topic' | 'all')}
        >
          <option value="topic">Selected topic</option>
          <option value="all">Entire review</option>
        </select>
      </div>
      {revisionID && (
        <button
          className="link"
          onClick={() =>
            composer.start({
              revisionID,
              anchor: { type: 'REVIEW' },
              label: 'Entire review',
            })
          }
        >
          Discuss review
        </button>
      )}
      {composer.target && (
        <button className="draft-link" onClick={openDraft}>
          Unposted draft · {composer.target.label}
        </button>
      )}
      {!visible.length && <Notice>No discussions yet.</Notice>}
      {visible.map((d) => (
        <button className="comment-link" key={d.id} onClick={() => openAnchor(d)}>
          <strong>{anchorLabel(d.anchor)}</strong>
          <span className="badge">{d.status}</span>
          <span>{d.messages[0]?.body}</span>
          <small>{discussionRevisionLabel(d, revisions)}</small>
          <small title={anchorStatuses.find((status) => status.discussion_id === d.id)?.reason}>
            {anchorStatuses.find((status) => status.discussion_id === d.id)?.status.toLowerCase()}
          </small>
        </button>
      ))}
    </section>
  );
}

export function ContextDiscussions({
  topicID,
  includeCode = false,
  focusedID,
  navigation,
}: {
  topicID: string | null;
  includeCode?: boolean;
  focusedID?: string | null;
  navigation?: number;
}) {
  const { discussions, composer } = useDiscussions();
  const visible = discussions.filter(
    (d) =>
      (discussionInTopic(d, topicID) || d.id === focusedID) &&
      (includeCode || d.anchor.type !== 'CODE_RANGE'),
  );
  const showComposer =
    composer.target && (includeCode || composer.target.anchor.type !== 'CODE_RANGE');
  if (!visible.length && !showComposer) return null;
  return (
    <section className="context-discussions" aria-label="Discussions">
      <h2>Discussions</h2>
      {showComposer && <DiscussionSlot id="composer" />}
      {visible.map((d) => (
        <DiscussionSlot key={d.id} id={d.id} reveal={d.id === focusedID ? navigation : undefined} />
      ))}
    </section>
  );
}

function ThreadController({
  api,
  discussion: d,
  contextLabel,
  anchorStatus,
  hosts,
  open,
  reportDraft,
}: {
  api: API;
  discussion: Model['Discussion'];
  contextLabel: string;
  anchorStatus?: Model['DiscussionAnchorStatus'];
  hosts: [string, Host][];
  open: () => void;
  reportDraft: (id: string, dirty: boolean) => void;
}) {
  const [reply, setReply] = useState('');
  const revealed = useRef<number>(undefined);
  const reveal = hosts.find(([, host]) => host.reveal !== undefined)?.[1].reveal;
  const replyAction = useSubmit();
  const statusAction = useSubmit();
  const busy = replyAction.busy || statusAction.busy;
  const error = [replyAction.error, statusAction.error].filter(Boolean).join('\n');
  useEffect(() => {
    reportDraft(d.id, !!reply.trim());
  }, [d.id, reply, reportDraft]);
  async function post() {
    if (!reply.trim()) return;
    if (
      await replyAction.submit([d.id, reply], (key) =>
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
    await statusAction.submit([d.id, d.version, d.status], () =>
      result(
        api.PATCH('/v1/discussions/{discussionId}', {
          params: { path: { discussionId: d.id }, header: { 'If-Match': `"${d.version}"` } },
          body: { status: d.status === 'OPEN' ? 'RESOLVED' : 'OPEN' },
        }),
      ),
    );
  }
  const content = (
    <article
      className="thread"
      data-discussion-id={d.id}
      tabIndex={-1}
      ref={(node) => {
        if (node && reveal !== undefined && revealed.current !== reveal) {
          revealed.current = reveal;
          node.scrollIntoView({ block: 'nearest' });
          node.focus({ preventScroll: true });
        }
      }}
    >
      <div className="section-heading">
        <button className="link" onClick={open}>
          {anchorLabel(d.anchor)}
        </button>
        <span className="badge">{d.status}</span>
      </div>
      <small className="muted" title={d.revision_id}>
        {contextLabel}
      </small>
      {anchorStatus && (
        <small className="anchor-status" title={anchorStatus.reason}>
          {anchorStatus.status.toLowerCase()} · {anchorStatus.reason}
        </small>
      )}
      {d.messages.map((m) => (
        <div className="message" key={m.id}>
          <Attribution author={m.author} />
          <time>{new Date(m.created_at).toLocaleString()}</time>
          <Markdown>{m.body}</Markdown>
        </div>
      ))}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void post();
        }}
      >
        <textarea
          aria-label={'Reply to ' + anchorLabel(d.anchor)}
          disabled={busy}
          placeholder="Reply…"
          value={reply}
          onChange={(event) => setReply(event.target.value)}
        />
        <div className="row">
          <button disabled={busy || !reply.trim()} type="submit">
            Reply
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => {
              void toggle();
            }}
          >
            {d.status === 'OPEN' ? 'Resolve' : 'Reopen'}
          </button>
        </div>
      </form>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </article>
  );
  return hosts.map(([key, host]) => createPortal(content, host.node, key));
}

export function Decisions({
  api,
  reviewID,
  revision,
  findings,
  discussions,
  decisions,
  onDraftChange,
}: {
  api: API;
  reviewID: string;
  revision: Model['Revision'];
  findings: Model['Finding'][];
  discussions: Model['Discussion'][];
  decisions: Model['Decision'][];
  onDraftChange: (dirty: boolean) => void;
}) {
  const [draftRevision, setDraftRevision] = useState(revision);
  const [verdict, setVerdict] = useState<Model['CreateDecisionRequest']['decision']>('COMMENT');
  const [body, setBody] = useState('');
  const [findingIDs, setFindingIDs] = useState<string[]>([]);
  const [discussionIDs, setDiscussionIDs] = useState<string[]>([]);
  const action = useSubmit();
  const { revisions } = useDiscussions();
  const draftView = useQuery({
    queryKey: ['revision-view', reviewID, draftRevision.id],
    enabled: draftRevision.id !== revision.id,
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/reviews/{reviewId}/revision-view', {
          params: { path: { reviewId: reviewID }, query: { revision_id: draftRevision.id } },
          signal,
        }),
      ),
  });
  const draftFindings =
    draftRevision.id === revision.id ? findings : draftView.data?.graph?.findings || [];
  const draftDiscussions = discussions.filter((discussion) => {
    const original = revisions.find((r) => r.id === discussion.revision_id);
    return original && original.ordinal <= draftRevision.ordinal;
  });
  const missingFindings = findingIDs.filter(
    (id) => !draftFindings.some((finding) => finding.id === id),
  );
  const missingDiscussions = discussionIDs.filter(
    (id) => !draftDiscussions.some((discussion) => discussion.id === id),
  );
  useEffect(() => {
    onDraftChange(
      !!body.trim() || verdict !== 'COMMENT' || findingIDs.length > 0 || discussionIDs.length > 0,
    );
  }, [body, verdict, findingIDs.length, discussionIDs.length, onDraftChange]);
  useEffect(() => () => onDraftChange(false), [onDraftChange]);
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
      setVerdict('COMMENT');
      setFindingIDs([]);
      setDiscussionIDs([]);
    }
  }
  if (
    !body &&
    verdict === 'COMMENT' &&
    !findingIDs.length &&
    !discussionIDs.length &&
    draftRevision.id !== revision.id
  )
    setDraftRevision(revision);
  const invalid =
    (verdict === 'COMMENT' && !body.trim()) ||
    (verdict === 'REQUEST_CHANGES' &&
      ((!findingIDs.length && !discussionIDs.length) ||
        missingFindings.length > 0 ||
        missingDiscussions.length > 0));
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
            {draftFindings.map((f) => (
              <label key={f.id}>
                <input
                  type="checkbox"
                  checked={findingIDs.includes(f.id)}
                  onChange={() => toggle(f.id, findingIDs, setFindingIDs)}
                />
                {f.title}
              </label>
            ))}
            {draftDiscussions.map((d) => (
              <label key={d.id}>
                <input
                  type="checkbox"
                  checked={discussionIDs.includes(d.id)}
                  onChange={() => toggle(d.id, discussionIDs, setDiscussionIDs)}
                />
                {d.messages[0]?.body} · {d.status}
              </label>
            ))}
            {[...missingFindings, ...missingDiscussions].map((id) => (
              <label key={id}>
                <input
                  type="checkbox"
                  checked
                  onChange={() => {
                    setFindingIDs((ids) => ids.filter((value) => value !== id));
                    setDiscussionIDs((ids) => ids.filter((value) => value !== id));
                  }}
                />
                Unavailable in this revision · {id} (uncheck to remove)
              </label>
            ))}
            {draftView.error && (
              <p role="alert" className="error">
                {draftView.error.message}
              </p>
            )}
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
