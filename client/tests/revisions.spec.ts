import { test, expect, type Page } from '@playwright/test';
import { DatabaseSync } from 'node:sqlite';
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
test.afterEach(async ({ page }) => {
  await page.close();
  await fixture.close();
});

async function capture() {
  const response = await fetch(`${server}/v1/reviews/${fixture.review.id}/revisions`, {
    method: 'POST',
  });
  expect(response.status).toBe(201);
  return (await response.json()) as Model['Revision'];
}
async function graph(revisionID: string) {
  const view = await fixture.get<Model['RevisionView']>(
    `/v1/reviews/${fixture.review.id}/revision-view?revision_id=${revisionID}`,
  );
  expect(view.graph).not.toBeNull();
  return view.graph!;
}
async function openUpload(page: Page) {
  await page.goto('/');
  await page.getByLabel('Perspective', { exact: true }).selectOption('Source');
  await page
    .getByRole('region', { name: 'All repositories', exact: true })
    .getByRole('region', { name: 'api', exact: true })
    .getByRole('treeitem', { name: 'upload.ts', exact: true })
    .click();
  await expect(page.getByRole('button', { name: 'Edit file topics', exact: true })).toBeEnabled();
}

test('revision selection changes semantic context and explicit finding state, not just source', async ({
  page,
}) => {
  const second = await capture();
  const current = await graph(second.id);
  const topic = current.topics.find((topic) => topic.id === fixture.ids.topics['T-async'])!;
  const changed = await fetch(`${server}/v1/topics/${topic.id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', 'If-Match': `"${topic.version}"` },
    body: JSON.stringify({ title: 'Human revision two title' }),
  });
  expect(changed.status).toBe(200);
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Human revision two title', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Findings', exact: true }).click();
  await page.getByRole('button', { name: /Retries can repeat processing side effects/ }).click();
  await page.getByRole('button', { name: 'Resolve finding', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Reopen finding', exact: true })).toBeEnabled();
  await expect(page.getByRole('region', { name: 'Selected evidence or finding' })).toContainText(
    'resolved',
  );
  await expect(page.getByLabel('Comparison baseline')).toHaveValue(fixture.revision.id);
  await page.getByLabel('Revision', { exact: true }).selectOption(fixture.revision.id);
  await expect(
    page.getByRole('heading', { name: 'Asynchronous upload', exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('region', { name: 'Selected evidence or finding' })).toContainText(
    'OPEN',
  );
  await expect(page.getByRole('button', { name: 'Resolve finding', exact: true })).toBeDisabled();
  await page.getByLabel('Revision', { exact: true }).selectOption(second.id);
  await expect(
    page.getByRole('heading', { name: 'Human revision two title', exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Reopen finding', exact: true })).toBeEnabled();
  const third = await capture();
  await expect(
    page.getByLabel('Revision', { exact: true }).locator('option', { hasText: 'Revision 3' }),
  ).toHaveAttribute('value', third.id);
  await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(second.id);
  await page.getByLabel('Revision', { exact: true }).selectOption(third.id);
  await page.getByLabel('Comparison baseline').selectOption(fixture.revision.id);
  await expect(
    page.getByRole('heading', { name: 'Human revision two title', exact: true }),
  ).toBeVisible();
  const comparison = page.getByLabel('Revision comparison', { exact: true });
  await comparison.locator('summary').click();
  const changedTopic = comparison
    .getByRole('listitem')
    .filter({ has: page.getByRole('button', { name: 'Human revision two title', exact: true }) });
  await expect(changedTopic).toContainText('changed');
  await expect(changedTopic).toContainText('title');
  await expect(
    comparison.getByRole('listitem').filter({
      has: page.getByRole('button', {
        name: 'Retries can repeat processing side effects',
        exact: true,
      }),
    }),
  ).toContainText('resolved');
});

test('lost conditional assignment response requires reload and preserves explicit unassignment and reset', async ({
  page,
}) => {
  await openUpload(page);
  await page.getByRole('button', { name: 'Edit file topics', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'File topics', exact: true });
  for (const checkbox of await dialog.getByRole('checkbox').all()) await checkbox.uncheck();
  await dialog.getByLabel('Configuration and tests', { exact: true }).check();
  await page.route('**/source-topics', async (route) => {
    await route.fetch();
    await route.abort('failed');
  });
  await dialog.getByRole('button', { name: 'Apply topics', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('Your original choices are kept');
  await page.unroute('**/source-topics');
  page.once('dialog', (dialog) => void dialog.accept());
  await dialog.getByRole('button', { name: 'Reload assignment', exact: true }).click();
  await expect(dialog.getByRole('alert')).toHaveCount(0);
  await expect(dialog.getByLabel('Configuration and tests', { exact: true })).toBeChecked();
  await dialog.getByLabel('Configuration and tests', { exact: true }).uncheck();
  await dialog.getByRole('button', { name: 'Apply topics', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  const source = (await graph(fixture.revision.id)).source_changes!;
  expect(
    source.repositories[0].files.find((file) => file.path === 'src/routes/upload.ts'),
  ).toMatchObject({ assignment_mode: 'EXPLICIT', topic_ids: [], unassigned: true });
  await expect(
    page
      .getByRole('region', { name: 'Unassigned Changes', exact: true })
      .getByRole('treeitem', { name: 'upload.ts', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Edit file topics', exact: true }).click();
  await dialog.getByRole('button', { name: 'Reset to derived', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  expect(
    (await graph(fixture.revision.id)).source_changes!.repositories[0].files.find(
      (file) => file.path === 'src/routes/upload.ts',
    )?.topic_ids,
  ).toContain(fixture.ids.topics['T-async']);
});

test('remote capture and deletion retain a correction and its original target, including navigation protection', async ({
  page,
}) => {
  await openUpload(page);
  await page.getByRole('button', { name: 'Edit file topics', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'File topics', exact: true });
  await dialog.getByLabel('Configuration and tests', { exact: true }).check();
  await page.getByRole('button', { name: 'Close File topics', exact: true }).click();
  const second = await capture();
  await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(second.id);
  await page.getByRole('button', { name: 'Resume file-topic correction', exact: true }).click();
  await expect(dialog.getByLabel('Configuration and tests', { exact: true })).toBeChecked();
  await expect(dialog).toContainText('This revision is no longer current');
  await expect(dialog.getByRole('button', { name: 'Apply topics', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Close File topics', exact: true }).click();
  const created = await fetch(`${server}/v1/reviews`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: fixture.review.context_path, title: 'Another review' }),
  });
  expect(created.status).toBe(201);
  const other = (await created.json()) as Model['Review'];
  await expect(
    page.getByLabel('Feature review', { exact: true }).locator(`option[value="${other.id}"]`),
  ).toHaveCount(1);
  page.once('dialog', (dialog) => void dialog.dismiss());
  await page.getByLabel('Feature review', { exact: true }).selectOption(other.id);
  await expect(page.getByLabel('Feature review', { exact: true })).toHaveValue(fixture.review.id);
  await page.getByRole('button', { name: 'Resume file-topic correction', exact: true }).click();
  const deleted = await fetch(`${server}/v1/reviews/${fixture.review.id}`, { method: 'DELETE' });
  expect(deleted.status).toBe(204);
  await expect(page.getByText(/Selected review was removed or is unavailable/)).toBeVisible();
  await expect(dialog.getByLabel('Configuration and tests', { exact: true })).toBeChecked();
  await expect(page.getByLabel('Feature review', { exact: true })).toHaveValue(fixture.review.id);
  page.once('dialog', (dialog) => void dialog.accept());
  await dialog.getByRole('button', { name: 'Discard correction', exact: true }).click();
  await expect(dialog).not.toBeVisible();
});

test('unrecorded semantic history stays unavailable while stored source remains readable', async ({
  page,
}) => {
  const second = await capture();
  const current = await graph(second.id);
  const topic = current.topics.find((topic) => topic.id === fixture.ids.topics['T-async'])!;
  const changed = await fetch(`${server}/v1/topics/${topic.id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', 'If-Match': `"${topic.version}"` },
    body: JSON.stringify({ title: 'Current legacy metadata' }),
  });
  expect(changed.status).toBe(200);
  await fixture.stop();
  const database = new DatabaseSync(`${fixture.dir}/data/prunelle.db`);
  try {
    database
      .prepare('DELETE FROM revision_review_states WHERE revision_id = ?')
      .run(fixture.revision.id);
  } finally {
    database.close();
  }
  await fixture.start();
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Current legacy metadata', exact: true }),
  ).toBeVisible();
  await page.getByLabel('Revision', { exact: true }).selectOption(fixture.revision.id);
  await expect(page.getByText(/Semantic history was not recorded/)).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Current legacy metadata', exact: true }),
  ).toHaveCount(0);
  await page.getByRole('button', { name: 'Files', exact: true }).click();
  await expect(page.getByText(/Empty topic sets do not mean/)).toBeVisible();
  await page
    .getByRole('region', { name: 'All repositories', exact: true })
    .getByRole('region', { name: 'api', exact: true })
    .getByRole('treeitem', { name: 'upload.ts', exact: true })
    .click();
  await expect(page.getByTestId('file-diff')).toContainText('PENDING');
  await expect(page.getByRole('button', { name: 'Edit file topics', exact: true })).toHaveCount(0);
});

test('an original diagram discussion opens its frozen graph rather than the current replacement', async ({
  page,
}) => {
  const original = await graph(fixture.revision.id);
  const diagram = original.representations.find(
    (rep) => rep.topic_id === fixture.ids.topics['T-status'] && rep.type === 'DIAGRAM',
  )!;
  const node = diagram.diagram!.nodes.find((node) => node.id === 'failed')!;
  const created = await fetch(`${server}/v1/reviews/${fixture.review.id}/discussions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      revision_id: fixture.revision.id,
      anchor: { type: 'DIAGRAM_ELEMENT', id: diagram.id, element_id: node.id },
      body: 'Explain the original failed state',
    }),
  });
  expect(created.status).toBe(201);
  const second = await capture();
  const current = (await graph(second.id)).representations.find((rep) => rep.id === diagram.id)!;
  const changed = await fetch(`${server}/v1/representations/${diagram.id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', 'If-Match': `"${current.version}"` },
    body: JSON.stringify({
      diagram: {
        nodes: [{ id: 'replacement', label: 'Replacement flow', kind: 'STATE' }],
        edges: [],
      },
    }),
  });
  expect(changed.status).toBe(200);
  await page.goto('/');
  await expect(
    page.getByRole('button', { name: 'Inspect POST /documents', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: /Processing status flow/ }).click();
  await expect(
    page.getByRole('button', { name: 'Inspect Replacement flow', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Inspect Replacement flow', exact: true }).click();
  const grounding = page.getByRole('region', { name: 'Selected evidence or finding' });
  await expect(grounding).toContainText('Replacement flow');
  await page.getByLabel('Revision', { exact: true }).selectOption(fixture.revision.id);
  await expect(
    page.getByRole('button', { name: 'Inspect ' + node.label, exact: true }),
  ).toBeVisible();
  await expect(
    grounding.getByRole('heading', { name: 'Replacement flow', exact: true }),
  ).not.toBeVisible();
  await page.getByLabel('Revision', { exact: true }).selectOption(second.id);
  await expect(
    page.getByRole('button', { name: 'Inspect Replacement flow', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Comments', exact: true }).click();
  await page.getByLabel('Discussion scope').selectOption('all');
  const comment = page
    .getByRole('region', { name: 'Comments', exact: true })
    .getByRole('button')
    .filter({ hasText: 'Explain the original failed state' });
  await expect(comment).toContainText('outdated');
  await comment.click();
  await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(fixture.revision.id);
  await expect(
    page.getByRole('button', { name: 'Inspect ' + node.label, exact: true }),
  ).toHaveAttribute('aria-pressed', 'true');
  await expect(
    page.getByRole('button', { name: 'Inspect Replacement flow', exact: true }),
  ).toHaveCount(0);
  await expect(
    page.locator('.thread').filter({ hasText: 'Explain the original failed state' }),
  ).toBeVisible();
});

test('a decision with only actionable selections is protected and stays on its original revision', async ({
  page,
}) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await page.getByLabel('Verdict').selectOption('REQUEST_CHANGES');
  await page.getByLabel('Retries can repeat processing side effects', { exact: true }).check();
  await page.getByRole('button', { name: 'Close Review decision', exact: true }).click();
  const second = await capture();
  await expect(page.getByLabel('Revision', { exact: true })).toHaveValue(second.id);
  const response = await fetch(`${server}/v1/reviews`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: fixture.review.context_path, title: 'Other review' }),
  });
  const other = (await response.json()) as Model['Review'];
  await expect(
    page.getByLabel('Feature review', { exact: true }).locator(`option[value="${other.id}"]`),
  ).toHaveCount(1);
  page.once('dialog', (dialog) => void dialog.dismiss());
  await page.getByLabel('Feature review', { exact: true }).selectOption(other.id);
  await expect(page.getByLabel('Feature review', { exact: true })).toHaveValue(fixture.review.id);
  await page.getByRole('button', { name: 'Review decision', exact: true }).click();
  await expect(
    page.getByLabel('Retries can repeat processing side effects', { exact: true }),
  ).toBeChecked();
  await expect(
    page.getByRole('heading', { name: 'Decision · revision 1', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Submit decision', exact: true }).click();
  await expect
    .poll(
      async () =>
        (await fixture.get<Model['Decision'][]>(`/v1/reviews/${fixture.review.id}/decisions`))
          .length,
    )
    .toBe(1);
  expect(
    (await fixture.get<Model['Decision'][]>(`/v1/reviews/${fixture.review.id}/decisions`))[0],
  ).toMatchObject({
    revision_id: fixture.revision.id,
    finding_ids: [fixture.ids.findings['F-1']],
    body: '',
  });
  await page.getByRole('button', { name: 'Close Review decision', exact: true }).click();
  await page.getByLabel('Feature review', { exact: true }).selectOption(other.id);
  await expect(page.getByLabel('Feature review', { exact: true })).toHaveValue(other.id);
});
