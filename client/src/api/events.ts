import { EventSourceParserStream } from 'eventsource-parser/stream';
import { transport } from './client';

export async function readEvents(
  url: string,
  signal: AbortSignal,
  onEvent: (type: string, sequence: number) => void,
) {
  const response = await transport(url, { signal, headers: { Accept: 'text/event-stream' } });
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
      onEvent(value.event || 'message', sequence);
    }
  } finally {
    await reader.cancel().catch(() => {});
  }
}
