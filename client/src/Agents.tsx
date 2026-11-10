import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { APIError, result, type API, type Model } from './api/client';
import { useSubmit } from './hooks';
import { anchorLabel } from './Discussions';
import { Markdown } from './Markdown';
import { ErrorNotice, Notice } from './Views';

export type AgentAction =
  | { intent: 'DISCUSS'; discussion: Model['Discussion']; message: Model['Message'] }
  | { intent: 'PREPARE_REVIEW' }
  | { intent: 'IMPLEMENT'; decision: Model['Decision'] };
export type AgentPreview = {
  key: string;
  action: AgentAction;
  revisionID: string;
  agentSessionID: string | null;
};

export function useAgentState(api: API, reviewID: string, poll: boolean) {
  return useQuery({
    queryKey: ['agents', reviewID],
    queryFn: async ({ signal }) => {
      const [binding, sessions] = await Promise.all([
        result(
          api.GET('/v1/reviews/{reviewId}/agent-binding', {
            params: { path: { reviewId: reviewID } },
            signal,
          }),
        ),
        result(api.GET('/v1/agent-sessions', { signal })),
      ]);
      return { binding, sessions };
    },
    refetchInterval: poll ? 5000 : false,
  });
}

type AgentQuery = ReturnType<typeof useAgentState>;

function useAgentInvocations(api: API, reviewID: string, poll: boolean) {
  return useQuery({
    queryKey: ['agent-invocations', reviewID],
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/reviews/{reviewId}/agent-invocations', {
          params: { path: { reviewId: reviewID } },
          signal,
        }),
      ),
    refetchInterval: poll ? 5000 : false,
  });
}

type CommentDelivery = {
  key: string;
  request: Model['CreateAgentInvocationRequest'];
  busy: boolean;
  uncertain: boolean;
  error?: string;
  invocation?: Model['AgentInvocation'];
};

export function useCommentDelivery(api: API, reviewID: string, agents: AgentQuery) {
  const cache = useQueryClient();
  const [deliveries, setDeliveries] = useState(new Map<string, CommentDelivery>());
  const attempts = useRef(deliveries);
  const invocations = useAgentInvocations(api, reviewID, false);
  function update(messageID: string, delivery: CommentDelivery) {
    const updatedDeliveries = new Map(attempts.current).set(messageID, delivery);
    attempts.current = updatedDeliveries;
    setDeliveries(updatedDeliveries);
  }
  async function send(messageID: string, delivery: CommentDelivery) {
    update(messageID, { ...delivery, busy: true, error: undefined });
    try {
      const invocation = await result(
        api.POST('/v1/reviews/{reviewId}/agent-invocations', {
          params: { path: { reviewId: reviewID }, header: { 'Idempotency-Key': delivery.key } },
          body: delivery.request,
        }),
      );
      update(messageID, {
        ...delivery,
        busy: false,
        uncertain: false,
        error: undefined,
        invocation,
      });
    } catch (error) {
      const uncertain = !(error instanceof APIError && error.status >= 400 && error.status < 500);
      update(messageID, {
        ...delivery,
        busy: false,
        uncertain,
        error: String(error instanceof Error ? error.message : error),
      });
    }
    await cache.invalidateQueries();
  }
  function post(discussion: Model['Discussion'], message: Model['Message']) {
    if (attempts.current.has(message.id)) return;
    const targetID = agents.data?.binding.agent_session_id;
    const request: Model['CreateAgentInvocationRequest'] = {
      intent: 'DISCUSS',
      agent_session_id: targetID || '',
      revision_id: discussion.revision_id,
      discussion_id: discussion.id,
      message_id: message.id,
    };
    const delivery: CommentDelivery = {
      key: crypto.randomUUID(),
      request,
      busy: false,
      uncertain: false,
    };
    if (!targetID) {
      update(message.id, { ...delivery, error: 'No Pi attached.' });
      return;
    }
    void send(message.id, delivery);
  }
  return {
    post,
    dirty: [...deliveries.values()].some((delivery) => delivery.busy || delivery.uncertain),
    status(messageID: string) {
      const delivery = deliveries.get(messageID);
      const invocation =
        invocations.data?.findLast((invocation) => invocation.message_id === messageID) ||
        delivery?.invocation;
      const paused = agents.data?.sessions.find(
        (session) => session.id === invocation?.agent_session_id,
      )?.queue_paused;
      return {
        delivery,
        invocation,
        paused,
        error: invocations.error,
        loading: invocations.isPending,
      };
    },
    retry(messageID: string) {
      const delivery = attempts.current.get(messageID);
      if (delivery?.uncertain && !delivery.busy) void send(messageID, delivery);
    },
  };
}

export type CommentDeliveryController = ReturnType<typeof useCommentDelivery>;

const requestLabels: Record<AgentAction['intent'], string> = {
  DISCUSS: 'Ask Pi',
  PREPARE_REVIEW: 'Prepare review',
  IMPLEMENT: 'Send implementation request',
};

export function AgentPanel({
  api,
  reviewID,
  query,
}: {
  api: API;
  reviewID: string;
  query: AgentQuery;
}) {
  const action = useSubmit();
  const invocations = useAgentInvocations(api, reviewID, true);
  function choose(request: Model['AgentBindingRequest']) {
    const binding = query.data?.binding;
    if (!binding) return;
    void action.submit([reviewID, binding.version, request], () =>
      result(
        api.PUT('/v1/reviews/{reviewId}/agent-binding', {
          params: { path: { reviewId: reviewID }, header: { 'If-Match': `"${binding.version}"` } },
          body: request,
        }),
      ),
    );
  }
  const binding = query.data?.binding;
  const queue = useQuery({
    queryKey: ['agent-queue', binding?.agent_session_id],
    enabled: !!binding?.agent_session_id,
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/agent-sessions/{agentSessionId}/queue', {
          params: { path: { agentSessionId: binding!.agent_session_id! } },
          signal,
        }),
      ),
    refetchInterval: 5000,
  });
  function resume(session: Model['AgentSession']) {
    if (
      !window.confirm(
        'Inspect uncertain requests and the Pi conversation first. Resume only pending comments? Previously dispatched work will not be replayed.',
      )
    )
      return;
    void action.submit([session.id, session.queue_version, 'resume'], () =>
      result(
        api.POST('/v1/agent-sessions/{agentSessionId}/resume-queue', {
          params: {
            path: { agentSessionId: session.id },
            header: { 'If-Match': `"${session.queue_version}"` },
          },
        }),
      ),
    );
  }
  let attachmentNotice = 'No Pi is attached. Connect the extension or select a conversation.';
  if (binding?.mode === 'DISCONNECTED') {
    attachmentNotice =
      'Intentionally disconnected. Automatic attachment stays off until you enable it.';
  } else if (binding && binding.candidates.length > 1) {
    attachmentNotice =
      'Several Pi conversations match this review. Select one; no work has been sent.';
  }
  return (
    <section className="agents" aria-label="Pi integration">
      <h2>Pi conversation</h2>
      <p>
        Attachment sends no work. Use the Prunelle extension in the existing Pi conversation to
        connect. No terminal host is required.
      </p>
      <ErrorNotice error={query.error} />
      {query.isPending && <Notice>Loading Pi connections…</Notice>}
      {binding && (
        <>
          <p>
            Selection: {binding.mode.toLowerCase()}. {binding.candidates.length} connected cwd
            matches.
          </p>
          {!binding.agent_session_id && <Notice>{attachmentNotice}</Notice>}
          <label>
            Attached Pi
            <select
              aria-label="Attached Pi"
              value={binding.agent_session_id || ''}
              disabled={action.busy}
              onChange={(event) => {
                if (event.target.value)
                  choose({ mode: 'MANUAL', agent_session_id: event.target.value });
                else choose({ mode: 'DISCONNECTED' });
              }}
            >
              <option value="">No Pi selected</option>
              {query.data?.sessions.map((session) => (
                <option key={session.id} value={session.id}>
                  Pi {session.external_id} · {session.state}
                </option>
              ))}
            </select>
          </label>
          <div className="row">
            <button disabled={action.busy} onClick={() => choose({ mode: 'AUTO' })}>
              Use automatic attachment
            </button>
            <button
              disabled={action.busy || binding.mode === 'DISCONNECTED'}
              onClick={() => choose({ mode: 'DISCONNECTED' })}
            >
              Disconnect Pi
            </button>
          </div>
        </>
      )}
      {query.data?.sessions.map((session) => (
        <article className="card" key={session.id}>
          <strong>Pi {session.external_id}</strong> <span className="badge">{session.state}</span>
          <p>{session.cwd}</p>
          {session.queue_paused && (
            <Notice>
              Queue paused after an uncertain outcome. Inspect Pi before continuing.
              {session.id === binding?.agent_session_id && (
                <button
                  disabled={
                    action.busy || !session.queue_version || queue.isPending || !!queue.error
                  }
                  onClick={() => resume(session)}
                >
                  Resume queued comments
                </button>
              )}
            </Notice>
          )}
          <small>
            {session.id} · extension {session.version} · {session.model || 'No model reported'} ·{' '}
            {session.local ? 'local namespace' : 'manual selection only'}
          </small>
        </article>
      ))}
      {action.error && (
        <p className="error" role="alert">
          {action.error}
        </p>
      )}
      {binding?.agent_session_id && (
        <section aria-label="Pi queue">
          <h3>Pi queue · all reviews</h3>
          <p>
            Comments run one at a time in acceptance order, even when the desktop is closed.
            Cancelling a queued request keeps its comment. Changing Pi selection cancels this
            review’s unsent queue entries.
          </p>
          <ErrorNotice error={queue.error} />
          {queue.isPending && <Notice>Loading queued requests…</Notice>}
          <ol>
            {queue.data?.map((invocation) => (
              <li key={invocation.id}>
                <strong>{invocation.status}</strong> · Review {invocation.review_id} ·{' '}
                {invocation.id}
                <details>
                  <summary>Saved request</summary>
                  <pre>{invocation.prompt}</pre>
                </details>
                <CancelQueuedRequest api={api} invocation={invocation} />
              </li>
            ))}
          </ol>
          {queue.data?.length === 0 && <p>No outstanding requests.</p>}
        </section>
      )}
      <h3>Requests</h3>
      <p className="muted">
        Unavailable delivery/completion is unknown, not a claim that Pi stopped. Reconnection never
        replays dispatched work; never-dispatched queued comments can continue after reconnection.
      </p>
      <ErrorNotice error={invocations.error} />
      {invocations.data?.map((invocation) => (
        <article className="card" key={invocation.id}>
          <strong>{invocation.intent.replaceAll('_', ' ')}</strong>{' '}
          <span className="badge">{invocation.status}</span>
          <small>
            {invocation.id} · {invocation.revision_id} · Pi {invocation.agent_session_id}
          </small>
          {invocation.error && <p>{invocation.error}</p>}
          {invocation.result && <p>{invocation.result.summary}</p>}
        </article>
      ))}
    </section>
  );
}

export function CancelQueuedRequest({
  api,
  invocation,
}: {
  api: API;
  invocation: Model['AgentInvocation'];
}) {
  const action = useSubmit();
  if (invocation.status !== 'QUEUED') return null;
  return (
    <>
      <button
        className="link"
        type="button"
        disabled={action.busy}
        onClick={() => {
          void action.submit([invocation.id, 'cancel'], () =>
            result(
              api.POST('/v1/agent-invocations/{invocationId}/cancel', {
                params: { path: { invocationId: invocation.id } },
              }),
            ),
          );
        }}
      >
        Cancel queued request
      </button>
      {action.error && <small role="alert">{action.error}</small>}
    </>
  );
}

export function AgentRequestForm({
  api,
  reviewID,
  preview,
  query,
  open,
  onDraftChange,
  settings,
}: {
  api: API;
  reviewID: string;
  preview: AgentPreview;
  query: AgentQuery;
  open: boolean;
  onDraftChange: (dirty: boolean) => void;
  settings: () => void;
}) {
  const [targetID, setTargetID] = useState(preview.agentSessionID);
  const [uncertain, setUncertain] = useState(false);
  const [submitted, setSubmitted] = useState<Model['AgentInvocation']>();
  const action = useSubmit();
  useEffect(() => onDraftChange(uncertain || action.busy), [uncertain, action.busy, onDraftChange]);
  useEffect(() => () => onDraftChange(false), [onDraftChange]);
  const invocation = useQuery({
    queryKey: ['agent-invocation', submitted?.id],
    initialData: submitted,
    initialDataUpdatedAt: 0,
    enabled: open && !!submitted,
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/agent-invocations/{invocationId}', {
          params: { path: { invocationId: submitted!.id } },
          signal,
        }),
      ),
  });
  const context = useQuery({
    queryKey: ['revision-view', reviewID, preview.revisionID],
    enabled: open && preview.action.intent === 'IMPLEMENT',
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/reviews/{reviewId}/revision-view', {
          params: { path: { reviewId: reviewID }, query: { revision_id: preview.revisionID } },
          signal,
        }),
      ),
  });
  const discussions = useQuery({
    queryKey: ['implementation-discussions', reviewID],
    enabled: open && preview.action.intent === 'IMPLEMENT',
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/reviews/{reviewId}/discussions', {
          params: { path: { reviewId: reviewID } },
          signal,
        }),
      ),
  });
  const target = query.data?.sessions.find((session) => session.id === targetID);
  const selectionChanged = query.data?.binding.agent_session_id !== targetID;
  const current = invocation.data || submitted;
  const requestLabel = requestLabels[preview.action.intent];
  let sendLabel = requestLabel;
  if (action.busy) sendLabel = 'Sending…';
  else if (uncertain) sendLabel = 'Retry original request';
  async function send() {
    if (!targetID) return;
    const input = preview.action;
    const base = { agent_session_id: targetID, revision_id: preview.revisionID };
    let request: Model['CreateAgentInvocationRequest'];
    switch (input.intent) {
      case 'DISCUSS':
        request = {
          ...base,
          intent: input.intent,
          discussion_id: input.discussion.id,
          message_id: input.message.id,
        };
        break;
      case 'IMPLEMENT':
        request = { ...base, intent: input.intent, decision_id: input.decision.id };
        break;
      case 'PREPARE_REVIEW':
        request = { ...base, intent: input.intent };
        break;
    }
    await action.submit([reviewID, request], async (key) => {
      try {
        const created = await result(
          api.POST('/v1/reviews/{reviewId}/agent-invocations', {
            params: { path: { reviewId: reviewID }, header: { 'Idempotency-Key': key } },
            body: request,
          }),
        );
        setSubmitted(created);
        setUncertain(false);
      } catch (error) {
        setUncertain(!(error instanceof APIError && error.status >= 400 && error.status < 500));
        throw error;
      }
    });
  }
  return (
    <section className="agents" aria-label="Pi request preview">
      <h2>{requestLabel}</h2>
      <p>
        Captured context: <code>{preview.revisionID}</code>
      </p>
      <p>
        Target:{' '}
        {target ? `Pi ${target.external_id} · ${target.state}` : targetID || 'No attached Pi'}
      </p>
      {target && (
        <p className="muted">
          {target.cwd} · {target.model || 'No model reported'}
        </p>
      )}
      <ErrorNotice error={query.error} />
      {!targetID && <Notice>Attach Pi before sending. This preview sends nothing.</Notice>}
      {selectionChanged && (
        <Notice>The attached Pi changed. This request will not silently switch targets.</Notice>
      )}
      <div className="row">
        <button
          disabled={
            action.busy || uncertain || !!submitted || !query.data?.binding.agent_session_id
          }
          onClick={() => setTargetID(query.data!.binding.agent_session_id)}
        >
          Use attached Pi
        </button>
        <button onClick={settings}>Pi settings</button>
      </div>
      {preview.action.intent === 'DISCUSS' && (
        <>
          <p>
            Original discussion: <code>{preview.action.discussion.id}</code> · original revision{' '}
            <code>{preview.action.discussion.revision_id}</code>
          </p>
          <p>Original anchor: {anchorLabel(preview.action.discussion.anchor)}</p>
          <details>
            <summary>Original stored anchor</summary>
            <pre>{JSON.stringify(preview.action.discussion.anchor, null, 2)}</pre>
          </details>
          <Markdown>{preview.action.message.body}</Markdown>
          <p>
            The saved message joins this Pi’s durable comment queue. Pi can retrieve further context
            by discussion ID.
          </p>
        </>
      )}
      {preview.action.intent === 'PREPARE_REVIEW' && (
        <Notice>
          Generate topics, explanations and source links for eligible unassigned captured text
          files. Existing content and explicit file grouping stay unchanged. Valid output is
          published directly, not queued for approval.
        </Notice>
      )}
      {preview.action.intent === 'IMPLEMENT' && (
        <>
          <Notice>
            This explicitly authorizes implementation of the saved REQUEST_CHANGES decision in Pi’s
            live working environment, which may differ from this captured revision. It does not
            authorize commit, publication or deployment.
          </Notice>
          <p>
            Saved decision: <code>{preview.action.decision.id}</code>
          </p>
          <Markdown>{preview.action.decision.body}</Markdown>
          <ErrorNotice error={context.error} />
          <ErrorNotice error={discussions.error} />
          <h3>Actionable findings</h3>
          <ul>
            {preview.action.decision.finding_ids.map((id) => (
              <li key={id}>
                <code>{id}</code> ·{' '}
                {context.data?.graph?.findings.find((finding) => finding.id === id)?.title ||
                  'Semantic context unavailable'}
              </li>
            ))}
          </ul>
          <h3>Actionable discussions and original anchors</h3>
          <ul>
            {preview.action.decision.discussion_ids.map((id) => {
              const discussion = discussions.data?.find((discussion) => discussion.id === id);
              return (
                <li key={id}>
                  <code>{id}</code> ·{' '}
                  {discussion
                    ? `${anchorLabel(discussion.anchor)} · original revision ${discussion.revision_id}`
                    : 'Original context unavailable'}
                  {discussion && (
                    <details>
                      <summary>Original stored anchor</summary>
                      <pre>{JSON.stringify(discussion.anchor, null, 2)}</pre>
                    </details>
                  )}
                </li>
              );
            })}
          </ul>
        </>
      )}
      {preview.action.intent !== 'IMPLEMENT' && (
        <p className="muted">
          This asks for review, not code changes. Pi remains the existing live conversation with its
          normal tools and permissions; this is not an enforced sandbox.
        </p>
      )}
      {uncertain && (
        <Notice>
          The response was lost or failed. Inspect Pi and existing requests before retrying. Retry
          uses the original request identity; it cannot replay an already committed dispatch.
        </Notice>
      )}
      <button
        className="primary"
        disabled={
          action.busy ||
          !!submitted ||
          !targetID ||
          (!uncertain &&
            (selectionChanged ||
              (preview.action.intent !== 'DISCUSS' &&
                (target?.state !== 'READY' || target.queue_paused)) ||
              (preview.action.intent === 'IMPLEMENT' && (!context.data || !discussions.data))))
        }
        onClick={() => {
          void send();
        }}
      >
        {sendLabel}
      </button>
      {action.error && (
        <p className="error" role="alert">
          {action.error}
        </p>
      )}
      <ErrorNotice error={invocation.error} />
      {current && (
        <article className="card" aria-label="Pi request status">
          <strong>{current.status}</strong> <code>{current.id}</code>
          {current.error && <p>{current.error}</p>}
          {current.result && <p>{current.result.summary}</p>}
          <CancelQueuedRequest api={api} invocation={current} />
          {['SUCCEEDED', 'FAILED', 'UNKNOWN', 'CANCELLED'].includes(current.status) && (
            <button
              onClick={() => {
                if (
                  current.status === 'UNKNOWN' &&
                  !window.confirm(
                    'Pi may already have acted. Inspect the original request before starting a new one. Continue to a new preview?',
                  )
                )
                  return;
                setSubmitted(undefined);
              }}
            >
              New request with the same saved input
            </button>
          )}
        </article>
      )}
    </section>
  );
}
