import { createHmac } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

// unsubscribeToken derives a link token the way internal/techblog/unsubscribe
// does, from the throwaway key scripts/run-e2e.sh gives the server.
function unsubscribeToken(canonicalAddress: string): string {
  const keyFile = process.env.TECH_BLOG_UNSUBSCRIBE_KEY_FILE;
  if (!keyFile) throw new Error('TECH_BLOG_UNSUBSCRIBE_KEY_FILE is not set; run through scripts/run-e2e.sh');
  const key = Buffer.from(readFileSync(keyFile, 'utf8').trim(), 'base64');
  const mac = createHmac('sha256', key);
  mac.update('dex-tech-blog/newsletter-unsubscribe/v1\0');
  mac.update(canonicalAddress);
  return mac.digest().subarray(0, 16).toString('base64url');
}

// Real journey: the page, the Go API, and the Dex subscriber list Flow on the
// throwaway stack of make test-e2e. The mock journey lives in
// mock-newsletter-subscription.spec.ts.
test.skip(process.env.E2E_MOCK === 'true', 'requires the real Dex stack');

test('subscribes an address to the Dex subscriber list and accepts it again', async ({ page, request }) => {
  // The application starts the subscriber list Flow once Dex answers, so the
  // first subscriptions may see 503 for a moment.
  await expect
    .poll(async () => (await request.post('/api/newsletter/subscriptions', { data: { email: 'warm-up@example.com' } })).status(), {
      timeout: 30_000,
    })
    .toBe(200);

  const reader = `e2e-${Date.now()}@example.com`;
  await page.goto('/');
  const email = page.getByRole('textbox', { name: 'Email' });
  const subscribe = page.getByRole('button', { name: 'Subscribe' });

  await email.fill(`  ${reader.toUpperCase()}  `);
  await subscribe.click();
  await expect(page.getByRole('status')).toHaveText(`Subscribed as ${reader}.`);
  await expect(email).toHaveValue('');

  await email.fill(reader);
  const repeated = page.waitForResponse((response) => response.url().endsWith('/api/newsletter/subscriptions'));
  await subscribe.click();
  expect((await repeated).status()).toBe(200);
  await expect(page.getByRole('status')).toHaveText(`Subscribed as ${reader}.`);

  // A single-label domain passes the browser's email check but not the
  // subscriber list's address policy.
  await email.fill('reader@localhost');
  await subscribe.click();
  await expect(page.getByRole('alert')).toHaveText('Enter a single email address, such as name@example.com.');
  await expect(page.getByRole('status')).toBeEmpty();
});

test('an unsubscribe link removes the reader as soon as it opens', async ({ page, request }) => {
  await expect
    .poll(async () => (await request.post('/api/newsletter/subscriptions', { data: { email: 'warm-up@example.com' } })).status(), {
      timeout: 30_000,
    })
    .toBe(200);
  const reader = `e2e-unsubscribe-${Date.now()}@example.com`;
  expect((await request.post('/api/newsletter/subscriptions', { data: { email: reader } })).status()).toBe(200);

  const unsubscribed = page.waitForResponse((response) => response.url().endsWith('/api/newsletter/unsubscriptions'));
  await page.goto(`/?unsubscribe=${unsubscribeToken(reader)}`);
  expect((await unsubscribed).status()).toBe(200);
  await expect(page.getByRole('status')).toHaveText("You're unsubscribed. You won't receive future issues.");
  await expect(page).toHaveURL((url) => !url.searchParams.has('unsubscribe'));
  // The same page still offers the one subscription form.
  await expect(page.getByRole('button')).toHaveCount(1);
  await expect(page.getByRole('textbox', { name: 'Email' })).toBeVisible();

  // Opening the link again, or a stranger's link, answers the same.
  for (const address of [reader, `stranger-${Date.now()}@example.com`]) {
    const response = await request.post('/api/newsletter/unsubscriptions', { data: { token: unsubscribeToken(address) } });
    expect(response.status()).toBe(200);
    expect(await response.json()).toEqual({ status: 'unsubscribed' });
  }
  expect((await request.post('/api/newsletter/unsubscriptions', { data: { token: 'too-short' } })).status()).toBe(400);
  expect((await request.get('/api/newsletter/unsubscriptions')).status()).toBe(405);
});
