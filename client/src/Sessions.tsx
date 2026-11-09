import { useQuery } from '@tanstack/react-query';
import { result, type API } from './api/client';
import { useSubmit } from './hooks';
import { ErrorNotice, Notice } from './Views';

export function Sessions({ api, reviewID }: { api: API; reviewID: string }) {
  const query = useQuery({
    queryKey: ['sessions'],
    queryFn: async ({ signal }) => {
      const [backends, runtimes] = await Promise.all([
        result(api.GET('/v1/session-backends', { signal })),
        result(api.GET('/v1/runtime-sessions', { signal })),
      ]);
      return { backends, runtimes };
    },
    refetchInterval: 5000,
  });
  const action = useSubmit();
  function toggleAssociation(runtimeID: string, linked: boolean) {
    const params = { path: { reviewId: reviewID, runtimeSessionId: runtimeID } };
    return action.submit([runtimeID, linked], () => {
      if (linked) {
        return result(
          api.DELETE('/v1/reviews/{reviewId}/runtime-sessions/{runtimeSessionId}', {
            params,
          }),
        );
      }
      return result(
        api.PUT('/v1/reviews/{reviewId}/runtime-sessions/{runtimeSessionId}', { params }),
      );
    });
  }
  return (
    <section className="sessions" aria-label="Runtime sessions">
      <h2>Runtime sessions</h2>
      <p className="muted">
        Optional terminals, not agent conversations. Associations are explicit. Refreshes every 5
        seconds.
      </p>
      <ErrorNotice error={query.error} />
      {query.isPending && <Notice>Loading session integrations…</Notice>}
      {query.data && !query.data.backends.length && (
        <Notice>
          No session backend is configured. Review, discussions and decisions work independently.
        </Notice>
      )}
      {query.data?.backends.map((b) => (
        <article className="card" key={b.id}>
          <strong>
            {b.name} · {b.kind}
          </strong>
          <span className="badge">{b.health.state}</span>
          {b.health.error && <p>{b.health.error}</p>}
        </article>
      ))}
      {query.data?.runtimes.map((r) => {
        const linked = r.review_ids.includes(reviewID);
        let associationLabel = 'Not associated with this review';
        if (linked) associationLabel = 'Explicitly associated with this review';
        else if (r.suggested_review_ids.includes(reviewID)) {
          associationLabel = 'Suggested by working directory — not associated';
        }
        return (
          <article className="card" key={r.id}>
            <strong>{r.label}</strong>
            <span className="badge">{r.state}</span>
            <p>{r.cwd}</p>
            <small>
              {r.id} · {r.backend_kind} · {r.harness_hint || 'Harness unknown'}
            </small>
            <details>
              <summary>Runtime identity</summary>
              <p>
                Backend: {r.backend_id}
                <br />
                External ID: {r.external_id}
                <br />
                Process: {r.process || 'Unknown'}
                <br />
                Local: {r.local ? 'yes' : 'no'}
                <br />
                Capabilities: {r.capabilities.join(', ') || 'none'}
                <br />
                Last seen: {new Date(r.last_seen_at).toLocaleString()}
              </p>
            </details>
            <p>{associationLabel}</p>
            <button
              disabled={action.busy}
              onClick={() => {
                void toggleAssociation(r.id, linked);
              }}
            >
              {linked ? 'Remove association' : 'Associate with review'}
            </button>
          </article>
        );
      })}
      {action.error && (
        <p className="error" role="alert">
          {action.error}
        </p>
      )}
    </section>
  );
}
