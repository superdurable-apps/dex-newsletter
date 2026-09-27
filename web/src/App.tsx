import { useEffect, useState } from 'react';
import { getApplicationInfo } from './api/generated/sdk.gen';
import type { ApplicationInfo } from './api/generated/types.gen';

type LoadState =
  | { status: 'loading' }
  | { status: 'ready'; info: ApplicationInfo }
  | { status: 'failed' };

// Non-business application shell. Dex Web v2 is the only process-management
// surface, so this page must not grow approval, status, list, detail, or retry
// controls.
export function App() {
  const [state, setState] = useState<LoadState>({ status: 'loading' });

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
    </main>
  );
}
