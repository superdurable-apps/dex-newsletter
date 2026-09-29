import { FormEvent, useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { subscribeToNewsletter, unsubscribeFromNewsletter } from './api/generated/sdk.gen';

const publicationName = 'Engineering Notes';

function errorMessage(error: unknown, fallback: string) {
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
    return error.message;
  }
  return fallback;
}

export function SubscribePage({ onSubscribed }: { onSubscribed: (already: boolean) => void }) {
  const [email, setEmail] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function submit(event: FormEvent) {
    event.preventDefault();
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
      <p className="eyebrow">{publicationName}</p>
      <h1>What we shipped, in your inbox.</h1>
      <p className="lede">Deep dives on new capabilities, written from the pull requests that built them.</p>
      <form onSubmit={submit} noValidate>
        <label htmlFor="email">Email</label>
        <div className="row">
          <input
            id="email"
            type="email"
            autoComplete="email"
            placeholder="you@example.com"
            value={email}
            maxLength={254}
            aria-invalid={error !== ''}
            aria-describedby={error ? 'subscribe-error' : undefined}
            onChange={(event) => setEmail(event.target.value)}
          />
          <button type="submit" disabled={busy || email.trim() === ''}>{busy ? 'Subscribing…' : 'Subscribe'}</button>
        </div>
      </form>
      {error && <p id="subscribe-error" role="alert" className="error">{error}</p>}
      <p className="note">One email per post. Unsubscribe from any email.</p>
    </main>
  );
}

export function SubscribedPage({ already }: { already: boolean }) {
  return (
    <main>
      <p className="eyebrow">{publicationName}</p>
      <h1>{already ? "You're already subscribed" : "You're subscribed"}</h1>
      <p className="lede">The next post arrives in your inbox. Every email has an unsubscribe link.</p>
      <p><a href="/">Back</a></p>
    </main>
  );
}

type UnsubscribeState = 'working' | 'unsubscribed' | 'not_subscribed' | 'invalid' | 'failed';

export function UnsubscribePage({ search }: { search: string }) {
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
    unsubscribed: ["You're unsubscribed", 'You will not receive more newsletters.'],
    not_subscribed: ["You're not subscribed", 'This address is not on the list, so there is nothing to remove.'],
    invalid: ['This link is invalid', 'Use the unsubscribe link from your newsletter email.'],
    failed: ['Unsubscribing failed', message || 'Open the link again in a moment.'],
  };
  const [heading, detail] = content[state];
  return (
    <main>
      <p className="eyebrow">{publicationName}</p>
      <h1>{heading}</h1>
      <p className="lede" role={state === 'failed' || state === 'invalid' ? 'alert' : 'status'}>{detail}</p>
      {state !== 'working' && <p><a href="/">{state === 'unsubscribed' ? 'Subscribe again' : 'Back'}</a></p>}
    </main>
  );
}

export function App() {
  const [path, setPath] = useState(window.location.pathname);
  const [already, setAlready] = useState(false);

  useEffect(() => {
    const onPopState = () => setPath(window.location.pathname);
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  function subscribed(wasAlready: boolean) {
    setAlready(wasAlready);
    window.history.pushState({}, '', '/subscribed');
    setPath('/subscribed');
  }

  const pages: Record<string, () => ReactElement> = {
    '/subscribed': () => <SubscribedPage already={already} />,
    '/unsubscribe': () => <UnsubscribePage search={window.location.search} />,
  };
  const Page = pages[path] ?? (() => <SubscribePage onSubscribed={subscribed} />);
  return <Page />;
}
