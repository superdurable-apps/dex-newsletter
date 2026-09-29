import { createHmac } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

function unsubscribeToken(email: string) {
  const key = Buffer.from(readFileSync(process.env.E2E_UNSUBSCRIBE_KEY_FILE!, 'utf8').trim(), 'hex');
  return createHmac('sha256', key).update(`unsubscribe\0${email}`).digest().subarray(0, 16).toString('hex');
}

test('a reader subscribes, resubscribes, and unsubscribes through the real subscriber list', async ({ page }) => {
  const email = `reader-${Date.now()}@example.com`;

  await page.goto('/');
  await page.getByLabel('Email').fill('not an address');
  await page.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.getByRole('alert')).toContainText('enter one email address');

  await page.getByLabel('Email').fill(email.toUpperCase());
  await page.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.getByRole('heading', { name: "You're subscribed" })).toBeVisible();
  await expect(page).toHaveURL(/\/subscribed$/);

  await page.goto('/');
  await page.getByLabel('Email').fill(email);
  await page.getByRole('button', { name: 'Subscribe' }).click();
  await expect(page.getByRole('heading', { name: "You're already subscribed" })).toBeVisible();

  await page.goto(`/unsubscribe?email=${encodeURIComponent(email)}&token=${'0'.repeat(32)}`);
  await expect(page.getByRole('heading', { name: 'This link is invalid' })).toBeVisible();

  const link = `/unsubscribe?email=${encodeURIComponent(email)}&token=${unsubscribeToken(email)}`;
  await page.goto(link);
  await expect(page.getByRole('heading', { name: "You're unsubscribed" })).toBeVisible();
  await page.goto(link);
  await expect(page.getByRole('heading', { name: "You're not subscribed" })).toBeVisible();
});
