// Low-fidelity static wireframe of the pre-publish editor. Inert: no state, no API calls.

export function EditorWireframe({ tab }: { tab: 'blog' | 'newsletter' }) {
  return (
    <main className="wireframe">
      <p className="eyebrow">NEWSLETTER · EDITOR</p>
      <h1>[Blog post title]</h1>
      <p className="lede">[Status: awaiting review · draft version 3 · revisions 1]</p>
      <nav className="wf-tabs">
        <a href="/edit/wireframe?tab=blog">{tab === 'blog' ? '▸ ' : ''}Blog post</a>
        <a href="/edit/wireframe?tab=newsletter">{tab === 'newsletter' ? '▸ ' : ''}Newsletter email</a>
      </nav>
      <div className="wf-split">
        <section className="wf-box">
          <h2>Edit</h2>
          {tab === 'blog' ? (
            <>
              <label>Title <input /></label>
              <label>Subtitle <input /></label>
              <label>Summary <textarea rows={3} /></label>
              <fieldset>
                <legend>Section 1</legend>
                <label>Heading <input /></label>
                <label>Paragraphs (blank line between paragraphs) <textarea rows={6} /></label>
                <label>Bullets (one per line) <textarea rows={3} /></label>
              </fieldset>
              <p className="wf-note">[… one group per section …]</p>
              <fieldset>
                <legend>Highlights</legend>
                <label>Title <input /></label>
                <label>Description <input /></label>
                <p className="wf-note">[Link: read-only, from the GitHub research]</p>
              </fieldset>
              <label>Closing <textarea rows={2} /></label>
            </>
          ) : (
            <>
              <label>Subject <input /></label>
              <label>Preheader <input /></label>
              <label>Intro <textarea rows={3} /></label>
              <fieldset>
                <legend>Item 1</legend>
                <label>Title <input /></label>
                <label>Summary <textarea rows={2} /></label>
              </fieldset>
              <p className="wf-note">[… one group per item …]</p>
              <label>Closing <textarea rows={2} /></label>
            </>
          )}
        </section>
        <section className="wf-box">
          <h2>Preview (as {tab === 'blog' ? 'published' : 'emailed'})</h2>
          <div className="wf-placeholder">[Rendered {tab === 'blog' ? 'blog HTML' : 'email HTML'} — the exact template that ships]</div>
        </section>
      </div>
      <p className="wf-note">[Message area: saved / draft changed since you opened it — reload / not in review]</p>
      <div className="wf-actions">
        <button type="button">Save changes</button>
        <button type="button">Approve and send</button>
        <a href="http://127.0.0.1:8842">Open in Dex Web</a>
      </div>
      <p className="wf-note">[Approve sends the saved version to every subscriber; it asks to save unsaved edits first]</p>
    </main>
  );
}
