import { type FormEvent, useEffect, useRef, useState } from 'react';
import { getApplicationInfo, subscribeToNewsletter, unsubscribeFromNewsletter } from './api/generated/sdk.gen';
import type { ApplicationInfo } from './api/generated/types.gen';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; info: ApplicationInfo }
  | { status: 'failed' };

type SubscriptionState =
  | { status: 'idle' }
  | { status: 'submitting' }
  | { status: 'subscribed'; email: string }
  | { status: 'unsubscribing' }
  | { status: 'unsubscribed' }
  | { status: 'failed'; message: string };

const unavailableMessage = 'Subscriptions are unavailable right now. Try again in a minute.';
const unsubscribeUnavailableMessage = "We couldn't unsubscribe you right now. Open the link again in a minute.";
const invalidLinkMessage = "This unsubscribe link isn't valid. Open the whole link from the newsletter email.";
const unsubscribeParameter = 'unsubscribe';
const unsubscribeToken = /^[A-Za-z0-9_-]{22}$/;

// takeUnsubscribeToken reads the token of an email's unsubscribe link and
// removes it from the address bar, so it does not linger in history or get
// sent again on reload. It returns null when the page was not opened from a
// link.
function takeUnsubscribeToken(): string | null {
  const url = new URL(window.location.href);
  const token = url.searchParams.get(unsubscribeParameter);
  if (token === null) return null;
  url.searchParams.delete(unsubscribeParameter);
  window.history.replaceState(window.history.state, '', url.pathname + url.search + url.hash);
  return token;
}

// serverMessage returns the message of an OpenAPI Error body. Network and
// parse failures surface as Error instances or text and get the generic copy.
function serverMessage(error: unknown) {
  if (!error || typeof error !== 'object' || error instanceof Error) return undefined;
  const body = error as { error?: unknown; message?: unknown };
  return typeof body.error === 'string' && typeof body.message === 'string' && body.message !== ''
    ? body.message
    : undefined;
}

// The newsletter page. Its only control is the subscription form; opening an
// email's unsubscribe link here unsubscribes immediately and keeps the form
// for resubscribing. Dex Web v2 remains the only process-management surface,
// so this page must not grow approval, status, list, detail, or retry controls.
export function App() {
  const [state, setState] = useState<LoadState>({ status: 'loading' });
  const [email, setEmail] = useState('');
  const [unsubscribeLinkToken] = useState(takeUnsubscribeToken);
  const [subscription, setSubscription] = useState<SubscriptionState>(() =>
    unsubscribeLinkToken === null ? { status: 'idle' } : { status: 'unsubscribing' },
  );
  const unsubscribeStarted = useRef(false);

  useEffect(() => {
    let active = true;
    getApplicationInfo()
      .then((response) => {
        if (!active) return;
        setState(response.data ? { status: 'ready', info: response.data } : { status: 'failed' });
      })
      .catch(() => {
        if (active) setState({ status: 'failed' });
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    // The ref keeps React StrictMode's second effect run from sending twice.
    if (unsubscribeLinkToken === null || unsubscribeStarted.current) return;
    unsubscribeStarted.current = true;
    if (!unsubscribeToken.test(unsubscribeLinkToken)) {
      setSubscription({ status: 'failed', message: invalidLinkMessage });
      return;
    }
    // A subscribe the reader started meanwhile owns the page; keep its state.
    const settle = (next: SubscriptionState) =>
      setSubscription((current) => (current.status === 'unsubscribing' ? next : current));
    unsubscribeFromNewsletter({ body: { token: unsubscribeLinkToken } })
      .then((response) => {
        if (response.data?.status === 'unsubscribed') {
          settle({ status: 'unsubscribed' });
        } else {
          const message = serverMessage(response.error);
          settle({
            status: 'failed',
            message: response.response?.status === 400 ? invalidLinkMessage : message ?? unsubscribeUnavailableMessage,
          });
        }
      })
      .catch(() => settle({ status: 'failed', message: unsubscribeUnavailableMessage }));
  }, [unsubscribeLinkToken]);

  async function subscribe(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (subscription.status === 'submitting') return;
    const submitted = email;
    setSubscription({ status: 'submitting' });
    try {
      const response = await subscribeToNewsletter({ body: { email: submitted } });
      if (response.data?.email) {
        setSubscription({ status: 'subscribed', email: response.data.email });
        // Keep an address the reader started editing while the request ran.
        setEmail((current) => (current === submitted ? '' : current));
      } else {
        setSubscription({ status: 'failed', message: serverMessage(response.error) ?? unavailableMessage });
      }
    } catch {
      setSubscription({ status: 'failed', message: unavailableMessage });
    }
  }

  const submitting = subscription.status === 'submitting';

  return (
    <main>
      <p className="eyebrow">NEWSLETTER</p>
      {state.status === 'loading' && <p aria-busy="true">Loading application…</p>}
      {state.status === 'failed' && <p role="alert" className="error">Application information is unavailable.</p>}
      {state.status === 'ready' && (
        <>
          <h1>{state.info.name}</h1>
          {state.info.dexWebUrl && (
            <p>
              <a className="dex-web-link" href={state.info.dexWebUrl}>Open Dex Web</a>
            </p>
          )}
        </>
      )}
      <p className="lede">Newsletter requests start from Slack and are reviewed in Dex Web.</p>

      <section className="panel">
        <form aria-label="Newsletter subscription" aria-busy={submitting} onSubmit={subscribe}>
          <label htmlFor="email">Email</label>
          <div className="form-row">
            <input
              id="email"
              type="email"
              name="email"
              autoComplete="email"
              required
              maxLength={254}
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
            {/* aria-disabled keeps keyboard focus on the button while the
                request runs; the submit guard above blocks a second request. */}
            <button aria-disabled={submitting} type="submit">Subscribe</button>
          </div>
        </form>
        {/* One live region that stays mounted announces each result. */}
        <p role="status" className="success">
          {subscription.status === 'subscribed' && `Subscribed as ${subscription.email}.`}
          {subscription.status === 'unsubscribing' && 'Unsubscribing…'}
          {subscription.status === 'unsubscribed' && "You're unsubscribed. You won't receive future issues."}
        </p>
        {subscription.status === 'failed' && <p role="alert" className="error">{subscription.message}</p>}
      </section>
    </main>
  );
}
