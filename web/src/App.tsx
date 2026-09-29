import { FormEvent, useEffect, useRef, useState } from 'react';
import { getApplicationInfo, subscribeToNewsletter, unsubscribeFromNewsletter } from './api/generated/sdk.gen';
import type { ApplicationInfo } from './api/generated/types.gen';

const fallbackInfo: ApplicationInfo = { name: 'Dex Tech Blog' };

function errorMessage(error: unknown, fallback: string) {
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
    return error.message;
  }
  return fallback;
}

function Header({ title, info }: { title: string; info: ApplicationInfo }) {
  return (
    <>
      <p className="eyebrow">NEWSLETTER</p>
      <h1>{title}</h1>
      {info.dexWebUrl && (
        <p>
          <a className="dex-web-link" href={info.dexWebUrl}>Open Dex Web</a>
        </p>
      )}
    </>
  );
}

export function SubscribePage({ info, onSubscribed }: { info: ApplicationInfo; onSubscribed: (already: boolean) => void }) {
  const [email, setEmail] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (email.trim() === '') {
      setError('Enter your email address.');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const response = await subscribeToNewsletter({ body: { email: email.trim() } });
      if (response.data) {
        onSubscribed(response.data.status === 'already_subscribed');
        return;
      }
      setError(errorMessage(response.error, 'Subscribing failed. Try again in a moment.'));
    } catch {
      setError('Subscribing failed. Check your connection and try again.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <main>
      <Header title={info.name} info={info} />
      <p className="lede">Newsletter requests start from Slack and are reviewed in Dex Web.</p>
      <section className="panel">
        <form aria-label="Newsletter subscription" aria-busy={busy} onSubmit={submit} noValidate>
          <label htmlFor="email">Email</label>
          <div className="form-row">
            <input
              id="email"
              type="email"
              autoComplete="email"
              value={email}
              maxLength={254}
              aria-invalid={error !== ''}
              aria-describedby={error ? 'subscribe-error' : undefined}
              onChange={(event) => setEmail(event.target.value)}
            />
            <button type="submit" disabled={busy}>{busy ? 'Subscribing…' : 'Subscribe'}</button>
          </div>
        </form>
        {error && <p id="subscribe-error" role="alert" className="error">{error}</p>}
      </section>
    </main>
  );
}

export function SubscribedPage({ info, already }: { info: ApplicationInfo; already: boolean }) {
  return (
    <main>
      <Header title={already ? "You're already subscribed" : "You're subscribed"} info={info} />
      <p className="lede">The next {info.name} newsletter arrives in your inbox. Every email has an unsubscribe link.</p>
      <p><a className="text-link" href="/">Back</a></p>
    </main>
  );
}

type UnsubscribeState = 'working' | 'unsubscribed' | 'not_subscribed' | 'invalid' | 'failed';

export function UnsubscribePage({ info, search }: { info: ApplicationInfo; search: string }) {
  const [state, setState] = useState<UnsubscribeState>('working');
  const [message, setMessage] = useState('');
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const query = new URLSearchParams(search);
    const email = query.get('email') ?? '';
    const token = query.get('token') ?? '';
    if (!email || !token) {
      setState('invalid');
      return;
    }
    unsubscribeFromNewsletter({ body: { email, token } })
      .then((response) => {
        if (response.data) {
          setState(response.data.status);
        } else if (response.response?.status === 400) {
          setState('invalid');
        } else {
          setMessage(errorMessage(response.error, 'Unsubscribing failed.'));
          setState('failed');
        }
      })
      .catch(() => setState('failed'));
  }, [search]);

  const content: Record<UnsubscribeState, [string, string]> = {
    working: ['Unsubscribing…', 'One moment.'],
    unsubscribed: ["You're unsubscribed", `You will not receive more ${info.name} newsletters.`],
    not_subscribed: ["You're not subscribed", 'This address is not on the list, so there is nothing to remove.'],
    invalid: ['This link is invalid', 'Use the unsubscribe link from your newsletter email.'],
    failed: ['Unsubscribing failed', message || 'Open the link again in a moment.'],
  };
  const [heading, detail] = content[state];
  return (
    <main>
      <Header title={heading} info={info} />
      <p className={state === 'failed' || state === 'invalid' ? 'lede error' : 'lede'} role={state === 'failed' || state === 'invalid' ? 'alert' : 'status'}>{detail}</p>
      {state !== 'working' && <p><a className="text-link" href="/">{state === 'unsubscribed' ? 'Subscribe again' : 'Back'}</a></p>}
    </main>
  );
}

export function App() {
  const [path, setPath] = useState(window.location.pathname);
  const [already, setAlready] = useState(false);
  const [info, setInfo] = useState<ApplicationInfo>(fallbackInfo);

  useEffect(() => {
    let active = true;
    getApplicationInfo()
      .then((response) => {
        if (active && response.data) setInfo(response.data);
      })
      .catch(() => {});
    const onPopState = () => setPath(window.location.pathname);
    window.addEventListener('popstate', onPopState);
    return () => {
      active = false;
      window.removeEventListener('popstate', onPopState);
    };
  }, []);

  useEffect(() => {
    document.title = `${info.name} newsletter`;
  }, [info.name]);

  function subscribed(wasAlready: boolean) {
    setAlready(wasAlready);
    window.history.pushState({}, '', '/subscribed');
    setPath('/subscribed');
  }

  // Render elements, not per-render component types, so a page keeps its state when App re-renders.
  if (path === '/subscribed') return <SubscribedPage info={info} already={already} />;
  if (path === '/unsubscribe') return <UnsubscribePage info={info} search={window.location.search} />;
  return <SubscribePage info={info} onSubscribed={subscribed} />;
}
