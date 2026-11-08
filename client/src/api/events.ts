import { EventSourceParserStream } from 'eventsource-parser/stream';
import type { API } from './client';

export async function readEvents(
  api: API,
  signal: AbortSignal,
  after: number | undefined,
  onSequence: (sequence: number) => void,
) {
  const { response } = await api.GET('/v1/events', {
    params: { query: { after } },
    signal,
    headers: { Accept: 'text/event-stream' },
    parseAs: 'stream',
  });
  if (!response.ok || !response.body)
    throw new Error('Event stream unavailable: ' + response.status);
  const reader = response.body
    .pipeThrough(new TextDecoderStream())
    .pipeThrough(new EventSourceParserStream())
    .getReader();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) throw new Error('Event stream disconnected');
      const sequence = Number(value.id || JSON.parse(value.data).sequence);
      if (!Number.isSafeInteger(sequence) || sequence < 0)
        throw new Error('Invalid event sequence');
      onSequence(sequence);
    }
  } finally {
    await reader.cancel().catch(() => {});
  }
}
