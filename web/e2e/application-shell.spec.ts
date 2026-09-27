import { expect, test } from '@playwright/test';

// Dex Web v2 is the only process-management surface. The production shell
// renders non-business application identity and exposes no management routes.
test('renders the application name without process-management controls', async ({ page, request }) => {
  const info = await request.get('/api/application-info');
  expect(info.status()).toBe(200);
  const { name } = await info.json();
  expect(typeof name).toBe('string');
  expect(name.length).toBeGreaterThan(0);

  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
  await expect(page.getByText('Newsletter requests start from Slack and are reviewed in Dex Web.')).toBeVisible();
  await expect(page.getByRole('button')).toHaveCount(0);
});

test('exposes no process-management API routes', async ({ request }) => {
  expect((await request.get('/api/application-info')).status()).toBe(200);
  expect((await request.post('/api/flows', { data: {} })).status()).toBe(404);
  expect((await request.get('/api/flows/x')).status()).toBe(404);
});
