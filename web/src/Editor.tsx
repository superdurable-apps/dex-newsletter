import { MouseEvent, ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { approveDraft, getDraft, previewDraft, saveDraft } from './api/generated/sdk.gen';
import type { ApplicationInfo, BlogDraft, BlogHighlight, DraftEditorView } from './api/generated/types.gen';

// There is one post to edit; the tab only picks which rendering of it the preview shows.
export type EditorTab = 'blog' | 'email';

// Unsaved edits are rendered by the server this long after the last keystroke.
export const PREVIEW_DELAY_MS = 500;

type SectionFields = { heading: string; paragraphs: string; bullets: string };
type BlogFields = Omit<BlogDraft, 'sections'> & { sections: SectionFields[] };
type Phase = 'loading' | 'ready' | 'invalid-link' | 'not-found' | 'failed';
type Busy = 'saving' | 'approving' | 'reloading' | null;
type Notice = { tone: 'success' | 'error'; text: string; reload?: boolean };
type Failure = { status?: number; code: string; message: string };
type Preview = { blogHtml: string; emailSubject: string; emailHtml: string };
type ApiResult = { error?: unknown; response?: { status: number } };

const statusLabels: Record<string, string> = {
  interpreting: 'reading the request',
  researching: 'researching',
  writing: 'writing',
  'awaiting-review': 'awaiting review',
  'needs-attention': 'needs attention',
  retrying: 'retrying',
  delivering: 'sending',
  sent: 'sent',
  rejected: 'rejected',
  'not-a-blog-request': 'not a blog request',
  'no-changes-found': 'no changes found',
  'delivery-stopped': 'delivery stopped',
};

function statusLabel(status: string) {
  return statusLabels[status] ?? status.replace(/-/g, ' ');
}

// Why a draft that is not awaiting review cannot be edited.
function closedReason(status: string) {
  switch (status) {
    case 'interpreting':
    case 'researching':
    case 'writing':
    case 'retrying':
      return 'The draft is not ready for review yet; editing opens when it is.';
    case 'needs-attention':
      return 'This run needs attention in Dex Web; editing is closed until the draft is back in review.';
    case 'delivering':
      return 'This draft is approved and being sent; editing is closed.';
    case 'sent':
      return 'This draft is sent; editing is closed.';
    case 'rejected':
      return 'This draft was rejected; editing is closed.';
    case 'delivery-stopped':
      return 'Delivery of this draft stopped; editing is closed. Check the run in Dex Web.';
    case 'not-a-blog-request':
      return 'This Slack message was not a blog request, so there is no draft to edit.';
    case 'no-changes-found':
      return 'No changes were found for this request, so there is no draft to edit.';
    default:
      return `This run is ${statusLabel(status)}; editing is closed.`;
  }
}

// Whether a closed draft comes back to review, so reloading later can reopen editing.
function reopens(status: string) {
  return ['interpreting', 'researching', 'writing', 'retrying', 'needs-attention'].includes(status);
}

function editorRoute(path: string, search: string) {
  let runId = '';
  try {
    runId = decodeURIComponent(path.slice('/edit/'.length).replace(/\/+$/, ''));
  } catch {
    runId = '';
  }
  const query = new URLSearchParams(search);
  // Links from before the email became the post itself still say tab=newsletter.
  const requested = query.get('tab');
  const tab: EditorTab = requested === 'email' || requested === 'newsletter' ? 'email' : 'blog';
  return { runId, token: query.get('token') ?? '', tab };
}

function blogFields(draft: BlogDraft): BlogFields {
  return {
    title: draft.title,
    subtitle: draft.subtitle,
    slug: draft.slug,
    summary: draft.summary,
    sections: (draft.sections ?? []).map((section) => ({
      heading: section.heading,
      paragraphs: (section.paragraphs ?? []).join('\n\n'),
      bullets: (section.bullets ?? []).join('\n'),
    })),
    highlights: (draft.highlights ?? []).map((highlight) => ({ ...highlight })),
    closing: draft.closing,
  };
}

function splitParagraphs(text: string) {
  return text.split(/\n\s*\n/).map((paragraph) => paragraph.trim()).filter((paragraph) => paragraph !== '');
}

function splitLines(text: string) {
  return text.split('\n').map((line) => line.trim()).filter((line) => line !== '');
}

function editedBlog(fields: BlogFields): BlogDraft {
  return {
    title: fields.title,
    subtitle: fields.subtitle,
    slug: fields.slug,
    summary: fields.summary,
    sections: fields.sections.map((section) => ({
      heading: section.heading,
      paragraphs: splitParagraphs(section.paragraphs),
      bullets: splitLines(section.bullets),
    })),
    highlights: fields.highlights.map((highlight) => ({ title: highlight.title, description: highlight.description, url: highlight.url })),
    closing: fields.closing,
  };
}

function previewOf(rendered: Preview): Preview {
  return { blogHtml: rendered.blogHtml, emailSubject: rendered.emailSubject, emailHtml: rendered.emailHtml };
}

function failureOf(result: ApiResult, fallback: string, offline: string): Failure {
  const status = result.response?.status;
  const body = result.error;
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    const message = 'message' in body && typeof body.message === 'string' && body.message !== '' ? body.message : fallback;
    return { status, code: body.error, message };
  }
  return { status, code: '', message: status === undefined ? offline : fallback };
}

function isWebLink(url: string) {
  return /^https?:\/\//i.test(url);
}

function Field({ id, label, group, value, rows, onChange }: {
  id: string;
  label: string;
  group?: string;
  value: string;
  rows?: number;
  onChange: (value: string) => void;
}) {
  const labelId = `${id}-label`;
  // Inside a group the accessible name includes the group, e.g. "Section 1 Heading".
  const labelledBy = group ? `${group} ${labelId}` : undefined;
  return (
    <div className="editor-field">
      <label id={labelId} htmlFor={id}>{label}</label>
      {rows ? (
        <textarea id={id} rows={rows} value={value} aria-labelledby={labelledBy} onChange={(event) => onChange(event.target.value)} />
      ) : (
        <input id={id} type="text" value={value} aria-labelledby={labelledBy} onChange={(event) => onChange(event.target.value)} />
      )}
    </div>
  );
}

function Group({ id, legend, children }: { id: string; legend: string; children: ReactNode }) {
  return (
    <fieldset className="editor-group">
      <legend id={id}>{legend}</legend>
      {children}
    </fieldset>
  );
}

export function Editor({ info, path, search }: { info: ApplicationInfo; path: string; search: string }) {
  const [route] = useState(() => editorRoute(path, search));
  const { runId, token } = route;
  const [tab, setTab] = useState<EditorTab>(route.tab);
  const [phase, setPhase] = useState<Phase>(runId && token ? 'loading' : 'invalid-link');
  const [loadError, setLoadError] = useState('');
  const [view, setView] = useState<DraftEditorView | null>(null);
  const [blog, setBlog] = useState<BlogFields | null>(null);
  const [preview, setPreview] = useState<Preview>({ blogHtml: '', emailSubject: '', emailHtml: '' });
  const [previewMessage, setPreviewMessage] = useState('');
  const [notice, setNotice] = useState<Notice | null>(null);
  const [busy, setBusy] = useState<Busy>(null);
  const [closed, setClosed] = useState(false);
  const loadSeq = useRef(0);
  const previewSeq = useRef(0);

  const show = useCallback((next: DraftEditorView) => {
    setView(next);
    setBlog(blogFields(next.blog));
    setPreview(previewOf(next));
    setPreviewMessage('');
    setClosed(false);
  }, []);

  // The first load reports failures as a page; later loads keep the page and report in the message area.
  const load = useCallback(async (first: boolean, after: Notice | null = null) => {
    const seq = ++loadSeq.current;
    const offline = 'Loading the draft failed. Check your connection and try again.';
    let problem: Failure;
    try {
      const result = await getDraft({ path: { runId }, query: { token } });
      if (seq !== loadSeq.current) return;
      if (result.data) {
        show(result.data);
        setNotice(after);
        setPhase('ready');
        return;
      }
      problem = failureOf(result, 'The draft could not be loaded. Try again in a moment.', offline);
    } catch {
      if (seq !== loadSeq.current) return;
      problem = { code: '', message: offline };
    }
    if (!first) {
      setNotice({ tone: 'error', text: after ? `${after.text}, but reloading it failed: ${problem.message}` : problem.message, reload: true });
    } else if (problem.status === 400 || problem.status === 403 || problem.code === 'invalid_editor_link') {
      setPhase('invalid-link');
    } else if (problem.status === 404 || problem.code === 'unknown_run') {
      setPhase('not-found');
    } else {
      setLoadError(problem.message);
      setPhase('failed');
    }
  }, [runId, token, show]);

  useEffect(() => {
    if (runId && token) void load(true);
  }, [load, runId, token]);

  const blogValue = useMemo(() => (blog ? editedBlog(blog) : null), [blog]);
  // Compare through the same text round trip so an untouched form is never dirty.
  const baseline = useMemo(() => (view ? JSON.stringify(editedBlog(blogFields(view.blog))) : ''), [view]);
  const dirty = blogValue !== null && JSON.stringify(blogValue) !== baseline;
  const readOnly = !view?.editable || closed;

  useEffect(() => {
    // Every edit invalidates earlier preview requests: only the latest one may replace the preview.
    const seq = ++previewSeq.current;
    if (!view || !blogValue || readOnly) return;
    if (!dirty) {
      setPreview(previewOf(view));
      setPreviewMessage('');
      return;
    }
    const timer = setTimeout(() => {
      const offline = 'The preview could not be updated. Check your connection.';
      previewDraft({ path: { runId }, body: { token, blog: blogValue } })
        .then((result) => {
          if (seq !== previewSeq.current) return;
          if (result.data?.valid) {
            // One rendering request answers both tabs, so the email and its subject never lag the post.
            setPreview(previewOf(result.data));
            setPreviewMessage('');
          } else if (result.data) {
            setPreviewMessage(result.data.message || 'These edits cannot be previewed yet.');
          } else {
            setPreviewMessage(failureOf(result, 'The preview could not be updated.', offline).message);
          }
        })
        .catch(() => {
          if (seq === previewSeq.current) setPreviewMessage(offline);
        });
    }, PREVIEW_DELAY_MS);
    return () => clearTimeout(timer);
  }, [view, blogValue, dirty, readOnly, runId, token]);

  function edited() {
    setNotice((current) => (current?.tone === 'success' ? null : current));
  }

  function changeBlog(change: Partial<BlogFields>) {
    setBlog((current) => current && { ...current, ...change });
    edited();
  }

  function changeSection(index: number, change: Partial<SectionFields>) {
    setBlog((current) => current && { ...current, sections: current.sections.map((section, at) => (at === index ? { ...section, ...change } : section)) });
    edited();
  }

  function changeHighlight(index: number, change: Partial<Pick<BlogHighlight, 'title' | 'description'>>) {
    setBlog((current) => current && { ...current, highlights: current.highlights.map((highlight, at) => (at === index ? { ...highlight, ...change } : highlight)) });
    edited();
  }

  function refused(problem: Failure) {
    if (problem.code === 'not_in_review') {
      // A revision or retry returns the draft to review later, so offer Reload instead of a dead end.
      setClosed(true);
      setNotice({ tone: 'error', text: problem.message, reload: true });
      return;
    }
    setNotice({ tone: 'error', text: problem.message, reload: problem.code === 'draft_changed' });
  }

  async function save() {
    if (!view || !blogValue || busy) return;
    setBusy('saving');
    setNotice(null);
    try {
      const result = await saveDraft({ path: { runId }, body: { token, baseVersion: view.draftVersion, blog: blogValue } });
      if (result.data) {
        // Reload so the next save and approval use the server's version and rendering.
        await load(false, { tone: 'success', text: `Saved (version ${result.data.draftVersion})` });
        return;
      }
      refused(failureOf(result, 'Saving failed. Try again in a moment.', 'Saving failed. Check your connection and try again.'));
    } catch {
      setNotice({ tone: 'error', text: 'Saving failed. Check your connection and try again.' });
    } finally {
      setBusy(null);
    }
  }

  async function approve() {
    if (!view || dirty || busy) return;
    setBusy('approving');
    setNotice(null);
    try {
      const result = await approveDraft({ path: { runId }, body: { token, baseVersion: view.draftVersion } });
      if (result.data) {
        setClosed(true);
        setNotice({ tone: 'success', text: 'Approved. Sending the newsletter to subscribers.' });
        return;
      }
      refused(failureOf(result, 'Approving failed. Try again in a moment.', 'Approving failed. Check your connection and try again.'));
    } catch {
      setNotice({ tone: 'error', text: 'Approving failed. Check your connection and try again.' });
    } finally {
      setBusy(null);
    }
  }

  async function reload() {
    if (busy) return;
    setBusy('reloading');
    setNotice(null);
    try {
      await load(false);
    } finally {
      setBusy(null);
    }
  }

  function retry() {
    setPhase('loading');
    void load(true);
  }

  function tabHref(next: EditorTab) {
    const query = new URLSearchParams(search);
    query.set('tab', next);
    return `${path}?${query.toString()}`;
  }

  // Switch the preview in place so a tab click never reloads the page and drops unsaved edits; the URL still opens the tab directly.
  function selectTab(event: MouseEvent<HTMLAnchorElement>, next: EditorTab) {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    setTab(next);
    window.history.replaceState(window.history.state, '', tabHref(next));
  }

  const dexWebUrl = info.dexWebUrl && runId ? `${info.dexWebUrl.replace(/\/+$/, '')}/v2/run/BlogPost/${encodeURIComponent(runId)}` : '';
  const dexWebLink = dexWebUrl ? <a className="text-link" href={dexWebUrl}>Open in Dex Web</a> : null;

  if (phase !== 'ready' || !view || !blog) {
    return (
      <main className="editor editor-page-state">
        <p className="eyebrow">NEWSLETTER · EDITOR</p>
        {phase === 'loading' && (
          <>
            <h1>Draft editor</h1>
            <p className="lede" role="status">Loading the draft…</p>
          </>
        )}
        {phase === 'invalid-link' && (
          <>
            <h1>This editor link is invalid</h1>
            <p className="lede error" role="alert">Use the editor link from the Slack review thread or Dex Web.</p>
          </>
        )}
        {phase === 'not-found' && (
          <>
            <h1>Blog post run not found</h1>
            <p className="lede error" role="alert">This run does not exist. Check the link, or find the run in Dex Web.</p>
          </>
        )}
        {phase === 'failed' && (
          <>
            <h1>The editor could not load</h1>
            <p className="lede error" role="alert">{loadError}</p>
            <p><button type="button" onClick={retry}>Retry</button></p>
          </>
        )}
        {dexWebLink && <p>{dexWebLink}</p>}
      </main>
    );
  }

  const locked = readOnly || busy !== null;
  const hasDraft = view.draftVersion > 0 || view.blog.title !== '';
  const html = tab === 'blog' ? preview.blogHtml : preview.emailHtml;

  return (
    <main className="editor">
      <header>
        <p className="eyebrow">NEWSLETTER · EDITOR</p>
        <h1>{view.blog.title || 'Untitled draft'}</h1>
        <p className="editor-meta">Status: {statusLabel(view.status)} · draft version {view.draftVersion} · revisions {view.revisionCount}</p>
        {!view.editable && (
          <p className="editor-closed" role="status">
            {closedReason(view.status)}{' '}
            {reopens(view.status) && (
              <button type="button" className="button-secondary" onClick={reload} disabled={busy !== null}>
                {busy === 'reloading' ? 'Reloading…' : 'Reload'}
              </button>
            )}
          </p>
        )}
      </header>
      {hasDraft ? (
        <div className="editor-split">
          <section className="panel" aria-labelledby="editor-edit-heading">
            <h2 id="editor-edit-heading">Edit</h2>
            <form aria-label="Edit post" onSubmit={(event) => event.preventDefault()}>
              <fieldset className="editor-fields" disabled={locked}>
                {!readOnly && <p className="editor-hint">Clear every field of a section or highlight to drop it.</p>}
                <Field id="blog-title" label="Title" value={blog.title} onChange={(title) => changeBlog({ title })} />
                <Field id="blog-subtitle" label="Subtitle" value={blog.subtitle} onChange={(subtitle) => changeBlog({ subtitle })} />
                <Field id="blog-summary" label="Summary" rows={3} value={blog.summary} onChange={(summary) => changeBlog({ summary })} />
                {blog.sections.map((section, index) => {
                  const group = `blog-section-${index + 1}`;
                  return (
                    <Group key={group} id={group} legend={`Section ${index + 1}`}>
                      <Field id={`${group}-heading`} group={group} label="Heading" value={section.heading} onChange={(heading) => changeSection(index, { heading })} />
                      <Field id={`${group}-paragraphs`} group={group} label="Paragraphs (blank line between paragraphs)" rows={8} value={section.paragraphs} onChange={(paragraphs) => changeSection(index, { paragraphs })} />
                      <Field id={`${group}-bullets`} group={group} label="Bullets (one per line)" rows={4} value={section.bullets} onChange={(bullets) => changeSection(index, { bullets })} />
                    </Group>
                  );
                })}
                {blog.highlights.length > 0 && (
                  <Group id="blog-highlights" legend="Highlights">
                    {blog.highlights.map((highlight, index) => {
                      const group = `blog-highlight-${index + 1}`;
                      return (
                        <Group key={group} id={group} legend={`Highlight ${index + 1}`}>
                          <Field id={`${group}-title`} group={group} label="Title" value={highlight.title} onChange={(title) => changeHighlight(index, { title })} />
                          <Field id={`${group}-description`} group={group} label="Description" rows={2} value={highlight.description} onChange={(description) => changeHighlight(index, { description })} />
                          <p className="editor-link">
                            <span>Link (from the research, not editable): </span>
                            {isWebLink(highlight.url) ? <a className="text-link" href={highlight.url} target="_blank" rel="noreferrer noopener">{highlight.url}</a> : <span>{highlight.url || 'none'}</span>}
                          </p>
                        </Group>
                      );
                    })}
                  </Group>
                )}
                <Field id="blog-closing" label="Closing" rows={3} value={blog.closing} onChange={(closing) => changeBlog({ closing })} />
              </fieldset>
            </form>
          </section>
          <section className="panel editor-preview" aria-labelledby="editor-preview-heading">
            <nav className="editor-tabs" aria-label="Preview format">
              <a href={tabHref('blog')} aria-current={tab === 'blog' ? 'page' : undefined} onClick={(event) => selectTab(event, 'blog')}>Blog post</a>
              <a href={tabHref('email')} aria-current={tab === 'email' ? 'page' : undefined} onClick={(event) => selectTab(event, 'email')}>Email</a>
            </nav>
            <h2 id="editor-preview-heading">Preview (as {tab === 'blog' ? 'published' : 'emailed'})</h2>
            <p className="editor-hint">The email sends this same post to subscribers; only the formatting differs.</p>
            {previewMessage && (
              <p className="editor-preview-message" role="status">
                <strong>Preview not updated.</strong> {previewMessage}
              </p>
            )}
            {tab === 'email' && <p className="editor-subject">Subject: {preview.emailSubject}</p>}
            {html ? (
              <iframe title={tab === 'blog' ? 'Blog post preview' : 'Email preview'} srcDoc={html} sandbox="" />
            ) : (
              <div className="editor-preview-empty">Nothing to preview yet.</div>
            )}
          </section>
        </div>
      ) : (
        <section className="panel">
          <p className="lede">There is no draft for this run yet.</p>
        </section>
      )}
      <div className="editor-footer">
        {notice && (
          <p className={notice.tone === 'error' ? 'editor-notice error' : 'editor-notice success'} role={notice.tone === 'error' ? 'alert' : 'status'}>
            {notice.text}
          </p>
        )}
        <div className="editor-actions">
          {notice?.reload && (
            <button type="button" className="button-secondary" onClick={reload} disabled={busy !== null}>
              {busy === 'reloading' ? 'Reloading…' : 'Reload'}
            </button>
          )}
          {!readOnly && hasDraft && (
            <>
              <button type="button" className="button-secondary" onClick={save} disabled={!dirty || busy !== null}>
                {busy === 'saving' ? 'Saving…' : 'Save changes'}
              </button>
              <button type="button" onClick={approve} disabled={dirty || busy !== null} aria-describedby="editor-approve-hint">
                {busy === 'approving' ? 'Approving…' : 'Approve and send'}
              </button>
            </>
          )}
          {dexWebLink}
        </div>
        {!readOnly && hasDraft && (
          <p id="editor-approve-hint" className="editor-hint">
            {dirty ? 'Save your changes before approving.' : 'Approve and send delivers the saved version to every subscriber.'}
          </p>
        )}
      </div>
    </main>
  );
}
