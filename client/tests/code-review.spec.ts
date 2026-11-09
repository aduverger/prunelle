import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import { devNull } from 'node:os';
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
  const repository = `${fixture.dir}/repos/api`;
  const contents = (offset: number) =>
    Array.from({ length: 1200 }, (_, i) => `export const line${i + 1} = ${i + 1 + offset};`).join(
      '\n',
    ) + '\n';
  writeFileSync(`${repository}/src/long.ts`, contents(0));
  for (const args of [
    ['add', 'src/long.ts'],
    ['commit', '-m', 'Add long review fixture'],
    ['update-ref', 'refs/heads/main', 'HEAD'],
  ]) {
    execFileSync('git', ['-C', repository, ...args], {
      stdio: 'pipe',
      env: {
        ...process.env,
        GIT_AUTHOR_NAME: 'Prunelle Fixture',
        GIT_AUTHOR_EMAIL: 'fixture@prunelle.invalid',
        GIT_AUTHOR_DATE: '2026-11-01T10:00:00Z',
        GIT_COMMITTER_NAME: 'Prunelle Fixture',
        GIT_COMMITTER_EMAIL: 'fixture@prunelle.invalid',
        GIT_COMMITTER_DATE: '2026-11-01T10:00:00Z',
        GIT_CONFIG_GLOBAL: devNull,
        GIT_CONFIG_NOSYSTEM: '1',
      },
    });
  }
  writeFileSync(`${repository}/src/long.ts`, contents(1000));
  writeFileSync(
    `${repository}/src/highlight.go`,
    'package review\n\nfunc label(v int) int {\n  return v + 1\n}\n',
  );
  writeFileSync(
    `${fixture.dir}/repos/worker/z-large.txt`,
    'Another file far beyond the first viewport.\n'.repeat(1400),
  );
  writeFileSync(
    `${repository}/opaque.unrecognized-language`,
    '<img src=x onerror="window.hacked=true">\nPlain stored source.\n',
  );
  const captured = await fetch(`${server}/v1/reviews/${fixture.review.id}/revisions`, {
    method: 'POST',
  });
  expect(captured.status).toBe(201);
  fixture.revision = (await captured.json()) as Model['Revision'];
});
test.afterEach(async ({ page }) => {
  await page.close();
  await fixture.close();
});

async function openLong(page: Page, backend = 'highlights') {
  await page.goto('/');
  await page.getByLabel('Feature review', { exact: true }).selectOption(fixture.review.id);
  await page.getByLabel('Perspective', { exact: true }).selectOption('Source');
  await page
    .getByRole('region', { name: 'All repositories', exact: true })
    .getByRole('region', { name: 'api', exact: true })
    .getByRole('treeitem', { name: 'long.ts', exact: true })
    .click();
  await expect(page.getByTestId('file-diff')).toHaveAttribute('data-highlighter', backend);
}
function line(page: Page, side: 'BASE' | 'HEAD', number: number) {
  return page
    .getByTestId('file-diff')
    .locator(
      `[data-gutter] [data-line-type="change-${side === 'BASE' ? 'deletion' : 'addition'}"][data-column-number="${number}"]`,
    );
}
async function commentRange(page: Page, side: 'BASE' | 'HEAD', start: number, end = start) {
  await expect(
    page.getByRole('button', { name: 'Collapse src/long.ts', exact: true }),
  ).toBeVisible();
  await page.getByText('Comment on lines…', { exact: true }).click();
  await page.getByLabel('Comment side', { exact: true }).selectOption(side);
  await page.getByLabel('Start line', { exact: true }).fill(String(start));
  await page.getByLabel('End line', { exact: true }).fill(String(end));
  await page.getByRole('button', { name: 'Add comment on lines', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'New discussion', exact: true })).toBeVisible();
}
async function discussions() {
  return fixture.get<Model['Discussion'][]>(`/v1/reviews/${fixture.review.id}/discussions`);
}
async function seedCodeThread(body: string) {
  const repository = fixture.revision.snapshots.find(
    (snapshot) => snapshot.repository_label === 'api',
  )!;
  const response = await fetch(`${server}/v1/reviews/${fixture.review.id}/discussions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      revision_id: fixture.revision.id,
      anchor: {
        type: 'CODE_RANGE',
        code_range: {
          repository_id: repository.repository_id,
          path: 'src/long.ts',
          side: 'HEAD',
          start_line: 450,
          end_line: 453,
        },
      },
      body,
    }),
  });
  expect(response.status).toBe(201);
  return (await response.json()) as Model['Discussion'];
}
async function navigateComment(page: Page, body: string) {
  await page.getByRole('button', { name: 'Comments', exact: true }).click();
  const comment = page
    .getByRole('region', { name: 'Comments', exact: true })
    .getByRole('button')
    .filter({ hasText: body });
  await expect(comment).toContainText('Revision 2 · api');
  await comment.click();
}

test('an already-selected file can be reactivated after scrolling elsewhere', async ({ page }) => {
  await openLong(page);
  const selectedFile = page
    .getByRole('region', { name: 'All repositories', exact: true })
    .getByRole('region', { name: 'api', exact: true })
    .getByRole('treeitem', { name: 'long.ts', exact: true });
  const firstLine = page
    .locator('diffs-container')
    .filter({ has: page.locator('[data-source-path="src/long.ts"]') })
    .locator('[data-gutter] [data-line-type="change-addition"][data-column-number="1"]');
  for (const activation of ['click', 'Enter', 'search']) {
    await expect(firstLine).toBeInViewport();
    await firstLine.hover();
    await page.mouse.wheel(0, 100_000);
    await expect(firstLine).not.toBeInViewport();
    await expect(selectedFile).toHaveAttribute('aria-selected', 'true');
    if (activation === 'click') await selectedFile.click();
    else if (activation === 'Enter') await selectedFile.press(activation);
    else {
      await selectedFile.press('Control+f');
      const search = page
        .getByRole('region', { name: 'All repositories', exact: true })
        .getByRole('region', { name: 'api', exact: true })
        .getByRole('region', { name: 'Changed files', exact: true })
        .getByPlaceholder('Search…', { exact: true });
      await search.fill('long.ts');
      await search.press('Enter');
    }
    await expect(firstLine).toBeInViewport();
  }
});

test('long-file selection, inline composer, posting and SSE retain code context', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await openLong(page);
  await commentRange(page, 'HEAD', 450, 453);
  await page.getByRole('button', { name: 'Discard', exact: true }).click();
  const first = (await line(page, 'HEAD', 450).boundingBox())!;
  const last = (await line(page, 'HEAD', 453).boundingBox())!;
  await page.mouse.move(first.x + first.width / 2, first.y + first.height / 2);
  await page.mouse.down();
  await page.mouse.move(last.x + last.width / 2, last.y + last.height / 2, { steps: 4 });
  await page.mouse.up();
  await expect(line(page, 'HEAD', 453)).toBeInViewport();
  await page.getByRole('button', { name: 'Comment on selected lines', exact: true }).click();
  const selectedLine = line(page, 'HEAD', 453);
  await expect(selectedLine).toBeInViewport();
  const composer = page
    .getByTestId('file-diff')
    .locator('form')
    .filter({ has: page.getByRole('textbox', { name: 'New discussion', exact: true }) });
  const lineBounds = (await selectedLine.boundingBox())!;
  const gap = (await composer.boundingBox())!.y - (lineBounds.y + lineBounds.height);
  expect(gap).toBeGreaterThanOrEqual(0);
  expect(gap).toBeLessThanOrEqual(lineBounds.height * 2);
  const before = lineBounds.y;
  await page
    .getByRole('textbox', { name: 'New discussion', exact: true })
    .fill('Explain this HEAD range.');
  await expect(selectedLine).toBeInViewport();
  expect(Math.abs((await selectedLine.boundingBox())!.y - before)).toBeLessThanOrEqual(
    lineBounds.height,
  );
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  const thread = page.locator('.thread').filter({ hasText: 'Explain this HEAD range.' });
  await expect(thread).toBeVisible();
  await expect(selectedLine).toBeInViewport();
  await expect(thread).toContainText('HEAD');
  await expect(thread).toContainText('450–453');
  await expect(thread).toContainText('Revision 2 · api');
  expect((await discussions())[0]).toMatchObject({
    revision_id: fixture.revision.id,
    anchor: {
      code_range: {
        side: 'HEAD',
        start_line: 450,
        end_line: 453,
        selected_text: expect.stringContaining('line450 = 1450'),
      },
    },
  });
  await fetch(`${server}/v1/discussions/${(await discussions())[0].id}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ body: 'A live update from another client.' }),
  });
  await expect(thread).toContainText('A live update from another client.');
  await expect(selectedLine).toBeInViewport();
  await page.reload();
  await navigateComment(page, 'Explain this HEAD range.');
  await expect(thread).toBeVisible();
  await expect(line(page, 'HEAD', 453)).toBeInViewport();
  expect(errors).toEqual([]);
});

test('BASE multiline anchors, unified layout and the gutter plus use the exact stored side', async ({
  page,
}) => {
  await openLong(page);
  await commentRange(page, 'BASE', 450, 452);
  const composer = page.getByTestId('file-diff').locator('.composer');
  const code = (await page.getByTestId('file-diff').boundingBox())!;
  expect((await composer.boundingBox())!.x).toBeLessThan(code.x + code.width / 2);
  await page
    .getByRole('textbox', { name: 'New discussion', exact: true })
    .fill('Original BASE range.');
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  await expect(page.locator('.thread')).toContainText('Original BASE range.');
  expect((await discussions())[0].anchor.code_range).toMatchObject({
    side: 'BASE',
    start_line: 450,
    end_line: 452,
    selected_text: expect.stringContaining('line450 = 450'),
  });
  await page.getByRole('button', { name: 'Unified', exact: true }).click();
  await commentRange(page, 'HEAD', 470);
  await page.getByRole('button', { name: 'Discard', exact: true }).click();
  await line(page, 'HEAD', 470).hover();
  await page.getByRole('button', { name: 'Add line comment', exact: true }).click();
  await page
    .getByRole('textbox', { name: 'New discussion', exact: true })
    .fill('Created with the gutter plus.');
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  await expect(
    page.locator('.thread').filter({ hasText: 'Created with the gutter plus.' }),
  ).toBeVisible();
  expect((await discussions())[1].anchor.code_range).toMatchObject({
    side: 'HEAD',
    start_line: 470,
    end_line: 470,
  });
  await page.reload();
  await navigateComment(page, 'Original BASE range.');
  await expect(line(page, 'BASE', 452)).toBeInViewport();
});

test('virtualized composers keep their frozen anchor and text across files and navigator tabs', async ({
  page,
}) => {
  await openLong(page);
  await commentRange(page, 'HEAD', 450, 453);
  await page
    .getByRole('textbox', { name: 'New discussion', exact: true })
    .fill('Draft kept outside the virtualized window.');
  await page
    .getByRole('region', { name: 'All repositories', exact: true })
    .getByRole('region', { name: 'worker', exact: true })
    .getByRole('treeitem', { name: 'z-large.txt', exact: true })
    .click();
  await expect(
    page.getByRole('textbox', { name: 'New discussion', exact: true }),
  ).not.toBeInViewport();
  await page.getByRole('button', { name: 'Comments', exact: true }).click();
  await page
    .getByRole('region', { name: 'Comments', exact: true })
    .getByRole('button', { name: /Unposted draft/ })
    .click();
  await expect(page.getByRole('textbox', { name: 'New discussion', exact: true })).toHaveValue(
    'Draft kept outside the virtualized window.',
  );
  await page.getByRole('button', { name: 'Files', exact: true }).click();
  const longFile = page
    .locator('diffs-container')
    .filter({ has: page.locator('[data-source-path="src/long.ts"]') });
  await longFile.getByRole('button', { name: 'Collapse src/long.ts', exact: true }).click();
  await expect(
    page.getByRole('textbox', { name: 'New discussion', exact: true }),
  ).not.toBeInViewport();
  await page.getByRole('button', { name: 'Return to draft', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'New discussion', exact: true })).toHaveValue(
    'Draft kept outside the virtualized window.',
  );
  await expect(line(page, 'HEAD', 453)).toBeInViewport();
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  expect((await discussions())[0].anchor.code_range).toMatchObject({
    side: 'HEAD',
    start_line: 450,
    end_line: 453,
  });
});

test('an inline draft retains its snapshot after capture and explicit return', async ({ page }) => {
  await openLong(page);
  await commentRange(page, 'HEAD', 450, 453);
  const body = 'Finish this review against the original captured lines.';
  await page.getByRole('textbox', { name: 'New discussion', exact: true }).fill(body);
  writeFileSync(`${fixture.dir}/repos/api/src/long.ts`, 'A later snapshot replaced this code.\n');
  await page.getByRole('button', { name: 'Capture', exact: true }).click();
  const revision = page.getByLabel('Revision', { exact: true });
  await expect(revision).not.toHaveValue(fixture.revision.id);
  const currentRevisionID = await revision.inputValue();
  await page.getByRole('button', { name: 'Return to draft', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'New discussion', exact: true })).toHaveValue(
    body,
  );
  await expect(line(page, 'HEAD', 453)).toBeInViewport();
  await expect(revision).toHaveValue(currentRevisionID);
  await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
  await expect(page.getByRole('article').filter({ hasText: body })).toBeVisible();
  expect(await discussions()).toMatchObject([
    {
      revision_id: fixture.revision.id,
      anchor: {
        code_range: {
          path: 'src/long.ts',
          side: 'HEAD',
          start_line: 450,
          end_line: 453,
          selected_text: expect.stringContaining('line450 = 1450'),
        },
      },
    },
  ]);
});

test('lost reply responses remain idempotent after resolve, recycling and a cancelled review switch', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const discussion = await seedCodeThread('A durable code thread.');
  const other = await fetch(`${server}/v1/reviews`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ workspace_id: fixture.review.workspace_id, title: 'Another review' }),
  });
  expect(other.status).toBe(201);
  const otherReview = (await other.json()) as Model['Review'];
  await openLong(page);
  await navigateComment(page, 'A durable code thread.');
  const thread = page.locator('.thread');
  const reply = thread.getByRole('textbox', { name: /^Reply to / });
  const body = 'This reply committed before its response was lost.';
  let firstKey = '';
  await page.route('**/messages', async (route) => {
    firstKey = route.request().headers()['idempotency-key'];
    expect((await route.fetch()).status()).toBe(201);
    await route.abort('failed');
  });
  await reply.fill(body);
  await thread.getByRole('button', { name: 'Reply', exact: true }).click();
  await expect(thread.getByRole('alert')).toBeVisible();
  await expect(thread).toContainText(body);
  await page.unroute('**/messages');
  await thread.getByRole('button', { name: 'Resolve', exact: true }).click();
  await expect(thread).toContainText('RESOLVED');
  await line(page, 'HEAD', 453).hover();
  await page.mouse.wheel(0, 100_000);
  await expect(reply).not.toBeInViewport();
  let warned = false;
  page.once('dialog', async (dialog) => {
    warned = dialog.message().includes('Unposted drafts');
    await dialog.dismiss();
  });
  await page.getByLabel('Feature review', { exact: true }).selectOption(otherReview.id);
  expect(warned).toBe(true);
  await expect(page.getByLabel('Feature review', { exact: true })).toHaveValue(fixture.review.id);
  await navigateComment(page, 'A durable code thread.');
  await expect(reply).toHaveValue(body);
  const retry = page.waitForRequest(
    (request) => request.method() === 'POST' && request.url().endsWith('/messages'),
  );
  await thread.getByRole('button', { name: 'Reply', exact: true }).click();
  expect((await retry).headers()['idempotency-key']).toBe(firstKey);
  await expect(reply).toHaveValue('');
  const persisted = await fixture.get<Model['Discussion']>(`/v1/discussions/${discussion.id}`);
  expect(persisted.messages.filter((message) => message.body === body)).toHaveLength(1);
  expect(errors).toEqual([]);
});

test('multiple threads at the same range stay inline and navigable', async ({ page }) => {
  await seedCodeThread('First conversation at this range.');
  await seedCodeThread('Second conversation at this range.');
  await openLong(page);
  await navigateComment(page, 'Second conversation at this range.');
  await expect(page.locator('.thread')).toHaveCount(2);
  await expect(
    page.locator('.thread').filter({ hasText: 'First conversation at this range.' }),
  ).toBeVisible();
  await expect(
    page.locator('.thread').filter({ hasText: 'Second conversation at this range.' }),
  ).toBeInViewport();
  await expect(line(page, 'HEAD', 453)).toBeInViewport();
});

test('Highlights tokenizes TypeScript, Python and Go in stored source', async ({ page }) => {
  await openLong(page);
  for (const [repository, path, text] of [
    ['api', 'src/long.ts', 'export const line1'],
    ['worker', 'worker/consumer.py', 'MAX_ATTEMPTS'],
    ['api', 'src/highlight.go', 'return v + 1'],
  ]) {
    await page
      .getByRole('region', { name: 'All repositories', exact: true })
      .getByRole('region', { name: repository, exact: true })
      .getByRole('treeitem', { name: path.split('/').at(-1)!, exact: true })
      .click();
    const source = page
      .locator('diffs-container')
      .filter({ has: page.locator(`[data-source-path=${JSON.stringify(path)}]`) });
    await expect(source).toContainText(text);
    await expect
      .poll(() =>
        source
          .locator('[data-line] span')
          .evaluateAll((nodes) => new Set(nodes.map((node) => getComputedStyle(node).color)).size),
      )
      .toBeGreaterThan(1);
  }
});

test('unsupported WebAssembly uses the actual JS fallback and unknown languages remain readable', async ({
  page,
}) => {
  await page.addInitScript(() => {
    Object.defineProperty(globalThis, 'WebAssembly', { value: undefined });
  });
  await openLong(page, 'shiki-js');
  await page
    .getByRole('region', { name: 'All repositories', exact: true })
    .getByRole('region', { name: 'api', exact: true })
    .getByRole('treeitem', { name: 'opaque.unrecognized-language', exact: true })
    .click();
  const source = page
    .locator('diffs-container')
    .filter({ has: page.locator('[data-source-path="opaque.unrecognized-language"]') });
  await expect(source).toContainText('Plain stored source.');
  await expect(source).toContainText('<img src=x');
  expect(await page.evaluate(() => 'hacked' in window)).toBe(false);
  await expect(page.getByRole('alert')).toHaveCount(0);
});

test('a cold stored-file failure keeps threads accessible and reply drafts survive recovery', async ({
  page,
}) => {
  await seedCodeThread('Thread with temporarily unavailable source.');
  await page.route('**/file?*', (route) =>
    new URL(route.request().url()).searchParams.get('path') === 'src/long.ts'
      ? route.abort('failed')
      : route.continue(),
  );
  await openLong(page);
  await navigateComment(page, 'Thread with temporarily unavailable source.');
  await expect(
    page.getByText('Original source unavailable · src/long.ts', { exact: true }),
  ).toBeVisible();
  const reply = page.locator('.thread').getByRole('textbox', { name: /^Reply to / });
  await reply.fill('A draft retained while the preview recovers.');
  await page.unroute('**/file?*');
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(line(page, 'HEAD', 453)).toBeInViewport();
  await expect(reply).toHaveValue('A draft retained while the preview recovers.');
  await expect(
    page.getByText('Original source unavailable · src/long.ts', { exact: true }),
  ).toHaveCount(0);
});

test('non-code comment navigation opens and focuses its topic; navigator scope survives tab switches', async ({
  page,
}) => {
  const response = await fetch(`${server}/v1/reviews/${fixture.review.id}/discussions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      anchor: { type: 'TOPIC', id: fixture.ids.topics['T-tests'] },
      body: 'Configuration context thread.',
    }),
  });
  expect(response.status).toBe(201);
  await openLong(page);
  await page.getByRole('button', { name: 'Comments', exact: true }).click();
  await page.getByLabel('Discussion scope', { exact: true }).selectOption('all');
  await page
    .getByRole('region', { name: 'Comments', exact: true })
    .getByRole('button')
    .filter({ hasText: 'Configuration context thread.' })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Configuration and tests', exact: true }),
  ).toBeVisible();
  await expect(page.locator('.thread')).toBeFocused();
  await page.getByRole('button', { name: 'Files', exact: true }).click();
  await page.getByRole('button', { name: 'Comments', exact: true }).click();
  await expect(page.getByLabel('Discussion scope', { exact: true })).toHaveValue('all');
});

test('an empty CodeView retains its context and footer; integrations only poll while open', async ({
  page,
}) => {
  const response = await fetch(`${server}/v1/reviews`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ workspace_id: fixture.review.workspace_id, title: 'No snapshot yet' }),
  });
  expect(response.status).toBe(201);
  const empty = (await response.json()) as Model['Review'];
  await page.clock.install();
  const sessionRequests: string[] = [];
  page.on('request', (request) => {
    if (/\/(session-backends|runtime-sessions)$/.test(request.url()))
      sessionRequests.push(request.url());
  });
  await page.goto('/');
  await page.getByLabel('Feature review', { exact: true }).selectOption(empty.id);
  await expect(
    page.getByText('No stored source changes in this scope.', { exact: true }),
  ).toBeVisible();
  await expect(page.getByText('End of stored source · 0 files', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Review decision', exact: true })).toBeDisabled();
  expect(sessionRequests).toEqual([]);
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(
    page.getByRole('dialog', { name: 'Settings · Integrations', exact: true }),
  ).toContainText('UNREACHABLE');
  const initialRequests = sessionRequests.length;
  await page.clock.fastForward(60_000);
  await expect.poll(() => sessionRequests.length).toBeGreaterThan(initialRequests);
  await page.getByRole('button', { name: 'Close Settings · Integrations', exact: true }).click();
  const count = sessionRequests.length;
  await page.clock.fastForward(60_000);
  expect(sessionRequests).toHaveLength(count);
});
