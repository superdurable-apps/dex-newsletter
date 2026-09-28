import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';

const api = vi.hoisted(() => ({
  getApplicationInfo: vi.fn(),
  subscribeToNewsletter: vi.fn(),
  unsubscribeFromNewsletter: vi.fn(),
}));

vi.mock('./api/generated/sdk.gen', () => api);

const unavailable = 'Subscriptions are unavailable right now. Try again in a minute.';

async function renderReady() {
  api.getApplicationInfo.mockResolvedValue({ data: { name: 'Dex Tech Blog', dexWebUrl: 'http://127.0.0.1:8802' } });
  render(<App />);
  await screen.findByRole('heading', { name: 'Dex Tech Blog' });
}

function submitEmail(email: string) {
  fireEvent.change(screen.getByRole('textbox', { name: 'Email' }), { target: { value: email } });
  fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
}

describe('App', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.history.replaceState(null, '', '/');
  });

  afterEach(cleanup);

  it('loads the application name through the generated client', async () => {
    api.getApplicationInfo.mockResolvedValue({ data: { name: 'Dex Tech Blog', dexWebUrl: 'http://127.0.0.1:8802' } });
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Dex Tech Blog' })).toBeInTheDocument();
    expect(api.getApplicationInfo).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('link', { name: 'Open Dex Web' })).toHaveAttribute('href', 'http://127.0.0.1:8802');
    expect(screen.getByText(/newsletter requests start from slack and are reviewed in dex web/i)).toBeInTheDocument();
  });

  it('omits the Dex Web link when no URL is configured', async () => {
    api.getApplicationInfo.mockResolvedValue({ data: { name: 'Dex Tech Blog' } });
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Dex Tech Blog' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Open Dex Web' })).not.toBeInTheDocument();
  });

  it('shows a loading state until application information arrives', () => {
    api.getApplicationInfo.mockReturnValue(new Promise(() => {}));
    render(<App />);
    expect(screen.getByText(/loading application/i)).toBeInTheDocument();
    expect(screen.queryByRole('heading')).not.toBeInTheDocument();
  });

  it('reports unavailable application information', async () => {
    api.getApplicationInfo.mockResolvedValue({ error: { message: 'unavailable' } });
    render(<App />);
    expect(await screen.findByRole('alert')).toHaveTextContent(/application information is unavailable/i);
  });

  it('presents only the subscription form and no process-management controls', async () => {
    await renderReady();
    expect(screen.getByRole('form', { name: 'Newsletter subscription' })).toBeInTheDocument();
    expect(screen.getAllByRole('textbox')).toHaveLength(1);
    expect(screen.getAllByRole('button')).toHaveLength(1);
    const email = screen.getByRole('textbox', { name: 'Email' });
    expect(email).toHaveAttribute('type', 'email');
    expect(email).toHaveAttribute('name', 'email');
    expect(email).toHaveAttribute('autocomplete', 'email');
    expect(email).toHaveAttribute('maxlength', '254');
    expect(email).toBeRequired();
    expect(screen.getByRole('button', { name: 'Subscribe' })).toHaveAttribute('type', 'submit');
    expect(screen.queryByText(/approve|retry|mock controls/i)).not.toBeInTheDocument();
    expect(api.subscribeToNewsletter).not.toHaveBeenCalled();
  });

  it('subscribes once and shows the canonical address', async () => {
    api.subscribeToNewsletter.mockResolvedValue({ data: { email: 'reader@example.com' } });
    await renderReady();
    submitEmail('Reader@Example.com');
    expect(await screen.findByRole('status')).toHaveTextContent('Subscribed as reader@example.com.');
    expect(api.subscribeToNewsletter).toHaveBeenCalledTimes(1);
    expect(api.subscribeToNewsletter).toHaveBeenCalledWith({ body: { email: 'Reader@Example.com' } });
    expect(screen.getByRole('textbox', { name: 'Email' })).toHaveValue('');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps an address the reader edits while the request runs', async () => {
    let finish: (value: unknown) => void = () => {};
    api.subscribeToNewsletter.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    await renderReady();
    submitEmail('typo@example.com');
    fireEvent.change(screen.getByRole('textbox', { name: 'Email' }), { target: { value: 'fixed@example.com' } });
    finish({ data: { email: 'typo@example.com' } });
    expect(await screen.findByRole('status')).toHaveTextContent('Subscribed as typo@example.com.');
    expect(screen.getByRole('textbox', { name: 'Email' })).toHaveValue('fixed@example.com');
  });

  it('shows the server message when the list is full', async () => {
    api.subscribeToNewsletter.mockResolvedValue({
      error: { error: 'subscriber_list_full', message: 'The newsletter is not accepting new subscribers right now.' },
      response: { status: 409 },
    });
    await renderReady();
    submitEmail('reader@example.com');
    expect(await screen.findByRole('alert')).toHaveTextContent('The newsletter is not accepting new subscribers right now.');
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
  });

  it('shows the server message for a rejected address', async () => {
    api.subscribeToNewsletter.mockResolvedValue({
      error: { error: 'invalid_email', message: 'Enter a single email address, such as name@example.com.' },
      response: { status: 400 },
    });
    await renderReady();
    submitEmail('reader@example');
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a single email address, such as name@example.com.');
    expect(screen.getByRole('textbox', { name: 'Email' })).toHaveValue('reader@example');
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
  });

  it('shows the generic message when the request never reaches the server', async () => {
    // The generated client resolves a failed fetch as { error: TypeError } with no response.
    api.subscribeToNewsletter.mockResolvedValue({ error: new TypeError('Failed to fetch') });
    await renderReady();
    submitEmail('reader@example.com');
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(unavailable);
    expect(alert).not.toHaveTextContent(/failed to fetch/i);
  });

  it('shows the generic message when the client call rejects', async () => {
    api.subscribeToNewsletter.mockRejectedValue(new TypeError('Failed to fetch'));
    await renderReady();
    submitEmail('reader@example.com');
    expect(await screen.findByRole('alert')).toHaveTextContent(unavailable);
  });

  it('marks Subscribe unavailable while the request is pending and clears the previous message on a new submit', async () => {
    api.subscribeToNewsletter.mockResolvedValueOnce({
      error: { error: 'unavailable', message: unavailable },
      response: { status: 503 },
    });
    await renderReady();
    submitEmail('reader@example.com');
    expect(await screen.findByRole('alert')).toHaveTextContent(unavailable);

    let finish: (value: unknown) => void = () => {};
    api.subscribeToNewsletter.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    // aria-disabled rather than disabled, so keyboard focus stays on the button.
    expect(screen.getByRole('button', { name: 'Subscribe' })).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByRole('button', { name: 'Subscribe' })).toBeEnabled();
    expect(screen.getByRole('form', { name: 'Newsletter subscription' })).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('textbox', { name: 'Email' })).toHaveValue('reader@example.com');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    expect(api.subscribeToNewsletter).toHaveBeenCalledTimes(2);

    finish({ data: { email: 'reader@example.com' } });
    expect(await screen.findByRole('status')).toHaveTextContent('Subscribed as reader@example.com.');
    expect(screen.getByRole('button', { name: 'Subscribe' })).toHaveAttribute('aria-disabled', 'false');
    expect(screen.getByRole('form', { name: 'Newsletter subscription' })).toHaveAttribute('aria-busy', 'false');
  });

  describe('opened from an unsubscribe link', () => {
    const token = 'Ab0-_Ab0-_Ab0-_Ab0-_Ab';

    it('unsubscribes immediately, once, and removes the token from the address bar', async () => {
      api.unsubscribeFromNewsletter.mockResolvedValue({ data: { status: 'unsubscribed' } });
      window.history.replaceState(null, '', `/?ref=mail&unsubscribe=${token}`);
      await renderReady();
      expect(await screen.findByText("You're unsubscribed. You won't receive future issues.")).toBeInTheDocument();
      expect(api.unsubscribeFromNewsletter).toHaveBeenCalledTimes(1);
      expect(api.unsubscribeFromNewsletter).toHaveBeenCalledWith({ body: { token } });
      expect(window.location.search).toBe('?ref=mail');
      // The page keeps its one form, so an accidental unsubscribe can be undone.
      expect(screen.getAllByRole('textbox')).toHaveLength(1);
      expect(screen.getAllByRole('button')).toHaveLength(1);
      expect(api.subscribeToNewsletter).not.toHaveBeenCalled();
    });

    it('shows progress until the list answers', async () => {
      api.unsubscribeFromNewsletter.mockReturnValue(new Promise(() => {}));
      window.history.replaceState(null, '', `/?unsubscribe=${token}`);
      await renderReady();
      expect(screen.getByRole('status')).toHaveTextContent('Unsubscribing…');
    });

    it('rejects a mangled link without calling the API', async () => {
      window.history.replaceState(null, '', '/?unsubscribe=not-a-token');
      await renderReady();
      expect(await screen.findByRole('alert')).toHaveTextContent("This unsubscribe link isn't valid.");
      expect(api.unsubscribeFromNewsletter).not.toHaveBeenCalled();
      expect(window.location.search).toBe('');
    });

    it('explains a link the server rejects', async () => {
      api.unsubscribeFromNewsletter.mockResolvedValue({
        error: { error: 'invalid_request', message: 'request does not match the OpenAPI contract' },
        response: { status: 400 },
      });
      window.history.replaceState(null, '', `/?unsubscribe=${token}`);
      await renderReady();
      expect(await screen.findByRole('alert')).toHaveTextContent("This unsubscribe link isn't valid.");
    });

    it('shows the server message when the list is unavailable', async () => {
      api.unsubscribeFromNewsletter.mockResolvedValue({
        error: { error: 'unavailable', message: "We couldn't unsubscribe you right now. Open the link again in a minute." },
        response: { status: 503 },
      });
      window.history.replaceState(null, '', `/?unsubscribe=${token}`);
      await renderReady();
      expect(await screen.findByRole('alert')).toHaveTextContent("We couldn't unsubscribe you right now.");
      expect(screen.getByRole('status')).toBeEmptyDOMElement();
    });

    it('shows the generic message when the request never reaches the server', async () => {
      api.unsubscribeFromNewsletter.mockRejectedValue(new TypeError('Failed to fetch'));
      window.history.replaceState(null, '', `/?unsubscribe=${token}`);
      await renderReady();
      expect(await screen.findByRole('alert')).toHaveTextContent("We couldn't unsubscribe you right now.");
    });

    it('lets the reader subscribe again after unsubscribing', async () => {
      api.unsubscribeFromNewsletter.mockResolvedValue({ data: { status: 'unsubscribed' } });
      api.subscribeToNewsletter.mockResolvedValue({ data: { email: 'reader@example.com' } });
      window.history.replaceState(null, '', `/?unsubscribe=${token}`);
      await renderReady();
      await screen.findByText("You're unsubscribed. You won't receive future issues.");
      submitEmail('reader@example.com');
      expect(await screen.findByRole('status')).toHaveTextContent('Subscribed as reader@example.com.');
    });
  });
});
