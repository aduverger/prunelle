import { connect } from 'node:net';
import { createInterface } from 'node:readline';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, execFileSync } from 'node:child_process';
import assert from 'node:assert/strict';
import { Fixture, buildServer, server as target } from '../tests/fixture.ts';
import { PiFixture } from '../tests/pi-fixture.ts';

const serverBuild = buildServer();
const fixture = new Fixture(serverBuild.binary);
const scratch = mkdtempSync(join(tmpdir(), 'prunelle-native-'));
const socketPath = join(scratch, 'automation.sock');
const binary = join(scratch, 'prunelle');
execFileSync('go', ['build', '-tags', 'e2e', '-o', binary, '.'], { stdio: 'inherit' });
let app;
let pi;
let log = '';
let socket;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
try {
  await fixture.start();
  await fixture.seed();
  pi = new PiFixture();
  await pi.connect(
    fixture.review.repositories.find((repository) => repository.label === 'api').root_path,
  );
  app = spawn('xvfb-run', ['-a', '-s', '-screen 0 1600x1100x24', binary], {
    detached: true,
    env: {
      ...process.env,
      MYGO_AUTOMATION: socketPath,
      XDG_DATA_HOME: scratch,
      XDG_CONFIG_HOME: scratch,
      WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS: '1',
    },
  });
  app.stderr.on('data', (chunk) => {
    log += chunk;
  });
  for (let i = 0; i < 100; i++) {
    try {
      socket = await new Promise((resolve, reject) => {
        const s = connect(socketPath);
        s.once('connect', () => resolve(s));
        s.once('error', reject);
      });
      break;
    } catch {
      if (app.exitCode !== null) throw new Error(log);
      await sleep(100);
    }
  }
  assert(socket, 'Desktop automation did not start: ' + log);
  const lines = createInterface({ input: socket });
  const waiting = new Map();
  let sequence = 0;
  lines.on('line', (line) => {
    const response = JSON.parse(line);
    const pending = waiting.get(response.id);
    if (!pending) return;
    waiting.delete(response.id);
    if (response.error) pending.reject(new Error(JSON.stringify(response.error)));
    else pending.resolve(response.result);
  });
  const call = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const id = ++sequence;
      waiting.set(id, { resolve, reject });
      socket.write(JSON.stringify({ id, method, params }) + '\n');
      setTimeout(() => {
        if (waiting.delete(id)) reject(new Error('Automation timed out: ' + method + '\n' + log));
      }, 15_000).unref();
    });
  const evaluate = async (script) => (await call('eval', { script })).value;
  const waitFor = async (script) => {
    for (let i = 0; i < 100; i++) {
      if (await evaluate(script)) return;
      await sleep(100);
    }
    throw new Error(
      'UI did not reach expected state: ' +
        script +
        '\n' +
        (await evaluate('document.body.innerText')) +
        '\n' +
        log,
    );
  };
  for (let i = 0; i < 100; i++) {
    if ((await call('windows')).windows.length) break;
    await sleep(100);
  }
  await call('idle', { timeout: 10_000 });
  await waitFor("!!document.querySelector('.server-settings input')");
  await evaluate(
    `const input = document.querySelector('.server-settings input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, ${JSON.stringify(target)}); input.dispatchEvent(new Event('input', {bubbles:true})); true`,
  );
  await evaluate("document.querySelector('.server-settings form').requestSubmit(); true");
  await waitFor(
    "document.body.innerText.includes('Asynchronous upload') && document.body.innerText.includes('Connected')",
  );
  await waitFor('!!document.querySelector(\'[aria-label="Inspect POST /documents"]\')');
  await evaluate(
    'document.querySelector(\'[aria-label="Inspect POST /documents"]\').dispatchEvent(new MouseEvent("click", {bubbles:true})); true',
  );
  await waitFor(
    "!!document.querySelector('diffs-container')?.shadowRoot?.querySelector('[data-content] [data-line]')",
  );
  assert(
    (
      await evaluate(
        "Array.from(document.querySelector('diffs-container').shadowRoot.querySelectorAll('[data-content]')).map(node => node.textContent).join('\\n')",
      )
    ).includes('PENDING'),
  );
  await waitFor("document.querySelector('.code-review')?.dataset.highlighter === 'highlights'");
  await evaluate(
    "Array.from(document.querySelectorAll('button')).find(b=>b.textContent==='Discuss topic').click(); true",
  );
  await evaluate(
    "const t=document.querySelector('[aria-label=\"New discussion\"]'); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(t,'Native WebKit comment'); t.dispatchEvent(new Event('input',{bubbles:true})); true",
  );
  await evaluate("document.querySelector('.composer').requestSubmit(); true");
  await waitFor(
    "!!Array.from(document.querySelectorAll('.message')).find(e=>e.textContent.includes('Native WebKit comment'))",
  );
  assert.equal(
    await evaluate('!!document.querySelector(\'dialog[aria-label="Pi request"]\')?.open'),
    false,
    'Posting must not require a Pi preview',
  );
  for (let i = 0; i < 100 && !pi.requests.length; i++) await sleep(100);
  assert.equal(pi.requests.length, 1);
  assert.equal(pi.requests[0].intent, 'DISCUSS');
  await pi.complete(pi.requests[0], {
    invocation_id: pi.requests[0].id,
    discussion_id: pi.requests[0].discussion_id,
    body: 'Native Pi reply on the original saved thread.',
  });
  await waitFor("document.querySelector('.thread')?.textContent.includes('Pi · SUCCEEDED')");
  await waitFor(
    "Array.from(document.querySelectorAll('.message')).some(message => message.textContent.includes('Native Pi reply on the original saved thread.'))",
  );
  await evaluate(
    "Array.from(document.querySelectorAll('.nav-tabs button')).find(button => button.textContent === 'Files').click(); true",
  );
  await waitFor(
    `!!document.querySelector('file-tree-container')?.shadowRoot?.querySelector('[role="treeitem"][data-item-path="docs/async-upload.md"]')`,
  );
  await evaluate(
    `document.querySelector('file-tree-container').shadowRoot.querySelector('[role="treeitem"][data-item-path="docs/async-upload.md"]').click(); true`,
  );
  await waitFor(
    `!!document.querySelector('diffs-container')?.shadowRoot?.querySelector('[data-content]')?.textContent.includes('Uploads return 202 Accepted')`,
  );
  await evaluate("document.querySelector('.line-comment-form').open = true; true");
  await evaluate(
    "const input = document.querySelector('[aria-label=\"Start line\"]'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'3'); input.dispatchEvent(new Event('input',{bubbles:true})); true",
  );
  await evaluate("document.querySelector('.line-comment-form form').requestSubmit(); true");
  await waitFor("!!document.querySelector('.code-review .composer')");
  await evaluate(
    "const t=document.querySelector('[aria-label=\"New discussion\"]'); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(t,'Native inline code comment'); t.dispatchEvent(new Event('input',{bubbles:true})); true",
  );
  await evaluate("document.querySelector('.composer').requestSubmit(); true");
  await waitFor(
    "Array.from(document.querySelectorAll('.code-review .thread')).some(thread => thread.textContent.includes('Native inline code comment') && thread.textContent.includes('HEAD'))",
  );
  const threads = await fixture.get(`/v1/reviews/${fixture.review.id}/discussions`);
  const inlineThread = threads.find(
    (thread) => thread.messages[0].body === 'Native inline code comment',
  );
  assert(inlineThread, 'Inline comment was not persisted');
  assert.equal(inlineThread.revision_id, fixture.revision.id);
  assert.equal(inlineThread.anchor.type, 'CODE_RANGE');
  const range = inlineThread.anchor.code_range;
  assert.equal(
    range.repository_id,
    fixture.revision.snapshots.find((snapshot) => snapshot.repository_label === 'api')
      .repository_id,
  );
  assert.equal(range.path, 'docs/async-upload.md');
  assert.equal(range.side, 'HEAD');
  assert.equal(range.start_line, 3);
  assert.equal(range.end_line, 3);
  for (let i = 0; i < 100 && pi.requests.length < 2; i++) await sleep(100);
  assert.equal(pi.requests.length, 2);
  assert.equal(pi.requests[1].discussion_id, inlineThread.id);
  const externalReply = await globalThis.fetch(
    `${target}/v1/discussions/${inlineThread.id}/messages`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ body: 'An external reply streamed into native WebKit.' }),
    },
  );
  assert.equal(externalReply.status, 201);
  await waitFor(
    "Array.from(document.querySelectorAll('.code-review .thread')).some(thread => thread.textContent.includes('An external reply streamed into native WebKit.'))",
  );
  await evaluate(
    "Array.from(document.querySelectorAll('button')).find(button => button.textContent === 'Review decision').click(); true",
  );
  await waitFor('!!document.querySelector(\'dialog[aria-label="Review decision"]\').open');
  await evaluate('document.querySelector(\'[aria-label="Close Review decision"]\').click(); true');
  await evaluate(
    "Array.from(document.querySelectorAll('button')).find(button => button.textContent === 'Settings').click(); true",
  );
  await waitFor(
    `document.querySelector('[aria-label="Pi integration"]')?.textContent.includes(${JSON.stringify(pi.externalID)})`,
  );
  await evaluate(
    'document.querySelector(\'[aria-label="Close Settings · Integrations"]\').click(); true',
  );
  const png = await call('capture');
  if (png.png) writeFileSync(resolve('native-smoke.png'), Buffer.from(png.png, 'base64'));
  console.log(
    'Native MyGo/WebKit: bundled UI, Go-backed REST/SSE, automatic comment dispatch/correlated reply/integration panel, Highlights, Pierre tree navigation, inline stored-code comments and decision dialog passed.',
  );
  lines.close();
} finally {
  socket?.destroy();
  if (app) {
    try {
      process.kill(-app.pid, 'SIGTERM');
    } catch {
      /* Already exited. */
    }
  }
  await pi?.close();
  await fixture.close();
  serverBuild.close();
  rmSync(scratch, { recursive: true, force: true });
}
