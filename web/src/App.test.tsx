import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App, SubscribePage, UnsubscribePage } from './App';

const info = { name: 'Dex Tech Blog', dexWebUrl: 'http://127.0.0.1:8842' };

const api = vi.hoisted(() => ({
  getApplicationInfo: vi.fn(),
  subscribeToNewsletter: vi.fn(),
  unsubscribeFromNewsletter: vi.fn(),
}));

vi.mock('./api/generated/sdk.gen', () => api);

describe('SubscribePage', () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);

  it('shows the publication copy and the Dex Web link', async () => {
    api.getApplicationInfo.mockResolvedValue({ data: info });
    window.history.replaceState({}, '', '/');
    render(<App />);
    expect(screen.getByText('NEWSLETTER')).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'Open Dex Web' })).toHaveAttribute('href', 'http://127.0.0.1:8842');
    expect(screen.getByRole('heading', { name: 'Dex Tech Blog' })).toBeInTheDocument();
    expect(screen.getByText('Newsletter requests start from Slack and are reviewed in Dex Web.')).toBeInTheDocument();
  });

  it('keeps the page usable when application info fails', async () => {
    api.getApplicationInfo.mockRejectedValue(new Error('offline'));
    window.history.replaceState({}, '', '/');
    render(<App />);
    expect(screen.getByRole('heading', { name: 'Dex Tech Blog' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Open Dex Web' })).not.toBeInTheDocument();
  });

  it('asks for an address instead of submitting an empty form', async () => {
    render(<SubscribePage info={info} onSubscribed={() => {}} />);
    expect(screen.getByRole('button', { name: 'Subscribe' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter your email address.');
    expect(api.subscribeToNewsletter).not.toHaveBeenCalled();
  });

  it('shows loading while the request is pending', () => {
    api.subscribeToNewsletter.mockReturnValue(new Promise(() => {}));
    render(<SubscribePage info={info} onSubscribed={() => {}} />);
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@example.com' } });
    fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    expect(screen.getByRole('button', { name: 'Subscribing…' })).toBeDisabled();
  });

  it('shows the server validation message', async () => {
    api.subscribeToNewsletter.mockResolvedValue({ error: { error: 'invalid_email', message: 'enter one email address' } });
    render(<SubscribePage info={info} onSubscribed={() => {}} />);
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'nope' } });
    fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('enter one email address');
    expect(screen.getByLabelText('Email')).toHaveAttribute('aria-invalid', 'true');
  });

  it('reports a network failure and lets the reader retry', async () => {
    api.subscribeToNewsletter.mockRejectedValueOnce(new Error('offline'));
    render(<SubscribePage info={info} onSubscribed={() => {}} />);
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@example.com' } });
    fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Check your connection');
    expect(screen.getByRole('button', { name: 'Subscribe' })).toBeEnabled();
  });

  it('moves to the confirmation page after subscribing', async () => {
    api.getApplicationInfo.mockResolvedValue({ data: info });
    api.subscribeToNewsletter.mockResolvedValue({ data: { status: 'already_subscribed' } });
    window.history.replaceState({}, '', '/');
    render(<App />);
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@example.com' } });
    fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    expect(await screen.findByRole('heading', { name: "You're already subscribed" })).toBeInTheDocument();
    expect(window.location.pathname).toBe('/subscribed');
  });
});

describe('UnsubscribePage', () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);

  it('calls unsubscribe once even when application info arrives later', async () => {
    let resolveInfo: (value: unknown) => void = () => {};
    api.getApplicationInfo.mockReturnValue(new Promise((resolve) => { resolveInfo = resolve; }));
    api.unsubscribeFromNewsletter.mockResolvedValue({ data: { status: 'unsubscribed' } });
    window.history.replaceState({}, '', '/unsubscribe?email=a%40example.com&token=abc');
    render(<App />);
    expect(await screen.findByRole('heading', { name: "You're unsubscribed" })).toBeInTheDocument();
    resolveInfo({ data: info });
    expect(await screen.findByRole('link', { name: 'Open Dex Web' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: "You're unsubscribed" })).toBeInTheDocument();
    expect(api.unsubscribeFromNewsletter).toHaveBeenCalledTimes(1);
  });

  it('unsubscribes once when the page opens', async () => {
    api.unsubscribeFromNewsletter.mockResolvedValue({ data: { status: 'unsubscribed' } });
    render(<UnsubscribePage info={info} search="?email=a%40example.com&token=abc" />);
    expect(await screen.findByRole('heading', { name: "You're unsubscribed" })).toBeInTheDocument();
    expect(api.unsubscribeFromNewsletter).toHaveBeenCalledTimes(1);
    expect(api.unsubscribeFromNewsletter).toHaveBeenCalledWith({ body: { email: 'a@example.com', token: 'abc' } });
  });

  it('rejects a link without a token without calling the API', async () => {
    render(<UnsubscribePage info={info} search="?email=a%40example.com" />);
    expect(await screen.findByRole('heading', { name: 'This link is invalid' })).toBeInTheDocument();
    expect(api.unsubscribeFromNewsletter).not.toHaveBeenCalled();
  });

  it('shows an invalid link when the server rejects the token', async () => {
    api.unsubscribeFromNewsletter.mockResolvedValue({ error: { error: 'invalid_link', message: 'invalid' }, response: { status: 400 } });
    render(<UnsubscribePage info={info} search="?email=a%40example.com&token=bad" />);
    expect(await screen.findByRole('heading', { name: 'This link is invalid' })).toBeInTheDocument();
  });

  it('shows a retryable failure when the service is unavailable', async () => {
    api.unsubscribeFromNewsletter.mockResolvedValue({ error: { error: 'unsubscription_unavailable', message: 'Try the link again.' }, response: { status: 503 } });
    render(<UnsubscribePage info={info} search="?email=a%40example.com&token=abc" />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Unsubscribing failed' })).toBeInTheDocument());
    expect(screen.getByRole('alert')).toHaveTextContent('Try the link again.');
  });
});
