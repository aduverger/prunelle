import { test, expect, type Locator } from '@playwright/test';
import { cpSync } from 'node:fs';
import { Fixture, buildServer, server } from './fixture';
import type { Model } from '../src/api/client';

let build: ReturnType<typeof buildServer>;
let fixture: Fixture;
let otherReview: Model['Review'];
test.beforeAll(() => {
  build = buildServer();
});
test.afterAll(() => build.close());
test.beforeEach(async () => {
  fixture = new Fixture(build.binary);
  await fixture.start();
  await fixture.seed();
  const path = `${fixture.dir}/other-project`;
  cpSync(`${fixture.dir}/repos/api`, path, { recursive: true });
  const response = await fetch(`${server}/v1/reviews`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, title: 'Unrelated review' }),
  });
  expect(response.status).toBe(201);
  otherReview = (await response.json()) as Model['Review'];
});
test.afterEach(async ({ page }) => {
  await page.close();
  await fixture.close();
});

for (const kind of ['code', 'reply', 'decision'] as const) {
  test(`hard deletion retains the selected review's ${kind} draft instead of switching context`, async ({
    page,
  }) => {
    if (kind === 'reply') {
      const response = await fetch(`${server}/v1/reviews/${fixture.review.id}/discussions`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          anchor: { type: 'TOPIC', id: fixture.ids.topics['T-async'] },
          body: 'A thread to reply to.',
        }),
      });
      expect(response.status).toBe(201);
    }
    await page.goto('/');
    const picker = page.getByLabel('Feature review', { exact: true });
    await expect(picker).toHaveValue(fixture.review.id);
    await expect(page.getByRole('button', { name: 'Review decision', exact: true })).toBeEnabled();
    let draft: Locator;
    let submit: Locator;
    if (kind === 'code') {
      await page.getByLabel('Perspective', { exact: true }).selectOption('Source');
      await page
        .getByRole('region', { name: 'All repositories', exact: true })
        .getByRole('region', { name: 'api', exact: true })
        .getByRole('treeitem', { name: 'upload.ts', exact: true })
        .click();
      await page.getByText('Comment on lines…', { exact: true }).click();
      await page.getByLabel('Comment side', { exact: true }).selectOption('HEAD');
      await page.getByLabel('Start line', { exact: true }).fill('1');
      await page.getByLabel('End line', { exact: true }).fill('2');
      await page.getByRole('button', { name: 'Add comment on lines', exact: true }).click();
      draft = page.getByRole('textbox', { name: 'New discussion', exact: true });
      submit = page.getByRole('button', { name: 'Post discussion', exact: true });
    } else if (kind === 'reply') {
      draft = page.getByRole('textbox', { name: /^Reply to / });
      submit = page.getByRole('button', { name: 'Reply', exact: true });
    } else {
      await page.getByRole('button', { name: 'Review decision', exact: true }).click();
      draft = page.getByRole('textbox', { name: 'Decision notes', exact: true });
      submit = page.getByRole('button', { name: 'Submit decision', exact: true });
    }
    const body = `Keep my unposted ${kind} text after remote deletion.`;
    await draft.fill(body);
    const deleted = await fetch(`${server}/v1/reviews/${fixture.review.id}`, {
      method: 'DELETE',
    });
    expect(deleted.status).toBe(204);
    await expect(page.getByText(/Selected review was removed or is unavailable/)).toBeVisible();
    await expect(picker).toHaveValue(fixture.review.id);
    await expect(draft).toHaveValue(body);
    const failedWrite = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' && new URL(response.url()).pathname.includes('/v1/'),
    );
    await submit.click();
    expect((await failedWrite).status()).toBe(404);
    await expect(draft).toHaveValue(body);
    if (kind === 'decision')
      await page.getByRole('button', { name: 'Close Review decision', exact: true }).click();
    let warned = false;
    page.once('dialog', async (dialog) => {
      warned = dialog.message().includes('Unposted drafts');
      await dialog.dismiss();
    });
    await picker.selectOption(otherReview.id);
    expect(warned).toBe(true);
    await expect(picker).toHaveValue(fixture.review.id);
    page.once('dialog', (dialog) => dialog.accept());
    await picker.selectOption(otherReview.id);
    await expect(page.getByRole('heading', { name: otherReview.title, exact: true })).toBeVisible();
  });
}

test('unreviewed source folders stay out of navigation until the first review is created', async ({
  page,
}) => {
  const path = `${fixture.dir}/unused-project`;
  cpSync(`${fixture.dir}/repos/api`, path, { recursive: true });
  const reviews = await fixture.get<Model['Review'][]>('/v1/reviews');
  expect(reviews.some((review) => review.context_path === path)).toBe(false);
  await page.goto('/');
  const picker = page.getByLabel('Feature review', { exact: true });
  await expect(picker).toHaveValue(fixture.review.id);
  await expect(picker).not.toContainText('unused-project');
  const response = await fetch(`${server}/v1/reviews`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path }),
  });
  expect(response.status).toBe(201);
  const review = (await response.json()) as Model['Review'];
  expect(review.title).toBe('unused-project');
  await picker.selectOption(review.id);
  await expect(page.getByRole('heading', { name: review.title, exact: true })).toBeVisible();
});
