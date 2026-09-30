import { readFileSync } from 'node:fs';
import { expect, test, type APIRequestContext } from '@playwright/test';

// Written by internal/testsupport/e2eseed: one BlogPost run awaiting review, served by the application.
// paragraph is the first paragraph of the first section as the model wrote it.
type Seed = {
  runId: string;
  editorPath: string;
  fakeUrl: string;
  dexWebUrl: string;
  title: string;
  paragraph: string;
  recipients: string[];
};

type SentEmail = { to: string; subject: string; text: string; html: string };
type FakeState = { slackPosts: string[]; emails: SentEmail[]; modelRequests: number };
type EditorView = { status: string; blog: { title: string }; emailSubject: string };

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

async function slackPostsWith(request: APIRequestContext, seed: Seed, fragment: string) {
  return (await fakeState(request, seed)).slackPosts.filter((post) => post.includes(fragment));
}

test('an editor edits the one post, previews it as published and as emailed, saves, and approves it for every subscriber', async ({ page, request }) => {
  test.setTimeout(180_000);
  const seed = readSeed();
  const token = new URLSearchParams(seed.editorPath.split('?')[1]).get('token') ?? '';
  const draftApi = `/api/drafts/${encodeURIComponent(seed.runId)}?token=${encodeURIComponent(token)}`;
  const editedTitle = 'Connectors, edited in the browser';
  const editedParagraph = 'An editor rewrote this paragraph in the browser before approving it.';
  // The longest run of the model's paragraph outside inline code, which every rendering shows verbatim.
  const replaced = seed.paragraph.split('`').map((part) => part.trim()).reduce((longest, part) => (part.length > longest.length ? part : longest), '');
  expect(replaced, 'the seeded paragraph has text outside inline code').not.toBe('');
  const modelRequestsAtReview = (await fakeState(request, seed)).modelRequests;

  await page.goto(seed.editorPath);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(seed.title);
  await expect(page.getByText('Status: awaiting review · draft version 1 · revisions 0')).toBeVisible();
  const blogTab = page.getByRole('navigation', { name: 'Preview format' }).getByRole('link', { name: 'Blog post' });
  const emailTab = page.getByRole('navigation', { name: 'Preview format' }).getByRole('link', { name: 'Email', exact: true });
  await expect(blogTab).toHaveAttribute('aria-current', 'page');
  await expect(emailTab).not.toHaveAttribute('aria-current');
  await expect(page.getByRole('link', { name: 'Open in Dex Web' })).toHaveAttribute(
    'href', `${seed.dexWebUrl}/v2/run/BlogPost/${encodeURIComponent(seed.runId)}`);
  const form = page.getByRole('form', { name: 'Edit post' });
  const title = form.getByRole('textbox', { name: 'Title', exact: true });
  const paragraphs = form.getByRole('textbox', { name: 'Section 1 Paragraphs (blank line between paragraphs)' });
  await expect(title).toHaveValue(seed.title);
  await expect(paragraphs).toHaveValue(seed.paragraph);
  const postFields = await form.getByRole('textbox').count();
  const sameContent = page.getByText('The email sends this same post to subscribers; only the formatting differs.');
  await expect(sameContent).toBeVisible();
  // The subject line belongs to the email tab only.
  await expect(page.getByText(/^Subject: /)).toHaveCount(0);
  // The preview is the server's published rendering; the model's markup stays text.
  const blogPreview = page.frameLocator('iframe[title="Blog post preview"]');
  await expect(blogPreview.getByRole('heading', { level: 1 })).toHaveText(seed.title);
  await expect(blogPreview.getByText(replaced)).toBeVisible();

  await title.fill(editedTitle);
  await paragraphs.fill(editedParagraph);
  const approve = page.getByRole('button', { name: 'Approve and send' });
  const save = page.getByRole('button', { name: 'Save changes' });
  await expect(approve).toBeDisabled();
  await expect(approve).toHaveAccessibleDescription('Save your changes before approving.');
  await expect(blogPreview.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(blogPreview.getByText(editedParagraph)).toBeVisible();
  await expect(blogPreview.getByText(replaced)).toHaveCount(0);

  // The Email tab previews the same unsaved edits as emailed; the form is still the one post.
  await emailTab.click();
  await expect(page).toHaveURL(/[?&]tab=email(&|$)/);
  await expect(emailTab).toHaveAttribute('aria-current', 'page');
  await expect(blogTab).not.toHaveAttribute('aria-current');
  await expect(page.getByRole('heading', { name: 'Preview (as emailed)' })).toBeVisible();
  await expect(page.locator('iframe[title="Blog post preview"]')).toHaveCount(0);
  const emailPreview = page.frameLocator('iframe[title="Email preview"]');
  await expect(emailPreview.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(emailPreview.getByText(editedParagraph)).toBeVisible();
  await expect(emailPreview.getByText(replaced)).toHaveCount(0);
  await expect(page.getByText(`Subject: ${editedTitle}`)).toBeVisible();
  await expect(sameContent).toBeVisible();
  await expect(title).toHaveValue(editedTitle);
  await expect(paragraphs).toHaveValue(editedParagraph);
  await expect(form.getByRole('textbox')).toHaveCount(postFields);
  await expect(page.getByRole('textbox', { name: /subject|preheader|intro/i })).toHaveCount(0);
  await expect(approve).toBeDisabled();

  await save.click();
  await expect(page.getByText('Saved (version 2)')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(page.getByText('Status: awaiting review · draft version 2 · revisions 0')).toBeVisible();
  await expect(save).toBeDisabled();
  await expect(approve).toBeEnabled();
  await expect(approve).toHaveAccessibleDescription('Approve and send delivers the saved version to every subscriber.');
  await expect(emailTab).toHaveAttribute('aria-current', 'page');
  await expect(page.getByText(`Subject: ${editedTitle}`)).toBeVisible();
  await expect(emailPreview.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(emailPreview.getByText(editedParagraph)).toBeVisible();
  // The application's ApplyDraftEdits Step posted the saved post once, and the email is that post.
  await expect.poll(async () => (await slackPostsWith(request, seed, `*Draft 2, edited in the editor*: ${editedTitle}`)).length, { timeout: 30_000 }).toBe(1);
  const [reviewPost] = await slackPostsWith(request, seed, `*Draft 2, edited in the editor*: ${editedTitle}`);
  expect(reviewPost).toContain(`The email sends this same post, with the subject line "${editedTitle}".`);
  expect(reviewPost).toContain(editedParagraph);
  expect(reviewPost).not.toContain(replaced);

  await approve.click();
  await expect(page.getByText('Approved. Sending the newsletter to subscribers.')).toBeVisible();
  await expect(title).toBeDisabled();
  await expect(save).toHaveCount(0);
  await expect(approve).toHaveCount(0);

  // Gmail received the saved post: one email per seeded reader, its subject the edited title.
  await expect.poll(async () => {
    const { emails } = await fakeState(request, seed);
    return seed.recipients.map((address) => emails.filter((email) => email.to === address).map((email) => email.subject));
  }, { timeout: 60_000 }).toEqual([[editedTitle], [editedTitle]]);
  await expect.poll(async () => {
    const response = await request.get(draftApi);
    return response.ok() ? ((await response.json()) as EditorView).status : `HTTP ${response.status()}`;
  }, { timeout: 60_000 }).toBe('sent');
  const sent = (await (await request.get(draftApi)).json()) as EditorView;
  expect([sent.blog.title, sent.emailSubject]).toEqual([editedTitle, editedTitle]);
  const state = await fakeState(request, seed);
  expect(state.emails.filter((email) => email.subject !== editedTitle)).toEqual([]);
  for (const address of seed.recipients) {
    const email = state.emails.find((received) => received.to === address)!;
    // Both bodies are the edited post: its title and its edited paragraph, never the replaced one.
    expect(email.html).toContain(`>${editedTitle}</h1>`);
    expect(email.html).toContain(`>${editedParagraph}</p>`);
    expect(email.text.startsWith(`${editedTitle}\n`)).toBe(true);
    expect(email.text).toContain(`\n${editedParagraph}\n`);
    expect(email.html).not.toContain(replaced);
    expect(email.text).not.toContain(replaced);
    expect(email.html).toContain(`/unsubscribe?email=${encodeURIComponent(address)}`);
  }
  for (const fragment of ['Draft 2 approved in the editor. Sending the newsletter.', 'Newsletter sent:']) {
    expect(state.slackPosts.filter((post) => post.includes(fragment))).toHaveLength(1);
  }
  // Editing, previewing, saving, approving, and sending never asked the model for anything.
  expect(state.modelRequests).toBe(modelRequestsAtReview);

  // Reloading shows the sent post read-only; the URL kept the Email tab.
  await page.reload();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(page.getByText('This draft is sent; editing is closed.')).toBeVisible();
  await expect(page.getByText('Status: sent · draft version 2 · revisions 0')).toBeVisible();
  await expect(emailTab).toHaveAttribute('aria-current', 'page');
  await expect(page.getByText(`Subject: ${editedTitle}`)).toBeVisible();
  await expect(emailPreview.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(emailPreview.getByText(editedParagraph)).toBeVisible();
  await expect(title).toHaveValue(editedTitle);
  await expect(title).toBeDisabled();
  await expect(paragraphs).toHaveValue(editedParagraph);
  await expect(paragraphs).toBeDisabled();
  await expect(save).toHaveCount(0);
  await expect(approve).toHaveCount(0);
  await blogTab.click();
  await expect(page).toHaveURL(/[?&]tab=blog(&|$)/);
  await expect(page.getByRole('heading', { name: 'Preview (as published)' })).toBeVisible();
  await expect(blogPreview.getByRole('heading', { level: 1 })).toHaveText(editedTitle);
  await expect(blogPreview.getByText(editedParagraph)).toBeVisible();
  await expect(page.getByText(/^Subject: /)).toHaveCount(0);
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
