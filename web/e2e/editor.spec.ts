import { readFileSync } from 'node:fs';
import { expect, test, type APIRequestContext } from '@playwright/test';

// Written by internal/testsupport/e2eseed: one BlogPost run awaiting review, served by the application.
type Seed = {
  runId: string;
  editorPath: string;
  fakeUrl: string;
  dexWebUrl: string;
  title: string;
  subject: string;
  recipients: string[];
};

type SentEmail = { to: string; subject: string; text: string; html: string };
type FakeState = { slackPosts: string[]; emails: SentEmail[]; modelRequests: number };

function readSeed(): Seed {
  const path = process.env.E2E_SEED_FILE;
  if (!path) throw new Error('E2E_SEED_FILE is unset; run scripts/run-e2e.sh, which seeds a BlogPost run awaiting review');
  return JSON.parse(readFileSync(path, 'utf8')) as Seed;
}

async function fakeState(request: APIRequestContext, seed: Seed): Promise<FakeState> {
  const response = await request.get(`${seed.fakeUrl}/__test/state`);
  expect(response.ok()).toBe(true);
  return (await response.json()) as FakeState;
}

async function slackPostCount(request: APIRequestContext, seed: Seed, fragment: string) {
  return (await fakeState(request, seed)).slackPosts.filter((post) => post.includes(fragment)).length;
}

test('an editor edits the post and the email, saves each, and approves the saved version for every subscriber', async ({ page, request }) => {
  test.setTimeout(180_000);
  const seed = readSeed();
  const token = new URLSearchParams(seed.editorPath.split('?')[1]).get('token') ?? '';
  const draftApi = `/api/drafts/${encodeURIComponent(seed.runId)}?token=${encodeURIComponent(token)}`;
  const editedTitle = 'Connectors, edited in the browser';
  const editedParagraph = 'An editor rewrote this paragraph in the browser before approving it.';
  const editedSubject = 'Connectors, edited for the inbox';
  const modelRequestsAtReview = (await fakeState(request, seed)).modelRequests;

  await page.goto(seed.editorPath);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(seed.title);
  await expect(page.getByText('Status: awaiting review · draft version 1 · revisions 0')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Blog post' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('link', { name: 'Open in Dex Web' })).toHaveAttribute(
    'href', `${seed.dexWebUrl}/v2/run/BlogPost/${encodeURIComponent(seed.runId)}`);
  const title = page.getByRole('textbox', { name: 'Title', exact: true });
  const paragraphs = page.getByRole('textbox', { name: 'Section 1 Paragraphs (blank line between paragraphs)' });
  await expect(title).toHaveValue(seed.title);
  // The preview is the server's published rendering; the model's markup stays text.
  const blogPreview = page.frameLocator('iframe[title="Blog post preview"]');
  await expect(blogPreview.getByRole('heading', { level: 1 })).toHaveText(seed.title);

  await title.fill(editedTitle);
  await paragraphs.fill(editedParagraph);
  const approve = page.getByRole('button', { name: 'Approve and send' });
  const save = page.getByRole('button', { name: 'Save changes' });
  await expect(approve).toBeDisabled();
  await expect(approve).toHaveAccessibleDescription('Save your changes before approving.');
  await expect(blogPreview.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(blogPreview.getByText(editedParagraph)).toBeVisible();

  await save.click();
  await expect(page.getByText('Saved (version 2)')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(page.getByText('Status: awaiting review · draft version 2 · revisions 0')).toBeVisible();
  await expect(save).toBeDisabled();
  await expect(approve).toBeEnabled();
  await expect(approve).toHaveAccessibleDescription('Approve and send delivers the saved version to every subscriber.');
  // The application's ApplyDraftEdits Step re-rendered the post and told the Slack thread.
  await expect.poll(() => slackPostCount(request, seed, `*Draft 2, edited in the editor*: ${editedTitle}`), { timeout: 30_000 }).toBe(1);

  await page.getByRole('link', { name: 'Newsletter email' }).click();
  await expect(page).toHaveURL(/[?&]tab=newsletter(&|$)/);
  await expect(page.getByRole('link', { name: 'Newsletter email' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('heading', { name: 'Preview (as emailed)' })).toBeVisible();
  const emailPreview = page.frameLocator('iframe[title="Newsletter email preview"]');
  await expect(emailPreview.getByRole('heading', { level: 1 })).toHaveText(seed.subject);
  const subject = page.getByRole('textbox', { name: 'Subject', exact: true });
  await expect(subject).toHaveValue(seed.subject);
  await subject.fill(editedSubject);
  await expect(approve).toBeDisabled();
  await expect(emailPreview.getByRole('heading', { level: 1 })).toHaveText(editedSubject);
  await save.click();
  await expect(page.getByText('Saved (version 3)')).toBeVisible();
  await expect(page.getByText('Status: awaiting review · draft version 3 · revisions 0')).toBeVisible();
  await expect(subject).toHaveValue(editedSubject);
  await expect.poll(() => slackPostCount(request, seed, '*Draft 3, edited in the editor*'), { timeout: 30_000 }).toBe(1);

  await approve.click();
  await expect(page.getByText('Approved. Sending the newsletter to subscribers.')).toBeVisible();
  await expect(subject).toBeDisabled();
  await expect(save).toHaveCount(0);
  await expect(approve).toHaveCount(0);

  // Gmail received the saved version: one email per seeded reader, each with the edited subject.
  await expect.poll(async () => {
    const { emails } = await fakeState(request, seed);
    return seed.recipients.map((address) => emails.filter((email) => email.to === address).map((email) => email.subject));
  }, { timeout: 60_000 }).toEqual([[editedSubject], [editedSubject]]);
  await expect.poll(async () => {
    const response = await request.get(draftApi);
    return response.ok() ? ((await response.json()) as { status: string }).status : `HTTP ${response.status()}`;
  }, { timeout: 60_000 }).toBe('sent');
  const state = await fakeState(request, seed);
  expect(state.emails.filter((email) => email.subject !== editedSubject)).toEqual([]);
  for (const address of seed.recipients) {
    const email = state.emails.find((sent) => sent.to === address)!;
    expect(email.html).toContain(editedSubject);
    expect(email.html).toContain(`/unsubscribe?email=${encodeURIComponent(address)}`);
  }
  for (const fragment of ['Draft 3 approved in the editor. Sending the newsletter.', 'Newsletter sent:']) {
    expect(state.slackPosts.filter((post) => post.includes(fragment))).toHaveLength(1);
  }
  // Editing, saving, and approving never asked the model for anything.
  expect(state.modelRequests).toBe(modelRequestsAtReview);

  // Reloading shows the sent draft read-only; the URL kept the newsletter tab.
  await page.reload();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(page.getByText('This draft is sent; editing is closed.')).toBeVisible();
  await expect(page.getByText('Status: sent · draft version 3 · revisions 0')).toBeVisible();
  await expect(subject).toHaveValue(editedSubject);
  await expect(subject).toBeDisabled();
  await expect(save).toHaveCount(0);
  await expect(approve).toHaveCount(0);
  await page.getByRole('link', { name: 'Blog post' }).click();
  await expect(title).toHaveValue(editedTitle);
  await expect(title).toBeDisabled();
  await expect(paragraphs).toHaveValue(editedParagraph);
  await expect(blogPreview.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
});

test('an editor link with a forged token opens the invalid-link page', async ({ page, request }) => {
  const seed = readSeed();
  const forged = `/edit/${encodeURIComponent(seed.runId)}?token=${'0'.repeat(32)}`;

  await page.goto(forged);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('This editor link is invalid');
  await expect(page.getByRole('alert')).toHaveText('Use the editor link from the Slack review thread or Dex Web.');
  await expect(page.getByRole('textbox')).toHaveCount(0);

  const response = await request.get(`/api/drafts/${encodeURIComponent(seed.runId)}?token=${'0'.repeat(32)}`);
  expect(response.status()).toBe(403);
  expect(await response.json()).toMatchObject({ error: 'invalid_editor_link' });
});
