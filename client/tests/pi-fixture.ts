import { createParser } from 'eventsource-parser';
import type { Model } from '../src/api/client';
import { server } from './fixture.ts';

export class PiFixture {
  readonly externalID = 'browser-fixture-' + crypto.randomUUID();
  session!: Model['AgentSession'];
  requests: Model['AgentInvocation'][] = [];
  connectionID = '';
  private controller = new AbortController();
  private reading?: Promise<void>;

  private async request<T>(path: string, method: string, body?: unknown): Promise<T> {
    const response = await fetch(server + path, {
      method,
      headers: { 'Content-Type': 'application/json', 'Prunelle-Agent': `pi:${this.externalID}` },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) throw new Error(`${response.status}: ${await response.text()}`);
    return response.json();
  }

  async connect(cwd: string) {
    this.session = await this.request('/v1/agent-sessions', 'POST', {
      external_id: this.externalID,
      cwd,
      version: '0.4.0',
    });
    const response = await fetch(`${server}/v1/agent-sessions/${this.session.id}/connect`, {
      headers: { 'Prunelle-Agent': `pi:${this.externalID}` },
      signal: this.controller.signal,
    });
    if (!response.ok || !response.body) throw new Error(`Pi stream: ${response.status}`);
    let ready!: () => void;
    let failed!: (error: unknown) => void;
    const handshake = new Promise<void>((resolve, reject) => {
      ready = resolve;
      failed = reject;
    });
    const parser = createParser({
      onEvent: (event) => {
        if (event.event === 'ready') {
          this.connectionID = JSON.parse(event.data).connection_id;
          ready();
        }
        if (event.event === 'request') this.requests.push(JSON.parse(event.data));
      },
    });
    const reader = response.body.getReader();
    this.reading = (async () => {
      const decoder = new TextDecoder();
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          parser.feed(decoder.decode(value, { stream: true }));
        }
      } finally {
        reader.releaseLock();
      }
    })().catch((error) => {
      if (!this.controller.signal.aborted) {
        failed(error);
        throw error;
      }
    });
    await handshake;
    await this.ready();
    return this;
  }

  async ready() {
    return this.request(`/v1/agent-sessions/${this.session.id}/state`, 'PUT', {
      connection_id: this.connectionID,
      ready: true,
      model: 'fixture/browser-reviewer',
      context_id: 'existing-browser-conversation',
    });
  }
  async complete(
    invocation: Model['AgentInvocation'],
    result: Model['CompleteAgentInvocationRequest'],
  ) {
    const completed = await this.request<Model['AgentInvocation']>(
      `/v1/agent-invocations/${invocation.id}/complete`,
      'POST',
      result,
    );
    await this.ready();
    return completed;
  }
  async close() {
    this.controller.abort();
    await this.reading;
  }
}
