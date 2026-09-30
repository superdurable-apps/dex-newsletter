import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
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
    newsletter: {
      subject: 'Connectors got faster',
      preheader: 'Retries and paging',
      intro: 'Hello readers.',
      items: [{ title: 'Retries', summary: 'Now automatic.' }],
      closing: 'See you next week.',
    },
    blogHtml: '<h1>Blog v3</h1>',
    newsletterHtml: '<h1>Email v3</h1>',
    ...overrides,
  };
}

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
    expect(screen.getByRole('heading', { name: 'Preview (as published)' })).toBeInTheDocument();
    const preview = screen.getByTitle('Blog post preview');
    expect(preview).toHaveAttribute('srcdoc', '<h1>Blog v3</h1>');
    expect(preview).toHaveAttribute('sandbox', '');
    expect(screen.getByRole('link', { name: 'Blog post' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('link', { name: 'Open in Dex Web' })).toHaveAttribute('href', `http://127.0.0.1:8842/v2/run/BlogPost/${runId}`);
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Approve and send' })).toBeEnabled();
  });

  it('switches to the newsletter tab without losing unsaved blog edits', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    renderEditor();
    fireEvent.change(await loaded(), { target: { value: 'Edited title' } });

    fireEvent.click(screen.getByRole('link', { name: 'Newsletter email' }));
    expect(window.location.search).toBe(`?token=${token}&tab=newsletter`);
    expect(screen.getByRole('link', { name: 'Newsletter email' })).toHaveAttribute('aria-current', 'page');
    expect(textbox('Subject')).toHaveValue('Connectors got faster');
    expect(textbox('Preheader')).toHaveValue('Retries and paging');
    expect(textbox('Intro')).toHaveValue('Hello readers.');
    expect(textbox('Item 1 Title')).toHaveValue('Retries');
    expect(textbox('Item 1 Summary')).toHaveValue('Now automatic.');
    expect(textbox('Closing')).toHaveValue('See you next week.');
    expect(screen.getByRole('heading', { name: 'Preview (as emailed)' })).toBeInTheDocument();
    expect(screen.getByTitle('Newsletter email preview')).toHaveAttribute('srcdoc', '<h1>Email v3</h1>');

    fireEvent.click(screen.getByRole('link', { name: 'Blog post' }));
    expect(textbox('Title')).toHaveValue('Edited title');
  });

  it('opens from the /edit/ route with the tab from the URL', async () => {
    api.getDraft.mockResolvedValue({ data: draftView() });
    window.history.replaceState({}, '', `/edit/${encodeURIComponent(runId)}?token=${token}&tab=newsletter`);
    render(<App />);
    expect(await screen.findByRole('textbox', { name: 'Subject' })).toHaveValue('Connectors got faster');
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
        newsletter: draftView().newsletter,
      },
    });

    await act(async () => save.resolve({ data: { outcome: 'saved', draftVersion: 4 } }));
    expect(await screen.findByText('Saved (version 4)')).toHaveAttribute('role', 'status');
    expect(api.getDraft).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Status: awaiting review · draft version 4 · revisions 2')).toBeInTheDocument();
    expect(textbox('Section 1 Paragraphs (blank line between paragraphs)')).toHaveValue('First.\n\nSecond.');
    expect(screen.getByTitle('Blog post preview')).toHaveAttribute('srcdoc', '<h1>Blog v4</h1>');
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();

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
      body: { token, blog: { ...draftView().blog, title: 'Draft B' }, newsletter: draftView().newsletter },
    });

    fireEvent.change(title, { target: { value: 'Draft C' } });
    act(() => { vi.advanceTimersByTime(PREVIEW_DELAY_MS); });
    expect(api.previewDraft).toHaveBeenCalledTimes(2);

    second.resolve({ data: { valid: true, blogHtml: '<h1>Draft C</h1>', newsletterHtml: '<p>Email C</p>' } });
    await settle();
    expect(preview()).toHaveAttribute('srcdoc', '<h1>Draft C</h1>');

    first.resolve({ data: { valid: true, blogHtml: '<h1>Draft B</h1>', newsletterHtml: '<p>Email B</p>' } });
    await settle();
    expect(preview()).toHaveAttribute('srcdoc', '<h1>Draft C</h1>');

    api.previewDraft.mockResolvedValueOnce({ data: { valid: false, message: 'Title is required', blogHtml: '', newsletterHtml: '' } });
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
});
