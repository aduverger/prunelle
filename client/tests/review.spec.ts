import { test, expect } from '@playwright/test';
import { writeFileSync } from 'node:fs';
import { Fixture, buildServer, server } from './fixture';
import type { Model } from '../src/api/client';

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
test.afterEach(async () => {
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
  const unassigned = await fixture.get<Model['SourceChanges']>(
    '/v1/revisions/' + fixture.revision.id + '/source-changes',
  );
  const f = unassigned.repositories
    .flatMap((r) => r.files.map((f) => ({ ...f, label: r.repository_label })))
    .find((f) => f.unassigned)!;
  await page.getByRole('button', { name: f.label + '/' + f.path, exact: true }).click();
  await expect(page.getByTestId('file-diff')).toContainText(f.path);
  await page.getByRole('button', { name: 'Runtime sessions', exact: false }).click();
  await expect(page.getByRole('region', { name: 'Runtime sessions' })).toContainText('UNREACHABLE');
  expect(errors).toEqual([]);
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
  await page.getByRole('button', { name: 'Discuss selected lines' }).click();
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

  for (const origin of ['linked evidence', 'existing discussion']) {
    if (origin === 'existing discussion') {
      writeFileSync(fixture.dir + '/repos/api/src/routes/upload.ts', 'DIFFERENT_REVISION\n');
      await page.getByRole('button', { name: 'Capture', exact: true }).click();
      await expect(page.getByLabel('Revision', { exact: true })).not.toHaveValue(
        evidence.revision_id,
      );
      await page
        .locator('.thread')
        .getByRole('button', { name: /src\/routes\/upload.ts:/ })
        .click();
    }
    await page.getByRole('button', { name: 'Discuss selected lines', exact: true }).click();
    await page
      .getByRole('textbox', { name: 'New discussion', exact: true })
      .fill('Comment from ' + origin);
    const posted = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().endsWith('/discussions'),
    );
    await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
    expect((await posted).status()).toBe(201);
    await expect(
      page
        .getByRole('region', { name: 'Discussions', exact: true })
        .getByText('Comment from ' + origin, { exact: true }),
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
  await page.getByRole('button', { name: 'Capture', exact: true }).click();
  await expect(page.getByLabel('Revision', { exact: true })).not.toHaveValue(fixture.revision.id);
  await expect(page.getByRole('region', { name: 'Review decision' })).toContainText(
    'Your decision draft still targets revision 1.',
  );
  await expect(page.getByRole('textbox', { name: 'Decision notes' })).toHaveValue(
    'A draft must not move to a new snapshot.',
  );
  await page.getByRole('textbox', { name: 'Decision notes' }).fill('');
  await page.getByLabel('Revision', { exact: true }).selectOption(fixture.revision.id);
  await expect(page.getByRole('region', { name: 'Review decision' })).toContainText(
    'Guard the side effect before retrying.',
  );
  await page
    .getByRole('button', {
      name: 'src/routes/upload.ts · Upload enqueues instead of processing',
      exact: true,
    })
    .click();
  await expect(page.getByTestId('file-diff').locator('[data-content]')).toContainText('PENDING');
  await expect(page.getByTestId('file-diff').locator('[data-content]')).not.toContainText(
    'UNSAVED_NEW_REVISION',
  );
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
