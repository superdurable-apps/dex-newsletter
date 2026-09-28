import { type FormEvent, useEffect, useState } from 'react';
import { getApplicationInfo, subscribeToNewsletter } from './api/generated/sdk.gen';
import type { ApplicationInfo } from './api/generated/types.gen';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; info: ApplicationInfo }
  | { status: 'failed' };

type SubscriptionState =
  | { status: 'idle' }
  | { status: 'submitting' }
  | { status: 'subscribed'; email: string }
  | { status: 'failed'; message: string };

const unavailableMessage = 'Subscriptions are unavailable right now. Try again in a minute.';

// serverMessage returns the message of an OpenAPI Error body. Network and
// parse failures surface as Error instances or text and get the generic copy.
function serverMessage(error: unknown) {
  if (!error || typeof error !== 'object' || error instanceof Error) return undefined;
  const body = error as { error?: unknown; message?: unknown };
  return typeof body.error === 'string' && typeof body.message === 'string' && body.message !== ''
    ? body.message
    : undefined;
}

// Application shell. Its only control is the newsletter subscription form;
// Dex Web v2 remains the only process-management surface, so this page must
// not grow approval, status, list, detail, or retry controls.
export function App() {
  const [state, setState] = useState<LoadState>({ status: 'loading' });
  const [email, setEmail] = useState('');
  const [subscription, setSubscription] = useState<SubscriptionState>({ status: 'idle' });

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

  async function subscribe(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (subscription.status === 'submitting') return;
    setSubscription({ status: 'submitting' });
    try {
      const response = await subscribeToNewsletter({ body: { email } });
      if (response.data?.email) {
        setSubscription({ status: 'subscribed', email: response.data.email });
        setEmail('');
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
      <p className="eyebrow">HELLO WORLD</p>
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
            <button disabled={submitting} type="submit">Subscribe</button>
          </div>
        </form>
        {subscription.status === 'subscribed' && (
          <p role="status" className="success">Subscribed as {subscription.email}.</p>
        )}
        {subscription.status === 'failed' && <p role="alert" className="error">{subscription.message}</p>}
      </section>
    </main>
  );
}
