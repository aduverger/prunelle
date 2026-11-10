import { test, expect } from '@playwright/test';
import { Fixture, buildServer, server } from './fixture';
import { PiFixture } from './pi-fixture';
import type { Model } from '../src/api/client';

let build: ReturnType<typeof buildServer>;
let fixture: Fixture;
let peers: PiFixture[];

test.beforeAll(() => {
  build = buildServer();
});
test.afterAll(() => build.close());
test.beforeEach(async () => {
  fixture = new Fixture(build.binary);
  peers = [];
  await fixture.start();
  await fixture.seed();
});
test.afterEach(async ({ page }) => {
  await page.close();
  await Promise.all(peers.map((peer) => peer.close()));
  await fixture.close();
});
async function pi() {
  const peer = new PiFixture();
  peers.push(peer);
  return peer.connect(
    fixture.review.repositories.find((repository) => repository.label === 'api')!.root_path,
  );
}
async function mutate<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(server + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  expect(response.ok, await response.clone().text()).toBe(true);
  return response.json();
}

test('attachment is visible, ambiguity requires selection and deliberate disconnection stays authoritative', async ({
  page,
}) => {
  const first = await pi();
  await page.goto('/');
  await expect(page.getByTitle(first.externalID, { exact: true })).toContainText('READY');
  const second = await pi();
  const original = await fixture.get<Model['AgentBinding']>(
    `/v1/reviews/${fixture.review.id}/agent-binding`,
  );
  expect(original.agent_session_id).toBe(first.session.id);
  const ambiguous = await mutate<Model['Review']>('/v1/reviews', {
    path: first.session.cwd,
    title: 'Choose between existing Pi conversations',
  });
  await page.getByLabel('Feature review', { exact: true }).selectOption(ambiguous.id);
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  const settings = page.getByRole('dialog', { name: 'Settings · Integrations', exact: true });
  await expect(settings).toContainText('Several Pi conversations match this review');
  await settings.getByLabel('Attached Pi', { exact: true }).selectOption(second.session.id);
  await expect(settings.getByLabel('Attached Pi', { exact: true })).toHaveValue(second.session.id);
  await first.close();
  await settings.getByRole('button', { name: 'Disconnect Pi', exact: true }).click();
  await expect(settings).toContainText('Intentionally disconnected');
  await page.getByRole('button', { name: 'Close Settings · Integrations', exact: true }).click();
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.getByText('Pi · Unattached', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(settings).toContainText('Intentionally disconnected');
  await expect(settings.getByLabel('Attached Pi', { exact: true })).toHaveValue('');
  await settings.getByRole('button', { name: 'Use automatic attachment', exact: true }).click();
  await expect(settings.getByLabel('Attached Pi', { exact: true })).toHaveValue(second.session.id);
  expect(first.requests).toHaveLength(0);
  expect(second.requests).toHaveLength(0);
});

test('Ask Pi previews a saved question and an unchanged lost-response retry survives capture and independent drafts', async ({
  page,
}) => {
  const peer = await pi();
  await page.goto('/');
  await expect(page.getByTitle(peer.externalID, { exact: true })).toContainText('READY');
  await page.getByRole('button', { name: 'Discuss topic', exact: true }).click();
  await page
    .getByRole('textbox', { name: 'New discussion', exact: true })
    .fill('Explain the captured retry, without changing code.');
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  const thread = page.locator('.thread');
  await expect(thread).toContainText('Explain the captured retry');
  const saved = (
    await fixture.get<Model['Discussion'][]>(`/v1/reviews/${fixture.review.id}/discussions`)
  )[0];
  await thread.locator('textarea').fill('Keep this independent manual reply draft.');
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await page
    .getByRole('textbox', { name: 'Decision notes', exact: true })
    .fill('Keep this independent verdict draft.');
  await page.getByRole('button', { name: 'Close Review decision', exact: true }).click();
  await thread.getByRole('button', { name: 'Ask Pi', exact: true }).click();
  const preview = page.getByRole('dialog', { name: 'Pi request', exact: true });
  await expect(preview).toContainText(saved.id);
  await expect(preview).toContainText(fixture.revision.id);
  expect(peer.requests).toHaveLength(0);
  let firstKey = '';
  await page.route('**/agent-invocations', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    firstKey = route.request().headers()['idempotency-key'];
    expect((await route.fetch()).status()).toBe(202);
    await route.abort('failed');
  });
  await preview.getByRole('button', { name: 'Ask Pi', exact: true }).click();
  await expect(preview.getByRole('alert')).toBeVisible();
  await expect.poll(() => peer.requests.length).toBe(1);
  await expect(
    preview.getByRole('button', { name: 'Retry original request', exact: true }),
  ).toBeEnabled();
  const later = await mutate<Model['Revision']>(`/v1/reviews/${fixture.review.id}/revisions`, {});
  await page.getByRole('button', { name: 'Close Pi request', exact: true }).click();
  await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(later.id);
  await page.getByRole('button', { name: /Configuration and tests/ }).click();
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Decision notes', exact: true })).toHaveValue(
    'Keep this independent verdict draft.',
  );
  await page.getByRole('button', { name: 'Close Review decision', exact: true }).click();
  await page.getByRole('button', { name: 'Resume Pi request', exact: true }).click();
  await expect(preview).toContainText(fixture.revision.id);
  await page.unroute('**/agent-invocations');
  const retry = page.waitForRequest(
    (request) => request.method() === 'POST' && request.url().endsWith('/agent-invocations'),
  );
  const replay = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' && response.url().endsWith('/agent-invocations'),
  );
  await preview.getByRole('button', { name: 'Retry original request', exact: true }).click();
  const retried = await retry;
  expect(retried.headers()['idempotency-key']).toBe(firstKey);
  expect(retried.headers()['prunelle-agent']).toBeUndefined();
  expect(retried.postDataJSON()).toMatchObject({
    intent: 'DISCUSS',
    revision_id: fixture.revision.id,
    discussion_id: saved.id,
    message_id: saved.messages[0].id,
    agent_session_id: peer.session.id,
  });
  expect((await replay).headers()['idempotent-replayed']).toBe('true');
  await peer.complete(peer.requests[0], {
    invocation_id: peer.requests[0].id,
    discussion_id: saved.id,
    body: 'An attributed explanation of the original captured retry.',
  });
  await expect(preview.getByRole('article', { name: 'Pi request status' })).toContainText(
    'SUCCEEDED',
  );
  await page.getByRole('button', { name: 'Close Pi request', exact: true }).click();
  await page.getByRole('button', { name: /Asynchronous upload/ }).click();
  await expect(thread).toContainText('An attributed explanation of the original captured retry.');
  await expect(thread).toContainText(`pi:${peer.externalID}`);
  await expect(thread.locator('textarea')).toHaveValue('Keep this independent manual reply draft.');
  expect(peer.requests).toHaveLength(1);
  const persisted = await fixture.get<Model['Discussion']>(`/v1/discussions/${saved.id}`);
  expect(persisted.messages).toHaveLength(2);
  expect(persisted.revision_id).toBe(fixture.revision.id);
});

test('Prepare review directly publishes navigable generated explanations and canonical captured code', async ({
  page,
}) => {
  const peer = await pi();
  await page.goto('/');
  await expect(page.getByTitle(peer.externalID, { exact: true })).toContainText('READY');
  await page.getByRole('button', { name: 'Prepare review', exact: true }).click();
  const preview = page.getByRole('dialog', { name: 'Pi request', exact: true });
  await expect(preview).toContainText('published directly, not queued for approval');
  expect(peer.requests).toHaveLength(0);
  let releaseStatus!: () => void;
  let statusCaptured!: () => void;
  const release = new Promise<void>((resolve) => {
    releaseStatus = resolve;
  });
  const capturedStatus = new Promise<void>((resolve) => {
    statusCaptured = resolve;
  });
  let heldStatus = false;
  await page.route('**/v1/agent-invocations/*', async (route) => {
    if (route.request().method() !== 'GET' || heldStatus) return route.continue();
    heldStatus = true;
    const pending = await route.fetch();
    expect((await pending.json()).status).toBe('PENDING');
    statusCaptured();
    await release;
    await route.fulfill({ response: pending });
  });
  await preview.getByRole('button', { name: 'Prepare review', exact: true }).click();
  await expect.poll(() => peer.requests.length).toBe(1);
  await capturedStatus;
  await expect(page.getByTitle(peer.externalID, { exact: true })).toContainText('BUSY');
  const invocation = peer.requests[0];
  expect(invocation.intent).toBe('PREPARE_REVIEW');
  const input = await fixture.get<Model['AgentInvocationContext']>(
    `/v1/agent-invocations/${invocation.id}/context`,
  );
  const file = input.eligible_files.find((file) => file.path === 'package.json')!;
  expect(file).toBeDefined();
  await peer.complete(invocation, {
    generation: {
      topics: [
        {
          key: 'dependencies',
          title: 'Feature build and dependencies',
          summary: 'Understand the captured dependency change',
          explanation:
            '**Captured dependency change.** This is inferred context, not a test result.',
        },
      ],
      files: [
        {
          repository_id: file.repository_id,
          path: file.path,
          topic_keys: ['dependencies'],
          topic_ids: [],
        },
      ],
    },
  });
  await expect(page.getByTitle(peer.externalID, { exact: true })).toContainText('READY');
  releaseStatus();
  await expect(preview.getByRole('article', { name: 'Pi request status' })).toContainText(
    'SUCCEEDED',
  );
  await page.getByRole('button', { name: 'Close Pi request', exact: true }).click();
  await page.getByRole('button', { name: 'Feature build and dependencies', exact: false }).click();
  await expect(
    page.getByRole('heading', { name: 'Feature build and dependencies', exact: true }),
  ).toBeVisible();
  await page
    .getByRole('navigation', { name: 'Primary view' })
    .getByRole('button', { name: 'Text', exact: true })
    .click();
  await expect(page.locator('.text-section .markdown strong')).toHaveText(
    'Captured dependency change.',
  );
  await page
    .getByRole('navigation', { name: 'Secondary view' })
    .getByRole('button', { name: 'Code', exact: true })
    .click();
  await page
    .locator('.file-choices')
    .getByRole('button', { name: 'package.json', exact: true })
    .click();
  await expect(page.getByTestId('file-diff')).toContainText('package.json');
  await expect(page.getByTestId('file-diff')).toContainText(fixture.revision.id);
  expect(
    await fixture.get<Model['Decision'][]>(`/v1/reviews/${fixture.review.id}/decisions`),
  ).toHaveLength(0);
  expect(peer.requests).toHaveLength(1);
});

test('implementation previews the saved decision and original anchors, dispatches deliberately and preserves a dirty verdict draft', async ({
  page,
}) => {
  const peer = await pi();
  const repository = fixture.revision.snapshots.find(
    (snapshot) => snapshot.repository_label === 'api',
  )!;
  const discussion = await mutate<Model['Discussion']>(
    `/v1/reviews/${fixture.review.id}/discussions`,
    {
      anchor: {
        type: 'CODE_RANGE',
        code_range: {
          repository_id: repository.repository_id,
          path: 'src/routes/upload.ts',
          side: 'HEAD',
          start_line: 8,
          end_line: 9,
        },
      },
      body: 'Preserve this original retry anchor.',
    },
  );
  await page.goto('/');
  await expect(page.getByTitle(peer.externalID, { exact: true })).toContainText('READY');
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await page.getByLabel('Verdict').selectOption('REQUEST_CHANGES');
  await page.getByLabel('Retries can repeat processing side effects', { exact: true }).check();
  await page.getByLabel(/Preserve this original retry anchor/).check();
  await page
    .getByRole('textbox', { name: 'Decision notes', exact: true })
    .fill('Implement only the saved actionable retry request.');
  await page.getByRole('button', { name: 'Submit decision', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Preview implementation request', exact: true }),
  ).toBeVisible();
  const saved = (
    await fixture.get<Model['Decision'][]>(`/v1/reviews/${fixture.review.id}/decisions`)
  )[0];
  expect(peer.requests).toHaveLength(0);
  await page
    .getByRole('textbox', { name: 'Decision notes', exact: true })
    .fill('Keep the next verdict separate from implementation.');
  await page.getByRole('button', { name: 'Preview implementation request', exact: true }).click();
  const preview = page.getByRole('dialog', { name: 'Pi request', exact: true });
  await expect(preview).toContainText(saved.id);
  await expect(preview).toContainText(fixture.ids.findings['F-1']);
  await expect(preview).toContainText('src/routes/upload.ts:8–9 (HEAD)');
  await expect(preview).toContainText('live working environment');
  const later = await mutate<Model['Revision']>(`/v1/reviews/${fixture.review.id}/revisions`, {});
  await expect(preview).toContainText(fixture.revision.id);
  expect(peer.requests).toHaveLength(0);
  await preview.getByRole('button', { name: 'Send implementation request', exact: true }).click();
  await expect.poll(() => peer.requests.length).toBe(1);
  expect(peer.requests[0]).toMatchObject({
    intent: 'IMPLEMENT',
    revision_id: fixture.revision.id,
    decision_id: saved.id,
    agent_session_id: peer.session.id,
  });
  expect(peer.requests[0].prompt).toContain(discussion.anchor.code_range!.blob_hash);
  await peer.complete(peer.requests[0], {
    body: 'Controlled implementation handoff finished; capture and verdict remain manual.',
  });
  await expect(preview.getByRole('article', { name: 'Pi request status' })).toContainText(
    'SUCCEEDED',
  );
  await page.getByRole('button', { name: 'Close Pi request', exact: true }).click();
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Decision notes', exact: true })).toHaveValue(
    'Keep the next verdict separate from implementation.',
  );
  await expect(page.getByRole('region', { name: 'Review decision', exact: true })).toContainText(
    'Your decision draft still targets revision 1.',
  );
  const current = await fixture.get<Model['Review']>(`/v1/reviews/${fixture.review.id}`);
  expect(current.current_revision_id).toBe(later.id);
  expect((await fixture.get<Model['Discussion']>(`/v1/discussions/${discussion.id}`)).status).toBe(
    'OPEN',
  );
  expect(
    await fixture.get<Model['Decision'][]>(`/v1/reviews/${fixture.review.id}/decisions`),
  ).toHaveLength(1);
});
