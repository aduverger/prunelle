import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { APIError, desktop, result, type API } from './api/client';
import { readEvents } from './api/events';

export function useReview(api: API, reviewID: string) {
  return useQuery({
    queryKey: ['review', reviewID],
    queryFn: async ({ signal }) => {
      const params = { path: { reviewId: reviewID } };
      const [
        review,
        topics,
        revisions,
        findings,
        intents,
        changes,
        evidence,
        attention,
        discussions,
        decisions,
      ] = await Promise.all([
        result(api.GET('/v1/reviews/{reviewId}', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/topics', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/revisions', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/findings', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/intents', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/changes', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/evidence', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/attention', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/discussions', { params, signal })),
        result(api.GET('/v1/reviews/{reviewId}/decisions', { params, signal })),
      ]);
      return {
        review,
        topics,
        revisions,
        findings,
        intents,
        changes,
        evidence,
        attention,
        discussions,
        decisions,
      };
    },
  });
}
export type ReviewData = NonNullable<ReturnType<typeof useReview>['data']>;

export function useConnection(server: string) {
  const cache = useQueryClient();
  const [status, setStatus] = useState('Connecting');
  useEffect(() => {
    const controller = new AbortController();
    let sequence: number | undefined;
    let timer: ReturnType<typeof setTimeout>;
    async function connect() {
      try {
        const base = desktop ? server : '/api';
        await readEvents(
          base + '/v1/events' + (sequence === undefined ? '' : '?after=' + sequence),
          controller.signal,
          (_type, next) => {
            sequence = next;
            setStatus('Connected');
            void cache.invalidateQueries();
          },
        );
      } catch {
        if (!controller.signal.aborted) {
          setStatus('Disconnected · reconnecting');
          timer = setTimeout(() => {
            void connect();
          }, 1000);
        }
      }
    }
    void connect();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [cache, server]);
  return status;
}

// An unchanged explicit retry keeps its key, including when the response was lost.
export function useSubmit() {
  const cache = useQueryClient();
  const last = useRef<{ signature: string; key: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function submit(identity: unknown, send: (key: string) => Promise<unknown>) {
    if (busy) return false;
    const signature = JSON.stringify(identity);
    if (last.current?.signature !== signature)
      last.current = { signature, key: crypto.randomUUID() };
    setBusy(true);
    setError('');
    try {
      await send(last.current.key);
      last.current = null;
      await cache.invalidateQueries();
      return true;
    } catch (e) {
      if (e instanceof APIError && e.status === 412) await cache.invalidateQueries();
      setError(
        e instanceof APIError && e.status === 412
          ? 'This record changed. Latest state is reloading; review it before retrying. Your draft is kept.'
          : e instanceof Error
            ? e.message
            : String(e),
      );
      return false;
    } finally {
      setBusy(false);
    }
  }
  return { submit, busy, error };
}
