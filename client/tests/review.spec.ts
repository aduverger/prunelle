import { test, expect, type Page } from '@playwright/test';
import { mkdirSync, rmSync, unlinkSync, writeFileSync } from 'node:fs';
import { Fixture, buildServer, server } from './fixture';
import type { Model } from '../src/api/client';

function storedDiff(page: Page, path: string, repositoryID?: string) {
  const identity =
    `[data-source-path=${JSON.stringify(path)}]` +
    (repositoryID ? `[data-repository-id=${JSON.stringify(repositoryID)}]` : '');
  return page
    .getByTestId('file-diff')
    .locator('diffs-container')
    .filter({ has: page.locator(identity) });
}

let build: ReturnType<typeof buildServer>;
let fixture: Fixture;
test.beforeAll(() => {
  build = buildServer();
});
test.afterAll(() => build.close());
test.beforeEach(async () => {
  fixture = new Fixture(build.binary);
  await fixture.start();
  await fixture.seed();
});
test.afterEach(async ({ page }) => {
  await page.close();
  await fixture.close();
});
test('topics, grounding, exhaustive source and persistent perspectives', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Asynchronous upload', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Inspect POST /documents', exact: true }).click();
  await expect(page.getByTestId('file-diff')).toContainText('src/routes/upload.ts');
  await expect(
    page.getByTestId('file-diff').locator('[data-line="9"][data-line-type="change-addition"]'),
  ).toContainText('PENDING');
  await page.getByLabel('Perspective', { exact: true }).selectOption('Design');
  await expect(
    page.getByRole('heading', { name: 'Asynchronous upload', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Uploading a document returns promptly, regardless of document size.'),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Perspective', { exact: true })).toHaveValue('Design');
  await page.getByRole('button', { name: 'Configuration and tests', exact: false }).click();
  await page
    .getByRole('navigation', { name: 'Secondary view' })
    .getByRole('button', { name: 'Diagram', exact: true })
    .click();
  await expect(
    page.getByText('No diagram representation was provided for this topic.'),
  ).toBeVisible();
  await page
    .getByRole('navigation', { name: 'Primary view' })
    .getByRole('button', { name: 'Evidence', exact: true })
    .click();
  await expect(
    page.getByTestId('evidence-card').filter({ hasText: 'pytest tests/test_consumer.py' }),
  ).toContainText('MEASURED');
  await page.getByRole('button', { name: 'Findings', exact: true }).click();
  await page.getByRole('button', { name: /Retries can repeat processing side effects/ }).click();
  await expect(page.getByRole('region', { name: 'Selected evidence or finding' })).toContainText(
    'INFERRED',
  );
  await page.getByLabel('Perspective', { exact: true }).selectOption('Source');
  await expect(page.getByRole('heading', { name: /Unassigned Changes/ })).toBeVisible();
  await expect(page.locator('.source-tree')).toContainText('api');
  await expect(page.locator('.source-tree')).toContainText('worker');
  await expect(
    page
      .getByRole('region', { name: 'Unassigned Changes', exact: true })
      .getByRole('region', { name: 'api', exact: true })
      .getByRole('treeitem', { name: 'package.json', exact: true }),
  ).toBeInViewport({ ratio: 1 });
  const unassigned = await fixture.get<Model['SourceChanges']>(
    '/v1/revisions/' + fixture.revision.id + '/source-changes',
  );
  const f = unassigned.repositories
    .flatMap((r) => r.files.map((f) => ({ ...f, label: r.repository_label })))
    .find((f) => f.unassigned)!;
  await page
    .getByRole('region', { name: 'Unassigned Changes', exact: true })
    .getByRole('region', { name: f.label, exact: true })
    .getByRole('treeitem', { name: f.path.split('/').at(-1)!, exact: true })
    .click();
  await expect(page.getByTestId('file-diff')).toContainText(f.path);
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Runtime sessions' })).toContainText('UNREACHABLE');
  expect(errors).toEqual([]);
});

test('Pierre source trees keep repository and revision identity through navigation and failed refresh', async ({
  page,
}) => {
  for (const label of ['api', 'worker']) {
    const dir = `${fixture.dir}/repos/${label}/shared`;
    mkdirSync(dir);
    writeFileSync(dir + '/review.txt', `Stored ${label} source\n`);
  }
  const replacement = `${fixture.dir}/repos/worker/README.md`;
  unlinkSync(replacement);
  mkdirSync(replacement);
  writeFileSync(replacement + '/replacement.txt', 'A directory replaced this file\n');
  const routes = `${fixture.dir}/repos/api/src/routes`;
  rmSync(routes, { recursive: true });
  writeFileSync(routes, 'A file replaced this directory\n');
  mkdirSync(`${fixture.dir}/repos/api/assets`);
  writeFileSync(`${fixture.dir}/repos/api/assets/review.bin`, Buffer.from([0, 1, 2]));
  await page.goto('/');
  await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(fixture.revision.id);
  await page.getByRole('button', { name: 'Capture', exact: true }).click();
  await expect(page.getByLabel('Revision', { exact: true })).not.toHaveValue(fixture.revision.id);
  const revisionID = await page.getByLabel('Revision', { exact: true }).inputValue();
  await page.getByLabel('Perspective', { exact: true }).selectOption('Source');
  const all = page.getByRole('region', { name: 'All repositories', exact: true });
  const api = all.getByRole('region', { name: 'api', exact: true });
  const worker = all.getByRole('region', { name: 'worker', exact: true });
  const shared = api.getByRole('treeitem', { name: 'shared', exact: true });
  const apiFile = api.getByRole('treeitem', { name: 'review.txt', exact: true });
  const workerFile = worker.getByRole('treeitem', { name: 'review.txt', exact: true });
  const diff = page.getByTestId('file-diff');
  const apiID = fixture.revision.snapshots.find((s) => s.repository_label === 'api')!.repository_id;
  const workerID = fixture.revision.snapshots.find(
    (s) => s.repository_label === 'worker',
  )!.repository_id;
  await worker
    .getByRole('region', { name: 'Deleted files', exact: true })
    .getByRole('treeitem', { name: 'README.md', exact: true })
    .click();
  await expect(diff).toContainText('DELETED');
  await expect(storedDiff(page, 'README.md', workerID)).toContainText('Background jobs');
  await worker
    .getByRole('region', { name: 'Changed files', exact: true })
    .getByRole('treeitem', { name: 'replacement.txt', exact: true })
    .click();
  await expect(storedDiff(page, 'README.md/replacement.txt', workerID)).toContainText(
    'A directory replaced this file',
  );
  await api
    .getByRole('region', { name: 'Deleted files', exact: true })
    .getByRole('treeitem', { name: 'upload.ts', exact: true })
    .click();
  await expect(diff).toContainText('DELETED');
  await expect(storedDiff(page, 'src/routes/upload.ts', apiID)).toContainText('processDocument');
  await api
    .getByRole('region', { name: 'Changed files', exact: true })
    .getByRole('treeitem', { name: 'routes', exact: true })
    .click();
  await expect(storedDiff(page, 'src/routes', apiID)).toContainText(
    'A file replaced this directory',
  );
  await expect(shared).toHaveAttribute('aria-expanded', 'true');
  await shared.click();
  await expect(shared).toHaveAttribute('aria-expanded', 'false');
  await expect(apiFile).toHaveCount(0);
  await shared.press('ArrowRight');
  await expect(shared).toHaveAttribute('aria-expanded', 'true');
  await shared.press('ArrowDown');
  await expect(apiFile).toBeFocused();
  await apiFile.press('Enter');
  await expect(apiFile).toHaveAttribute('aria-selected', 'true');
  await expect(apiFile.getByText('U', { exact: true })).toBeVisible();
  await expect(storedDiff(page, 'shared/review.txt', apiID)).toContainText('Stored api source');
  await apiFile.press('Control+f');
  const search = api
    .getByRole('region', { name: 'Changed files', exact: true })
    .getByPlaceholder('Search…', { exact: true });
  await search.fill('review.txt');
  await expect(api.getByRole('treeitem', { name: 'package.json', exact: true })).toHaveCount(0);
  await search.press('Escape');
  await expect(api.getByRole('treeitem', { name: 'package.json', exact: true })).toBeVisible();
  await workerFile.click();
  await expect(workerFile).toHaveAttribute('aria-selected', 'true');
  await expect(apiFile).toHaveAttribute('aria-selected', 'false');
  await expect(storedDiff(page, 'shared/review.txt', workerID)).toContainText(
    'Stored worker source',
  );
  await expect(diff).toContainText(revisionID);
  await expect(
    page
      .getByRole('region', { name: 'Unassigned Changes', exact: true })
      .getByRole('region', { name: 'worker', exact: true })
      .getByRole('treeitem', { name: 'review.txt', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');

  await shared.click();
  await expect(shared).toHaveAttribute('aria-expanded', 'false');
  await page.route('**/source-changes', (route) => route.abort('failed'));
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(shared).toHaveAttribute('aria-expanded', 'false');
  await expect(workerFile).toHaveAttribute('aria-selected', 'true');
  await expect(storedDiff(page, 'shared/review.txt', workerID)).toContainText(
    'Stored worker source',
  );
  await page.unroute('**/source-changes');
  const refreshed = page.waitForResponse((r) => r.url().endsWith('/source-changes') && r.ok());
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await refreshed;
  await expect(page.getByRole('alert')).toHaveCount(0);
  await expect(shared).toHaveAttribute('aria-expanded', 'false');
  await expect(workerFile).toHaveAttribute('aria-selected', 'true');

  await api.getByRole('treeitem', { name: 'review.bin', exact: true }).click();
  await expect(diff).toContainText('api/assets/review.bin');
  await expect(
    page.getByRole('heading', { name: 'api/assets/review.bin', exact: true }),
  ).toBeInViewport();
  await expect(page.getByText('BINARY', { exact: true })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Comment on selected lines', exact: true }),
  ).toBeDisabled();
  await page.getByLabel('Revision', { exact: true }).selectOption(fixture.revision.id);
  await expect(all.getByRole('treeitem', { name: 'review.txt', exact: true })).toHaveCount(0);
  await expect(all.getByRole('treeitem', { selected: true })).toHaveCount(0);
  writeFileSync(`${fixture.dir}/repos/worker/shared/review.txt`, 'Uncaptured working copy\n');
  await page.getByLabel('Revision', { exact: true }).selectOption(revisionID);
  await workerFile.click();
  await expect(storedDiff(page, 'shared/review.txt', workerID)).toContainText(
    'Stored worker source',
  );
  await expect(storedDiff(page, 'shared/review.txt', workerID)).not.toContainText(
    'Uncaptured working copy',
  );
  expect(
    await fixture.get<Model['Discussion'][]>(`/v1/reviews/${fixture.review.id}/discussions`),
  ).toHaveLength(0);
});

test('a diagram draft returns to its owning topic, diagram and element', async ({ page }) => {
  const representations = await fixture.get<Model['Representation'][]>(
    `/v1/topics/${fixture.ids.topics['T-async']}/representations`,
  );
  const diagram = representations.find((representation) => representation.type === 'DIAGRAM')!;
  await page.goto('/');
  await page.getByRole('button', { name: 'Discuss Client', exact: true }).click();
  const body = 'Keep this question on the original diagram element.';
  await page.getByRole('textbox', { name: 'New discussion', exact: true }).fill(body);
  await page.getByRole('button', { name: /Configuration and tests/ }).click();
  await page.getByRole('button', { name: 'Files', exact: true }).click();
  await page.getByRole('button', { name: 'Comments', exact: true }).click();
  await page
    .getByRole('region', { name: 'Comments', exact: true })
    .getByRole('button', { name: /Unposted draft/ })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Asynchronous upload', exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('img', { name: 'Upload flow', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Inspect Client', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await expect(page.getByRole('textbox', { name: 'New discussion', exact: true })).toHaveValue(
    body,
  );
  const posted = page.waitForResponse(
    (response) => response.request().method() === 'POST' && response.url().endsWith('/discussions'),
  );
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  expect((await posted).status()).toBe(201);
  const discussions = await fixture.get<Model['Discussion'][]>(
    `/v1/reviews/${fixture.review.id}/discussions`,
  );
  expect(discussions).toMatchObject([
    {
      revision_id: fixture.revision.id,
      topic_ids: [fixture.ids.topics['T-async']],
      anchor: { type: 'DIAGRAM_ELEMENT', id: diagram.id, element_id: 'client' },
    },
  ]);
});

test('exact code discussion is shared across views, replies and resolution persist', async ({
  page,
}) => {
  await page.goto('/');
  await page
    .getByRole('button', {
      name: 'src/routes/upload.ts · Upload enqueues instead of processing',
      exact: true,
    })
    .click();
  const diff = page.getByTestId('file-diff');
  await diff
    .locator('[data-gutter] [data-line-type="change-addition"][data-column-number="9"]')
    .click();
  await page.getByRole('button', { name: 'Comment on selected lines' }).click();
  await page
    .getByRole('textbox', { name: 'New discussion', exact: true })
    .fill('Why is the upload status PENDING here?');
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  await expect(page.locator('.thread')).toContainText('Why is the upload status PENDING here?');
  const discussions = await fixture.get<Model['Discussion'][]>(
    '/v1/reviews/' + fixture.review.id + '/discussions',
  );
  expect(discussions).toHaveLength(1);
  expect(discussions[0].anchor.code_range).toMatchObject({
    side: 'HEAD',
    start_line: 9,
    end_line: 9,
  });
  expect(discussions[0].topic_ids).toContain(fixture.ids.topics['T-async']);
  await page
    .getByRole('navigation', { name: 'Secondary view' })
    .getByRole('button', { name: 'Text', exact: true })
    .click();
  await expect(page.locator('.thread')).toContainText('Why is the upload status PENDING here?');
  await page.locator('.thread textarea').fill('The queued worker updates it.');
  await page.locator('.thread').getByRole('button', { name: 'Reply', exact: true }).click();
  await expect(page.locator('.thread')).toContainText('The queued worker updates it.');
  await page.locator('.thread').getByRole('button', { name: 'Resolve', exact: true }).click();
  await expect(page.locator('.thread')).toContainText('RESOLVED');
  await page.reload();
  await expect(page.locator('.thread')).toContainText('The queued worker updates it.');
  await page
    .locator('.thread')
    .getByRole('button', { name: /src\/routes\/upload.ts:9/ })
    .click();
  await expect(page.getByTestId('file-diff')).toContainText('HEAD lines 9–9');
});

test('a lost reply response stays retry-safe after resolving its thread', async ({ page }) => {
  const seeded = await fetch(server + '/v1/reviews/' + fixture.review.id + '/discussions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      anchor: { type: 'TOPIC', id: fixture.ids.topics['T-async'] },
      body: 'Thread awaiting review.',
    }),
  });
  expect(seeded.status).toBe(201);
  const discussion = (await seeded.json()) as Model['Discussion'];
  await page.goto('/');
  const thread = page
    .getByRole('region', { name: 'Discussions', exact: true })
    .getByRole('article');
  await expect(thread).toContainText('Thread awaiting review.');
  const replyBody = 'This reply committed before its response was lost.';
  let firstKey = '';
  await page.route('**/messages', async (route) => {
    firstKey = route.request().headers()['idempotency-key'];
    const committed = await route.fetch();
    expect(committed.status()).toBe(201);
    await route.abort('failed');
  });
  const reply = thread.getByRole('textbox', { name: /^Reply to / });
  await reply.fill(replyBody);
  await thread.getByRole('button', { name: 'Reply', exact: true }).click();
  await expect(thread.getByRole('alert')).toBeVisible();
  await expect(thread).toContainText(replyBody);
  await page.unroute('**/messages');

  await thread.getByRole('button', { name: 'Resolve', exact: true }).click();
  await expect(thread).toContainText('RESOLVED');
  await expect(reply).toHaveValue(replyBody);
  const retry = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/messages'));
  const replayed = page.waitForResponse(
    (r) => r.request().method() === 'POST' && r.url().endsWith('/messages'),
  );
  await thread.getByRole('button', { name: 'Reply', exact: true }).click();
  expect((await replayed).status()).toBe(201);
  const persisted = await fixture.get<Model['Discussion']>('/v1/discussions/' + discussion.id);
  expect(persisted.messages.filter((message) => message.body === replyBody)).toHaveLength(1);
  expect((await retry).headers()['idempotency-key']).toBe(firstKey);
  await expect
    .poll(() =>
      reply.evaluateAll((fields) =>
        fields.every((field) => (field as HTMLTextAreaElement).value === ''),
      ),
    )
    .toBe(true);
});

test('evidence-loaded and reopened ranges keep their original revision when discussed', async ({
  page,
}) => {
  const evidence = await fixture.get<Model['Evidence']>(
    '/v1/evidence/' + fixture.ids.evidence['E-upload'],
  );
  await page.goto('/');
  await page
    .getByRole('button', {
      name: 'src/routes/upload.ts · Upload enqueues instead of processing',
      exact: true,
    })
    .click();

  await page.getByRole('button', { name: 'Files', exact: true }).click();
  const source = page.getByRole('region', { name: 'All repositories', exact: true });
  await expect(
    source
      .getByRole('region', { name: 'api', exact: true })
      .getByRole('treeitem', { name: 'upload.ts', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');

  for (const origin of ['linked evidence', 'existing discussion']) {
    if (origin === 'existing discussion') {
      writeFileSync(fixture.dir + '/repos/api/src/routes/upload.ts', 'DIFFERENT_REVISION\n');
      await page.getByRole('button', { name: 'Capture', exact: true }).click();
      await expect(page.getByLabel('Revision', { exact: true })).not.toHaveValue(
        evidence.revision_id,
      );
      await page.getByRole('button', { name: 'Comments', exact: true }).click();
      await page
        .getByRole('region', { name: 'Comments', exact: true })
        .getByRole('button')
        .filter({ hasText: 'Comment from linked evidence' })
        .click();
      await page.getByRole('button', { name: 'Files', exact: true }).click();
      await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(evidence.revision_id);
      await expect(
        source
          .getByRole('region', { name: 'api', exact: true })
          .getByRole('treeitem', { name: 'upload.ts', exact: true }),
      ).toHaveAttribute('aria-selected', 'true');
    }
    await page.getByRole('button', { name: 'Comment on selected lines', exact: true }).click();
    await page
      .getByRole('textbox', { name: 'New discussion', exact: true })
      .fill('Comment from ' + origin);
    const posted = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().endsWith('/discussions'),
    );
    await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
    expect((await posted).status()).toBe(201);
    await expect(
      page.locator('.thread').getByText('Comment from ' + origin, { exact: true }),
    ).toBeVisible();
  }

  const discussions = await fixture.get<Model['Discussion'][]>(
    '/v1/reviews/' + fixture.review.id + '/discussions',
  );
  expect(discussions).toHaveLength(2);
  for (const discussion of discussions) {
    expect(discussion).toMatchObject({
      revision_id: evidence.revision_id,
      anchor: { type: 'CODE_RANGE', code_range: evidence.code_range },
    });
  }

  const revisions = await fixture.get<Model['Revision'][]>(
    `/v1/reviews/${fixture.review.id}/revisions`,
  );
  const latest = revisions.at(-1)!;
  const imported = await fetch(`${server}/v1/reviews/${fixture.review.id}/imports`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      manifest: {
        author: evidence.author,
        evidence: [
          {
            key: 'later',
            kind: 'TEST_RESULT',
            title: 'Later revision evidence',
            content: 'An artifact unavailable in the earlier revision',
            source: 'Browser fixture',
          },
        ],
      },
    }),
  });
  expect(imported.status).toBe(201);
  const result = (await imported.json()) as Model['ImportResult'];
  const created = await fetch(`${server}/v1/reviews/${fixture.review.id}/discussions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      revision_id: latest.id,
      anchor: { type: 'EVIDENCE', id: result.ids.evidence.later },
      body: 'Discuss the later evidence',
    }),
  });
  expect(created.status).toBe(201);
  await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(evidence.revision_id);
  await page.getByRole('button', { name: 'Comments', exact: true }).click();
  await page.getByLabel('Discussion scope').selectOption('all');
  await page
    .getByRole('region', { name: 'Comments', exact: true })
    .getByRole('button')
    .filter({ hasText: 'Discuss the later evidence' })
    .click();
  await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(latest.id);
  await expect(page.getByRole('region', { name: 'Selected evidence or finding' })).toContainText(
    'Later revision evidence',
  );
  await expect(page.getByRole('region', { name: 'Selected evidence or finding' })).toContainText(
    'An artifact unavailable in the earlier revision',
  );
});

test('decisions name a revision and source history survives working-tree edits', async ({
  page,
}) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await page.getByLabel('Verdict').selectOption('REQUEST_CHANGES');
  await expect(page.getByRole('button', { name: 'Submit decision' })).toBeDisabled();
  await page.getByLabel('Retries can repeat processing side effects', { exact: true }).check();
  await page
    .getByRole('textbox', { name: 'Decision notes' })
    .fill('Guard the side effect before retrying.');
  await page.getByRole('button', { name: 'Submit decision' }).click();
  await expect(page.getByRole('region', { name: 'Review decision' })).toContainText(
    'Guard the side effect before retrying.',
  );
  const decisions = await fixture.get<Model['Decision'][]>(
    '/v1/reviews/' + fixture.review.id + '/decisions',
  );
  expect(decisions[0]).toMatchObject({
    revision_id: fixture.revision.id,
    decision: 'REQUEST_CHANGES',
    finding_ids: [fixture.ids.findings['F-1']],
  });
  writeFileSync(fixture.dir + '/repos/api/src/routes/upload.ts', 'UNSAVED_NEW_REVISION\n');
  await page
    .getByRole('textbox', { name: 'Decision notes' })
    .fill('A draft must not move to a new snapshot.');
  await page.getByRole('button', { name: 'Close Review decision', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Review decision', exact: true })).toBeFocused();
  await page.getByRole('button', { name: 'Capture', exact: true }).click();
  await expect(page.getByLabel('Revision', { exact: true })).not.toHaveValue(fixture.revision.id);
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Review decision' })).toContainText(
    'Your decision draft still targets revision 1.',
  );
  await expect(page.getByRole('textbox', { name: 'Decision notes' })).toHaveValue(
    'A draft must not move to a new snapshot.',
  );
  await page.getByRole('textbox', { name: 'Decision notes' }).fill('');
  await page.getByRole('button', { name: 'Close Review decision', exact: true }).click();
  await page.getByLabel('Revision', { exact: true }).selectOption(fixture.revision.id);
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Review decision' })).toContainText(
    'Guard the side effect before retrying.',
  );
  await page.getByRole('button', { name: 'Close Review decision', exact: true }).click();
  await page
    .getByRole('button', {
      name: 'src/routes/upload.ts · Upload enqueues instead of processing',
      exact: true,
    })
    .click();
  await expect(storedDiff(page, 'src/routes/upload.ts')).toContainText('PENDING');
  await expect(storedDiff(page, 'src/routes/upload.ts')).not.toContainText('UNSAVED_NEW_REVISION');
});

test('lost response retries safely and reconnect refetches after a real server restart', async ({
  page,
}) => {
  await page.goto('/');
  await expect(page.getByRole('status')).toContainText('Connected');
  await page.getByRole('button', { name: 'Discuss topic', exact: true }).click();
  await page
    .getByRole('textbox', { name: 'New discussion', exact: true })
    .fill('Keep this thread through a restart.');
  let firstKey = '';
  await page.route('**/discussions', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    firstKey = route.request().headers()['idempotency-key'];
    await route.fetch(); // The actual server commits; only its response is lost.
    await route.abort('failed');
  });
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Your draft is kept');
  await expect(page.getByRole('textbox', { name: 'New discussion', exact: true })).toHaveValue(
    'Keep this thread through a restart.',
  );
  await page.unroute('**/discussions');
  const retry = page.waitForRequest(
    (r) => r.method() === 'POST' && r.url().endsWith('/discussions'),
  );
  const replayed = page.waitForResponse(
    (r) => r.request().method() === 'POST' && r.url().endsWith('/discussions'),
  );
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  expect((await retry).headers()['idempotency-key']).toBe(firstKey);
  expect((await replayed).status()).toBe(201);
  await expect
    .poll(() =>
      page
        .getByRole('textbox', { name: 'New discussion', exact: true })
        .evaluateAll((fields) =>
          fields.every((field) => (field as HTMLTextAreaElement).value === ''),
        ),
    )
    .toBe(true);
  expect(
    await fixture.get<Model['Discussion'][]>('/v1/reviews/' + fixture.review.id + '/discussions'),
  ).toHaveLength(1);
  await page.context().setOffline(true);
  await fixture.stop();
  await expect(page.getByRole('status')).toContainText('Disconnected');
  await expect(page.locator('.thread')).toContainText('Keep this thread through a restart.');
  await fixture.start();
  const missed = await fetch(server + '/v1/reviews/' + fixture.review.id + '/discussions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      revision_id: fixture.revision.id,
      anchor: { type: 'TOPIC', id: fixture.ids.topics['T-async'] },
      body: 'Written while this client was disconnected.',
    }),
  });
  expect(missed.status).toBe(201);
  await page.context().setOffline(false);
  await expect(page.getByRole('status')).toContainText('Connected');
  await expect(
    page.locator('.thread').filter({ hasText: 'Written while this client was disconnected.' }),
  ).toBeVisible();
  const response = await fetch(server + '/v1/reviews/' + fixture.review.id + '/discussions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      revision_id: fixture.revision.id,
      anchor: { type: 'TOPIC', id: fixture.ids.topics['T-async'] },
      body: 'Written by another client after reconnect.',
    }),
  });
  expect(response.status).toBe(201);
  await expect(
    page.locator('.thread').filter({ hasText: 'Written by another client after reconnect.' }),
  ).toBeVisible();
});
