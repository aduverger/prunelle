import { test, expect } from '@playwright/test';
import { Fixture, buildServer, server } from './fixture';
import type { Model } from '../src/api/client';

test('draft anchors, safe Markdown, stale resolution and reply drafts survive navigation', async ({
  page,
}) => {
  const build = buildServer();
  const fixture = new Fixture(build.binary);
  try {
    await fixture.start();
    await fixture.seed();
    await page.goto('/');
    await page.getByRole('button', { name: 'Discuss topic', exact: true }).click();
    await page
      .getByRole('textbox', { name: 'New discussion', exact: true })
      .fill('**Keep this anchor** <img src=x onerror="window.hacked=true">');
    await page.getByRole('button', { name: /Configuration and tests/, exact: false }).click();
    await page.getByRole('button', { name: 'Discuss topic', exact: true }).click();
    await expect(
      page.getByText('Your draft stays on its original anchor.', { exact: false }),
    ).toBeVisible();
    const posted = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().endsWith('/discussions'),
    );
    await page.getByRole('button', { name: 'Post discussion', exact: true }).click();
    expect((await posted).status()).toBe(201);
    const threads = await fixture.get<Model['Discussion'][]>(
      '/v1/reviews/' + fixture.review.id + '/discussions',
    );
    expect(threads[0].anchor.id).toBe(fixture.ids.topics['T-async']);
    await page.getByRole('button', { name: /Asynchronous upload/, exact: false }).click();
    const thread = page.locator('.thread');
    await expect(thread.locator('.markdown strong')).toHaveText('Keep this anchor');
    await expect(thread.locator('.markdown img')).toHaveCount(0);
    expect(await page.evaluate(() => 'hacked' in window)).toBe(false);
    await thread.locator('textarea').fill('Unposted reply');
    await page.getByRole('button', { name: /Configuration and tests/, exact: false }).click();
    await page.getByRole('button', { name: /Asynchronous upload/, exact: false }).click();
    await expect(thread.locator('textarea')).toHaveValue('Unposted reply');
    await page.route(
      '**/discussions/' + threads[0].id,
      async (route) => {
        if (route.request().method() !== 'PATCH') return route.continue();
        await fetch(server + '/v1/discussions/' + threads[0].id + '/messages', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ body: 'Concurrent reply changes the version.' }),
        });
        await route.fulfill({ response: await route.fetch() });
      },
      { times: 1 },
    );
    await thread.getByRole('button', { name: 'Resolve', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('This record changed.');
    await expect(thread).toContainText('Concurrent reply changes the version.');
    await expect(thread.locator('textarea')).toHaveValue('Unposted reply');
    await thread.getByRole('button', { name: 'Resolve', exact: true }).click();
    await expect(thread).toContainText('RESOLVED');
  } finally {
    await fixture.close();
    build.close();
  }
});
