import { expect, test } from '@playwright/test';

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
  await expect(page.getByRole('status')).toHaveCount(0);
});
