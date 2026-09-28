import { expect, test } from '@playwright/test';

// Contract-level journey against the in-memory mock API (make test-mock-e2e).
// Only the real stack proves the production subscriber list.
test.skip(process.env.E2E_MOCK !== 'true', 'requires the local mock server');

test.beforeEach(async ({ request }) => {
  expect((await request.post('/__mock__/control', { data: { action: 'reset' } })).status()).toBe(200);
});

test('subscribes an address, shows its canonical form, and accepts it again', async ({ page, request }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Dex Tech Blog' })).toBeVisible();
  const email = page.getByRole('textbox', { name: 'Email' });
  const subscribe = page.getByRole('button', { name: 'Subscribe' });

  await email.fill('Reader@Example.COM');
  await subscribe.click();
  await expect(page.getByRole('status')).toHaveText('Subscribed as reader@example.com.');
  await expect(email).toHaveValue('');

  await email.fill('reader@example.com');
  const repeated = page.waitForResponse((response) => response.url().endsWith('/api/newsletter/subscriptions'));
  await subscribe.click();
  expect((await repeated).status()).toBe(200);
  await expect(page.getByRole('status')).toHaveText('Subscribed as reader@example.com.');
  await expect(email).toHaveValue('');
  await expect(page.getByRole('alert')).toHaveCount(0);

  const control = await (await request.get('/__mock__/control')).json();
  expect(control.subscribers).toEqual(['reader@example.com']);
});

test('shows the server message for an address without a dotted domain', async ({ page }) => {
  await page.goto('/');
  const email = page.getByRole('textbox', { name: 'Email' });
  await email.fill('reader@example');
  await page.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.getByRole('alert')).toHaveText('Enter a single email address, such as name@example.com.');
  await expect(email).toHaveValue('reader@example');
  await expect(page.getByRole('status')).toHaveCount(0);
});

test('shows the unavailable message when the subscriber list fails', async ({ page, request }) => {
  await page.goto('/');
  expect((await request.post('/__mock__/control', { data: { action: 'fail-next' } })).status()).toBe(200);
  const email = page.getByRole('textbox', { name: 'Email' });
  await email.fill('reader@example.com');
  await page.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.getByRole('alert')).toHaveText('Subscriptions are unavailable right now. Try again in a minute.');
  await expect(email).toHaveValue('reader@example.com');

  await page.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.getByRole('status')).toHaveText('Subscribed as reader@example.com.');
  await expect(page.getByRole('alert')).toHaveCount(0);
});
