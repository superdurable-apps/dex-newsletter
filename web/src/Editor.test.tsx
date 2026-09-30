import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';
import { Editor, PREVIEW_DELAY_MS } from './Editor';
import type { DraftEditorView } from './api/generated/types.gen';

const api = vi.hoisted(() => ({
  getApplicationInfo: vi.fn(),
  subscribeToNewsletter: vi.fn(),
  unsubscribeFromNewsletter: vi.fn(),
  getDraft: vi.fn(),
  previewDraft: vi.fn(),
  saveDraft: vi.fn(),
  approveDraft: vi.fn(),
}));

vi.mock('./api/generated/sdk.gen', () => api);

const info = { name: 'Dex Tech Blog', dexWebUrl: 'http://127.0.0.1:8842' };
const runId = 'blog-post-T1-C1-1700000000.000100';
const token = 'editor-token';
const highlightUrl = 'https://example.com/acme/connectors/pull/1';

function draftView(overrides: Partial<DraftEditorView> = {}): DraftEditorView {
  return {
    runId,
    status: 'awaiting-review',
    editable: true,
    draftVersion: 3,
    revisionCount: 1,
    blog: {
      title: 'Connectors got faster',
      subtitle: 'Two weeks of acme.test work',
      slug: 'connectors-got-faster',
      summary: 'Retries and paging landed.',
      sections: [{ heading: 'Retries', paragraphs: ['First paragraph.', 'Second paragraph.'], bullets: ['One', 'Two'] }],
      highlights: [{ title: 'Retry PR', description: 'Adds retries.', url: highlightUrl }],
      closing: 'Thanks for reading.',
    },
    blogHtml: '<h1>Blog v3</h1>',
    emailSubject: 'Connectors got faster',
    emailHtml: '<h1>Email v3</h1>',
    ...overrides,
  };
}

const samePostNote = 'The email sends this same post to subscribers; only the formatting differs.';

function conflict(code: string, message: string) {
  return { error: { error: code, message }, response: { status: 409 } };
}

function deferred<T>() {
  let resolve: (value: T) => void = () => {};
  const promise = new Promise<T>((settle) => { resolve = settle; });
  return { promise, resolve };
}

function renderEditor(search = `?token=${token}&tab=blog`) {
  return render(<Editor info={info} path={`/edit/${runId}`} search={search} />);
}

function textbox(name: string) {
  return screen.getByRole('textbox', { name });
}

async function loaded() {
  return screen.findByRole('textbox', { name: 'Title' });
}

// Request bodies carry only the post: the email is rendered from it, never edited separately.
function bodyKeys(call: unknown[]) {
  return Object.keys((call[0] as { body: object }).body).sort();
}

// Lets resolved client promises and the React updates they trigger settle while timers are fake.
async function settle() {
  await act(async () => {
    for (let tick = 0; tick < 10; tick += 1) await Promise.resolve();
  });
}

describe('Editor', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.getApplicationInfo.mockResolvedValue({ data: info });
    api.previewDraft.mockReturnValue(new Promise(() => {}));
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('loads the draft into the form and shows the published rendering', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    renderEditor();
    expect(screen.getByRole('status')).toHaveTextContent('Loading the draft…');

    expect(await screen.findByRole('heading', { level: 1, name: 'Connectors got faster' })).toBeInTheDocument();
    expect(api.getDraft).toHaveBeenCalledWith({ path: { runId }, query: { token } });
    expect(screen.getByText('NEWSLETTER · EDITOR')).toBeInTheDocument();
    expect(screen.getByText('Status: awaiting review · draft version 3 · revisions 1')).toBeInTheDocument();
    expect(textbox('Title')).toHaveValue('Connectors got faster');
    expect(textbox('Subtitle')).toHaveValue('Two weeks of acme.test work');
    expect(textbox('Summary')).toHaveValue('Retries and paging landed.');
    expect(textbox('Section 1 Heading')).toHaveValue('Retries');
    expect(textbox('Section 1 Paragraphs (blank line between paragraphs)')).toHaveValue('First paragraph.\n\nSecond paragraph.');
    expect(textbox('Section 1 Bullets (one per line)')).toHaveValue('One\nTwo');
    expect(textbox('Highlight 1 Title')).toHaveValue('Retry PR');
    expect(textbox('Highlight 1 Description')).toHaveValue('Adds retries.');
    expect(screen.getByRole('link', { name: highlightUrl })).toHaveAttribute('href', highlightUrl);
    expect(screen.queryByRole('textbox', { name: highlightUrl })).not.toBeInTheDocument();
    expect(textbox('Closing')).toHaveValue('Thanks for reading.');
    expect(screen.getByRole('form', { name: 'Edit post' })).toBeInTheDocument();
    // Title, subtitle, summary, the section's three fields, the highlight's two, and the closing: nothing else.
    expect(screen.getAllByRole('textbox')).toHaveLength(9);
    expect(screen.getByRole('heading', { name: 'Preview (as published)' })).toBeInTheDocument();
    expect(screen.getByText(samePostNote)).toBeInTheDocument();
    const preview = screen.getByTitle('Blog post preview');
    expect(preview).toHaveAttribute('srcdoc', '<h1>Blog v3</h1>');
    expect(preview).toHaveAttribute('sandbox', '');
    expect(screen.queryByText(/^Subject:/)).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Blog post' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('link', { name: 'Email' })).not.toHaveAttribute('aria-current');
    expect(screen.getByRole('link', { name: 'Open in Dex Web' })).toHaveAttribute('href', `http://127.0.0.1:8842/v2/run/BlogPost/${runId}`);
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Approve and send' })).toBeEnabled();
  });

  it('switches only the preview between the blog post and the email, keeping the same form and its unsaved edits', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    renderEditor();
    fireEvent.change(await loaded(), { target: { value: 'Edited title' } });
    const fields = screen.getAllByRole('textbox');

    fireEvent.click(screen.getByRole('link', { name: 'Email' }));
    expect(window.location.search).toBe(`?token=${token}&tab=email`);
    expect(screen.getByRole('link', { name: 'Email' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('link', { name: 'Blog post' })).not.toHaveAttribute('aria-current');
    // The very same fields stay mounted: there is one post, and the email is only its other rendering.
    const emailFields = screen.getAllByRole('textbox');
    expect(emailFields).toHaveLength(fields.length);
    emailFields.forEach((field, at) => expect(field).toBe(fields[at]));
    expect(textbox('Title')).toHaveValue('Edited title');
    expect(screen.getByRole('form', { name: 'Edit post' })).toBeInTheDocument();
    for (const name of ['Subject', 'Preheader', 'Intro', 'Item 1 Title', 'Item 1 Summary']) {
      expect(screen.queryByRole('textbox', { name })).not.toBeInTheDocument();
    }
    expect(screen.getByRole('heading', { name: 'Preview (as emailed)' })).toBeInTheDocument();
    expect(screen.getByText('Subject: Connectors got faster')).toBeInTheDocument();
    expect(screen.getByTitle('Email preview')).toHaveAttribute('srcdoc', '<h1>Email v3</h1>');
    expect(screen.getByTitle('Email preview')).toHaveAttribute('sandbox', '');
    expect(screen.queryByTitle('Blog post preview')).not.toBeInTheDocument();
    expect(screen.getAllByText(samePostNote)).toHaveLength(1);

    fireEvent.click(screen.getByRole('link', { name: 'Blog post' }));
    expect(window.location.search).toBe(`?token=${token}&tab=blog`);
    expect(textbox('Title')).toHaveValue('Edited title');
    expect(screen.getByRole('heading', { name: 'Preview (as published)' })).toBeInTheDocument();
    expect(screen.getByTitle('Blog post preview')).toHaveAttribute('srcdoc', '<h1>Blog v3</h1>');
    expect(screen.queryByText(/^Subject:/)).not.toBeInTheDocument();
  });

  it.each(['email', 'newsletter'])('opens the email preview from the /edit/ route with ?tab=%s', async (tab) => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    window.history.replaceState({}, '', `/edit/${encodeURIComponent(runId)}?token=${token}&tab=${tab}`);
    render(<App />);
    expect(await screen.findByText('Subject: Connectors got faster')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Email' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByTitle('Email preview')).toHaveAttribute('srcdoc', '<h1>Email v3</h1>');
    expect(textbox('Title')).toHaveValue('Connectors got faster');
    expect(api.getDraft).toHaveBeenCalledWith({ path: { runId }, query: { token } });
  });

  it('shows an invalid link page when the server rejects the token', async () => {
    api.getDraft.mockResolvedValue({ error: { error: 'invalid_editor_link', message: 'This editor link is invalid.' }, response: { status: 403 } });
    renderEditor('?token=forged');
    expect(await screen.findByRole('heading', { name: 'This editor link is invalid' })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Use the editor link from the Slack review thread or Dex Web.');
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('treats a link without a token as invalid without calling the API', () => {
    renderEditor('?tab=blog');
    expect(screen.getByRole('heading', { name: 'This editor link is invalid' })).toBeInTheDocument();
    expect(api.getDraft).not.toHaveBeenCalled();
  });

  it('reports a run that does not exist', async () => {
    api.getDraft.mockResolvedValue({ error: { error: 'unknown_run', message: 'This blog post run was not found.' }, response: { status: 404 } });
    renderEditor();
    expect(await screen.findByRole('heading', { name: 'Blog post run not found' })).toBeInTheDocument();
  });

  it('offers Retry when the editor is unavailable', async () => {
    api.getDraft
      .mockResolvedValueOnce({ error: { error: 'editor_unavailable', message: 'The editor is unavailable right now. Try again in a moment.' }, response: { status: 503 } })
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ data: draftView() });
    renderEditor();
    expect(await screen.findByRole('heading', { name: 'The editor could not load' })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('The editor is unavailable right now. Try again in a moment.');

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Loading the draft failed. Check your connection and try again.')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await loaded()).toHaveValue('Connectors got faster');
    expect(api.getDraft).toHaveBeenCalledTimes(3);
  });

  it('saves the edits and reloads the new version from the server', async () => {
    const saved = draftView({
      draftVersion: 4,
      revisionCount: 2,
      blog: { ...draftView().blog, title: 'Connectors got much faster', sections: [{ heading: 'Retries', paragraphs: ['First.', 'Second.'], bullets: ['One', 'Two'] }] },
      blogHtml: '<h1>Blog v4</h1>',
      emailSubject: 'Connectors got much faster',
      emailHtml: '<h1>Email v4</h1>',
    });
    api.getDraft.mockResolvedValueOnce({ data: draftView() }).mockResolvedValueOnce({ data: saved });
    const save = deferred<unknown>();
    api.saveDraft.mockReturnValue(save.promise);
    renderEditor();
    fireEvent.change(await loaded(), { target: { value: 'Connectors got much faster' } });
    fireEvent.change(textbox('Section 1 Paragraphs (blank line between paragraphs)'), { target: { value: 'First.\n\n\n  Second.\n' } });

    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(screen.getByRole('button', { name: 'Saving…' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Approve and send' })).toBeDisabled();
    expect(textbox('Title')).toBeDisabled();
    expect(api.saveDraft).toHaveBeenCalledWith({
      path: { runId },
      body: {
        token,
        baseVersion: 3,
        blog: saved.blog,
      },
    });
    expect(bodyKeys(api.saveDraft.mock.calls[0])).toEqual(['baseVersion', 'blog', 'token']);

    await act(async () => save.resolve({ data: { outcome: 'saved', draftVersion: 4 } }));
    expect(await screen.findByText('Saved (version 4)')).toHaveAttribute('role', 'status');
    expect(api.getDraft).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Status: awaiting review · draft version 4 · revisions 2')).toBeInTheDocument();
    expect(textbox('Section 1 Paragraphs (blank line between paragraphs)')).toHaveValue('First.\n\nSecond.');
    expect(screen.getByTitle('Blog post preview')).toHaveAttribute('srcdoc', '<h1>Blog v4</h1>');
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    // The reloaded version also replaced the email rendering and its subject.
    fireEvent.click(screen.getByRole('link', { name: 'Email' }));
    expect(screen.getByText('Subject: Connectors got much faster')).toBeInTheDocument();
    expect(screen.getByTitle('Email preview')).toHaveAttribute('srcdoc', '<h1>Email v4</h1>');

    api.approveDraft.mockResolvedValue({ data: { outcome: 'approved', draftVersion: 4 } });
    fireEvent.click(screen.getByRole('button', { name: 'Approve and send' }));
    expect(await screen.findByText('Approved. Sending the newsletter to subscribers.')).toBeInTheDocument();
    expect(api.approveDraft).toHaveBeenCalledWith({ path: { runId }, body: { token, baseVersion: 4 } });
  });

  it('offers Reload when the draft changed since it was opened', async () => {
    const newer = draftView({ draftVersion: 5, revisionCount: 2, blog: { ...draftView().blog, title: 'A newer model draft' } });
    api.getDraft.mockResolvedValueOnce({ data: draftView() }).mockResolvedValueOnce({ data: newer });
    api.saveDraft.mockResolvedValue(conflict('draft_changed', 'The draft changed since you opened it. Reload to see the new version.'));
    renderEditor();
    fireEvent.change(await loaded(), { target: { value: 'My local edit' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('The draft changed since you opened it. Reload to see the new version.');
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    expect(await screen.findByText('Status: awaiting review · draft version 5 · revisions 2')).toBeInTheDocument();
    expect(textbox('Title')).toHaveValue('A newer model draft');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reload' })).not.toBeInTheDocument();
  });

  it('shows the validation message when the server refuses the edits', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    api.saveDraft.mockResolvedValue({ error: { error: 'invalid_draft', message: 'Title is required' }, response: { status: 400 } });
    renderEditor();
    fireEvent.change(await loaded(), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Title is required');
    expect(textbox('Title')).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled();
    expect(api.getDraft).toHaveBeenCalledTimes(1);
  });

  it('switches to read-only when the draft left review before saving', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    api.saveDraft.mockResolvedValue(conflict('not_in_review', 'This draft is delivering, so it can no longer be edited.'));
    renderEditor();
    fireEvent.change(await loaded(), { target: { value: 'Late edit' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('This draft is delivering, so it can no longer be edited.');
    expect(textbox('Title')).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Save changes' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Approve and send' })).not.toBeInTheDocument();
  });

  it('offers Reload when a revision took the draft out of review, and reopens editing after it', async () => {
    api.getDraft
      .mockResolvedValueOnce({ data: draftView() })
      .mockResolvedValueOnce({ data: { ...draftView(), draftVersion: 2, blog: { ...draftView().blog, title: 'Revised by the model' } } });
    api.saveDraft.mockResolvedValue(conflict('not_in_review', 'The draft is being updated (writing). Reload when the next version is posted to Slack.'));
    renderEditor();
    fireEvent.change(await loaded(), { target: { value: 'Edit during a revision' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('The draft is being updated (writing).');
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    await waitFor(() => expect(textbox('Title')).toHaveValue('Revised by the model'));
    expect(textbox('Title')).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Approve and send' })).toBeEnabled();
  });

  it('asks to save before approving while there are unsaved edits', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    renderEditor();
    const title = await loaded();
    fireEvent.change(title, { target: { value: 'Unsaved title' } });

    const approve = screen.getByRole('button', { name: 'Approve and send' });
    expect(approve).toBeDisabled();
    expect(approve).toHaveAccessibleDescription('Save your changes before approving.');
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled();
    fireEvent.click(approve);
    expect(api.approveDraft).not.toHaveBeenCalled();

    fireEvent.change(title, { target: { value: 'Connectors got faster' } });
    expect(screen.getByRole('button', { name: 'Approve and send' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  });

  it('approves the loaded version and locks the form', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    const approval = deferred<unknown>();
    api.approveDraft.mockReturnValue(approval.promise);
    renderEditor();
    await loaded();

    fireEvent.click(screen.getByRole('button', { name: 'Approve and send' }));
    expect(screen.getByRole('button', { name: 'Approving…' })).toBeDisabled();
    expect(api.approveDraft).toHaveBeenCalledWith({ path: { runId }, body: { token, baseVersion: 3 } });

    await act(async () => approval.resolve({ data: { outcome: 'approved', draftVersion: 3 } }));
    expect(screen.getByRole('status')).toHaveTextContent('Approved. Sending the newsletter to subscribers.');
    expect(textbox('Title')).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Approve and send' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save changes' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open in Dex Web' })).toBeInTheDocument();
  });

  it('offers Reload when approval finds a newer draft', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    api.approveDraft.mockResolvedValue(conflict('draft_changed', 'The draft changed since you opened it. Reload and review it before approving.'));
    renderEditor();
    await loaded();
    fireEvent.click(screen.getByRole('button', { name: 'Approve and send' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Reload and review it before approving.');
    expect(screen.getByRole('button', { name: 'Reload' })).toBeEnabled();
    expect(textbox('Title')).toBeEnabled();
  });

  it('shows a closed draft read-only', async () => {
    api.getDraft.mockResolvedValue({ data: draftView({ status: 'sent', editable: false }) });
    renderEditor();
    const title = await loaded();

    expect(title).toBeDisabled();
    expect(textbox('Section 1 Heading')).toBeDisabled();
    expect(screen.getByText('Status: sent · draft version 3 · revisions 1')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('This draft is sent; editing is closed.');
    expect(screen.queryByRole('button', { name: 'Save changes' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Approve and send' })).not.toBeInTheDocument();
    expect(screen.getByTitle('Blog post preview')).toHaveAttribute('srcdoc', '<h1>Blog v3</h1>');
    expect(screen.getByRole('link', { name: 'Open in Dex Web' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('link', { name: 'Email' }));
    expect(screen.getByText('Subject: Connectors got faster')).toBeInTheDocument();
    expect(screen.getByTitle('Email preview')).toHaveAttribute('srcdoc', '<h1>Email v3</h1>');
    expect(textbox('Title')).toBeDisabled();
    expect(api.previewDraft).not.toHaveBeenCalled();
  });

  it('debounces previews, ignores stale responses, and keeps the last good preview when invalid', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    api.getDraft.mockResolvedValue({ data: draftView() });
    const first = deferred<unknown>();
    const second = deferred<unknown>();
    api.previewDraft.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    renderEditor();
    await settle();
    const title = textbox('Title');
    const preview = () => screen.getByTitle('Blog post preview');

    fireEvent.change(title, { target: { value: 'Draft A' } });
    act(() => { vi.advanceTimersByTime(PREVIEW_DELAY_MS - 1); });
    fireEvent.change(title, { target: { value: 'Draft B' } });
    act(() => { vi.advanceTimersByTime(PREVIEW_DELAY_MS - 1); });
    expect(api.previewDraft).not.toHaveBeenCalled();
    act(() => { vi.advanceTimersByTime(1); });
    expect(api.previewDraft).toHaveBeenCalledTimes(1);
    expect(api.previewDraft).toHaveBeenLastCalledWith({
      path: { runId },
      body: { token, blog: { ...draftView().blog, title: 'Draft B' } },
    });
    expect(bodyKeys(api.previewDraft.mock.calls[0])).toEqual(['blog', 'token']);

    fireEvent.change(title, { target: { value: 'Draft C' } });
    act(() => { vi.advanceTimersByTime(PREVIEW_DELAY_MS); });
    expect(api.previewDraft).toHaveBeenCalledTimes(2);

    second.resolve({ data: { valid: true, blogHtml: '<h1>Draft C</h1>', emailSubject: 'Draft C', emailHtml: '<p>Email C</p>' } });
    await settle();
    expect(preview()).toHaveAttribute('srcdoc', '<h1>Draft C</h1>');

    first.resolve({ data: { valid: true, blogHtml: '<h1>Draft B</h1>', emailSubject: 'Draft B', emailHtml: '<p>Email B</p>' } });
    await settle();
    expect(preview()).toHaveAttribute('srcdoc', '<h1>Draft C</h1>');

    api.previewDraft.mockResolvedValueOnce({ data: { valid: false, message: 'Title is required', blogHtml: '', emailSubject: '', emailHtml: '' } });
    fireEvent.change(title, { target: { value: '' } });
    act(() => { vi.advanceTimersByTime(PREVIEW_DELAY_MS); });
    await settle();
    expect(screen.getByText('Title is required')).toBeInTheDocument();
    expect(preview()).toHaveAttribute('srcdoc', '<h1>Draft C</h1>');

    fireEvent.change(title, { target: { value: 'Connectors got faster' } });
    await settle();
    expect(preview()).toHaveAttribute('srcdoc', '<h1>Blog v3</h1>');
    expect(screen.queryByText('Title is required')).not.toBeInTheDocument();
    expect(api.previewDraft).toHaveBeenCalledTimes(3);
  });

  it('updates the email subject and both previews from one preview of the edited post', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    api.getDraft.mockResolvedValue({ data: draftView() });
    const rendered = deferred<unknown>();
    api.previewDraft.mockReturnValueOnce(rendered.promise);
    renderEditor(`?token=${token}&tab=email`);
    await settle();
    const emailPreview = () => screen.getByTitle('Email preview');
    expect(screen.getByText('Subject: Connectors got faster')).toBeInTheDocument();
    expect(emailPreview()).toHaveAttribute('srcdoc', '<h1>Email v3</h1>');

    // The subject is the post's title, edited in the one form, and follows the server's rendering.
    fireEvent.change(textbox('Title'), { target: { value: 'Connectors, edited for the inbox' } });
    act(() => { vi.advanceTimersByTime(PREVIEW_DELAY_MS); });
    expect(api.previewDraft).toHaveBeenCalledWith({
      path: { runId },
      body: { token, blog: { ...draftView().blog, title: 'Connectors, edited for the inbox' } },
    });
    expect(screen.getByText('Subject: Connectors got faster')).toBeInTheDocument();

    rendered.resolve({ data: { valid: true, blogHtml: '<h1>Blog edited</h1>', emailSubject: 'Connectors, edited for the inbox', emailHtml: '<h1>Email edited</h1>' } });
    await settle();
    expect(screen.getByText('Subject: Connectors, edited for the inbox')).toBeInTheDocument();
    expect(emailPreview()).toHaveAttribute('srcdoc', '<h1>Email edited</h1>');

    // The same response already rendered the blog post; switching tabs asks the server for nothing.
    fireEvent.click(screen.getByRole('link', { name: 'Blog post' }));
    expect(screen.getByTitle('Blog post preview')).toHaveAttribute('srcdoc', '<h1>Blog edited</h1>');
    fireEvent.click(screen.getByRole('link', { name: 'Email' }));
    act(() => { vi.advanceTimersByTime(PREVIEW_DELAY_MS); });
    await settle();
    expect(api.previewDraft).toHaveBeenCalledTimes(1);
    expect(screen.getByText('Subject: Connectors, edited for the inbox')).toBeInTheDocument();

    api.previewDraft.mockResolvedValueOnce({ data: { valid: false, message: 'Title is required', blogHtml: '', emailSubject: '', emailHtml: '' } });
    fireEvent.change(textbox('Title'), { target: { value: '' } });
    act(() => { vi.advanceTimersByTime(PREVIEW_DELAY_MS); });
    await settle();
    expect(screen.getByText('Title is required')).toBeInTheDocument();
    expect(screen.getByText('Subject: Connectors, edited for the inbox')).toBeInTheDocument();
    expect(emailPreview()).toHaveAttribute('srcdoc', '<h1>Email edited</h1>');

    fireEvent.change(textbox('Title'), { target: { value: 'Connectors got faster' } });
    await settle();
    expect(screen.getByText('Subject: Connectors got faster')).toBeInTheDocument();
    expect(emailPreview()).toHaveAttribute('srcdoc', '<h1>Email v3</h1>');
    expect(screen.queryByText('Title is required')).not.toBeInTheDocument();
    expect(api.previewDraft).toHaveBeenCalledTimes(2);
  });
});
