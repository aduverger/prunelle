import createClient from 'openapi-fetch';
import { fetch as desktopFetch } from '@mygo-plugins/fetch';
import { isMyGo } from 'mygo-runtime';
import type { components, paths } from './schema';

export type Model = components['schemas'];
export type DiagramNode = NonNullable<Model['Representation']['diagram']>['nodes'][number];
export type AnchorInput = Model['CreateDiscussionRequest']['anchor'];
export type CodeRangeInput = NonNullable<AnchorInput['code_range']>;
export const desktop = isMyGo();
export const transport: typeof fetch = desktop ? desktopFetch : globalThis.fetch.bind(globalThis);

export function serverURL(value: string) {
  const url = new URL(value);
  const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
  if (
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/' ||
    !(
      (local && ['http:', 'https:'].includes(url.protocol)) ||
      (url.protocol === 'https:' && url.hostname.endsWith('.ts.net'))
    )
  ) {
    throw new Error(
      'Use a loopback HTTP(S) URL or a Tailscale HTTPS URL, without a path or credentials.',
    );
  }
  return url.origin;
}

export function apiFor(server: string) {
  const baseUrl = desktop ? serverURL(server) : '/api';
  return createClient<paths>({ baseUrl, fetch: transport });
}
export type API = ReturnType<typeof apiFor>;

export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function result<T>(
  pending: Promise<{ data?: T; error?: Model['Error']; response: Response }>,
): Promise<T> {
  const { data, error, response } = await pending;
  if (!response.ok)
    throw new APIError(
      response.status,
      [error?.error?.message ?? response.statusText, ...(error?.error?.details ?? [])].join('\n'),
    );
  return data as T;
}
